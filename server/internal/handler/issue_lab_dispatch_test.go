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
	researchIDStr := util.UUIDToString(researchID)

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
	if resp.AssigneeID == nil || *resp.AssigneeID != researchIDStr {
		t.Fatalf("P0#4 regression: stale assignee was not rewritten to research leader. got=%v want=%s",
			resp.AssigneeID, researchIDStr)
	}

	// 0.3.47: the rewrite must also arm the dispatch path. Pre-0.3.46
	// WillEnqueueRun never fired here because the PATCH carried no
	// assignee_* field; the labAutoAssigned fold in UpdateIssue is what
	// closes that loop. Pin a non-zero research-run count to catch any
	// regression that drops the fold or breaks the lab-leader dispatch.
	if got := taskCountFor(t, issue.ID, researchIDStr); got == 0 {
		t.Fatalf("case D dispatch: stale-assignee rewrite enqueued no research run (P0#4 regression)")
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

// TestUpdateIssueLabSourceUntouchedNoOp (case A) — 0.3.47 coverage.
//
// A PATCH that does not mention lab_source at all must not touch the
// assignee. The pre-0.3.46 gate `touchedLabSource` already short-circuits
// when the field is absent in rawFields, but the 0.3.46 ship test plan
// flagged case A as untested. This pins it: a PATCH carrying only a
// `title` field leaves a pre-existing research-leader assignee alone.
func TestUpdateIssueLabSourceUntouchedNoOp(t *testing.T) {
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

	// Issue with the research leader already assigned and lab_source
	// unset. The PATCH only flips `title`; lab_source stays absent.
	issue := createIssueForTest(t, map[string]any{
		"title":         "lab-p047-case-a-untouched",
		"status":        "todo",
		"assignee_type": "agent",
		"assignee_id":   researchIDStr,
	})

	w := httptest.NewRecorder()
	req := withURLParam(
		newRequest("PUT", "/api/issues/"+issue.ID, map[string]any{
			"title": "lab-p047-case-a-renamed",
		}),
		"id", issue.ID,
	)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue (untouched lab_source): expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp IssueResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode issue response: %v", err)
	}
	if resp.AssigneeID == nil || *resp.AssigneeID != researchIDStr {
		t.Fatalf("case A contract: untouched lab_source must NOT rewrite assignee. got=%v want=%s",
			resp.AssigneeID, researchIDStr)
	}
	if resp.LabSource != nil && *resp.LabSource != "" {
		t.Fatalf("case A contract: untouched lab_source must stay null. got=%q", *resp.LabSource)
	}
}

// TestUpdateIssueLabSourceMythosSoleNoAutoAssign (case B) — 0.3.47 coverage.
//
// Flipping lab_source onto mythos_swarm must NOT auto-assign a leader
// agent. The 5-agent mythos RDT runner owns the roster end-to-end; the
// sole-mode mutex gate already cleared the assignee, and the
// `defaultLabLeaderForKey("mythos_swarm")` helper returns ("", false)
// so shouldRewriteAssigneeForLabLeader short-circuits before any DB
// lookup. This test pins that short-circuit so a future regression
// adding a leader to mythos_swarm fails loudly instead of silently
// dispatching mythos_prelude (which would race the runner).
func TestUpdateIssueLabSourceMythosSoleNoAutoAssign(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}

	wsUUID := mustParseUUID(t, testWorkspaceID)
	mustCreateTestMember(t, wsUUID)

	issue := createIssueForTest(t, map[string]any{
		"title":  "lab-p047-case-b-mythos",
		"status": "todo",
	})

	w := httptest.NewRecorder()
	req := withURLParam(
		newRequest("PUT", "/api/issues/"+issue.ID, map[string]any{
			"lab_source": "mythos_swarm",
		}),
		"id", issue.ID,
	)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue (mythos_swarm): expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp IssueResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode issue response: %v", err)
	}
	if resp.AssigneeType != nil && *resp.AssigneeType != "" {
		t.Fatalf("case B contract: mythos_swarm must NOT auto-assign. got assignee_type=%q", *resp.AssigneeType)
	}
	if resp.AssigneeID != nil && *resp.AssigneeID != "" {
		t.Fatalf("case B contract: mythos_swarm must NOT auto-assign. got assignee_id=%q", *resp.AssigneeID)
	}
	if resp.LabSource == nil || *resp.LabSource != "mythos_swarm" {
		t.Fatalf("expected lab_source=mythos_swarm on the response, got=%v", resp.LabSource)
	}
}

// TestBatchUpdateIssuesLabSourceAutoAssignsLeader (case D via batch) —
// 0.3.47 (P0#4 Batch parity). BatchUpdateIssues used to silently skip
// the lab-leader rewrite path that UpdateIssue implemented in 0.3.46.
// A batch PATCH carrying only `lab_source` against N unassigned
// issues must auto-assign the lab leader for every issue and arm the
// dispatch path (labAutoRewrote folded into assigneeChanged). Pin
// both the per-issue rewrite AND a non-zero research-run count.
func TestBatchUpdateIssuesLabSourceAutoAssignsLeader(t *testing.T) {
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

	// Three unassigned, active issues. Batch flips all three to
	// claude_science_lab with NO assignee_* fields in the request.
	issues := []string{
		createIssueForTest(t, map[string]any{
			"title": "batch-lab-leader-1", "status": "todo",
		}).ID,
		createIssueForTest(t, map[string]any{
			"title": "batch-lab-leader-2", "status": "todo",
		}).ID,
		createIssueForTest(t, map[string]any{
			"title": "batch-lab-leader-3", "status": "todo",
		}).ID,
	}

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/batch?workspace_id="+testWorkspaceID, map[string]any{
		"issue_ids": issues,
		"updates": map[string]any{
			"lab_source": "claude_science_lab",
		},
	})
	testHandler.BatchUpdateIssues(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("BatchUpdateIssues lab_source: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Each issue must now carry the research leader as its assignee.
	for _, id := range issues {
		var atype pgtype.Text
		var aid pgtype.UUID
		err := testPool.QueryRow(context.Background(),
			`SELECT assignee_type, assignee_id FROM issue WHERE id = $1`, id,
		).Scan(&atype, &aid)
		if err != nil {
			t.Fatalf("re-read issue %s: %v", id, err)
		}
		if !atype.Valid || atype.String != "agent" {
			t.Errorf("issue %s: expected assignee_type=agent, got=%v", id, atype)
		}
		if !aid.Valid || util.UUIDToString(aid) != researchIDStr {
			t.Errorf("issue %s: expected assignee_id=%s, got=%v", id, researchIDStr, aid)
		}

		// Dispatch assertion: every active issue must enqueue a run
		// for the rewritten leader (mirrors UpdateIssue case D +
		// TestUpdateIssueLabSourceDispatchesResearch coverage).
		if got := taskCountFor(t, id, researchIDStr); got == 0 {
			t.Errorf("issue %s: batch lab-source flip enqueued no research run (P0#4 Batch parity regression)", id)
		}
	}
}

// ── 0.3.54 coverage ────────────────────────────────────────────────────────────
//
// defaultLabLeaderForKey was extended in 0.3.54 to cover every A-class
// lab (`pythia_oracle` → `pythia_runtime`, `code_canvas` →
// `code_canvas_worker`, plus the existing `mythos_swarm` no-op short
// circuit). The three tests below pin the new mapping so a future
// regression that swaps a leader name (or accidentally adds a leader
// to mythos_swarm) fails loudly here instead of silently dispatching
// the wrong agent.

// ensureReadyLabLeader is the generic companion to
// ensureReadyResearchAgent: it reads / creates an agent row with the
// given name and binds a runtime + clears archived_at so WillEnqueueRun
// treats it as ready. The leader might be installed by the lab
// install handler in another test run; we never rely on that side
// effect.
func ensureReadyLabLeader(
	t *testing.T,
	wsID pgtype.UUID,
	owner pgtype.UUID,
	leaderName string,
) pgtype.UUID {
	t.Helper()
	ctx := context.Background()

	var runtimeID pgtype.UUID
	if err := testPool.QueryRow(ctx, `SELECT id FROM agent_runtime LIMIT 1`).Scan(&runtimeID); err != nil {
		t.Fatalf("no agent_runtime available: %v", err)
	}

	var id pgtype.UUID
	err := testPool.QueryRow(ctx, `
		SELECT id FROM agent
		WHERE workspace_id = $1 AND name = $2
		ORDER BY created_at ASC
		LIMIT 1`,
		wsID, leaderName,
	).Scan(&id)
	if err != nil {
		id = mustCreateTestAgent(t, wsID, leaderName, owner)
		t.Cleanup(func() {
			_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, id)
		})
	}
	if _, err := testPool.Exec(ctx,
		`UPDATE agent SET runtime_id = $1, archived_at = NULL WHERE id = $2`,
		runtimeID, id,
	); err != nil {
		t.Fatalf("bind %s agent runtime: %v", leaderName, err)
	}
	return id
}

// TestUpdateIssueLabSourcePythiaOracle — 0.3.54 leader coverage.
//
// Flipping lab_source onto pythia_oracle MUST auto-assign the
// `pythia_runtime` leader agent (mirrors the claude_science_lab /
// research contract for the new flag). Same shape as
// TestUpdateIssueLabSourceDispatchesResearch so the regression read
// is mechanical.
func TestUpdateIssueLabSourcePythiaOracle(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}

	wsUUID := mustParseUUID(t, testWorkspaceID)
	owner := mustCreateTestMember(t, wsUUID)
	pythiaID := ensureReadyLabLeader(t, wsUUID, owner, "pythia_runtime")
	pythiaIDStr := util.UUIDToString(pythiaID)

	issue := createIssueForTest(t, map[string]any{
		"title":  "lab-p054-pythia-oracle",
		"status": "todo",
	})

	w := httptest.NewRecorder()
	req := withURLParam(
		newRequest("PUT", "/api/issues/"+issue.ID, map[string]any{
			"lab_source": "pythia_oracle",
		}),
		"id", issue.ID,
	)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue failed: %d  body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		AssigneeType pgtype.Text  `json:"assignee_type"`
		AssigneeID   pgtype.UUID  `json:"assignee_id"`
		LabSource    *string      `json:"lab_source"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.LabSource == nil || *resp.LabSource != "pythia_oracle" {
		t.Fatalf("expected lab_source=pythia_oracle on response, got=%v", resp.LabSource)
	}
	if !resp.AssigneeType.Valid || resp.AssigneeType.String != "agent" {
		t.Fatalf("expected assignee_type=agent, got=%v", resp.AssigneeType)
	}
	if !resp.AssigneeID.Valid || util.UUIDToString(resp.AssigneeID) != pythiaIDStr {
		t.Fatalf("expected assignee_id=%s (= pythia_runtime), got=%v",
			pythiaIDStr, resp.AssigneeID)
	}

	// Dispatch: the issue must have at least one queued task on the
	// pythia_runtime leader so the daemon will pick it up.
	if got := taskCountFor(t, issue.ID, pythiaIDStr); got == 0 {
		t.Errorf("expected at least 1 pythia_runtime task for issue %s after lab_source flip, got 0", issue.ID)
	}
}

// TestUpdateIssueLabSourceCodeCanvas — 0.3.54 leader coverage.
//
// Same shape as PythiaOracle, but for code_canvas → code_canvas_worker.
// Verified independently because each new leader mapping deserves its
// own regression test (a future refactor that swaps a name will catch
// a single test, not all four in one shot).
func TestUpdateIssueLabSourceCodeCanvas(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}

	wsUUID := mustParseUUID(t, testWorkspaceID)
	owner := mustCreateTestMember(t, wsUUID)
	canvasID := ensureReadyLabLeader(t, wsUUID, owner, "code_canvas_worker")
	canvasIDStr := util.UUIDToString(canvasID)

	issue := createIssueForTest(t, map[string]any{
		"title":  "lab-p054-code-canvas",
		"status": "todo",
	})

	w := httptest.NewRecorder()
	req := withURLParam(
		newRequest("PUT", "/api/issues/"+issue.ID, map[string]any{
			"lab_source": "code_canvas",
		}),
		"id", issue.ID,
	)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue failed: %d  body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		AssigneeType pgtype.Text `json:"assignee_type"`
		AssigneeID   pgtype.UUID `json:"assignee_id"`
		LabSource    *string     `json:"lab_source"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.LabSource == nil || *resp.LabSource != "code_canvas" {
		t.Fatalf("expected lab_source=code_canvas, got=%v", resp.LabSource)
	}
	if !resp.AssigneeType.Valid || resp.AssigneeType.String != "agent" {
		t.Fatalf("expected assignee_type=agent, got=%v", resp.AssigneeType)
	}
	if !resp.AssigneeID.Valid || util.UUIDToString(resp.AssigneeID) != canvasIDStr {
		t.Fatalf("expected assignee_id=%s (= code_canvas_worker), got=%v",
			canvasIDStr, resp.AssigneeID)
	}
	if got := taskCountFor(t, issue.ID, canvasIDStr); got == 0 {
		t.Errorf("expected at least 1 code_canvas_worker task for issue %s after lab_source flip, got 0", issue.ID)
	}
}

// TestUpdateIssueLabSourceMythosSoleModeNoAutoAssign — 0.3.54 pin of
// the NO-leader short-circuit. The previous TestUpdateIssueLabSource
// MythosSoleNoAutoAssign covers the same invariant; this one is the
// 0.3.47 → 0.3.54 backward-compat guard so a future contributor
// extending defaultLabLeaderForKey to mythos_swarm (e.g. accidentally
// adding `mythos_prelude`) fails this test BEFORE the production
// deployment starts racing the RDT runner.
func TestUpdateIssueLabSourceMythosSoleModeNoAutoAssign(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	if testWorkspaceID == "" {
		t.Skip("workspace fixture not initialized")
	}

	wsUUID := mustParseUUID(t, testWorkspaceID)
	mustCreateTestMember(t, wsUUID)

	// Pre-seed mythos_prelude as if the lab were installed — the
	// update path must NOT pick it up because the helper returns
	// ("", false) for mythos_swarm. We deliberately do NOT bind a
	// runtime; if the helper is ever broken (returns mythos_prelude)
	// the test would assert that the issue has been re-assigned to
	// mythos_prelude and we'd see a runtime_id mismatch — fail
	// loudly.
	mustCreateTestAgent(t, wsUUID, "mythos_prelude", pgtype.UUID{})

	issue := createIssueForTest(t, map[string]any{
		"title":  "lab-p054-mythos-no-leader",
		"status": "todo",
	})

	w := httptest.NewRecorder()
	req := withURLParam(
		newRequest("PUT", "/api/issues/"+issue.ID, map[string]any{
			"lab_source": "mythos_swarm",
		}),
		"id", issue.ID,
	)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue failed: %d  body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		AssigneeType *pgtype.Text `json:"assignee_type"`
		AssigneeID   *pgtype.UUID `json:"assignee_id"`
		LabSource    *string      `json:"lab_source"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.LabSource == nil || *resp.LabSource != "mythos_swarm" {
		t.Fatalf("expected lab_source=mythos_swarm, got=%v", resp.LabSource)
	}
	// The sole-mode mutex gate clears the assignee; the
	// mythos-swarm no-leader short-circuit must keep it cleared.
	if resp.AssigneeType != nil && resp.AssigneeType.Valid {
		t.Fatalf("mythos_swarm must NOT auto-assign a leader; "+
			"got assignee_type=%v (P0#4 mythos regression — someone "+
			"added a leader to the helper map for mythos_swarm)",
			resp.AssigneeType)
	}
}
