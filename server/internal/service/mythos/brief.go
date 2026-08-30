package mythos

// BuildEnhancerBrief (0.5.90) renders the compact OpenMythos strategy
// section injected into an agent's task-claim briefing when the claimed
// issue has an outer-loop (enhancer) run anchored on it. The delivery
// contract has two halves — the system comment on the root issue is the
// human half (handler/deliverMythosEnhancerResult), this briefing is
// the agent half: the target assignee starts from the swarm's converged
// conclusions instead of rediscovering them.
//
// Mirrors the causal claim_brief contract (service/causal_graph/
// claim_brief.go): read-only, byte-bounded, and silent — every error or
// empty path returns ("", nil) so a broken lookup never blocks the
// claim hot path. The run rows are the gate: an issue with no mythos
// runs renders nothing, so the caller needs no flag check beyond the
// cheap issue.lab_source=='mythos_swarm' scope it already holds.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	// EnhancerBriefMaxBytes caps the injected section. A claim briefing
	// is prompt context, not a document — same budget class as the
	// causal claim brief's hard cap.
	EnhancerBriefMaxBytes = 4096

	enhancerBriefProblemMax = 600
	enhancerBriefCodaMax    = 2600
)

// EnhancerBriefQuerier is the slice of db.Queries the brief builder
// reads. An interface so DB-less tests can fake the rows without a
// database (same seam as causalgraph's claim_brief tests).
type EnhancerBriefQuerier interface {
	ListMythosRunsByIssueAndWorkspace(ctx context.Context, arg db.ListMythosRunsByIssueAndWorkspaceParams) ([]db.MythosRun, error)
}

// BuildEnhancerBrief returns the markdown strategy section for the
// freshest enhancer run anchored on issueID, or "" when there is
// nothing to inject (no runs, a mid-pipeline run whose coda has not
// landed, or a failed run).
func BuildEnhancerBrief(ctx context.Context, q EnhancerBriefQuerier, workspaceID, issueID pgtype.UUID) (string, error) {
	if q == nil || !issueID.Valid || !workspaceID.Valid {
		return "", nil
	}
	runs, err := q.ListMythosRunsByIssueAndWorkspace(ctx, db.ListMythosRunsByIssueAndWorkspaceParams{
		RootIssueID: issueID,
		WorkspaceID: workspaceID,
	})
	if err != nil || len(runs) == 0 {
		return "", nil
	}
	run := runs[0] // freshest — the SQL orders by started_at DESC
	// 'running' means the pipeline is still mid-flight: coda has not
	// landed, so there is no strategy to hand over yet (the coda gate
	// below would catch it too; the status check keeps failed runs out
	// explicitly).
	if run.Status != "supervising" && run.Status != "completed" {
		return "", nil
	}
	coda := enhancerBriefCodaText(run.CodaConclusions)
	if strings.TrimSpace(coda) == "" {
		return "", nil
	}
	problem := truncateUTF8(run.Problem, enhancerBriefProblemMax)
	if len(problem) < len(run.Problem) {
		problem += "…"
	}
	brief := fmt.Sprintf(
		"## OpenMythos Strategy (outer loop, read-only)\n\n"+
			"An OpenMythos outer-loop run (%s) iterated on this issue and distilled the strategy below. "+
			"Treat it as advisory context on HOW to approach the work — your task instructions take precedence.\n\n"+
			"**Task as framed for the loop:** %s\n\n"+
			"**Converged strategy:**\n\n%s",
		run.Status, problem, coda,
	)
	if len(brief) > EnhancerBriefMaxBytes {
		brief = truncateUTF8(brief, EnhancerBriefMaxBytes) + "\n\n…(truncated)"
	}
	return brief, nil
}

// enhancerBriefCodaText decodes the coda_conclusions JSONB string array
// into readable text. Malformed JSON degrades to the raw bytes (best
// effort); an empty array or SQL NULL renders as nothing.
func enhancerBriefCodaText(raw []byte) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var parts []string
	if err := json.Unmarshal(raw, &parts); err != nil || len(parts) == 0 {
		return trimmed
	}
	return strings.Join(parts, "\n\n")
}

// truncateUTF8 cuts s to at most max bytes without splitting a trailing
// rune, so the injected brief never carries invalid UTF-8.
func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	for i := 0; i < utf8.UTFMax && len(cut) > 0; i++ {
		if utf8.ValidString(cut) {
			break
		}
		cut = cut[:len(cut)-1]
	}
	return cut
}
