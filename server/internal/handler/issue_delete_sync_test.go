package handler

// 0.5.116 issue-deletion sync pins:
//   - bare-FK detach family (mythos_run root/final, mythos_members
//     .result_issue_id) runs BEFORE the cascade delete — an unreferenced
//     delete used to 23503 into a generic 500 (verified live: deleting a
//     mythos-referenced issue violated mythos_run_root_issue_id_fkey).
//   - retired swarm tombstone rows (root_issue_id NOT NULL, mig 241) are
//     removed; terminal mythos runs keep their status while the link NULLs.
//   - batch delete now runs the pythia termination closure (Active Contract
//     #10 parity — the batch path had drifted from the single path).
//   - mig 291 FKs: session.issue_id SET NULL on issue delete; artifact rows
//     CASCADE away with their session (before it, no production path ever
//     deleted an artifact row).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
)

func createDeleteSyncIssue(t *testing.T, ws, creator pgtype.UUID, title string, number int32) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO issue (workspace_id, creator_id, creator_type, title, status, number)
		VALUES ($1, $2, 'member', $3, 'todo', $4)
		RETURNING id`, ws, creator, title, number).Scan(&id)
	if err != nil {
		t.Fatalf("create issue %q: %v", title, err)
	}
	return id
}

func TestDeleteIssueDetachBareFKReferences(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}
	ctx := context.Background()
	wsUUID := mustParseUUID(t, testWorkspaceID)
	memberID := mustCreateTestMember(t, wsUUID)

	issueID := createDeleteSyncIssue(t, wsUUID, memberID, "delete-sync-mythos", 987200)
	agentID := mustCreateTestAgent(t, wsUUID, "delete-sync-agent-"+t.Name(), memberID)

	var supervisingRunID, completedRunID, swarmRunID pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		INSERT INTO mythos_run (workspace_id, creator_user_id, problem, status, root_issue_id, mode, supervision_state)
		VALUES ($1, $2, 'delete-sync audit', 'supervising', $3, 'enhancer', '{"phase":"supervising"}'::jsonb)
		RETURNING id`, wsUUID, memberID, issueID).Scan(&supervisingRunID); err != nil {
		t.Fatalf("insert supervising mythos_run: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO mythos_run (workspace_id, creator_user_id, problem, status, final_issue_id, mode)
		VALUES ($1, $2, 'delete-sync audit', 'completed', $3, 'enhancer')
		RETURNING id`, wsUUID, memberID, issueID).Scan(&completedRunID); err != nil {
		t.Fatalf("insert completed mythos_run: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO mythos_members (run_id, agent_id, role, result_issue_id)
		VALUES ($1, $2, 'coda', $3)`, supervisingRunID, agentID, issueID); err != nil {
		t.Fatalf("insert mythos_members: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO swarm_run (workspace_id, creator_user_id, root_issue_id, problem, status)
		VALUES ($1, $2, $3, 'delete-sync audit', 'failed')
		RETURNING id`, wsUUID, memberID, issueID).Scan(&swarmRunID); err != nil {
		t.Fatalf("insert swarm_run: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM agent WHERE id = $1`, agentID)
		_, _ = testPool.Exec(ctx, `DELETE FROM mythos_members WHERE run_id = $1`, supervisingRunID)
		_, _ = testPool.Exec(ctx, `DELETE FROM mythos_run WHERE id IN ($1, $2)`, supervisingRunID, completedRunID)
		_, _ = testPool.Exec(ctx, `DELETE FROM swarm_run WHERE id = $1`, swarmRunID)
		_, _ = testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID)
	})

	// Direct handler call: chi route params don't exist outside the router —
	// stub the {id} param the way the route would.
	req := withChiURLParam(newRequest("DELETE", "/api/issues/"+util.UUIDToString(issueID), nil), "id", util.UUIDToString(issueID))
	rec := httptest.NewRecorder()
	testHandler.DeleteIssue(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE issue: expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	var rootNull bool
	var status string
	if err := testPool.QueryRow(ctx,
		`SELECT root_issue_id IS NULL, status FROM mythos_run WHERE id = $1`, supervisingRunID,
	).Scan(&rootNull, &status); err != nil {
		t.Fatalf("supervising run missing: %v", err)
	}
	if !rootNull || status != "aborted" {
		t.Errorf("supervising run: root_null=%v status=%q, want true/aborted", rootNull, status)
	}
	var finalNull bool
	var completedStatus string
	if err := testPool.QueryRow(ctx,
		`SELECT final_issue_id IS NULL, status FROM mythos_run WHERE id = $1`, completedRunID,
	).Scan(&finalNull, &completedStatus); err != nil {
		t.Fatalf("completed run missing: %v", err)
	}
	if !finalNull || completedStatus != "completed" {
		t.Errorf("completed run: final_null=%v status=%q, want true/completed (terminal rows keep status)", finalNull, completedStatus)
	}
	var resultNull bool
	if err := testPool.QueryRow(ctx,
		`SELECT result_issue_id IS NULL FROM mythos_members WHERE run_id = $1`, supervisingRunID,
	).Scan(&resultNull); err != nil {
		t.Fatalf("member missing: %v", err)
	}
	if !resultNull {
		t.Errorf("member result_issue_id should be NULL after issue delete")
	}
	var swarmGone bool
	if err := testPool.QueryRow(ctx,
		`SELECT NOT EXISTS (SELECT 1 FROM swarm_run WHERE id = $1)`, swarmRunID,
	).Scan(&swarmGone); err != nil {
		t.Fatalf("swarm probe: %v", err)
	}
	if !swarmGone {
		t.Errorf("retired swarm tombstone row should be deleted with its root issue")
	}
}

func TestBatchDeleteIssuesPreCleanParity(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}
	ctx := context.Background()
	wsUUID := mustParseUUID(t, testWorkspaceID)
	memberID := mustCreateTestMember(t, wsUUID)
	agentID := mustCreateTestAgent(t, wsUUID, "batch-sync-agent-"+t.Name(), memberID)

	// Batch-deleting an issue cascades its pythia_forecast_run rows away, so
	// the abort itself is not observable post-hoc (the abort call is pinned
	// in TestPythiaTerminationClosureAndMonitor; this test pins the shared
	// pre-clean BLOCK in BatchDeleteIssues — removing it breaks the mythos
	// assertions below). The mythos bare-FK family IS observable: run/member
	// rows survive the delete with their links detached and in-flight runs
	// flipped to aborted.
	issueID := createDeleteSyncIssue(t, wsUUID, memberID, "delete-sync-batch", 987201)
	otherIssueID := createDeleteSyncIssue(t, wsUUID, memberID, "delete-sync-batch-control", 987203)

	var supervisingRunID, controlRunID pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		INSERT INTO mythos_run (workspace_id, creator_user_id, problem, status, root_issue_id, mode, supervision_state)
		VALUES ($1, $2, 'batch sync audit', 'supervising', $3, 'enhancer', '{"phase":"supervising"}'::jsonb)
		RETURNING id`, wsUUID, memberID, issueID).Scan(&supervisingRunID); err != nil {
		t.Fatalf("insert supervising mythos_run: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO pythia_forecast_run (workspace_id, issue_id, rounds, source, envelopes, run_kind, status)
		VALUES ($1, $2, 2, 'synthetic', '[]', 'initial', 'running')
		RETURNING id`, wsUUID, otherIssueID).Scan(&controlRunID); err != nil {
		t.Fatalf("insert control pythia run: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM agent WHERE id = $1`, agentID)
		_, _ = testPool.Exec(ctx, `DELETE FROM mythos_run WHERE id = $1`, supervisingRunID)
		_, _ = testPool.Exec(ctx, `DELETE FROM pythia_forecast_run WHERE id IN ($1, $2)`, controlRunID, supervisingRunID)
		_, _ = testPool.Exec(ctx, `DELETE FROM issue WHERE id IN ($1, $2)`, issueID, otherIssueID)
	})

	req := newRequest("POST", "/api/issues/batch-delete", map[string]any{
		"issue_ids": []string{util.UUIDToString(issueID)},
	})
	rec := httptest.NewRecorder()
	testHandler.BatchDeleteIssues(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch delete: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Deleted int `json:"deleted"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Deleted != 1 {
		t.Fatalf("batch delete response %q (err=%v): want deleted=1", rec.Body.String(), err)
	}

	// The bare-FK pre-clean ran: the run survived the cascade with its root
	// link detached and the in-flight run flipped to aborted.
	var rootNull bool
	var status string
	if err := testPool.QueryRow(ctx,
		`SELECT root_issue_id IS NULL, status FROM mythos_run WHERE id = $1`, supervisingRunID,
	).Scan(&rootNull, &status); err != nil {
		t.Fatalf("mythos run missing after batch delete: %v", err)
	}
	if !rootNull || status != "aborted" {
		t.Errorf("batch delete must detach + abort the supervising mythos run, got root_null=%v status=%q", rootNull, status)
	}
	// Control: a run bound to a different issue is untouched.
	var controlStatus string
	if err := testPool.QueryRow(ctx,
		`SELECT status FROM pythia_forecast_run WHERE id = $1`, controlRunID,
	).Scan(&controlStatus); err != nil {
		t.Fatalf("control pythia run missing: %v", err)
	}
	if controlStatus != "running" {
		t.Errorf("control run on another issue must stay untouched, got %q", controlStatus)
	}
}

func TestRuntimeSessionIssueDeleteFKBehavior(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}
	ctx := context.Background()
	wsUUID := mustParseUUID(t, testWorkspaceID)
	memberID := mustCreateTestMember(t, wsUUID)

	issueID := createDeleteSyncIssue(t, wsUUID, memberID, "delete-sync-runtime", 987202)
	var sessionID pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		INSERT INTO experimental_claude_runtime_session (workspace_id, agent_id, issue_id, language, code)
		VALUES ($1, $2, $3, 'python', 'print(1)')
		RETURNING id`, wsUUID, memberID, issueID).Scan(&sessionID); err != nil {
		t.Fatalf("insert runtime session: %v", err)
	}
	var artifactID pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		INSERT INTO experimental_runtime_artifact (session_id, workspace_id, issue_id, name, kind, bytes, sha256, path)
		VALUES ($1, $2, $3, 'fig.png', 'png', 12, 'deadbeef', 'runs/s1/fig.png')
		RETURNING id`, sessionID, wsUUID, issueID).Scan(&artifactID); err != nil {
		t.Fatalf("insert artifact: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM experimental_runtime_artifact WHERE id = $1`, artifactID)
		_, _ = testPool.Exec(ctx, `DELETE FROM experimental_claude_runtime_session WHERE id = $1`, sessionID)
		_, _ = testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID)
	})

	// Deleting the issue detaches (SET NULL) — the session keeps its history
	// until the 30-day GC, and the by-issue endpoints return nothing.
	if _, err := testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID); err != nil {
		t.Fatalf("delete issue: %v", err)
	}
	var issueNull bool
	if err := testPool.QueryRow(ctx,
		`SELECT issue_id IS NULL FROM experimental_claude_runtime_session WHERE id = $1`, sessionID,
	).Scan(&issueNull); err != nil {
		t.Fatalf("session missing: %v", err)
	}
	if !issueNull {
		t.Errorf("session.issue_id should be NULL after the issue is deleted (mig 291 SET NULL)")
	}
	// Deleting the session cascades the artifact rows away (mig 291 CASCADE).
	if _, err := testPool.Exec(ctx, `DELETE FROM experimental_claude_runtime_session WHERE id = $1`, sessionID); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	var artifactGone bool
	if err := testPool.QueryRow(ctx,
		`SELECT NOT EXISTS (SELECT 1 FROM experimental_runtime_artifact WHERE id = $1)`, artifactID,
	).Scan(&artifactGone); err != nil {
		t.Fatalf("artifact probe: %v", err)
	}
	if !artifactGone {
		t.Errorf("artifact rows must cascade away with their session (mig 291)")
	}
}
