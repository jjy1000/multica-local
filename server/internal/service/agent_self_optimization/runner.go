// Package agent_self_optimization — runner.go (0.3.45.1).
//
// One-pass runner: the user-facing spec is "已完结的任务 → 学习 → 建议"。
// The runner pulls every "done" issue (filtered by SourceFilter), groups
// the titles + descriptions into clusters, derives heuristic prompt
// suggestions, writes a report, and creates the self-opt issue row.
//
// MVP boundary (per 0.3.45.1 release notes): NO LLM call. Suggestions
// are derived from keyword frequency + simple heuristic rules. 0.3.46+
// will swap the heuristic for an LLM-driven rationale via
// /api/runtime/llm-call. The wire shape (prompt_suggestions JSONB +
// report_md + created_issue_id) is stable, so the swap is local.
//
// Auto-archive: the runner creates the self-opt issue with
// lab_source='agent_self_optimization'. The main workspace task
// panel's exclude_lab=true filter (ListIssues / ListOpenIssues,
// introduced 0.3.33) hides the row immediately. The
// /experimental/self-opt-history view surfaces it for the user.
// That's "归档" in the user-visible sense without needing an
// archived_at column on the issue table.
package agent_self_optimization

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/experimental"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// MaxIssuesPerRun bounds the SQL scan size so a multi-month back-log
// doesn't blow up the runner's memory or the LLM call (in 0.3.46+).
// 1000 issues × ~5 KB each = ~5 MB in worst case; we accept the cap
// because older issues contribute diminishing signal.
const MaxIssuesPerRun = 1000

// LookbackWindow is the default time horizon when no previous run
// exists. Aligns with the user spec "已完结的任务" — the runner
// should look back at least the last month on a fresh install.
const LookbackWindow = 30 * 24 * time.Hour

// PromptSuggestion is one entry in the JSONB prompt_suggestions
// column. The shape is append-only — adding fields does not break
// existing rows because the renderer ignores unknown keys.
type PromptSuggestion struct {
	AgentID         uuid.UUID   `json:"agent_id"`
	AgentName       string      `json:"agent_name"`
	CurrentExcerpt  string      `json:"current_excerpt,omitempty"`
	SuggestedChange string      `json:"suggested_change"`
	Rationale       string      `json:"rationale"`
	Confidence      float64     `json:"confidence"` // 0.0-1.0
	BackingIssueIDs []uuid.UUID `json:"backing_issue_ids,omitempty"`
}

// RunReport is the markdown report rendered into
// agent_self_opt_run.report_md. Sections are stable (the renderer
// parses by heading); new sections go below.
type RunReport struct {
	WorkspaceID      uuid.UUID          `json:"-"`
	StartedAt        time.Time          `json:"started_at"`
	FinishedAt       time.Time          `json:"finished_at"`
	SourceIssueCount int                `json:"source_issue_count"`
	Suggestions      []PromptSuggestion `json:"suggestions"`
	FailureThemes    []FailureTheme     `json:"failure_themes,omitempty"`
	// TrustLearning (0.5.2) is the "what did users correct and why" section:
	// corrections + failed reviews in the scan window, folded into the
	// learning loop (Boris Cherny ablation principle — remove → add back
	// line by line → test).
	TrustLearning []TrustLearningItem `json:"trust_learning,omitempty"`
	// OptEdits (0.5.2): SkillOpt-style instruction edits from this run
	// (applied + suggested + rejected, distinguished by Application),
	// recorded in agent_opt_edit.
	OptEdits []InstructionEdit `json:"opt_edits,omitempty"`
	// SuggestedEdits (0.5.2): the subset of OptEdits awaiting human
	// confirmation — surfaced in the view's 待确认建议 section.
	SuggestedEdits []InstructionEdit `json:"suggested_edits,omitempty"`
}

// TrustLearningItem is one correction / failed-review event the runner
// surfaces in the report so the user sees *why* agents keep failing.
type TrustLearningItem struct {
	AgentName string    `json:"agent_name"`
	AgentID   uuid.UUID `json:"agent_id"`
	EventType string    `json:"event_type"` // "correction" | "review_fail"
	Note      string    `json:"note,omitempty"`
	IssueID   uuid.UUID `json:"issue_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// FailureTheme is a heuristic cluster of titles that share keywords.
// 0.3.46+ may replace this with an LLM-driven summary.
type FailureTheme struct {
	Theme      string      `json:"theme"`
	IssueCount int         `json:"issue_count"`
	SampleIDs  []uuid.UUID `json:"sample_ids,omitempty"`
}

// MinIssuesForOptimize is the deferral threshold: below this many done
// issues AND this few trust events the run is parked (deferred) instead
// of producing a report — the optimizer would have no evidence to work
// with (0.5.2 spec: "如果历史数据不够的则记录后待记录后进行优化").
const MinIssuesForOptimize = 5

// MinTrustEventsForOptimize is the companion threshold for the trust
// ledger side of the deferral gate.
const MinTrustEventsForOptimize = 3

// DeferralRetryWindow is how long a deferred run waits before the
// scheduler may re-attempt it (data accumulates in the meantime).
const DeferralRetryWindow = 24 * time.Hour

// RunInputs captures the per-run inputs the runner needs. Passed by
// value so the runner is goroutine-safe across concurrent workspaces.
type RunInputs struct {
	WorkspaceID pgtype.UUID
	TriggerKind string // "scheduled" | "manual"
	// Optional override for LookbackWindow — pass zero value to use
	// the package default. Useful for the CLI manual-trigger path
	// where the user wants to scan "all history".
	SinceOverride time.Time
}

// RunResult is what the runner returns to Service for persistence.
type RunResult struct {
	RunID             pgtype.UUID
	CreatedIssueID    pgtype.UUID
	SourceIssueCount  int
	Report            RunReport
	ReportMarkdown    string
	PromptSuggestions []PromptSuggestion
	KBAppendixPath    string
	// Deferred (0.5.2): when the source data is too thin, the runner sets
	// this and the Service persists status='deferred' instead of 'done'.
	Deferred       bool
	DeferredReason string
	DeferredUntil  time.Time
	DataCount      int
	// OptEdits (0.5.2): all edits proposed this run (applied / suggested /
	// rejected), recorded in agent_opt_edit. SuggestedEdits is the subset
	// awaiting human confirmation (surfaced in the 待确认建议 section).
	OptEdits       []InstructionEdit
	SuggestedEdits []InstructionEdit
}

// Run executes one self-opt pass. Caller (Service.Tick) holds the
// advisory lock so two daemons can't run for the same workspace
// concurrently.
//
// Failure modes:
//   - DB read error → return (RunResult{}, err); Service writes 'failed'
//   - Issue scan yields zero rows → still writes a "no input" report
//     so the user sees a row in the history view even when nothing
//     was scanned (rather than a gap that looks like "the lab broke").
//   - CreateIssue error → return err; Service writes 'failed'.
//   - KB write error → soft fail; result.KBAppendixPath stays "" and
//     Service persists the run without it. The user still gets the
//     report in the history view.
func Run(ctx context.Context, q *db.Queries, inputs RunInputs, kb KBWriter) (*RunResult, error) {
	if !inputs.WorkspaceID.Valid {
		return nil, fmt.Errorf("runner: workspace_id required")
	}
	if inputs.TriggerKind != "scheduled" && inputs.TriggerKind != "manual" {
		return nil, fmt.Errorf("runner: invalid trigger_kind %q", inputs.TriggerKind)
	}

	// 1. Resolve the source filter (visibility + hardcoded safety net).
	filter, err := NewSourceFilter(ctx, q)
	if err != nil {
		slog.Warn("agent-self-opt: source filter resolve failed; "+
			"using hardcoded safety net only",
			"workspace", inputs.WorkspaceID, "err", err)
		filter = &SourceFilter{HiddenAgentNames: append([]string(nil), hiddenAgentNames...)}
	}

	// 2. Compute the since timestamp. If a previous run exists, use
	// finished_at - 1h as the inclusive lower bound (the 1h overlap
	// catches issues that landed in flight).
	since := inputs.SinceOverride
	if since.IsZero() {
		last, lerr := q.LastSuccessfulAgentSelfOptRun(ctx, inputs.WorkspaceID)
		if lerr == nil && last.FinishedAt.Valid {
			since = last.FinishedAt.Time.Add(-1 * time.Hour)
		} else if lerr != nil && lerr != pgx.ErrNoRows {
			return nil, fmt.Errorf("read last successful run: %w", lerr)
		} else {
			since = time.Now().Add(-LookbackWindow)
		}
	}

	// 3. Scan done issues, filtering by SourceFilter. We do this with
	// a raw SQL composition because the WHERE clause needs NOT IN for
	// agent IDs + NOT IN for agent names via subquery. The fallback
	// path when the filter is empty is `WHERE ... AND TRUE` so the
	// runner doesn't have to branch the SQL string.
	rows, err := scanDoneIssues(ctx, q, inputs.WorkspaceID, since, filter)
	if err != nil {
		return nil, fmt.Errorf("scan done issues: %w", err)
	}
	slog.Info("agent-self-opt: scan complete",
		"workspace", inputs.WorkspaceID,
		"since", since,
		"matched", len(rows),
		"filter", filter.String())

	// 3b. Data-sufficiency gate (0.5.2, widened 0.5.3): the optimizer needs
	// evidence to work with. When the scan yields too few done issues AND
	// the trust ledger is quiet AND no other subject changed, park the run
	// as deferred (the Service persists status='deferred' + deferred_until)
	// instead of burning a thin report. The scheduler re-attempts once the
	// window passes.
	trustCount, terr := q.CountAgentTrustEventsByType(ctx, db.CountAgentTrustEventsByTypeParams{
		WorkspaceID: inputs.WorkspaceID,
		EventType:   "correction",
		CreatedAt:   pgtype.Timestamptz{Time: since, Valid: true},
	})
	if terr != nil {
		slog.Warn("agent-self-opt: trust count failed; assuming 0", "workspace", inputs.WorkspaceID, "err", terr)
	}
	// 0.5.3: other-subject scans widen the evidence pool. A run with
	// nothing on the issue/trust side but a changed skill/squad/autopilot
	// is still worth a pass (those edits are editorial, human-confirmed).
	skills, serr := q.ListChangedSkillsForSelfOpt(ctx, db.ListChangedSkillsForSelfOptParams{
		WorkspaceID: inputs.WorkspaceID,
		UpdatedAt:   pgtype.Timestamptz{Time: since, Valid: true},
		Limit:       int32(MaxIssuesPerRun),
	})
	if serr != nil {
		slog.Warn("agent-self-opt: skill scan failed; treating as empty", "workspace", inputs.WorkspaceID, "err", serr)
	}
	squads, qerr := q.ListChangedSquadsForSelfOpt(ctx, db.ListChangedSquadsForSelfOptParams{
		WorkspaceID: inputs.WorkspaceID,
		UpdatedAt:   pgtype.Timestamptz{Time: since, Valid: true},
		Limit:       int32(MaxIssuesPerRun),
	})
	if qerr != nil {
		slog.Warn("agent-self-opt: squad scan failed; treating as empty", "workspace", inputs.WorkspaceID, "err", qerr)
	}
	autopilots, aerr := q.ListChangedAutopilotsForSelfOpt(ctx, db.ListChangedAutopilotsForSelfOptParams{
		WorkspaceID: inputs.WorkspaceID,
		UpdatedAt:   pgtype.Timestamptz{Time: since, Valid: true},
		Limit:       int32(MaxIssuesPerRun),
	})
	if aerr != nil {
		slog.Warn("agent-self-opt: autopilot scan failed; treating as empty", "workspace", inputs.WorkspaceID, "err", aerr)
	}
	// Low-trust mandate (0.5.3): a workspace with a low-trust agent (score
	// < OptimizeTrustThreshold AND negative evidence) must NOT defer — the
	// system is mandated to fix it. Scan the ledger for any such agent.
	hasLowTrustSubject := false
	if lowTrustAgents, lerr := q.ListLowTrustAgents(ctx, db.ListLowTrustAgentsParams{
		WorkspaceID: inputs.WorkspaceID,
		Score:       floatToNumericLocal(OptimizeTrustThreshold),
		CreatedAt:   pgtype.Timestamptz{Time: since, Valid: true},
	}); lerr == nil {
		hasLowTrustSubject = len(lowTrustAgents) > 0
	} else {
		slog.Warn("agent-self-opt: low-trust scan failed; deferral gate assumes none", "workspace", inputs.WorkspaceID, "err", lerr)
	}
	if len(rows) < MinIssuesForOptimize && int(trustCount) < MinTrustEventsForOptimize &&
		len(skills) == 0 && len(squads) == 0 && len(autopilots) == 0 && !hasLowTrustSubject {
		reason := fmt.Sprintf(
			"insufficient data: %d done issues, %d trust corrections in window (need ≥ %d issues OR ≥ %d trust events), no changed skills/squads/autopilots, no low-trust subjects",
			len(rows), trustCount, MinIssuesForOptimize, MinTrustEventsForOptimize)
		return &RunResult{
			Deferred:       true,
			DeferredReason: reason,
			DeferredUntil:  time.Now().Add(DeferralRetryWindow),
			DataCount:      len(rows),
		}, nil
	}

	// 3c. SkillOpt-style optimization (0.5.2, generalized 0.5.3): group
	// evidence per subject (agents + skills + squads + autopilots) and
	// propose bounded instruction edits, validation-gated, written back to
	// the subject's instruction text on acceptance. Falls back gracefully
	// to the heuristic suggestions below when no provider CLI is available.
	optimizer := NewOptimizer()
	appliedEdits, suggestedEdits, rejectedEdits, optErr := optimizeAllSubjects(ctx, q, optimizer, inputs.WorkspaceID, rows, skills, squads, autopilots)
	if optErr != nil {
		slog.Info("agent-self-opt: optimizer unavailable; using heuristics only",
			"workspace", inputs.WorkspaceID, "err", optErr)
	}

	// 4. Group + derive suggestions (heuristic baseline — the optimizer
	// edits above are the primary output; these keywords remain the
	// fallback when no provider CLI is available).
	suggestions, themes := deriveSuggestions(rows)

	// 4b. Trust learning (0.5.2): pull corrections + failed reviews in the
	// scan window so the loop learns from *why* agents fail, not just from
	// keyword clustering.
	trustLearning := scanTrustLearning(ctx, q, inputs.WorkspaceID, since)

	// 5. Create the self-opt issue (lab_source='agent_self_optimization'
	// → automatically hidden from the main panel via exclude_lab).
	// The runner bypasses IssueService.Create (no broadcast / enqueue
	// needed for an auto-archived lab report), but it MUST allocate the
	// workspace issue number the same way — issue.number is NOT nullable
	// and has a (workspace_id, number) UNIQUE constraint; without the
	// counter every self-opt issue would collide on number=0. 0.5.2 fix:
	// the pre-0.5.2 runner created exactly one issue successfully
	// (number=0) and every subsequent run failed with
	// "duplicate key value violates unique constraint
	// uq_issue_workspace_number".
	issueNumber, nerr := q.IncrementIssueCounter(ctx, inputs.WorkspaceID)
	if nerr != nil {
		return nil, fmt.Errorf("increment issue counter: %w", nerr)
	}
	issueTitle := fmt.Sprintf("[self-opt] 学习报告 · %s",
		time.Now().Format("2006-01-02 15:04"))
	created, err := q.CreateIssue(ctx, db.CreateIssueParams{
		WorkspaceID:  inputs.WorkspaceID,
		Title:        issueTitle,
		Description:  pgtype.Text{String: buildIssueDescription(rows, suggestions), Valid: true},
		Status:       "done", // immediately done; the row carries the report
		Priority:     "low",
		Number:       issueNumber,
		AssigneeType: pgtype.Text{String: "agent", Valid: true},
		// AssigneeID is the 智能体优化专家 agent — provisioned by
		// install_agent_self_opt.go on flag-on. Its UUID is a
		// deployment constant in experimental.AgentSelfOptimizationAgentID
		// so the runner does not need to look it up.
		AssigneeID: pgtype.UUID{
			Bytes: experimental.AgentSelfOptimizationAgentID(),
			Valid: true,
		},
		// CreatorType / CreatorID are plain SQL types (not pgtype).
		CreatorType: "agent",
		CreatorID: pgtype.UUID{
			Bytes: experimental.AgentSelfOptimizationAgentID(),
			Valid: true,
		},
		Stage:     pgtype.Int4{}, // not a stage; lab_source carries the badge
		LabSource: pgtype.Text{String: "agent_self_optimization", Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("create self-opt issue: %w", err)
	}

	// 6. Compose the report markdown. All edits (applied + suggested +
	// rejected) land in the report's 优化编辑 section; suggested ones are
	// additionally surfaced for human confirmation.
	allEdits := append(append(append([]InstructionEdit{}, appliedEdits...), suggestedEdits...), rejectedEdits...)
	report := RunReport{
		WorkspaceID:      uuid.UUID(inputs.WorkspaceID.Bytes),
		StartedAt:        time.Now(),
		FinishedAt:       time.Now(),
		SourceIssueCount: len(rows),
		Suggestions:      suggestions,
		FailureThemes:    themes,
		TrustLearning:    trustLearning,
		OptEdits:         allEdits,
	}
	md := renderReportMarkdown(report)

	// 7. Write to KB (best-effort). A KB failure does not abort the run.
	var kbPath string
	if kb != nil {
		if path, kerr := kb.Append(ctx, uuid.UUID(inputs.WorkspaceID.Bytes), md); kerr != nil {
			slog.Warn("agent-self-opt: kb write failed",
				"workspace", inputs.WorkspaceID, "err", kerr)
		} else {
			kbPath = path
		}
	}

	return &RunResult{
		CreatedIssueID:    created.ID,
		SourceIssueCount:  len(rows),
		Report:            report,
		ReportMarkdown:    md,
		PromptSuggestions: suggestions,
		KBAppendixPath:    kbPath,
		OptEdits:          allEdits,
		SuggestedEdits:    suggestedEdits,
	}, nil
}

// scanDoneIssues pulls done issues since `since` for the workspace,
// filtered by SourceFilter. Returns the rows in updated_at DESC order.
//
// Implementation note: we use a SQL composition rather than a
// generated sqlc query because the NOT IN clause needs to be
// conditionally omitted (when no IDs are hidden, the WHERE becomes
// `WHERE assignee_id NOT IN ('00000000-...'::uuid)` which is harmless
// but verbose). Inline SQL keeps the runner's call site compact.
func scanDoneIssues(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	since time.Time,
	filter *SourceFilter,
) ([]db.ListDoneIssuesForSelfOptRow, error) {
	// The actual query lives in pkg/db/queries/agent_self_optimization.sql
	// as ListDoneIssuesForSelfOpt — added in this commit. We pass the
	// exclusion lists via the dedicated parameters.
	excludedIDs := filter.ExcludedAgentIDList()
	excludedNames := filter.ExcludedAgentNameList()

	rows, err := q.ListDoneIssuesForSelfOpt(ctx, db.ListDoneIssuesForSelfOptParams{
		WorkspaceID: workspaceID,
		UpdatedAt:   pgtype.Timestamptz{Time: since, Valid: true},
		Column3:     pgUUIDsFromUUIDs(excludedIDs),
		Column4:     excludedNames,
		Limit:       int32(MaxIssuesPerRun),
	})
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// optimizeAllSubjects runs the SkillOpt-style optimizer per subject that has
// evidence in the scan window. Subjects are the four optimizable kinds:
// agents (done issues + trust ledger), skills (content), squads
// (instructions), autopilots (issue_title_template). Returns the applied /
// suggested / rejected edits. Only APPLIED edits are written back to the
// subject's instruction text; all three buckets are recorded in
// agent_opt_edit (suggested edits wait for human confirmation, rejected
// edits are negative experience). An error is returned only when the LLM
// seam is completely unavailable — the caller falls back to heuristics.
func optimizeAllSubjects(
	ctx context.Context,
	q *db.Queries,
	opt *Optimizer,
	workspaceID pgtype.UUID,
	rows []db.ListDoneIssuesForSelfOptRow,
	skills []db.ListChangedSkillsForSelfOptRow,
	squads []db.ListChangedSquadsForSelfOptRow,
	autopilots []db.ListChangedAutopilotsForSelfOptRow,
) (applied, suggested, rejected []InstructionEdit, err error) {
	// Group done issues by assignee agent.
	byAgent := make(map[uuid.UUID][]db.ListDoneIssuesForSelfOptRow)
	for _, r := range rows {
		if !r.AssigneeID.Valid || r.AssigneeType.String != "agent" {
			continue
		}
		aid := uuid.UUID(r.AssigneeID.Bytes)
		byAgent[aid] = append(byAgent[aid], r)
	}

	// 0.5.3 low-trust-first ordering: agents with corrections / failed
	// reviews (the trust-mandated optimization targets) run BEFORE the
	// rest, so a run that hits the LLM budget still fixes the subjects the
	// user explicitly flagged.
	type agentItem struct {
		aid    uuid.UUID
		issues []db.ListDoneIssuesForSelfOptRow
		neg    bool
	}
	agentItems := make([]agentItem, 0, len(byAgent))
	for aid, issues := range byAgent {
		neg := false
		// Peek the trust ledger for negative evidence (cheap: limit 1
		// correction/review_fail scan is not available, so reuse the
		// per-agent loader below — here we only need the ordering hint).
		evts, terr := q.ListAgentTrustEventsByAgent(ctx, db.ListAgentTrustEventsByAgentParams{
			AgentID:     pgtype.UUID{Bytes: [16]byte(aid), Valid: true},
			WorkspaceID: workspaceID,
			Limit:       20,
		})
		if terr == nil {
			for _, e := range evts {
				if e.EventType == "correction" || e.EventType == "review_fail" {
					neg = true
					break
				}
			}
		}
		agentItems = append(agentItems, agentItem{aid: aid, issues: issues, neg: neg})
	}
	// Negative-evidence agents first, then by issue count desc (more
	// evidence = higher optimization priority).
	sort.SliceStable(agentItems, func(i, j int) bool {
		if agentItems[i].neg != agentItems[j].neg {
			return agentItems[i].neg
		}
		return len(agentItems[i].issues) > len(agentItems[j].issues)
	})

	var firstErr error

	// 0.5.2: run subjects in parallel — per-subject LLM calls dominate the
	// run wall-clock (propose + validate per subject, ~4-60s each depending
	// on provider). Bounded concurrency keeps provider CLI spawning sane
	// and stays under the run's MaxRunLifetime.
	const maxAgentParallel = 3
	sem := make(chan struct{}, maxAgentParallel)
	var wg sync.WaitGroup
	var mu sync.Mutex

	// 0.5.3: generic per-subject optimizer invocation. The runner loads the
	// subject's current text + rejection buffer, runs the optimizer, and
	// writes back only applied edits. The trust ledger is agent-only.
	runSubject := func(targetType string, targetID pgtype.UUID, targetName, currentText string, issues []db.ListDoneIssuesForSelfOptRow) {
		defer wg.Done()
		sem <- struct{}{}
		defer func() { <-sem }()

		ev := OptimizeEvidence{
			TargetType:  targetType,
			TargetID:    targetID,
			TargetName:  targetName,
			CurrentText: currentText,
			Issues:      issues,
		}
		if targetType == SubjectAgent {
			trustEvents, terr := q.ListAgentTrustEventsByAgent(ctx, db.ListAgentTrustEventsByAgentParams{
				AgentID:     targetID,
				WorkspaceID: workspaceID,
				Limit:       20,
			})
			if terr != nil {
				trustEvents = nil
			}
			ev.TrustEvents = trustEvents
			// Hard-block: the primary product agent (and lab agents, via
			// isLabManaged in the optimizer) never auto-applies — even if
			// the marker is present, the block wins.
			ev.AutoApplyEnrolled = enrollmentMarkerStr(currentText) && !isHardBlockedAgent(targetName)
		}
		// Negative-experience buffer (0.5.2 adversarial review d6/d7):
		// ONLY 'rejected' + 'reverted' edits are fed to the optimizer as
		// "do not re-propose". Suggested / ignored rows stay out — ignored
		// is re-proposable, and a user-reverted edit must never be
		// re-applied identically.
		rejectedHist, rerr := q.ListNegativeExperienceEdits(ctx, db.ListNegativeExperienceEditsParams{
			TargetType:  targetType,
			TargetID:    targetID,
			WorkspaceID: workspaceID,
			Limit:       RejectionBufferSize,
		})
		if rerr != nil {
			rejectedHist = nil
		}
		ev.RejectedEdits = rejectedHist

		// Post-hoc commit-gate revalidation (0.5.2 adversarial review d1):
		// before proposing NEW edits, the next run re-scores the subject's
		// previously auto-applied edits against their pre-edit snapshots
		// and rolls back any that regressed. Best-effort — a gate failure
		// (no LLM) just skips; the next run re-tries.
		if rerr := opt.RevalidateAppliedEdits(ctx, q, targetType, targetID, targetName, currentText, workspaceID); rerr != nil {
			slog.Warn("agent-self-opt: post-hoc revalidation skipped",
				"subject", targetName, "err", rerr)
		}
		appliedA, suggestedA, rejectedA, finalState, oerr := opt.OptimizeSubject(ctx, q, ev)
		if oerr != nil {
			mu.Lock()
			if firstErr == nil {
				firstErr = oerr
			}
			mu.Unlock()
			return
		}
		// Write back the cumulative instruction state ONCE, but only
		// when at least one edit was APPLIED (suggested edits wait for
		// human confirmation and must not change the text). Agent writes
		// go through UpdateAgent; other subjects are suggested-only by
		// construction (writeBackText refuses them), so only agents land
		// here in practice.
		if len(appliedA) > 0 && finalState != currentText && targetType == SubjectAgent {
			if _, uerr := q.UpdateAgent(ctx, db.UpdateAgentParams{
				ID:           targetID,
				Instructions: pgtype.Text{String: finalState, Valid: true},
			}); uerr != nil {
				slog.Warn("agent-self-opt: instruction write-back failed",
					"subject", targetName, "err", uerr)
			} else {
				slog.Info("agent-self-opt: instructions updated",
					"subject", targetName, "applied", len(appliedA), "suggested", len(suggestedA))
			}
		}
		mu.Lock()
		applied = append(applied, appliedA...)
		suggested = append(suggested, suggestedA...)
		rejected = append(rejected, rejectedA...)
		mu.Unlock()
	}

	for _, it := range agentItems {
		wg.Add(1)
		go runSubject(SubjectAgent, pgtype.UUID{Bytes: [16]byte(it.aid), Valid: true}, "", "", it.issues)
	}
	for _, s := range skills {
		wg.Add(1)
		go runSubject(SubjectSkill, s.ID, s.Name, s.Content, nil)
	}
	for _, sq := range squads {
		wg.Add(1)
		go runSubject(SubjectSquad, sq.ID, sq.Name, sq.Instructions, nil)
	}
	for _, a := range autopilots {
		text := a.IssueTitleTemplate.String
		if strings.TrimSpace(text) == "" {
			text = a.Description.String
		}
		wg.Add(1)
		go runSubject(SubjectAutopilot, a.ID, a.Title, text, nil)
	}
	wg.Wait()
	return applied, suggested, rejected, firstErr
}

// instructionsOf extracts the current instruction text (may be empty).
func instructionsOf(a db.Agent) string {
	return strings.TrimSpace(a.Instructions)
}

// EnrollmentMarker is the opt-in token a user adds to an agent's
// instructions to enable auto-apply for that agent. Default OFF — no
// schema change, the marker doubles as visible consent in the instruction
// text itself.
const EnrollmentMarker = "【self-opt:enroll】"

// enrollmentMarker reports whether the agent has opted into auto-apply.
func enrollmentMarker(a db.Agent) bool {
	return strings.Contains(a.Instructions, EnrollmentMarker)
}

// enrollmentMarkerStr is the string form used by the generalized runner
// (the agent's current text is already loaded as a string).
func enrollmentMarkerStr(text string) bool {
	return strings.Contains(text, EnrollmentMarker)
}

// primaryAgentName is the hard-blocked primary product agent — never
// auto-enrolled, never auto-applied.
const primaryAgentName = "Multica Helper"

// isHardBlockedAgent reports whether the agent sits on the hard-block
// list (the primary product agent + lab agents are excluded from
// auto-apply regardless of score).
func isHardBlockedAgent(name string) bool {
	return name == primaryAgentName
}

// scanTrustLearning pulls correction / review_fail events in the window,
// newest first, bounded so a busy workspace cannot blow the report.
func scanTrustLearning(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, since time.Time) []TrustLearningItem {
	rows, err := q.ListTrustLearningEvents(ctx, db.ListTrustLearningEventsParams{
		WorkspaceID: workspaceID,
		CreatedAt:   pgtype.Timestamptz{Time: since, Valid: true},
		Limit:       50,
	})
	if err != nil {
		slog.Warn("agent-self-opt: trust learning scan failed", "workspace", workspaceID, "err", err)
		return nil
	}
	out := make([]TrustLearningItem, 0, len(rows))
	for _, r := range rows {
		item := TrustLearningItem{
			AgentName: r.AgentName,
			AgentID:   uuid.UUID(r.AgentID.Bytes),
			EventType: r.EventType,
			CreatedAt: r.CreatedAt.Time,
		}
		if r.Note.Valid {
			item.Note = r.Note.String
		}
		if r.IssueID.Valid {
			item.IssueID = uuid.UUID(r.IssueID.Bytes)
		}
		out = append(out, item)
	}
	return out
}

// wordSplit splits a Chinese + English mix title into normalised
// tokens. Whitespace + punctuation are separators; CJK characters are
// each their own 1-gram token. Returned slice is sorted/deduped by the
// caller — this helper just emits the raw stream.
var wordSplit = regexp.MustCompile(`[\s\p{P}]+`)

func tokenize(text string) []string {
	text = strings.ToLower(text)
	parts := wordSplit.Split(text, -1)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// deriveSuggestions runs the MVP heuristic: for every distinct agent
// in the issue set, count the top failure keywords in their assigned
// issues and emit a prompt_suggestion per agent.
func deriveSuggestions(rows []db.ListDoneIssuesForSelfOptRow) ([]PromptSuggestion, []FailureTheme) {
	type agentAccum struct {
		name   string
		id     uuid.UUID
		issues []uuid.UUID
		titles []string
	}
	byAgent := make(map[uuid.UUID]*agentAccum)
	for _, r := range rows {
		if !r.AssigneeID.Valid || r.AssigneeType.String != "agent" {
			continue
		}
		aid := uuid.UUID(r.AssigneeID.Bytes)
		acc, ok := byAgent[aid]
		if !ok {
			acc = &agentAccum{id: aid, name: r.AssigneeName.String}
			byAgent[aid] = acc
		}
		acc.issues = append(acc.issues, uuid.UUID(r.ID.Bytes))
		acc.titles = append(acc.titles, r.Title)
	}

	suggestions := make([]PromptSuggestion, 0, len(byAgent))
	for _, acc := range byAgent {
		top := topKeywords(acc.titles, 3)
		if len(top) == 0 {
			continue
		}
		suggestion := strings.Join(top, " / ")
		confidence := 0.4
		if len(acc.issues) >= 5 {
			confidence = 0.65
		}
		if len(acc.issues) >= 20 {
			confidence = 0.8
		}
		suggestions = append(suggestions, PromptSuggestion{
			AgentID:         acc.id,
			AgentName:       acc.name,
			SuggestedChange: fmt.Sprintf("在 system prompt 中显式覆盖以下高频关键词场景: %s", suggestion),
			Rationale:       fmt.Sprintf("过去窗口共 %d 个已完结 issue 与该智能体相关,关键词聚类提示该智能体在这些主题上有重复任务。", len(acc.issues)),
			Confidence:      confidence,
			BackingIssueIDs: acc.issues,
		})
	}
	sort.Slice(suggestions, func(i, j int) bool {
		return suggestions[i].Confidence > suggestions[j].Confidence
	})

	// Failure themes: aggregate keyword counts across ALL agents.
	allTitles := make([]string, 0, len(rows))
	for _, r := range rows {
		allTitles = append(allTitles, r.Title)
	}
	themes := clusterThemes(allTitles, rows)

	return suggestions, themes
}

// topKeywords returns the n most-frequent tokens across titles.
func topKeywords(titles []string, n int) []string {
	counts := make(map[string]int)
	for _, t := range titles {
		for _, tok := range tokenize(t) {
			if len(tok) < 2 {
				continue
			}
			counts[tok]++
		}
	}
	type kv struct {
		k string
		v int
	}
	pairs := make([]kv, 0, len(counts))
	for k, v := range counts {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].v > pairs[j].v })
	out := make([]string, 0, n)
	for i := 0; i < n && i < len(pairs); i++ {
		out = append(out, pairs[i].k)
	}
	return out
}

// clusterThemes returns the top 5 cross-agent keyword clusters with
// the sample issue IDs that contributed. Used by the renderer to draw
// a "high-frequency topic" card.
func clusterThemes(titles []string, rows []db.ListDoneIssuesForSelfOptRow) []FailureTheme {
	counts := make(map[string]int)
	sampleIDs := make(map[string][]uuid.UUID)
	for i, t := range titles {
		for _, tok := range tokenize(t) {
			if len(tok) < 2 {
				continue
			}
			counts[tok]++
			if i < len(rows) {
				sampleIDs[tok] = append(sampleIDs[tok], uuid.UUID(rows[i].ID.Bytes))
			}
		}
	}
	type kv struct {
		k string
		v int
	}
	pairs := make([]kv, 0, len(counts))
	for k, v := range counts {
		if v >= 2 { // skip single-occurrence noise
			pairs = append(pairs, kv{k, v})
		}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].v > pairs[j].v })
	out := make([]FailureTheme, 0, 5)
	for i := 0; i < 5 && i < len(pairs); i++ {
		ids := sampleIDs[pairs[i].k]
		if len(ids) > 5 {
			ids = ids[:5]
		}
		out = append(out, FailureTheme{
			Theme:      pairs[i].k,
			IssueCount: pairs[i].v,
			SampleIDs:  ids,
		})
	}
	return out
}

// buildIssueDescription composes the markdown body the user sees in
// the issue panel. Keep it short — the full report is in the run row.
func buildIssueDescription(rows []db.ListDoneIssuesForSelfOptRow, suggestions []PromptSuggestion) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Self-opt 扫描窗口内共匹配到 %d 个已完结 issue。\n\n", len(rows))
	fmt.Fprintf(&b, "派生 %d 条 prompt 改进建议(置信度从高到低):\n", len(suggestions))
	for _, s := range suggestions {
		fmt.Fprintf(&b, "- **%s** (%.0f%%): %s\n", s.AgentName, s.Confidence*100, s.SuggestedChange)
	}
	b.WriteString("\n完整报告见「试验性功能 / 智能体自优化」面板。\n")
	return b.String()
}

// renderReportMarkdown serialises the report to markdown. The
// renderer (self-opt-history-view.tsx) parses by `## ` headings.
func renderReportMarkdown(r RunReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 智能体自优化报告\n\n")
	fmt.Fprintf(&b, "**工作区**: `%s`\n\n", r.WorkspaceID)
	fmt.Fprintf(&b, "**扫描窗口**: %s — %s\n\n",
		r.StartedAt.Format("2006-01-02 15:04"), r.FinishedAt.Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "**扫描源 issue 数**: %d\n\n", r.SourceIssueCount)

	b.WriteString("## 建议清单\n\n")
	if len(r.Suggestions) == 0 {
		b.WriteString("_本窗口内无可派生建议(issue 数过少)。_\n")
	} else {
		for _, s := range r.Suggestions {
			fmt.Fprintf(&b, "### %s _(置信度 %.0f%%)_\n\n", s.AgentName, s.Confidence*100)
			fmt.Fprintf(&b, "- **建议**: %s\n", s.SuggestedChange)
			fmt.Fprintf(&b, "- **依据**: %s\n", s.Rationale)
			if len(s.BackingIssueIDs) > 0 {
				fmt.Fprintf(&b, "- **样本数**: %d 个已完结 issue\n", len(s.BackingIssueIDs))
			}
			b.WriteString("\n")
		}
	}

	if len(r.FailureThemes) > 0 {
		b.WriteString("## 高频主题\n\n")
		for _, t := range r.FailureThemes {
			fmt.Fprintf(&b, "- **%s** (%d 个 issue 命中)\n", t.Theme, t.IssueCount)
		}
		b.WriteString("\n")
	}

	if len(r.TrustLearning) > 0 {
		b.WriteString("## 信任学习(纠正与审核失败)\n\n")
		b.WriteString("_消融原则:每次纠正 = 一次删除→逐行加回→测试验证。以下事件来自信任评分账本(agent_trust_event)。_\n\n")
		for _, item := range r.TrustLearning {
			kind := "纠正"
			if item.EventType == "review_fail" {
				kind = "审核失败"
			}
			fmt.Fprintf(&b, "- **[%s]** %s(%s) %s", kind, item.AgentName,
				item.CreatedAt.Format("2006-01-02 15:04"), "")
			if item.Note != "" {
				fmt.Fprintf(&b, " — %s", item.Note)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if len(r.OptEdits) > 0 {
		b.WriteString("## 优化编辑(SkillOpt)\n\n")
		b.WriteString("_分级验证:达到阈值的自动应用(写回 instructions,经验被智能体自己学习),接近阈值的待人工确认,其余作为负经验进入缓冲。_\n\n")
		for _, e := range r.OptEdits {
			var mark string
			switch e.Application {
			case ApplicationApplied:
				mark = "✅ 自动应用"
			case ApplicationSuggested:
				mark = "📝 待确认"
			default:
				mark = "❌ 已拒绝"
			}
			fmt.Fprintf(&b, "- **%s** [%s] %s", mark, e.EditType, e.TargetName)
			if e.ValidationScore > 0 {
				fmt.Fprintf(&b, " _(评分 %.0f)_", e.ValidationScore)
			}
			b.WriteString("\n")
			switch e.EditType {
			case "add":
				fmt.Fprintf(&b, "  - 新增: `%s`\n", e.AfterText)
			case "delete":
				fmt.Fprintf(&b, "  - 删除: `%s`\n", e.BeforeText)
			case "replace":
				fmt.Fprintf(&b, "  - 替换: `%s` → `%s`\n", e.BeforeText, e.AfterText)
			}
			if e.Rationale != "" {
				fmt.Fprintf(&b, "  - 依据: %s\n", e.Rationale)
			}
			if e.ValidationReason != "" {
				fmt.Fprintf(&b, "  - 验证: %s\n", e.ValidationReason)
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("---\n\n_由 agent_self_optimization 服务在 daemon 内自动生成。0.5.2:报告纳入信任学习(纠正/审核失败)事件。_\n")
	return b.String()
}

// MarshalPromptSuggestions serialises suggestions for the JSONB column.
// Returned bytes are valid JSON (either `[]` or `[...]`).
func MarshalPromptSuggestions(s []PromptSuggestion) ([]byte, error) {
	if s == nil {
		return []byte(`[]`), nil
	}
	return json.Marshal(s)
}

// pgUUIDsFromUUIDs converts a slice of uuid.UUID to []pgtype.UUID for
// sqlc. The empty case returns a non-nil zero-length slice so the
// placeholder array still binds cleanly (matches experimental.UUIDsToPgtype
// but in the reverse direction — the runner prefers uuid.UUID values
// everywhere and only converts at the sqlc boundary).
func pgUUIDsFromUUIDs(ids []uuid.UUID) []pgtype.UUID {
	if ids == nil {
		return nil
	}
	out := make([]pgtype.UUID, len(ids))
	for i, id := range ids {
		out[i] = pgtype.UUID{Bytes: id, Valid: true}
	}
	return out
}
