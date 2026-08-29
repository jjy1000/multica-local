// Package causalgraph — delegation-loop edge (0.5.88).
//
// `multica lab delegate --parent <issue> <lab> "<task>"` creates the
// lab child with parent_issue_id + lab_source in one POST. Until
// 0.5.88 that linkage lived only in the issue tree (and, once the run
// finished, in the child-done system comment). This file records the
// same linkage on the causal graph: parent issue root node
// --depends_on--> lab child root node. The claim-time subgraph brief
// (claim_brief.go) then surfaces the delegation to whichever agent
// works either side, closing the trace the way #8/#9 closed the read
// side.
//
// Edge-type choice: depends_on from the EXISTING CHECK set (migrations
// 277/278) — no new edge type, no migration. Direction is parent →
// child: the parent's outcome depends on the child's completion, which
// is the same direction the issue tree encodes (the parent waits on
// the child; the child-done wake fires the parent).
//
// Failure law (identical to recorder.go): every error path logs at WRN
// and returns — issue creation must NEVER fail because the causal
// write failed. The flag gate is fail-closed inside Recorder.Enabled.
// Idempotency: both endpoints are dedup-keyed root nodes, and the edge
// insert is guarded by an any-status FindCausalEdgeBetween probe (the
// 0.5.84 ICP-5 never-nag contract), so a retried create (or a
// create+PATCH round trip) lands at most one edge.
package causalgraph

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// RecordDelegationEdge records parent --depends_on--> child for a
// delegated lab sub-issue (child carries parent_issue_id + lab_source).
// The parent row is re-loaded through the recorder's own Queries so the
// handler call site stays a one-liner (mirrors RecordTaskOutcome's
// issue_lookup stage). Best-effort on every path; nil-safe; flag-gated
// through Enabled (fail-closed) exactly like the Tier A hooks.
func (r *Recorder) RecordDelegationEdge(ctx context.Context, child db.Issue) {
	const op = "causal record delegation edge"
	if r == nil || r.Queries == nil {
		return
	}
	if !child.ParentIssueID.Valid || !child.ID.Valid {
		return
	}
	if !r.Enabled(ctx) {
		return
	}
	ctx = context.WithoutCancel(ctx) // survive HTTP-client disconnects

	// The delegated child must actually be lab-bound — the shape
	// `multica lab delegate --parent` produces. Plain sub-issues keep
	// their tree linkage only (the causal graph stays about runs and
	// decisions, not every parent/child pair).
	if !child.LabSource.Valid || child.LabSource.String == "" {
		return
	}

	parent, err := r.Queries.GetIssue(ctx, child.ParentIssueID)
	if err != nil {
		slog.Warn(op+" failed", "stage", "parent_lookup",
			"child_id", util.UUIDToString(child.ID),
			"parent_id", util.UUIDToString(child.ParentIssueID),
			"error", err)
		return
	}

	parentNodeID, err := r.ensureIssueRootNode(ctx, parent)
	if err != nil {
		slog.Warn(op+" failed", "stage", "parent_node",
			"parent_id", util.UUIDToString(parent.ID), "error", err)
		return
	}
	childNodeID, err := r.ensureIssueRootNode(ctx, child)
	if err != nil {
		slog.Warn(op+" failed", "stage", "child_node",
			"child_id", util.UUIDToString(child.ID), "error", err)
		return
	}
	if parentNodeID == childNodeID {
		return // degenerate self-parenting; never record a self-loop
	}

	// Any-status probe: a decided (or stale-marked) delegation edge is
	// still the record of the linkage — never re-insert a second copy.
	if _, err := r.Queries.FindCausalEdgeBetween(ctx, db.FindCausalEdgeBetweenParams{
		FromNodeID: parentNodeID,
		ToNodeID:   childNodeID,
		EdgeType:   "depends_on",
	}); err == nil {
		return // already recorded
	}

	if _, err := r.Queries.CreateCausalEdge(ctx, db.CreateCausalEdgeParams{
		WorkspaceID: child.WorkspaceID,
		FromNodeID:  parentNodeID,
		ToNodeID:    childNodeID,
		EdgeType:    "depends_on",
		Provenance:  []byte(`{"source":"issue_delegate"}`),
		CreatedBy:   pgtype.Text{Valid: true, String: "system"},
	}); err != nil {
		slog.Warn(op+" failed", "stage", "edge",
			"child_id", util.UUIDToString(child.ID),
			"parent_id", util.UUIDToString(parent.ID),
			"error", err)
	}
}
