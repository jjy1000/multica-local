// Package handler — causal_graph_test.go (0.5.83 WL3)
//
// Regression pins for the causal-graph surface (roadmap §3.6 item 25):
//
//   - TestIssueDependencyRevive — the mig-276 CRUD round trip through
//     the gated REST path (loader scoping, self-dependency + duplicate
//     guards, delete).
//   - TestCausalGraphSubgraphDepth — N-hop BFS slice correctness and
//     the [1,4] depth clamp (the seam both the UI popup and the next
//     phase's agent-context injection read through).
//   - TestCausalEdgeSuggestGate — the Tier D curation gate: suggested
//     edges confirm/reject, decided edges 409, and the partial unique
//     active-triple index surfaces as 409 (never a raw 500).
//   - TestCausalGraphRecorderFlagGate — the Tier A recorder is silent
//     while the flag is off and idempotent while it is on.
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

	"github.com/go-chi/chi/v5"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// causalTestIssue reuses the timesfm fixture helper (same package) and
// renames the concept for readability here.
func causalTestIssue(t *testing.T, title string) string {
	t.Helper()
	return timesfmTestIssue(t, title)
}

// causalCreateNode is the POST /api/causal-graph/nodes helper.
func causalCreateNode(t *testing.T, body map[string]any) causalNodeJSON {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/causal-graph/nodes?workspace_id="+testWorkspaceID, body)
	testHandler.createCausalNode(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create node: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var node causalNodeJSON
	if err := json.NewDecoder(w.Body).Decode(&node); err != nil {
		t.Fatalf("decode node: %v", err)
	}
	return node
}

// causalCreateEdge is the POST /api/causal-graph/edges helper. Returns
// the recorder so callers can inspect the status code (conflict tests).
func causalCreateEdgeRaw(t *testing.T, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/causal-graph/edges?workspace_id="+testWorkspaceID, body)
	testHandler.createCausalEdge(w, req)
	return w
}

func causalGetSubgraph(t *testing.T, issueID string, depth string) (int, map[string]any) {
	t.Helper()
	path := "/api/causal-graph/subgraph?issue_id=" + issueID + "&workspace_id=" + testWorkspaceID
	if depth != "" {
		path += "&depth=" + depth
	}
	w := httptest.NewRecorder()
	req := newRequest("GET", path, nil)
	testHandler.causalSubgraph(w, req)
	if w.Code != http.StatusOK {
		return w.Code, nil
	}
	var out map[string]any
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatalf("decode subgraph: %v", err)
	}
	return w.Code, out
}

func TestIssueDependencyRevive(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler fixture unavailable (no DATABASE_URL)")
	}
	issueA := causalTestIssue(t, "Causal dep A")
	issueB := causalTestIssue(t, "Causal dep B")

	// Create: defaults to blocked_by.
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/"+issueA+"/dependencies?workspace_id="+testWorkspaceID, map[string]any{
		"target_issue_id": issueB,
	})
	req = withURLParam(req, "issueID", issueA)
	testHandler.createIssueDependency(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create dependency: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var dep issueDependencyJSON
	if err := json.NewDecoder(w.Body).Decode(&dep); err != nil {
		t.Fatalf("decode dependency: %v", err)
	}
	if dep.Type != "blocked_by" {
		t.Errorf("default dependency type = %q, want blocked_by", dep.Type)
	}

	// List: outgoing from A, incoming on B.
	w = httptest.NewRecorder()
	req = newRequest("GET", "/api/issues/"+issueA+"/dependencies?workspace_id="+testWorkspaceID, nil)
	req = withURLParam(req, "issueID", issueA)
	testHandler.listIssueDependencies(w, req)
	var listed struct {
		Dependencies []issueDependencyJSON `json:"dependencies"`
		Dependents   []issueDependencyJSON `json:"dependents"`
	}
	if err := json.NewDecoder(w.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Dependencies) != 1 || len(listed.Dependents) != 0 {
		t.Fatalf("issue A list = %+v, want 1 dependency / 0 dependents", listed)
	}
	w = httptest.NewRecorder()
	req = newRequest("GET", "/api/issues/"+issueB+"/dependencies?workspace_id="+testWorkspaceID, nil)
	req = withURLParam(req, "issueID", issueB)
	testHandler.listIssueDependencies(w, req)
	if err := json.NewDecoder(w.Body).Decode(&listed); err != nil {
		t.Fatalf("decode reverse list: %v", err)
	}
	if len(listed.Dependents) != 1 {
		t.Fatalf("issue B dependents = %d, want 1", len(listed.Dependents))
	}

	// Duplicate pair → 409.
	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues/"+issueA+"/dependencies?workspace_id="+testWorkspaceID, map[string]any{
		"target_issue_id": issueB,
	})
	req = withURLParam(req, "issueID", issueA)
	testHandler.createIssueDependency(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("duplicate dependency: expected 409, got %d", w.Code)
	}

	// Self-dependency → 400.
	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues/"+issueA+"/dependencies?workspace_id="+testWorkspaceID, map[string]any{
		"target_issue_id": issueA,
	})
	req = withURLParam(req, "issueID", issueA)
	testHandler.createIssueDependency(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("self dependency: expected 400, got %d", w.Code)
	}

	// Bad type → 400.
	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues/"+issueA+"/dependencies?workspace_id="+testWorkspaceID, map[string]any{
		"target_issue_id": issueB,
		"type":            "loves",
	})
	req = withURLParam(req, "issueID", issueA)
	testHandler.createIssueDependency(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad type: expected 400, got %d", w.Code)
	}

	// Delete → gone.
	w = httptest.NewRecorder()
	req = newRequest("DELETE", "/api/issues/"+issueA+"/dependencies/"+dep.ID+"?workspace_id="+testWorkspaceID, nil)
	// withURLParam REPLACES the route context, so chained calls lose
	// the first key — both params go into one context.
	{
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("issueID", issueA)
		rctx.URLParams.Add("dependencyID", dep.ID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	}
	testHandler.deleteIssueDependency(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete dependency: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	req = newRequest("GET", "/api/issues/"+issueA+"/dependencies?workspace_id="+testWorkspaceID, nil)
	req = withURLParam(req, "issueID", issueA)
	testHandler.listIssueDependencies(w, req)
	if err := json.NewDecoder(w.Body).Decode(&listed); err != nil {
		t.Fatalf("decode post-delete list: %v", err)
	}
	if len(listed.Dependencies) != 0 {
		t.Errorf("dependencies after delete = %d, want 0", len(listed.Dependencies))
	}
}

func TestCausalGraphSubgraphDepth(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler fixture unavailable (no DATABASE_URL)")
	}
	issue := causalTestIssue(t, "Causal subgraph seed")

	a := causalCreateNode(t, map[string]any{"label": "action A", "type": "action", "issue_id": issue})
	b := causalCreateNode(t, map[string]any{"label": "evidence B", "type": "evidence"})
	c := causalCreateNode(t, map[string]any{"label": "outcome C", "type": "outcome"})

	if w := causalCreateEdgeRaw(t, map[string]any{"from_node_id": a.ID, "to_node_id": b.ID, "type": "causes"}); w.Code != http.StatusCreated {
		t.Fatalf("edge A->B: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if w := causalCreateEdgeRaw(t, map[string]any{"from_node_id": b.ID, "to_node_id": c.ID, "type": "enables"}); w.Code != http.StatusCreated {
		t.Fatalf("edge B->C: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// Depth 1: seed + direct neighbours only.
	_, sub1 := causalGetSubgraph(t, issue, "1")
	if len(sub1["nodes"].([]any)) != 2 || len(sub1["edges"].([]any)) != 1 {
		t.Fatalf("depth 1 = %d nodes / %d edges, want 2/1", len(sub1["nodes"].([]any)), len(sub1["edges"].([]any)))
	}
	// Depth 2: the whole chain.
	_, sub2 := causalGetSubgraph(t, issue, "2")
	if len(sub2["nodes"].([]any)) != 3 || len(sub2["edges"].([]any)) != 2 {
		t.Fatalf("depth 2 = %d nodes / %d edges, want 3/2", len(sub2["nodes"].([]any)), len(sub2["edges"].([]any)))
	}
	// Clamp: 99 behaves as 4.
	_, sub99 := causalGetSubgraph(t, issue, "99")
	if len(sub99["nodes"].([]any)) != 3 {
		t.Fatalf("depth 99 = %d nodes, want 3 (clamped)", len(sub99["nodes"].([]any)))
	}

	// An issue with no nodes yields an empty slice, not null.
	empty := causalTestIssue(t, "Causal subgraph empty")
	code, subE := causalGetSubgraph(t, empty, "2")
	if code != http.StatusOK {
		t.Fatalf("empty subgraph: expected 200, got %d", code)
	}
	if subE["nodes"] == nil || len(subE["nodes"].([]any)) != 0 {
		t.Fatalf("empty subgraph nodes = %v, want empty slice", subE["nodes"])
	}
}

func TestCausalEdgeSuggestGate(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler fixture unavailable (no DATABASE_URL)")
	}
	a := causalCreateNode(t, map[string]any{"label": "gate A", "type": "action"})
	b := causalCreateNode(t, map[string]any{"label": "gate B", "type": "outcome"})
	c := causalCreateNode(t, map[string]any{"label": "gate C", "type": "evidence"})

	// Manual edge lands active immediately.
	w := causalCreateEdgeRaw(t, map[string]any{"from_node_id": a.ID, "to_node_id": b.ID, "type": "causes"})
	if w.Code != http.StatusCreated {
		t.Fatalf("manual edge: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var active causalEdgeJSON
	if err := json.NewDecoder(w.Body).Decode(&active); err != nil {
		t.Fatalf("decode edge: %v", err)
	}
	if active.Status != "active" {
		t.Fatalf("manual edge status = %q, want active", active.Status)
	}

	// A second ACTIVE edge with the same (from, to, type) hits the
	// partial unique index → 409 (SQLSTATE 23514 mapped), never 500.
	w = causalCreateEdgeRaw(t, map[string]any{"from_node_id": a.ID, "to_node_id": b.ID, "type": "causes"})
	if w.Code != http.StatusConflict {
		t.Errorf("duplicate active edge: expected 409, got %d: %s", w.Code, w.Body.String())
	}

	// Seed a suggested proposal directly (Tier D writers do this in the
	// next phase) on the free supports triple, then confirm it flips
	// suggested -> active.
	if _, err := testHandler.Queries.CreateCausalEdge(t.Context(), db.CreateCausalEdgeParams{
		WorkspaceID: mustParseUUID(t, testWorkspaceID),
		FromNodeID:  mustParseUUID(t, a.ID),
		ToNodeID:    mustParseUUID(t, b.ID),
		EdgeType:    "supports",
		EdgeStatus:  pgtype.Text{Valid: true, String: "suggested"},
		ProposedBy:  pgtype.Text{Valid: true, String: "curator"},
		Provenance:  []byte(`{"source":"test"}`),
	}); err != nil {
		t.Fatalf("seed suggested edge: %v", err)
	}
	var suggestedRow db.CausalEdge
	suggestedRows, err := testHandler.Queries.ListCausalEdges(t.Context(), db.ListCausalEdgesParams{
		WorkspaceID: mustParseUUID(t, testWorkspaceID),
		EdgeStatus:  pgtype.Text{Valid: true, String: "suggested"},
	})
	if err != nil || len(suggestedRows) == 0 {
		t.Fatalf("list suggested edges: %v (%d rows)", err, len(suggestedRows))
	}
	suggestedRow = suggestedRows[0]
	w = httptest.NewRecorder()
	req := newRequest("POST", "/api/causal-graph/edges/"+suggestedIDToString(suggestedRow.ID)+"/confirm?workspace_id="+testWorkspaceID, nil)
	req = withURLParam(req, "edgeID", suggestedIDToString(suggestedRow.ID))
	testHandler.confirmCausalEdge(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("confirm: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var confirmed causalEdgeJSON
	if err := json.NewDecoder(w.Body).Decode(&confirmed); err != nil {
		t.Fatalf("decode confirmed: %v", err)
	}
	if confirmed.Status != "active" {
		t.Errorf("confirmed status = %q, want active", confirmed.Status)
	}

	// Re-confirming a decided edge → 409.
	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/causal-graph/edges/"+suggestedIDToString(suggestedRow.ID)+"/confirm?workspace_id="+testWorkspaceID, nil)
	req = withURLParam(req, "edgeID", suggestedIDToString(suggestedRow.ID))
	testHandler.confirmCausalEdge(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("re-confirm: expected 409, got %d", w.Code)
	}

	// Seed + reject another proposal; a rejected edge is gone.
	if _, err := testHandler.Queries.CreateCausalEdge(t.Context(), db.CreateCausalEdgeParams{
		WorkspaceID: mustParseUUID(t, testWorkspaceID),
		FromNodeID:  mustParseUUID(t, a.ID),
		ToNodeID:    mustParseUUID(t, c.ID),
		EdgeType:    "blocks",
		EdgeStatus:  pgtype.Text{Valid: true, String: "suggested"},
		ProposedBy:  pgtype.Text{Valid: true, String: "evolver"},
		Provenance:  []byte(`{"source":"test"}`),
	}); err != nil {
		t.Fatalf("seed reject-candidate edge: %v", err)
	}
	rejectRows, err := testHandler.Queries.ListCausalEdges(t.Context(), db.ListCausalEdgesParams{
		WorkspaceID: mustParseUUID(t, testWorkspaceID),
		EdgeStatus:  pgtype.Text{Valid: true, String: "suggested"},
	})
	if err != nil || len(rejectRows) == 0 {
		t.Fatalf("list reject candidates: %v (%d rows)", err, len(rejectRows))
	}
	target := rejectRows[0]
	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/causal-graph/edges/"+suggestedIDToString(target.ID)+"/reject?workspace_id="+testWorkspaceID, nil)
	req = withURLParam(req, "edgeID", suggestedIDToString(target.ID))
	testHandler.rejectCausalEdge(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("reject: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if _, err := testHandler.Queries.GetCausalEdge(t.Context(), target.ID); err == nil {
		t.Errorf("rejected edge still exists")
	}
}

// TestCausalGraphRecorderFlagGate pins the Tier A contract end to end
// through the real recorder: flag-off writes NOTHING, flag-on writes
// the root/action/edge group, and a re-fire is a no-op (the action
// node is the idempotency marker).
func TestCausalGraphRecorderFlagGate(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler fixture unavailable (no DATABASE_URL)")
	}
	rec := testHandler.TaskService.CausalRecorder
	if rec == nil {
		t.Fatal("TaskService.CausalRecorder is nil — the WL3 wiring regressed")
	}
	userUUID := mustParseUUID(t, testUserID)
	ctx := t.Context()

	// Start from OFF for this flag. Any-user semantics + a shared dev
	// DB mean rows from earlier (possibly crashed) fixture-user
	// incarnations count — clear them all, not just this run's user.
	if _, err := testPool.Exec(ctx, `DELETE FROM experimental_pref WHERE flag_key = 'causal_graph'`); err != nil {
		t.Fatalf("reset pref rows: %v", err)
	}
	t.Cleanup(func() {
		// context.Background, NOT t.Context: the test context is
		// cancelled before cleanup functions run, which silently
		// killed the DELETE and leaked an enabled row into
		// TestListExperimentalFlags_DefaultValues.
		_, _ = testPool.Exec(context.Background(), `DELETE FROM experimental_pref WHERE flag_key = 'causal_graph'`)
	})

	issue := causalTestIssue(t, "Causal recorder gate")
	issueRow, err := testHandler.Queries.GetIssue(ctx, mustParseUUID(t, issue))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	taskID := uuid.New()
	task := db.AgentTaskQueue{
		ID:      pgtype.UUID{Valid: true, Bytes: taskID},
		IssueID: pgtype.UUID{Valid: true, Bytes: issueRow.ID.Bytes},
	}

	// Flag OFF: zero nodes.
	rec.RecordTaskAction(ctx, issueRow, task, "issue")
	nodes, err := testHandler.Queries.ListCausalNodesByIssue(ctx, issueRow.ID)
	if err != nil {
		t.Fatalf("list nodes off-flag: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("flag-off recorder wrote %d nodes, want 0", len(nodes))
	}

	// Flag ON: root + action + enables edge.
	if _, err := testHandler.Queries.UpsertExperimentalPref(ctx, db.UpsertExperimentalPrefParams{
		UserID:  userUUID,
		FlagKey: "causal_graph",
		Enabled: true,
	}); err != nil {
		t.Fatalf("enable flag: %v", err)
	}
	if !rec.Enabled(ctx) {
		t.Fatal("recorder reports disabled after enabling the flag")
	}
	rec.RecordTaskAction(ctx, issueRow, task, "issue")
	nodes, err = testHandler.Queries.ListCausalNodesByIssue(ctx, issueRow.ID)
	if err != nil {
		t.Fatalf("list nodes on-flag: %v", err)
	}
	if len(nodes) != 2 { // root constraint + action
		t.Fatalf("flag-on recorder wrote %d nodes, want 2 (root + action)", len(nodes))
	}

	// Idempotency: a second fire writes nothing new.
	rec.RecordTaskAction(ctx, issueRow, task, "issue")
	nodes, err = testHandler.Queries.ListCausalNodesByIssue(ctx, issueRow.ID)
	if err != nil {
		t.Fatalf("re-list nodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("re-fire wrote extra nodes: %d, want 2", len(nodes))
	}

	// Outcome: completes the chain (root -> action -> outcome).
	rec.RecordTaskOutcome(ctx, task, "the run finished cleanly")
	outcome, err := testHandler.Queries.FindLatestOutcomeNodeForIssue(ctx, issueRow.ID)
	if err != nil {
		t.Fatalf("outcome node missing: %v", err)
	}
	if outcome.Type != "outcome" {
		t.Errorf("latest node type = %q, want outcome", outcome.Type)
	}
}

func suggestedIDToString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return uuid.UUID(b).String()
}
