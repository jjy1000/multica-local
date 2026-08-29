// Package handler — issue_child_done_lab_delegate_test.go (0.5.88)
//
// DB-backed pin for the DELEGATION LOOP's wake channel: a lab-delegated
// sub-issue (parent_issue_id + lab_source, assignee left empty — the
// exact shape `multica lab delegate --parent` creates via
// POST /api/issues) must flow through the EXISTING child-done
// notification when it transitions into a terminal status. The guards
// in notifyParentOfChildDone skip member-assigned and backlog parents;
// the parent shape here (in_progress, AGENT assignee) fires the system
// comment AND the parent-agent wake, proving a lab_source child is not
// structurally excluded.
//
// Companion pins in the same file:
//   - TestCreateDelegatedLabChildRecordsCausalDependsOnEdge — the
//     0.5.88 server-side causal edge (parent --depends_on--> child)
//     recorded on the CreateIssue success path, flag-gated + idempotent.
//   - TestDelegationBriefListsEnabledAssigneeLabs — the daemon
//     briefing builder against the real DB + leader tables, including
//     the frozen-lab skip.
//
// DB-backed cases are skipped by TestMain when DATABASE_URL is
// unreachable (same harness as issue_child_done_test.go).

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	causalgraph "github.com/multica-ai/multica/server/internal/service/causal_graph"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// createDelegatedLabChild creates the `multica lab delegate --parent`
// issue shape through the real HTTP create path: parent_issue_id +
// lab_source set, NO assignee (empty always passes the 0.5.86
// assignee-lock; the leader-rewrite is a no-op here because the test
// workspace has no pythia_runtime row, and pythia_oracle opts out of
// auto-dispatch — so nothing enqueues at create time).
func createDelegatedLabChild(t *testing.T, parentID, title string) IssueResponse {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":           title,
		"status":          "in_progress",
		"parent_issue_id": parentID,
		"lab_source":      "pythia_oracle",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create delegated lab child: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var child IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&child); err != nil {
		t.Fatalf("decode delegated lab child: %v", err)
	}
	if child.LabSource == nil || *child.LabSource != "pythia_oracle" {
		t.Fatalf("child lab_source = %v, want pythia_oracle", child.LabSource)
	}
	return child
}

// TestChildDoneNotifiesParentForDelegatedLabChild — the 0.5.88
// delegation-loop wake pin. Parent: in_progress with an AGENT assignee
// (the shape that fires the comment + wake; member-assigned and
// backlog parents are deliberately skipped by the guards). Child:
// parent_issue_id + lab_source=pythia_oracle, assignee empty. Flipping
// the child to `done` closes the single implicit stage (it is the only
// child) and must produce exactly one system comment on the parent
// embedding the parent-agent mention, plus exactly one pending wake
// task on the parent agent.
func TestChildDoneNotifiesParentForDelegatedLabChild(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler fixture unavailable (no DATABASE_URL)")
	}

	// Parent (no other children — the lab child is the sole sibling, so
	// its completion closes the implicit stage barrier).
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":  "delegation-loop parent " + time.Now().Format(time.RFC3339Nano),
		"status": "in_progress",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create parent: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var parent IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&parent); err != nil {
		t.Fatalf("decode parent: %v", err)
	}

	var agentID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT id FROM agent WHERE workspace_id = $1 AND name = $2`,
		testWorkspaceID, "Handler Test Agent",
	).Scan(&agentID); err != nil {
		t.Fatalf("locate test agent: %v", err)
	}
	// Direct assignee write avoids the assignment-trigger side effects
	// at setup (same technique as TestChildDoneMentionsParentAssignee_Agent).
	setIssueAssigneeDirect(t, parent.ID, "agent", agentID)

	child := createDelegatedLabChild(t, parent.ID, "delegation-loop lab child "+time.Now().Format(time.RFC3339Nano))

	t.Cleanup(func() {
		ctx := context.Background()
		testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE issue_id = $1`, parent.ID)
		// Cascades through comment.
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, child.ID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, parent.ID)
	})

	updateChildStatus(t, child.ID, "done")

	content := parentSystemCommentContent(t, parent.ID)
	if !strings.Contains(content, child.Identifier) {
		t.Errorf("expected child identifier %q in system comment, got: %s", child.Identifier, content)
	}
	if !strings.Contains(content, "mention://agent/"+agentID) {
		t.Errorf("expected parent-agent mention %q in system comment, got: %s", "mention://agent/"+agentID, content)
	}
	if got := countPendingTasksForAgent(t, parent.ID, agentID); got != 1 {
		t.Errorf("expected 1 pending wake task for the parent agent, got %d", got)
	}
}

// TestCreateDelegatedLabChildRecordsCausalDependsOnEdge — the 0.5.88
// server-side causal pin. With the causal_graph flag ON, creating the
// delegated-lab-child shape through POST /api/issues must record a
// parent --depends_on--> child edge between the two issues' root
// nodes; a re-record (retry / create+PATCH round trip) must stay at
// exactly one edge.
func TestCreateDelegatedLabChildRecordsCausalDependsOnEdge(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler fixture unavailable (no DATABASE_URL)")
	}
	rec := testHandler.TaskService.CausalRecorder
	if rec == nil {
		t.Fatal("TaskService.CausalRecorder is nil — the WL3 wiring regressed")
	}
	ctx := t.Context()
	userUUID := mustParseUUID(t, testUserID)

	// Start from OFF and clean ANY-user rows (shared dev DB — same
	// discipline as TestCausalGraphRecorderFlagGate).
	if _, err := testPool.Exec(ctx, `DELETE FROM experimental_pref WHERE flag_key = 'causal_graph'`); err != nil {
		t.Fatalf("reset pref rows: %v", err)
	}
	t.Cleanup(func() {
		// context.Background, NOT t.Context: the test context is
		// cancelled before cleanups run (see the recorder gate test).
		_, _ = testPool.Exec(context.Background(), `DELETE FROM experimental_pref WHERE flag_key = 'causal_graph'`)
	})
	if _, err := testHandler.Queries.UpsertExperimentalPref(ctx, db.UpsertExperimentalPrefParams{
		UserID:  userUUID,
		FlagKey: "causal_graph",
		Enabled: true,
	}); err != nil {
		t.Fatalf("enable flag: %v", err)
	}

	// Parent + delegated lab child through the HTTP create path.
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":  "causal delegation parent " + time.Now().Format(time.RFC3339Nano),
		"status": "in_progress",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create parent: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var parent IssueResponse
	json.NewDecoder(w.Body).Decode(&parent)
	child := createDelegatedLabChild(t, parent.ID, "causal delegation child "+time.Now().Format(time.RFC3339Nano))

	t.Cleanup(func() {
		cctx := context.Background()
		for _, issueID := range []string{parent.ID, child.ID} {
			testPool.Exec(cctx, `
				DELETE FROM causal_edge
				WHERE from_node_id IN (SELECT id FROM causal_node WHERE issue_id = $1::uuid)
				   OR to_node_id   IN (SELECT id FROM causal_node WHERE issue_id = $1::uuid)`, issueID)
			testPool.Exec(cctx, `DELETE FROM causal_node WHERE issue_id = $1::uuid`, issueID)
			testPool.Exec(cctx, `DELETE FROM issue WHERE id = $1`, issueID)
		}
	})

	parentUUID := mustParseUUID(t, parent.ID)
	childUUID := mustParseUUID(t, child.ID)
	wsUUID := mustParseUUID(t, testWorkspaceID)

	parentNode, err := testHandler.Queries.FindCausalNodeByDedupKey(ctx, db.FindCausalNodeByDedupKeyParams{
		WorkspaceID: wsUUID,
		DedupKey:    "issue_root:" + parent.ID,
	})
	if err != nil {
		t.Fatalf("parent root node missing: %v", err)
	}
	childNode, err := testHandler.Queries.FindCausalNodeByDedupKey(ctx, db.FindCausalNodeByDedupKeyParams{
		WorkspaceID: wsUUID,
		DedupKey:    "issue_root:" + child.ID,
	})
	if err != nil {
		t.Fatalf("child root node missing: %v", err)
	}
	if !childNode.LabSource.Valid || childNode.LabSource.String != "pythia_oracle" {
		t.Errorf("child root node lab_source = %v, want pythia_oracle", childNode.LabSource)
	}

	edge, err := testHandler.Queries.FindCausalEdgeBetween(ctx, db.FindCausalEdgeBetweenParams{
		FromNodeID: parentNode.ID,
		ToNodeID:   childNode.ID,
		EdgeType:   "depends_on",
	})
	if err != nil {
		t.Fatalf("expected parent --depends_on--> child edge, got error: %v", err)
	}
	if edge.FromNodeID != parentNode.ID || edge.ToNodeID != childNode.ID {
		t.Errorf("edge endpoints reversed: %s -> %s", uuid.UUID(edge.FromNodeID.Bytes).String(), uuid.UUID(edge.ToNodeID.Bytes).String())
	}

	// Idempotency: a re-record of the same linkage lands no second edge.
	rec.RecordDelegationEdge(ctx, db.Issue{
		ID:            childUUID,
		WorkspaceID:   wsUUID,
		ParentIssueID: pgtype.UUID{Valid: true, Bytes: parentUUID.Bytes},
		LabSource:     pgtype.Text{Valid: true, String: "pythia_oracle"},
	})
	var n int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM causal_edge
		WHERE from_node_id = $1::uuid AND to_node_id = $2::uuid AND type = 'depends_on'`,
		parentNode.ID, childNode.ID).Scan(&n); err != nil {
		t.Fatalf("count edges: %v", err)
	}
	if n != 1 {
		t.Fatalf("re-record wrote a second edge: count = %d, want 1", n)
	}
}

// TestDelegationBriefListsEnabledAssigneeLabs — the daemon briefing
// builder against the real DB + the real leader tables. claude_science_lab
// (assignee-model, auto-dispatch standard contract) must be listed when
// enabled; pythia_oracle must NEVER be listed even when enabled (0.5.88
// AutoDispatch=false skip — the live verification caught the first cut
// advertising a lab `lab delegate` can never dispatch), swarm_topology
// must NEVER be listed even when enabled (0.5.88 Frozen), and nothing
// renders when nothing delegatable is enabled.
func TestDelegationBriefListsEnabledAssigneeLabs(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler fixture unavailable (no DATABASE_URL)")
	}
	ctx := t.Context()
	userUUID := mustParseUUID(t, testUserID)

	// The dev DB is shared and long-lived: leftover enabled pref rows for
	// the assignee-model labs would defeat the empty-when-none baseline.
	// Clear the whole assignee-model family up front (same
	// key-targeted discipline as TestCausalGraphRecorderFlagGate — the
	// user's non-lab pref rows are untouched).
	const assigneeModelKeys = "'claude_science_lab','pythia_oracle','mythos_swarm','semantica','timesfm','swarm_topology'"
	if _, err := testPool.Exec(ctx, `DELETE FROM experimental_pref WHERE flag_key IN (`+assigneeModelKeys+`)`); err != nil {
		t.Fatalf("reset pref rows: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM experimental_pref WHERE flag_key IN ('claude_science_lab','pythia_oracle', 'swarm_topology')`)
	})

	leaderFor := func(key string) (string, bool) { return defaultLabLeaderForKey(key) }

	// Nothing enabled → nothing rendered (empty-when-none against the
	// live DB).
	brief, err := causalgraph.BuildDelegateBrief(ctx, testHandler.Queries, leaderFor, "issue-1")
	if err != nil {
		t.Fatalf("empty brief: %v", err)
	}
	if brief != "" {
		t.Fatalf("expected no section with no labs enabled, got %q", brief)
	}

	// Enable claude_science_lab (delegatable) AND pythia_oracle
	// (AutoDispatch=false) AND the frozen swarm_topology: only claude
	// may appear.
	for _, key := range []string{"claude_science_lab", "pythia_oracle", "swarm_topology"} {
		if _, err := testHandler.Queries.UpsertExperimentalPref(ctx, db.UpsertExperimentalPrefParams{
			UserID:  userUUID,
			FlagKey: key,
			Enabled: true,
		}); err != nil {
			t.Fatalf("enable %s: %v", key, err)
		}
	}
	brief, err = causalgraph.BuildDelegateBrief(ctx, testHandler.Queries, leaderFor, "issue-1")
	if err != nil {
		t.Fatalf("brief with labs: %v", err)
	}
	if !strings.Contains(brief, "## Available Labs (delegation)") {
		t.Fatalf("missing heading, got %q", brief)
	}
	if !strings.Contains(brief, "- claude_science_lab (leader: research)") {
		t.Errorf("expected claude_science_lab line, got %q", brief)
	}
	if strings.Contains(brief, "pythia_oracle") {
		t.Errorf("AutoDispatch=false pythia_oracle must never be advertised (guaranteed delegate timeout), got %q", brief)
	}
	if strings.Contains(brief, "swarm_topology") {
		t.Errorf("frozen swarm_topology must never be advertised, got %q", brief)
	}
	if !strings.Contains(brief, "multica lab delegate --parent issue-1") {
		t.Errorf("expected delegate command line, got %q", brief)
	}
}
