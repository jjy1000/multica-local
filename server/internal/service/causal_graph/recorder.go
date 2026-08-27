// Package causalgraph — Tier A native-provenance recorder (0.5.83 WL3,
// roadmap §3.0 Tier A). Zero LLM: the enqueue/complete seams in
// service/task.go call into this package, and every write is derived
// from rows that already exist (issue, task, result payload).
//
// Trust-tier rationale: Tier A edges are the only fully machine-native
// provenance in the causal graph — they rank above Semantica mirrors
// (Tier B), Pythia closures (Tier C), and LLM curation (Tier D,
// suggested-gated). The recorder therefore stamps an explicit
// provenance block on every node:
//
//	{"source": "task_enqueue" | "task_complete" | "issue_root",
//	 "dedup_key": ..., "task_id": ..., "trigger": ...}
//
// Idempotency contract: the ACTION node IS the marker. Its dedup_key
// is "task_action:<taskID>" — a task can only be enqueued once, so a
// found marker means the whole (trigger + action + edge) group already
// landed and the call is a no-op. The OUTCOME node dedups on
// "task_outcome:<taskID>" the same way (CompleteTask has an
// already-finalized fast path that does not reach the hook, but the
// daemon may still retry after a partial failure).
//
// Failure law: recorder errors are ALWAYS non-fatal for the caller —
// they log at WRN and return. A broken causal graph must never break
// a task enqueue or completion. The flag check is fail-closed (a
// broken check disables recording) so an infra failure can neither
// break tasks nor silently spam writes.
package causalgraph

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// FlagKeyCausalGraph is the VERBATIM catalog flag key (duplication
// law — same literal as catalog.go / router.go / lock.go / migration
// 279; pinned by TestCatalogAutoDispatchContract).
const FlagKeyCausalGraph = "causal_graph"

// dedup key namespaces (contract — the maintenance ticker and the
// evolver key off these prefixes).
const (
	dedupIssueRoot  = "issue_root:"
	dedupTaskAction = "task_action:"
	dedupTaskDone   = "task_outcome:"
)

// Recorder writes Tier A nodes/edges through the task seams.
type Recorder struct {
	Queries *db.Queries
}

func New(q *db.Queries) *Recorder { return &Recorder{Queries: q} }

// Enabled reports whether the causal_graph flag is on for any user in
// this single-user fork (ListEnabledFlagKeys semantics, narrowed to
// one EXISTS). Fail-closed on error.
func (r *Recorder) Enabled(ctx context.Context) bool {
	if r == nil || r.Queries == nil {
		return false
	}
	v, err := r.Queries.FlagEnabledForAnyUser(ctx, FlagKeyCausalGraph)
	if err != nil {
		return false
	}
	return v
}

// RecordTaskAction is the enqueue-seam hook. It resolves the trigger
// node (latest outcome of this issue, else the issue's root
// constraint node), writes the action node for the freshly created
// task, and links trigger --enables--> action. All writes are
// best-effort; trigger describes the funnel that fired ("issue" for
// the assignment/creation path, "mention" or "squad_leader" for the
// comment funnels).
func (r *Recorder) RecordTaskAction(ctx context.Context, issue db.Issue, task db.AgentTaskQueue, trigger string) {
	const op = "causal record action"
	if !r.Enabled(ctx) {
		return
	}
	ctx = context.WithoutCancel(ctx) // survive HTTP-client disconnects

	actionID, err := r.ensureActionNode(ctx, issue, task, trigger)
	if err != nil {
		slog.Warn(op+" failed", "stage", "action_node", "task_id", util.UUIDToString(task.ID), "error", err)
		return
	}
	if !actionID.Valid {
		return // already recorded
	}

	triggerNodeID, err := r.resolveTriggerNode(ctx, issue)
	if err != nil {
		slog.Warn(op+" failed", "stage", "trigger_node", "task_id", util.UUIDToString(task.ID), "error", err)
		return
	}
	if !triggerNodeID.Valid || triggerNodeID == actionID {
		return
	}
	if _, err := r.Queries.CreateCausalEdge(ctx, db.CreateCausalEdgeParams{
		WorkspaceID: issue.WorkspaceID,
		FromNodeID:  triggerNodeID,
		ToNodeID:    actionID,
		EdgeType:    "enables",
		Provenance:  []byte(`{"source":"task_enqueue"}`),
		CreatedBy:   pgtype.Text{Valid: true, String: "system"},
	}); err != nil {
		slog.Warn(op+" failed", "stage", "edge", "task_id", util.UUIDToString(task.ID), "error", err)
	}
}

// RecordTaskOutcome is the complete-seam hook. It writes the outcome
// node (label carved from the run's final output) and links
// action --causes--> outcome. The workspace scope comes from the
// issue row (AgentTaskQueue carries no workspace column).
func (r *Recorder) RecordTaskOutcome(ctx context.Context, task db.AgentTaskQueue, output string) {
	const op = "causal record outcome"
	if !r.Enabled(ctx) || !task.IssueID.Valid {
		return
	}
	ctx = context.WithoutCancel(ctx)

	issue, err := r.Queries.GetIssue(ctx, task.IssueID)
	if err != nil {
		slog.Warn(op+" failed", "stage", "issue_lookup", "task_id", util.UUIDToString(task.ID), "error", err)
		return
	}
	workspaceID := issue.WorkspaceID
	issueID := task.IssueID

	dedupKey := dedupTaskDone + util.UUIDToString(task.ID)
	if _, err := r.Queries.FindCausalNodeByDedupKey(ctx, db.FindCausalNodeByDedupKeyParams{
		WorkspaceID: workspaceID,
		DedupKey:    dedupKey,
	}); err == nil {
		return // already recorded
	}

	label := truncateLabel(output, 120)
	if label == "" {
		label = "run " + task.Status
	}

	outcome, err := r.Queries.CreateCausalNode(ctx, db.CreateCausalNodeParams{
		WorkspaceID: workspaceID,
		IssueID:     pgtype.UUID{Valid: true, Bytes: issueID.Bytes},
		NodeType:    "outcome",
		Label:       label,
		Provenance: mustJSON(map[string]string{
			"source":    "task_complete",
			"dedup_key": dedupKey,
			"task_id":   util.UUIDToString(task.ID),
			"status":    task.Status,
		}),
		CreatedBy: pgtype.Text{Valid: true, String: "system"},
	})
	if err != nil {
		slog.Warn(op+" failed", "stage", "outcome_node", "task_id", util.UUIDToString(task.ID), "error", err)
		return
	}

	action, err := r.Queries.FindCausalNodeByDedupKey(ctx, db.FindCausalNodeByDedupKeyParams{
		WorkspaceID: workspaceID,
		DedupKey:    dedupTaskAction + util.UUIDToString(task.ID),
	})
	if err != nil {
		return // no action node (recorded before WL3, or enqueue hook skipped) — the outcome stands alone
	}
	if _, err := r.Queries.CreateCausalEdge(ctx, db.CreateCausalEdgeParams{
		WorkspaceID: workspaceID,
		FromNodeID:  action.ID,
		ToNodeID:    outcome.ID,
		EdgeType:    "causes",
		Provenance:  []byte(`{"source":"task_complete"}`),
		CreatedBy:   pgtype.Text{Valid: true, String: "system"},
	}); err != nil {
		slog.Warn(op+" failed", "stage", "edge", "task_id", util.UUIDToString(task.ID), "error", err)
	}
}

// ensureActionNode writes the action node for the task if new. The
// returned UUID is Valid only when the node was freshly created
// (found markers return an invalid UUID so the caller skips the edge
// — the group already landed).
func (r *Recorder) ensureActionNode(ctx context.Context, issue db.Issue, task db.AgentTaskQueue, trigger string) (pgtype.UUID, error) {
	dedupKey := dedupTaskAction + util.UUIDToString(task.ID)
	if _, err := r.Queries.FindCausalNodeByDedupKey(ctx, db.FindCausalNodeByDedupKeyParams{
		WorkspaceID: issue.WorkspaceID,
		DedupKey:    dedupKey,
	}); err == nil {
		return pgtype.UUID{}, nil
	}
	label := task.TriggerSummary.String
	if !task.TriggerSummary.Valid || label == "" {
		label = "run on " + issueTitle(issue)
	}
	node, err := r.Queries.CreateCausalNode(ctx, db.CreateCausalNodeParams{
		WorkspaceID: issue.WorkspaceID,
		IssueID:     pgtype.UUID{Valid: true, Bytes: issue.ID.Bytes},
		NodeType:    "action",
		Label:       truncateLabel(label, 120),
		LabSource:   issue.LabSource,
		Provenance: mustJSON(map[string]string{
			"source":    "task_enqueue",
			"dedup_key": dedupKey,
			"task_id":   util.UUIDToString(task.ID),
			"trigger":   trigger,
		}),
		CreatedBy: pgtype.Text{Valid: true, String: "system"},
	})
	if err != nil {
		return pgtype.UUID{}, err
	}
	return node.ID, nil
}

// resolveTriggerNode prefers the issue's latest outcome node (the run
// that just finished caused this next run) and falls back to the
// issue's root constraint node (created once per issue).
func (r *Recorder) resolveTriggerNode(ctx context.Context, issue db.Issue) (pgtype.UUID, error) {
	if outcome, err := r.Queries.FindLatestOutcomeNodeForIssue(ctx, issue.ID); err == nil {
		return outcome.ID, nil
	}
	rootKey := dedupIssueRoot + util.UUIDToString(issue.ID)
	if root, err := r.Queries.FindCausalNodeByDedupKey(ctx, db.FindCausalNodeByDedupKeyParams{
		WorkspaceID: issue.WorkspaceID,
		DedupKey:    rootKey,
	}); err == nil {
		return root.ID, nil
	}
	root, err := r.Queries.CreateCausalNode(ctx, db.CreateCausalNodeParams{
		WorkspaceID: issue.WorkspaceID,
		IssueID:     pgtype.UUID{Valid: true, Bytes: issue.ID.Bytes},
		NodeType:    "constraint",
		Label:       truncateLabel(issueTitle(issue), 120),
		Provenance: mustJSON(map[string]string{
			"source":    "issue_root",
			"dedup_key": rootKey,
			"issue_id":  util.UUIDToString(issue.ID),
		}),
		CreatedBy: pgtype.Text{Valid: true, String: "system"},
	})
	if err != nil {
		return pgtype.UUID{}, err
	}
	return root.ID, nil
}

func issueTitle(issue db.Issue) string {
	if issue.Title != "" {
		return issue.Title
	}
	return "issue " + util.UUIDToString(issue.ID)
}

func truncateLabel(s string, max int) string {
	for i := range s {
		if i > max {
			return s[:i] + "…"
		}
	}
	return s
}

func mustJSON(v map[string]string) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}
