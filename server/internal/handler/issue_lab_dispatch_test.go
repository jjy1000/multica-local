package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
)

// ensureReadyResearchAgent returns the workspace's `research` leader agent
// (the claude_science_lab default leader resolved by
// defaultLabLeaderForKey), creating it if the fixture / lab install has not
// already. In either case it forces a bound runtime and clears archived_at
// so WillEnqueueRun treats it as ready — the dispatch decision must not hinge
// on whether a live daemon happened to rebind the agent in the test env.
func ensureReadyResearchAgent(t *testing.T, wsID pgtype.UUID, owner pgtype.UUID) pgtype.UUID {
	t.Helper()
	ctx := context.Background()

	var runtimeID pgtype.UUID
	if err := testPool.QueryRow(ctx, `SELECT id FROM agent_runtime LIMIT 1`).Scan(&runtimeID); err != nil {
		t.Fatalf("no agent_runtime available: %v", err)
	}

	var id pgtype.UUID
	err := testPool.QueryRow(ctx, `
		SELECT id FROM agent
		WHERE workspace_id = $1 AND name = 'research'
		ORDER BY created_at ASC
		LIMIT 1`,
		wsID,
	).Scan(&id)
	if err != nil {
		// None yet — create one. mustCreateTestAgent binds a runtime.
		id = mustCreateTestAgent(t, wsID, "research", owner)
		t.Cleanup(func() {
			_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, id)
		})
	}

	// Force readiness regardless of prior state.
	if _, err := testPool.Exec(ctx,
		`UPDATE agent SET runtime_id = $1, archived_at = NULL WHERE id = $2`,
		runtimeID, id,
	); err != nil {
		t.Fatalf("bind research agent runtime: %v", err)
	}
	return id
}

// TestUpdateIssueLabSourceDispatchesResearch is the core regression for the
// "selecting the science lab must start the research run" defect (MUL:
// UpdateIssue path). Flipping lab_source onto claude_science_lab on an
// unassigned, active (todo) issue must:
//  1. auto-assign the `research` leader agent, and
//  2. enqueue a run for it (RunSourceAssign).
//
// The bug: the PATCH carries no assignee_* field, so assigneeChanged stayed
// false and WillEnqueueRun fell through to its default (no run). The fix folds
// the lab auto-assign into assigneeChanged.
func TestUpdateIssueLabSourceDispatchesResearch(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}

	wsUUID := mustParseUUID(t, testWorkspaceID)
	owner := mustCreateTestMember(t, wsUUID)
	researchID := ensureReadyResearchAgent(t, wsUUID, owner)
	researchIDStr := util.UUIDToString(researchID)

	// Unassigned, active issue with no lab yet.
	issue := createIssueForTest(t, map[string]any{
		"title":  "lab-dispatch-active",
		"status": "todo",
	})

	w := httptest.NewRecorder()
	req := withURLParam(
		newRequest("PUT", "/api/issues/"+issue.ID, map[string]any{
			"lab_source": "claude_science_lab",
		}),
		"id", issue.ID,
	)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue lab_source: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp IssueResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode issue response: %v\nbody: %s", err, w.Body.String())
	}
	if resp.AssigneeType == nil || *resp.AssigneeType != "agent" {
		t.Fatalf("expected research agent auto-assigned, got assignee_type=%v", resp.AssigneeType)
	}
	if resp.AssigneeID == nil || *resp.AssigneeID != researchIDStr {
		t.Fatalf("expected assignee_id=%s, got %v", researchIDStr, resp.AssigneeID)
	}

	if got := taskCountFor(t, issue.ID, researchIDStr); got == 0 {
		t.Fatalf("selecting the science lab enqueued no research run (dispatch regression)")
	}
}

// TestUpdateIssueLabSourceBacklogParks pins the parking-lot invariant: a
// backlog issue that gets tagged with the science lab auto-assigns the leader
// but must NOT start a run (backlog is the parking lot). This guards the fix
// from over-firing on issues the user has parked.
func TestUpdateIssueLabSourceBacklogParks(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}

	wsUUID := mustParseUUID(t, testWorkspaceID)
	owner := mustCreateTestMember(t, wsUUID)
	researchID := ensureReadyResearchAgent(t, wsUUID, owner)
	researchIDStr := util.UUIDToString(researchID)

	issue := createIssueForTest(t, map[string]any{
		"title":  "lab-dispatch-backlog",
		"status": "backlog",
	})

	w := httptest.NewRecorder()
	req := withURLParam(
		newRequest("PUT", "/api/issues/"+issue.ID, map[string]any{
			"lab_source": "claude_science_lab",
		}),
		"id", issue.ID,
	)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue lab_source (backlog): expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if got := taskCountFor(t, issue.ID, researchIDStr); got != 0 {
		t.Fatalf("backlog lab issue must park, but %d run(s) were enqueued", got)
	}
}

// TestUpdateIssueLabSourceRewritesStaleAssignee — 0.3.46 (P0#4).
//
// Regression for the silent breakage where an issue was assigned to a
// non-leader agent, then later flipped to claude_science_lab. The
// pre-0.3.46 gate `!issue.AssigneeType.Valid` skipped the auto-assign
// in that case, leaving the issue running on the wrong agent while
// the UI showed the lab badge. The new contract rewrites the
// assignee to the lab's leader whenever the existing one is not
// already pointing at it.
func TestUpdateIssueLabSourceRewritesStaleAssignee(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}

	wsUUID := mustParseUUID(t, testWorkspaceID)
	owner := mustCreateTestMember(t, wsUUID)
	researchID := ensureReadyResearchAgent(t, wsUUID, owner)

	// A separate, non-leader agent that we deliberately assign first.
	decoyID := mustCreateTestAgent(t, wsUUID, "lab-decoy-"+util.UUIDToString(researchID)[:8], owner)
	decoyIDStr := util.UUIDToString(decoyID)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, decoyID)
	})

	issue := createIssueForTest(t, map[string]any{
		"title":         "lab-p04-stale-assignee",
		"status":        "todo",
		"assignee_type": "agent",
		"assignee_id":   decoyIDStr,
	})

	w := httptest.NewRecorder()
	req := withURLParam(
		newRequest("PUT", "/api/issues/"+issue.ID, map[string]any{
			"lab_source": "claude_science_lab",
		}),
		"id", issue.ID,
	)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue lab_source (stale assignee): expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp IssueResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode issue response: %v", err)
	}
	if resp.AssigneeType == nil || *resp.AssigneeType != "agent" {
		t.Fatalf("expected agent assignee, got %v", resp.AssigneeType)
	}
	if resp.AssigneeID == nil || *resp.AssigneeID != util.UUIDToString(researchID) {
		t.Fatalf("P0#4 regression: stale assignee was not rewritten to research leader. got=%v want=%s",
			resp.AssigneeID, util.UUIDToString(researchID))
	}
}

// TestUpdateIssueLabSourceKeepsMatchingAssignee — 0.3.46 (P0#4) companion.
//
// When the existing assignee already IS the lab leader (e.g. AssigneePicker
// fired before LabPicker, picking research intentionally), flipping
// lab_source onto claude_science_lab must leave the assignee untouched.
// The new contract only rewrites when the assignee does not match.
func TestUpdateIssueLabSourceKeepsMatchingAssignee(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}

	wsUUID := mustParseUUID(t, testWorkspaceID)
	owner := mustCreateTestMember(t, wsUUID)
	researchID := ensureReadyResearchAgent(t, wsUUID, owner)
	researchIDStr := util.UUIDToString(researchID)

	// Pre-assign the leader before any lab is set, simulating a user who
	// picked research from the AssigneePicker first.
	issue := createIssueForTest(t, map[string]any{
		"title":         "lab-p04-matching-assignee",
		"status":        "todo",
		"assignee_type": "agent",
		"assignee_id":   researchIDStr,
	})

	w := httptest.NewRecorder()
	req := withURLParam(
		newRequest("PUT", "/api/issues/"+issue.ID, map[string]any{
			"lab_source": "claude_science_lab",
		}),
		"id", issue.ID,
	)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue lab_source (matching assignee): expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp IssueResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode issue response: %v", err)
	}
	if resp.AssigneeID == nil || *resp.AssigneeID != researchIDStr {
		t.Fatalf("P0#4 contract: matching assignee must NOT be rewritten. got=%v want=%s",
			resp.AssigneeID, researchIDStr)
	}
}
