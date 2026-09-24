// Package handler — causal_graph_refresh_test.go (0.5.84 P0 #3)
//
// Regression pins for the TouchCausalNode / RefreshCausalNodesForIssue
// hot-path wiring. Before this fix TouchCausalNode had zero callers —
// last_observed_at froze at INSERT and every volatile node flipped to
// status='stale' after 30 days (the maintenance ticker's contract).
// The headline pin is TestStaleNodesSurviveAfterRefresh: simulate a
// 31-day-old node, fire RefreshForIssue, assert last_observed_at is
// back inside the 30-day window.
//
// DB-backed cases are skipped by TestMain when DATABASE_URL is
// unreachable (same harness as timesfm_forecast_test.go).
package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// enableCausalGraphFlag flips the single-user-fork flag ON for this
// test (every other user pref is left alone) and registers a Cleanup
// to flip it back. context.Background — t.Context is cancelled before
// cleanup fires (the test fixture's known footgun).
func enableCausalGraphFlag(t *testing.T, ctx context.Context) {
	t.Helper()
	if _, err := testPool.Exec(ctx, `DELETE FROM experimental_pref WHERE flag_key = 'causal_graph'`); err != nil {
		t.Fatalf("reset pref rows: %v", err)
	}
	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO experimental_pref (user_id, flag_key, enabled) VALUES ($1, 'causal_graph', true) ON CONFLICT (user_id, flag_key) DO UPDATE SET enabled = EXCLUDED.enabled`,
		testUserID); err != nil {
		t.Fatalf("enable causal_graph flag: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM experimental_pref WHERE flag_key = 'causal_graph'`)
	})
}

// lastObserved reads last_observed_at for a node (a helper because
// the CausalNode struct's last_observed_at is the column we care
// about). pgtype.Timestamptz scan → time.Time via .Time.
func lastObserved(t *testing.T, ctx context.Context, nodeID pgtype.UUID) time.Time {
	t.Helper()
	var node db.CausalNode
	err := testPool.QueryRow(ctx,
		`SELECT id, last_observed_at FROM causal_node WHERE id = $1`, nodeID).Scan(&node.ID, &node.LastObservedAt)
	if err != nil {
		t.Fatalf("load node %v: %v", nodeID, err)
	}
	if !node.LastObservedAt.Valid {
		return time.Time{}
	}
	return node.LastObservedAt.Time
}

// forceNodeStale bumps last_observed_at to 31 days ago so the
// maintenance ticker would mark the node stale on its next sweep.
// Mirrors the threshold in maintenance.go (NodeStaleAfter = 30d).
func forceNodeStale(t *testing.T, ctx context.Context, nodeID pgtype.UUID) {
	t.Helper()
	cutoff := time.Now().UTC().Add(-31 * 24 * time.Hour)
	if _, err := testPool.Exec(ctx,
		`UPDATE causal_node SET last_observed_at = $1 WHERE id = $2`,
		cutoff, nodeID); err != nil {
		t.Fatalf("force stale on node %v: %v", nodeID, err)
	}
}

// TestStaleNodesSurviveAfterRefresh is the headline regression pin
// (0.5.84 P0 #3). Before the fix: stale node + 31 days of idle =
// status='stale' → invisible in active-only UI filter. After the
// fix: any UpdateIssue / CreateComment / enqueueTask /
// CompleteTask on the issue touches last_observed_at back to now()
// and the node survives the next maintenance sweep.
func TestStaleNodesSurviveAfterRefresh(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler fixture unavailable (no DATABASE_URL)")
	}
	rec := testHandler.CausalRecorder
	if rec == nil {
		t.Fatal("Handler.CausalRecorder is nil — the 0.5.84 wiring regressed")
	}
	enableCausalGraphFlag(t, context.Background())
	ctx := t.Context()

	issue := causalTestIssue(t, "Stale refresh pin")
	issueRow, err := testHandler.Queries.GetIssue(ctx, mustParseUUID(t, issue))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	taskID := uuid.New()
	task := db.AgentTaskQueue{
		ID:      pgtype.UUID{Valid: true, Bytes: taskID},
		IssueID: pgtype.UUID{Valid: true, Bytes: issueRow.ID.Bytes},
	}

	// Recorder stamps the action node + the issue-root constraint
	// (enables-edge between them). Both start fresh at now().
	rec.RecordTaskAction(ctx, issueRow, task, "issue")
	nodes, err := testHandler.Queries.ListCausalNodesByIssue(ctx, issueRow.ID)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("recorder wrote %d nodes, want 2 (root + action)", len(nodes))
	}

	// Force both nodes stale (mimicking 31 days of idle).
	for _, n := range nodes {
		forceNodeStale(t, ctx, n.ID)
	}
	for _, n := range nodes {
		if got := lastObserved(t, ctx, n.ID); time.Since(got) < 30*24*time.Hour {
			t.Fatalf("stale-seed failed: node %v last_observed_at = %v (age %v)", n.ID, got, time.Since(got))
		}
	}

	// Refresh via the same hook the hot paths call.
	rec.RefreshForIssue(ctx, issueRow.ID)

	// Every active node for the issue must be back inside the
	// 30-day window. Constraint/assumption are exempt from the
	// stale ladder by design, but the bulk touch is harmless to
	// them — pinning both directions.
	for _, n := range nodes {
		got := lastObserved(t, ctx, n.ID)
		if time.Since(got) > 30*24*time.Hour {
			t.Errorf("RefreshForIssue did not touch node %v (type=%s): age=%v, want <30d",
				n.ID, n.Type, time.Since(got))
		}
	}
}

// TestRefreshForIssueTouchesNeighborNodes covers the cross-issue
// edge-neighbour case. Build two issues A and B; create an edge
// (A_node -> B_node); force B_node stale; refresh A; B_node must
// survive (it's a 1-hop graph neighbour of A).
func TestRefreshForIssueTouchesNeighborNodes(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler fixture unavailable (no DATABASE_URL)")
	}
	rec := testHandler.CausalRecorder
	if rec == nil {
		t.Fatal("Handler.CausalRecorder is nil — the 0.5.84 wiring regressed")
	}
	enableCausalGraphFlag(t, context.Background())
	ctx := t.Context()

	issueA := causalTestIssue(t, "Touch neighbor — A")
	issueB := causalTestIssue(t, "Touch neighbor — B")
	issueARow, err := testHandler.Queries.GetIssue(ctx, mustParseUUID(t, issueA))
	if err != nil {
		t.Fatalf("load issue A: %v", err)
	}
	issueBRow, err := testHandler.Queries.GetIssue(ctx, mustParseUUID(t, issueB))
	if err != nil {
		t.Fatalf("load issue B: %v", err)
	}

	// Two action nodes, one per issue.
	taskA := db.AgentTaskQueue{
		ID:      pgtype.UUID{Valid: true, Bytes: uuid.New()},
		IssueID: pgtype.UUID{Valid: true, Bytes: issueARow.ID.Bytes},
	}
	taskB := db.AgentTaskQueue{
		ID:      pgtype.UUID{Valid: true, Bytes: uuid.New()},
		IssueID: pgtype.UUID{Valid: true, Bytes: issueBRow.ID.Bytes},
	}
	rec.RecordTaskAction(ctx, issueARow, taskA, "issue")
	rec.RecordTaskAction(ctx, issueBRow, taskB, "issue")
	nodesA, err := testHandler.Queries.ListCausalNodesByIssue(ctx, issueARow.ID)
	if err != nil {
		t.Fatalf("list nodes A: %v", err)
	}
	nodesB, err := testHandler.Queries.ListCausalNodesByIssue(ctx, issueBRow.ID)
	if err != nil {
		t.Fatalf("list nodes B: %v", err)
	}
	// Pick A's action node + B's action node (skip the constraint
	// root, which is shared-state across many tests).
	var actionA, actionB db.CausalNode
	for _, n := range nodesA {
		if n.Type == "action" {
			actionA = n
			break
		}
	}
	for _, n := range nodesB {
		if n.Type == "action" {
			actionB = n
			break
		}
	}
	if !actionA.ID.Valid || !actionB.ID.Valid {
		t.Fatalf("missing action nodes: A=%v B=%v", actionA, actionB)
	}

	// Hand-built active edge: A_action --causes--> B_action.
	if _, err := testPool.Exec(ctx, `
		INSERT INTO causal_edge (workspace_id, from_node_id, to_node_id, type, status, created_by)
		VALUES ($1, $2, $3, 'causes', 'active', 'test')
	`, issueARow.WorkspaceID, actionA.ID, actionB.ID); err != nil {
		t.Fatalf("seed cross-issue edge: %v", err)
	}

	// Force B's action node stale (A stays fresh).
	forceNodeStale(t, ctx, actionB.ID)

	// Refresh issue A — B's action node is a 1-hop neighbour via
	// the active edge, so it must come back fresh too.
	rec.RefreshForIssue(ctx, issueARow.ID)

	if got := lastObserved(t, ctx, actionB.ID); time.Since(got) > 30*24*time.Hour {
		t.Errorf("neighbour B_action not refreshed: age=%v, want <30d", time.Since(got))
	}
	// And A's action node itself must be fresh (primary touch).
	if got := lastObserved(t, ctx, actionA.ID); time.Since(got) > 30*24*time.Hour {
		t.Errorf("primary A_action not refreshed: age=%v, want <30d", time.Since(got))
	}
}

// TestRefreshForIssueNoOpWhenFlagOff pins the flag-gated no-op
// behaviour — when the flag is off, RefreshForIssue must NOT touch
// last_observed_at (one indexed EXISTS + skip). Otherwise the
// single-user fork would write to causal_node on every comment /
// issue update regardless of opt-in, defeating the lab's opt-in
// model.
func TestRefreshForIssueNoOpWhenFlagOff(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler fixture unavailable (no DATABASE_URL)")
	}
	rec := testHandler.CausalRecorder
	if rec == nil {
		t.Fatal("Handler.CausalRecorder is nil — the 0.5.84 wiring regressed")
	}
	// Force flag OFF for this test. Cleanup flips every other
	// test's pref row off too, which is fine — flag-on tests run
	// after this one call enableCausalGraphFlag themselves.
	if _, err := testPool.Exec(context.Background(), `DELETE FROM experimental_pref WHERE flag_key = 'causal_graph'`); err != nil {
		t.Fatalf("reset flag: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM experimental_pref WHERE flag_key = 'causal_graph'`)
	})
	ctx := t.Context()

	issue := causalTestIssue(t, "Refresh flag-off")
	issueRow, err := testHandler.Queries.GetIssue(ctx, mustParseUUID(t, issue))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}

	// Seed a node directly (the recorder itself is silent under
	// flag-off, so it can't author one for us).
	nodeID := uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO causal_node (id, workspace_id, issue_id, type, label, provenance, status)
		VALUES ($1, $2, $3, 'decision', 'seed', '{"source":"test"}', 'active')
	`, nodeID, issueRow.WorkspaceID, issueRow.ID); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	forceNodeStale(t, ctx, pgtype.UUID{Valid: true, Bytes: nodeID})
	preAge := time.Since(lastObserved(t, ctx, pgtype.UUID{Valid: true, Bytes: nodeID}))
	if preAge < 30*24*time.Hour {
		t.Fatalf("stale-seed failed: age=%v, want ≥30d", preAge)
	}

	rec.RefreshForIssue(ctx, issueRow.ID)

	postAge := time.Since(lastObserved(t, ctx, pgtype.UUID{Valid: true, Bytes: nodeID}))
	if postAge < 30*24*time.Hour {
		t.Errorf("flag-off refresh touched the row: age=%v, want ≥30d (unchanged)", postAge)
	}
}

// TestCausalReadsList — the 0.5.121 read-receipt read path. Receipts are
// written by the daemon claim seam (daemon.go); this pin covers the
// gated listing endpoint end-to-end against real rows: newest first,
// agent name joined, source echoed, workspace membership enforced by
// the shared loadIssueForUser path.
func TestCausalReadsList(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler fixture unavailable (no DATABASE_URL)")
	}
	ctx := t.Context()

	enableCausalGraphFlag(t, ctx)

	// Create a throwaway issue + agent + two receipts (older first).
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":  "causal reads issue " + time.Now().Format(time.RFC3339Nano),
		"status": "todo",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create issue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var issue IssueResponse
	json.NewDecoder(w.Body).Decode(&issue)
	t.Cleanup(func() {
		cctx := context.Background()
		testPool.Exec(cctx, `DELETE FROM causal_graph_read_receipt WHERE issue_id = $1::uuid`, issue.ID)
		testPool.Exec(cctx, `DELETE FROM causal_node WHERE issue_id = $1::uuid`, issue.ID)
		testPool.Exec(cctx, `DELETE FROM issue WHERE id = $1`, issue.ID)
	})

	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_config, visibility)
		VALUES ($1::uuid, 'causal-read-agent-test', 'local', '{}'::jsonb, 'workspace')
		RETURNING id`, testWorkspaceID).Scan(&agentID); err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1::uuid`, agentID)
	})

	for _, src := range []string{"claim_brief", "claim_brief"} {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO causal_graph_read_receipt (workspace_id, issue_id, agent_id, source)
			VALUES ($1::uuid, $2::uuid, $3::uuid, $4)`,
			testWorkspaceID, issue.ID, agentID, src); err != nil {
			t.Fatalf("insert receipt: %v", err)
		}
	}

	w2 := httptest.NewRecorder()
	req2 := newRequest("GET", "/api/causal-graph/reads?issue_id="+issue.ID, nil)
	testHandler.causalReads(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("GET reads: expected 200, got %d: %s", w2.Code, w2.Body.String())
	}
	var body struct {
		Reads []struct {
			AgentID   string `json:"agent_id"`
			AgentName string `json:"agent_name"`
			Source    string `json:"source"`
		} `json:"reads"`
	}
	if err := json.NewDecoder(w2.Body).Decode(&body); err != nil {
		t.Fatalf("decode reads: %v", err)
	}
	if len(body.Reads) != 2 {
		t.Fatalf("expected 2 reads, got %d", len(body.Reads))
	}
	if body.Reads[0].AgentName != "causal-read-agent-test" {
		t.Errorf("agent_name = %q, want joined name", body.Reads[0].AgentName)
	}
	if body.Reads[0].Source != "claim_brief" {
		t.Errorf("source = %q, want claim_brief", body.Reads[0].Source)
	}
	if body.Reads[0].AgentID != agentID {
		t.Errorf("agent_id = %q, want %q", body.Reads[0].AgentID, agentID)
	}
}
