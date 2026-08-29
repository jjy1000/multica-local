// Package causalgraph — delegation briefing (0.5.88).
//
// The 0.5.85 claim brief closed the "agent cannot see the graph" gap;
// this file closes the "agent cannot see the labs" gap. A main agent
// working an issue has no way to discover that enabled labs exist and
// could run a sub-task for it — the knowledge lived in the Labs
// settings tab and in prose. BuildDelegateBrief renders ONE compact
// markdown section the daemon injects next to the claim-time causal
// subgraph brief:
//
//	## Available Labs (delegation)
//
//	You can delegate a self-contained sub-task to an enabled lab and
//	read the result back:
//
//	- claude_science_lab (leader: research)
//	- pythia_oracle (leader: pythia_runtime)
//
//	Delegate a sub-task with: multica lab delegate --parent <issue-id>
//	<flag-key> "<task>"
//
// Enumeration contract: the ENABLED flag keys come from the existing
// ListEnabledFlagKeys query layer (the same "any user" semantics the
// agent skill loader uses — in this single-user fork the set collapses
// to the one user). Each key is then filtered through the catalog's
// own metadata (experimental.FlagByKey — single source of truth):
//
//   - interaction_model must be "assignee" (独立工作型) — auxiliary
//     labs (causal_graph, llm_wiki_bridge) are never delegation
//     targets.
//   - Frozen labs are skipped (swarm_topology) — advertising a lab
//     that cannot accept work is exactly the trap the 0.5.86
//     consolidation retired.
//   - a resolvable, non-empty leader name is required — the
//     delegation dispatch itself requires capabilities.leader
//     (cmd_lab.go's documented prerequisite), so a roster lab like
//     mythos_swarm would only produce a "no run was dispatched"
//     timeout.
//
// Lifecycle mirrors claim_brief.go: 200ms ctx budget, silent fallback
// ("", nil) on EVERY error path — a broken lab lookup must NEVER block
// the claim hot path. Hard cap 2000 bytes with a "…(truncated)"
// suffix; zero enabled delegatable labs render NOTHING (the caller
// concatenates an empty string, no heading stub in the prompt).
package causalgraph

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/experimental"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Brief-formatter knobs.
const (
	// maxDelegateBriefChars caps the rendered section at 2000 bytes.
	// The section is a fixed header + one line per lab + a fixed
	// footer, so even a workspace with every lab enabled stays far
	// under the causal claim brief's 16 000-char budget; the cap
	// only guards against pathological plugin fleets.
	maxDelegateBriefChars = 2000

	// delegateBriefTimeout bounds the worst-case DB latency of the
	// enabled-flag lookup. Same budget discipline as
	// claimBriefTimeout — the deadline rides the claim request scope.
	delegateBriefTimeout = 200 * time.Millisecond
)

// DelegateLabEntry is one delegatable lab as rendered in the briefing.
// Leader is the workspace-resident leader agent name (may differ per
// workspace only through the user-plugin layer; built-ins are global).
type DelegateLabEntry struct {
	FlagKey string
	Leader  string
}

// BuildDelegateBrief returns the "## Available Labs (delegation)"
// markdown section for the workspace's enabled labs, or "" on any
// error path (silent fallback — never block the claim).
//
// leaderFor resolves the leader agent name for a flag key. The
// resolver is injected because leader tables live in the handler /
// service layers (defaultLabLeaderForKey / defaultLeaderAgentForLab +
// the user-plugin manifest); importing them here would invert the
// dependency direction. Callers pass their existing resolveLabLeader
// helper — one resolution scheme, no third copy of the table.
//
// issueID is the claimed issue's UUID string, substituted into the
// delegate command line so the agent can copy-paste it as --parent.
// Empty is allowed and renders the literal <issue-id> placeholder.
func BuildDelegateBrief(ctx context.Context, q *db.Queries, leaderFor func(flagKey string) (string, bool), issueID string) (string, error) {
	// Nil-safety: tests and minimal builds may pass nil; a nil
	// Queries or resolver means the helper cannot run — return the
	// empty default rather than panic.
	if q == nil || leaderFor == nil {
		return "", nil
	}
	// Cancelled-parent short-circuit (mirrors bfsClaimSubgraph's
	// ctx.Err() gate): return the empty default BEFORE any DB touch so
	// a cancelled claim request can never reach the query layer.
	if err := ctx.Err(); err != nil {
		return "", nil
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, delegateBriefTimeout)
	defer cancel()

	enabled, err := q.ListEnabledFlagKeys(timeoutCtx)
	if err != nil {
		// WRN — silent to the caller ("", nil), but ops can
		// correlate "claim ran with no labs briefing" with the DB
		// error.
		slog.Warn("delegate brief: enabled-flag lookup failed",
			"issue_id", issueID,
			"error", err,
		)
		return "", nil
	}

	labs := make([]DelegateLabEntry, 0, len(enabled))
	for _, key := range enabled {
		f, ok := experimental.FlagByKey(key)
		if !ok || f.Frozen {
			continue
		}
		if f.InteractionModel != experimental.InteractionModelAssignee {
			continue
		}
		leader, ok := leaderFor(key)
		if !ok || leader == "" {
			continue
		}
		labs = append(labs, DelegateLabEntry{FlagKey: key, Leader: leader})
	}

	return renderDelegateBrief(labs, issueID), nil
}

// renderDelegateBrief assembles the final markdown section and applies
// the 2000-byte hard cap. Pure — the DB-less tests target this
// directly. Zero delegatable labs render nothing at all (no heading
// stub): the caller concatenates the result into the prompt and an
// empty string is a no-op there.
func renderDelegateBrief(labs []DelegateLabEntry, issueID string) string {
	if len(labs) == 0 {
		return ""
	}

	parentRef := issueID
	if parentRef == "" {
		parentRef = "<issue-id>"
	}

	var sb strings.Builder
	sb.WriteString("## Available Labs (delegation)\n\n")
	sb.WriteString("You can delegate a self-contained sub-task to an enabled lab and read the result back:\n\n")
	for _, l := range labs {
		fmt.Fprintf(&sb, "- %s (leader: %s)\n", l.FlagKey, l.Leader)
	}
	fmt.Fprintf(&sb, "\nDelegate a sub-task with: multica lab delegate --parent %s <flag-key> \"<task>\"\n", parentRef)

	out := sb.String()
	if len(out) <= maxDelegateBriefChars {
		return out
	}
	// Over the cap: cut back to the last complete line that fits, then
	// append the truncation suffix so the agent knows the list is
	// incomplete (mirrors the claim brief's truncation-tail contract).
	capped := out[:maxDelegateBriefChars]
	if idx := strings.LastIndexByte(capped, '\n'); idx > 0 {
		capped = capped[:idx+1]
	}
	return capped + "…(truncated)\n"
}
