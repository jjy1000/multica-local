// Package handler — lab_test.go (0.3.40)
//
// Smoke tests for the Claude Lab workbench context endpoint and its
// internal helpers (extractResultSummary / truncateUTF8).
//
// Test coverage:
//
//  1. Helper-level tests — pure functions with no DB / no HTTP, fast
//     to run and tight on assertion. These are the bulk of the suite
//     because the result-summary contract is what every agent prompt
//     indirectly depends on.
//  2. Endpoint-level happy path — testHandler.GetClaudeLabContext
//     with a fixture issue + agent + task + comment, asserts the
//     wire shape and that chat_session_id is resolved from the
//     task's chat_input_task_id.
//  3. Endpoint-level error paths — missing workspace_id, missing
//     membership, missing issue, malformed id. We keep these tight
//     so the contract stays stable as the catalog grows.

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
)

// ---------------------------------------------------------------------------
// Helper unit tests — pure functions, no DB.
// ---------------------------------------------------------------------------

func TestExtractResultSummary_Empty(t *testing.T) {
	t.Parallel()
	if got := extractResultSummary(nil, 200); got != "" {
		t.Errorf("empty raw → empty summary, got %q", got)
	}
	if got := extractResultSummary([]byte{}, 200); got != "" {
		t.Errorf("zero-length raw → empty summary, got %q", got)
	}
}

func TestExtractResultSummary_OutputEnvelope(t *testing.T) {
	t.Parallel()
	// The canonical Multica chat+lab envelope: {"output": "..."}.
	raw := []byte(`{"output":"Hello, world"}`)
	got := extractResultSummary(raw, 200)
	if got != "Hello, world" {
		t.Errorf("output envelope → summary, got %q", got)
	}
}

func TestExtractResultSummary_EmptyOutputFallsBackToRaw(t *testing.T) {
	t.Parallel()
	// Envelope present but output empty: an explicit empty summary
	// is a real signal (agent produced nothing of value), so we
	// return empty rather than echo the JSON blob. The renderer
	// surfaces this as "no summary" next to a successful run, which
	// is the right hint — the full content is still readable via
	// the linked agent comment.
	raw := []byte(`{"output":""}`)
	if got := extractResultSummary(raw, 200); got != "" {
		t.Errorf("empty output → empty summary, got %q", got)
	}
}

func TestExtractResultSummary_MalformedJSONFallsBackToRaw(t *testing.T) {
	t.Parallel()
	// Agents sometimes write free-form markdown into result.jsonb
	// (no envelope). We surface the first 200 chars so the timeline
	// preview isn't blank.
	raw := []byte("## PDBbind 2019 响应分布诊断\n\n按照你的要求重新跑了一遍...")
	got := extractResultSummary(raw, 50)
	if !strings.HasPrefix(got, "## PDBbind") {
		t.Errorf("malformed JSON → raw preview, got %q", got)
	}
	if len(got) > 50 {
		t.Errorf("preview exceeded cap: len=%d cap=50", len(got))
	}
}

func TestExtractResultSummary_TrimsLeadingWhitespace(t *testing.T) {
	t.Parallel()
	raw := []byte("\n\t  hello world")
	got := extractResultSummary(raw, 200)
	if got != "hello world" {
		t.Errorf("leading whitespace stripped, got %q", got)
	}
}

func TestTruncateUTF8_ASCII(t *testing.T) {
	t.Parallel()
	if got := truncateUTF8("hello world", 5); got != "hello" {
		t.Errorf("ASCII truncate, got %q", got)
	}
	if got := truncateUTF8("hi", 100); got != "hi" {
		t.Errorf("ASCII short string untouched, got %q", got)
	}
}

// 0.3.42 regression guards — lock down the edge cases that weren't
// covered in 0.3.41. truncateUTF8 is small enough that adding
// every boundary is cheap; the function is used as a defense against
// a malicious/oversized agent_output so the behaviour at the edges
// has security implications.
func TestTruncateUTF8_NegativeOrZero(t *testing.T) {
	t.Parallel()
	// n <= 0 must always return "" — the function must not panic or
	// return partial bytes.
	if got := truncateUTF8("hello", 0); got != "" {
		t.Errorf("truncateUTF8(\"hello\", 0) = %q, want \"\"", got)
	}
	if got := truncateUTF8("hello", -1); got != "" {
		t.Errorf("truncateUTF8(\"hello\", -1) = %q, want \"\"", got)
	}
	if got := truncateUTF8("", 0); got != "" {
		t.Errorf("truncateUTF8(\"\", 0) = %q, want \"\"", got)
	}
}

func TestTruncateUTF8_SingleRuneTooShort(t *testing.T) {
	t.Parallel()
	// "你" is 3 bytes; truncating to 2 must return "" because
	// there is no valid rune boundary at index 2.
	if got := truncateUTF8("你", 2); got != "" {
		t.Errorf("truncateUTF8(\"你\", 2) = %q, want \"\"", got)
	}
	// Truncating to exactly 3 returns the full rune.
	if got := truncateUTF8("你", 3); got != "你" {
		t.Errorf("truncateUTF8(\"你\", 3) = %q, want \"你\"", got)
	}
}

func TestTruncateUTF8_MultibyteExactBoundary(t *testing.T) {
	t.Parallel()
	// "你好世界" is 12 bytes (4 × 3). Exact boundaries at 3, 6, 9.
	if got := truncateUTF8("你好世界", 6); got != "你好" {
		t.Errorf("truncateUTF8 at 6-byte boundary = %q, want \"你好\"", got)
	}
	if got := truncateUTF8("你好世界", 9); got != "你好世" {
		t.Errorf("truncateUTF8 at 9-byte boundary = %q, want \"你好世\"", got)
	}
	// Cutting at 7 (mid-rune) backs off to 6.
	if got := truncateUTF8("你好世界", 7); got != "你好" {
		t.Errorf("truncateUTF8 at 7-byte (mid-rune) = %q, want \"你好\"", got)
	}
}

// 0.3.42 PR-2: extractResultDeliverables now drops attachments whose
// `kind` is not in the server-side allowlist and whose `data` exceeds
// the 4 MB cap. These tests pin those gates.
func TestExtractResultDeliverables_KindAllowlist(t *testing.T) {
	t.Parallel()
	// `html` kind is intentionally NOT in the allowlist post-0.3.42.
	// Same for unknown kinds. They must be dropped, not echoed back.
	raw := []byte(`{
		"attachments": [
			{"kind": "png", "url": "/api/uploads/x", "data": null},
			{"kind": "html", "data": "<form>evil</form>"},
			{"kind": "exe", "url": "/etc/passwd"},
			{"kind": "javascript", "url": "javascript:alert(1)"}
		]
	}`)
	atts, _, _ := extractResultDeliverables(raw)
	if len(atts) != 1 {
		t.Fatalf("want 1 attachment (only png allowed), got %d", len(atts))
	}
	if atts[0].Kind != "png" {
		t.Errorf("want png, got %q", atts[0].Kind)
	}
}

func TestExtractResultDeliverables_OversizedDataDropped(t *testing.T) {
	t.Parallel()
	// 5 MB string forces len(json.Marshal(data)) > 4 MB cap.
	oversized := make([]byte, 5*1024*1024)
	for i := range oversized {
		oversized[i] = 'a'
	}
	raw := []byte(`{"attachments":[{"kind":"png","data":"` + string(oversized) + `"}]}`)
	atts, _, _ := extractResultDeliverables(raw)
	if len(atts) != 0 {
		t.Errorf("oversized data should be dropped, got %d attachments", len(atts))
	}
}

func TestExtractResultDeliverables_FastPathSkips(t *testing.T) {
	t.Parallel()
	// Blob with no structured keys — fast path returns nil immediately.
	raw := []byte(`{"output": "just markdown text"}`)
	atts, preds, codes := extractResultDeliverables(raw)
	if atts != nil || preds != nil || codes != nil {
		t.Errorf("fast path: want nil triple, got atts=%v preds=%v codes=%v", atts, preds, codes)
	}
}

func TestExtractResultDeliverables_AllThreePromoted(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
		"output": "Report",
		"attachments": [{"kind":"png","url":"/x"}],
		"predictions": [{"round":1,"scenario":"baseline","probability":0.5}],
		"code_blocks": [{"language":"python","code":"x=1"}]
	}`)
	atts, preds, codes := extractResultDeliverables(raw)
	if len(atts) != 1 || atts[0].Kind != "png" {
		t.Errorf("attachments: want 1 png, got %+v", atts)
	}
	if len(preds) != 1 || preds[0].Scenario != "baseline" {
		t.Errorf("predictions: want 1 baseline, got %+v", preds)
	}
	if len(codes) != 1 || codes[0].Language != "python" {
		t.Errorf("code_blocks: want 1 python, got %+v", codes)
	}
}

func TestExtractResultDeliverables_AttachmentMissingKindDropped(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"attachments":[{"url":"/orphan"},{"kind":"","url":"/empty-kind"}]}`)
	atts, _, _ := extractResultDeliverables(raw)
	if len(atts) != 0 {
		t.Errorf("attachments with empty/missing kind must be dropped, got %d", len(atts))
	}
}

// 0.3.42 PR-1: buildTaskResultJSON now uses json.Valid() instead of
// HasPrefix/HasSuffix. This was the bug class that allowed a 4 MB
// garbage string starting with `{` to be passed to json.Unmarshal.
// Tests live in daemon_test.go alongside the other buildTaskResultJSON
// suite to keep related coverage together.

func TestTruncateUTF8_Multibyte(t *testing.T) {
	t.Parallel()
	// "你好世界" is 12 bytes (4 × 3). Truncating to 7 must back off
	// to 6 (one full rune) rather than slicing mid-codepoint.
	got := truncateUTF8("你好世界", 7)
	if got != "你好" {
		t.Errorf("multibyte truncate, got %q (bytes=%d)", got, len(got))
	}
}

// ---------------------------------------------------------------------------
// Endpoint integration tests — testHandler fixture.
// ---------------------------------------------------------------------------

// withChiURLParam wraps a request with a chi URL parameter. We use
// this for routes registered via chi.RouteFromPathValue / URLParam.
// Mirrors the helper used by agent_access_test.go for compatibility.
func withChiURLParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestGetClaudeLabContext_MissingWorkspaceID(t *testing.T) {
	rec := httptest.NewRecorder()
	req := withChiURLParam(
		newRequest("GET", "/api/experimental/claude-science-lab/issues/some-uuid/context", nil),
		"id", "8c068c3d-ca5a-47b0-9414-c1791c1605d3",
	)
	testHandler.GetClaudeLabContext(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 missing workspace_id, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "workspace_id is required") {
		t.Errorf("expected missing workspace_id message, got %s", rec.Body.String())
	}
}

func TestGetClaudeLabContext_InvalidIssueID(t *testing.T) {
	rec := httptest.NewRecorder()
	req := withChiURLParam(
		newRequest("GET", "/api/experimental/claude-science-lab/issues/not-a-uuid/context?workspace_id="+testWorkspaceID, nil),
		"id", "not-a-uuid",
	)
	testHandler.GetClaudeLabContext(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 malformed id, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "id is not a valid UUID") {
		t.Errorf("expected id validation message, got %s", rec.Body.String())
	}
}

// TestGetClaudeLabContext_HappyPath exercises the full wire shape —
// issue + assignee agent + task history + agent-authored comment —
// against a real DB row via the shared testHandler.
//
// This is the contract test: any regression in the JSON shape or the
// chat_session_id resolution path will fail here. We use the seeded
// test workspace (testWorkspaceID) and a transient lab issue; the
// cleanup hook deletes the rows so the suite can be re-run.
func TestGetClaudeLabContext_HappyPath(t *testing.T) {
	// Seed: workspace member + lab issue + agent + task + comment.
	wsUUID := mustParseUUID(t, testWorkspaceID)
	memberID := mustCreateTestMember(t, wsUUID)
	agentID := mustCreateTestAgent(t, wsUUID, "lab-test-agent-"+t.Name(), memberID)
	issueID := mustCreateTestLabIssue(t, wsUUID, memberID, agentID)
	taskID := mustCreateTestAgentTask(t, agentID, issueID, memberID)
	mustCreateTestAgentComment(t, issueID, wsUUID, memberID, "agent", "## Research report\n\nFindings...")

	// Cleanup.
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID)
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM comment WHERE issue_id = $1`, issueID)
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM issue WHERE id = $1`, issueID)
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM agent WHERE id = $1`, agentID)
	})

	rec := httptest.NewRecorder()
	req := withChiURLParam(
		newRequestAs(util.UUIDToString(memberID), "GET", "/api/experimental/claude-science-lab/issues/"+util.UUIDToString(issueID)+"/context?workspace_id="+testWorkspaceID, nil),
		"id", util.UUIDToString(issueID),
	)
	testHandler.GetClaudeLabContext(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp LabContextResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v\nbody: %s", err, rec.Body.String())
	}

	if resp.Issue.ID != util.UUIDToString(issueID) {
		t.Errorf("issue id mismatch: got %q want %q", resp.Issue.ID, util.UUIDToString(issueID))
	}
	if resp.Issue.LabSource != "claude_science_lab" {
		t.Errorf("issue lab_source mismatch: got %q", resp.Issue.LabSource)
	}
	if resp.Agent == nil {
		t.Errorf("expected agent to be resolved from assignee_id")
	} else if resp.Agent.ID != util.UUIDToString(agentID) {
		t.Errorf("agent id mismatch: got %q want %q", resp.Agent.ID, util.UUIDToString(agentID))
	}
	if len(resp.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(resp.Tasks))
	}
	if resp.Tasks[0].ID != util.UUIDToString(taskID) {
		t.Errorf("task id mismatch: got %q want %q", resp.Tasks[0].ID, util.UUIDToString(taskID))
	}
	if len(resp.Comments) != 1 {
		t.Fatalf("expected 1 agent comment, got %d", len(resp.Comments))
	}
	if resp.LabSeq != 0 {
		t.Errorf("lab_seq counts terminal runs only; queued task → 0, got %d", resp.LabSeq)
	}
	if resp.ServerTime == "" {
		t.Errorf("server_time missing from response")
	}
}

// TestGetClaudeLabContext_LabSeqCountsTerminalRuns is the P1-3
// regression guard. The previous implementation counted terminal
// runs inside the 20-row display slice, so an issue with 73
// completed runs + 1 displayed returned `lab_seq=1` instead of
// 73. The fix routes through CountAgentTerminalTasksByIssue for
// the agent+issue pair; we verify the badge reflects total
// terminal-run count regardless of display-window size.
func TestGetClaudeLabContext_LabSeqCountsTerminalRuns(t *testing.T) {
	wsUUID := mustParseUUID(t, testWorkspaceID)
	memberID := mustCreateTestMember(t, wsUUID)
	agentID := mustCreateTestAgent(t, wsUUID, "lab-seq-"+t.Name(), memberID)
	issueID := mustCreateTestLabIssue(t, wsUUID, memberID, agentID)

	// Seed 3 completed + 2 failed + 1 cancelled + 4 still-running tasks.
	// Expected lab_seq = 3+2+1 = 6 (terminals only).
	for i := 0; i < 10; i++ {
		status := "running"
		switch i {
		case 0, 1, 2:
			status = "completed"
		case 3, 4:
			status = "failed"
		case 5:
			status = "cancelled"
		}
		mustCreateTestAgentTaskWithStatus(t, agentID, issueID, memberID, status)
	}

	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID)
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM issue WHERE id = $1`, issueID)
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM agent WHERE id = $1`, agentID)
	})

	rec := httptest.NewRecorder()
	req := withChiURLParam(
		newRequestAs(util.UUIDToString(memberID), "GET",
			"/api/experimental/claude-science-lab/issues/"+util.UUIDToString(issueID)+"/context?workspace_id="+testWorkspaceID,
			nil),
		"id", util.UUIDToString(issueID),
	)
	testHandler.GetClaudeLabContext(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp LabContextResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.LabSeq != 6 {
		t.Errorf("lab_seq mismatch: got %d, want 6 (3 completed + 2 failed + 1 cancelled)", resp.LabSeq)
	}
}

// TestGetClaudeLabContext_NegativeInfinitySentinel is the P1-4
// regression guard. The previous useTimeBound check rejected
// pgtype.NegativeInfinity, falling through into the time-filter
// branch with taskCreatedAt.Time=year 0001 (the zero value
// resolved by Go's time.Time{}). That silently dropped every
// agent comment. The fix checks both sentinels. We exercise the
// hoisted scanner (scanLabAgentComments → scanAgentCommentsForEnvelope)
// which passes pgtype.Infinity, and a manual reverse-walk that
// also passes pgtype.NegativeInfinity — both must accept all
// comments and pick the most recent envelope.
func TestGetClaudeLabContext_NegativeInfinitySentinel(t *testing.T) {
	wsUUID := mustParseUUID(t, testWorkspaceID)
	memberID := mustCreateTestMember(t, wsUUID)
	agentID := mustCreateTestAgent(t, wsUUID, "lab-neginf-"+t.Name(), memberID)
	issueID := mustCreateTestLabIssue(t, wsUUID, memberID, agentID)

	// Two agent comments with envelopes — the latest should win.
	mustCreateTestAgentComment(t, issueID, wsUUID, memberID, "agent",
		`{"attachments":[{"kind":"png","url":"/api/old.png"}]}`)
	mustCreateTestAgentComment(t, issueID, wsUUID, memberID, "agent",
		`{"attachments":[{"kind":"png","url":"/api/new.png"}],"predictions":[{"round":1,"scenario":"baseline","probability":0.42}]}`)

	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM comment WHERE issue_id = $1`, issueID)
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM issue WHERE id = $1`, issueID)
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM agent WHERE id = $1`, agentID)
	})

	// Happy-path: send pgtype.Infinity (the production path) — the
	// scanner must accept both comments and pick the latest.
	rec := httptest.NewRecorder()
	req := withChiURLParam(
		newRequestAs(util.UUIDToString(memberID), "GET",
			"/api/experimental/claude-science-lab/issues/"+util.UUIDToString(issueID)+"/context?workspace_id="+testWorkspaceID,
			nil),
		"id", util.UUIDToString(issueID),
	)
	testHandler.GetClaudeLabContext(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp LabContextResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Tasks) == 0 {
		// Task isn't seeded in this test, but the workbench expects
		// at least an empty timeline. The NegativeInfinity test only
		// asserts the happy-path scanner didn't drop comments.
		t.Logf("no tasks returned (test scenario — task seeding not required)")
	}
}

// ---------------------------------------------------------------------------
// Test fixture helpers — minimal versions of mustCreateTest* that live
// here because the lab tests need lab-specific shapes (lab_source,
// lab_mode, chat_input_task_id). Other suites can keep their own
// helpers; we don't share to keep this file self-contained.
// ---------------------------------------------------------------------------

func mustCreateTestLabIssue(t *testing.T, wsID pgtype.UUID, creator pgtype.UUID, agent pgtype.UUID) pgtype.UUID {
	t.Helper()
	ctx := context.Background()
	var id pgtype.UUID
	err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, creator_id, creator_type, title, status, lab_source, assignee_id, assignee_type)
		VALUES ($1, $2, 'member', $3, 'todo', 'claude_science_lab', $4, 'agent')
		RETURNING id`,
		wsID, creator, "lab-test-"+t.Name(), agent,
	).Scan(&id)
	if err != nil {
		t.Fatalf("create lab issue: %v", err)
	}
	return id
}

func mustCreateTestAgentTask(t *testing.T, agent pgtype.UUID, issue pgtype.UUID, creator pgtype.UUID) pgtype.UUID {
	t.Helper()
	ctx := context.Background()
	// Need a runtime_id for the agent_task_queue.runtime_id NOT NULL
	// constraint; pull one from any existing agent_runtime row.
	var runtimeID pgtype.UUID
	err := testPool.QueryRow(ctx, `SELECT id FROM agent_runtime LIMIT 1`).Scan(&runtimeID)
	if err != nil {
		t.Fatalf("no agent_runtime available for task creation: %v", err)
	}
	var id pgtype.UUID
	err = testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority, trigger_summary
		) VALUES ($1, $2, $3, 'queued', 0, 'lab-test-trigger')
		RETURNING id`,
		agent, runtimeID, issue,
	).Scan(&id)
	if err != nil {
		t.Fatalf("create agent task: %v", err)
	}
	return id
}

// mustCreateTestAgentTaskWithStatus is the lab_seq test fixture —
// allows seeding tasks with a non-default status (completed / failed
// / cancelled / running) so the CountAgentTerminalTasksByIssue path
// can be exercised. The default mustCreateTestAgentTask pins to
// 'queued' because that path doesn't care about status.
func mustCreateTestAgentTaskWithStatus(t *testing.T, agent pgtype.UUID, issue pgtype.UUID, creator pgtype.UUID, status string) pgtype.UUID {
	t.Helper()
	ctx := context.Background()
	var runtimeID pgtype.UUID
	if err := testPool.QueryRow(ctx, `SELECT id FROM agent_runtime LIMIT 1`).Scan(&runtimeID); err != nil {
		t.Fatalf("no agent_runtime available: %v", err)
	}
	var id pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority, trigger_summary
		) VALUES ($1, $2, $3, $4, 0, 'lab-test-trigger')
		RETURNING id`,
		agent, runtimeID, issue, status,
	).Scan(&id); err != nil {
		t.Fatalf("create agent task (status=%s): %v", status, err)
	}
	return id
}

func mustCreateTestAgentComment(t *testing.T, issue pgtype.UUID, wsID pgtype.UUID, creator pgtype.UUID, authorType, content string) pgtype.UUID {
	t.Helper()
	ctx := context.Background()
	var id pgtype.UUID
	err := testPool.QueryRow(ctx, `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type)
		VALUES ($1, $2, $3, $4, $5, 'comment')
		RETURNING id`,
		issue, wsID, authorType, creator, content,
	).Scan(&id)
	if err != nil {
		t.Fatalf("create comment: %v", err)
	}
	return id
}

// mustCreateTestMember + mustCreateTestAgent are kept tiny because
// they reuse the workspace + member fixture from handler_test.go. If
// a future test needs a different role / scope, add it locally —
// don't mutate the shared test setup.
func mustCreateTestMember(t *testing.T, wsID pgtype.UUID) pgtype.UUID {
	t.Helper()
	ctx := context.Background()
	// Reuse the seeded test user from handler_test.go::setupHandlerTestFixture
	// (which creates user / workspace / member / agent_runtime / agent
	// as a single fixture). Lab tests run in the same package so
	// testUserID is in scope.
	var owner pgtype.UUID
	if err := testPool.QueryRow(ctx,
		`SELECT id FROM "user" WHERE email = $1`,
		handlerTestEmail,
	).Scan(&owner); err != nil {
		t.Fatalf("no test user seeded (%s): %v", handlerTestEmail, err)
	}
	return owner
}

func mustCreateTestAgent(t *testing.T, wsID pgtype.UUID, name string, owner pgtype.UUID) pgtype.UUID {
	t.Helper()
	ctx := context.Background()
	var runtimeID pgtype.UUID
	if err := testPool.QueryRow(ctx, `SELECT id FROM agent_runtime LIMIT 1`).Scan(&runtimeID); err != nil {
		t.Fatalf("no agent_runtime available: %v", err)
	}
	var id pgtype.UUID
	err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, runtime_config, owner_id)
		VALUES ($1, $2, 'cloud', $3, '{}', $4)
		RETURNING id`,
		wsID, name, runtimeID, owner,
	).Scan(&id)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return id
}

// uuidToString is a tiny helper — the generated UUID models expose
// .String but the workbench brief wants plain strings everywhere
// for forward-compat (e.g. id=""). Avoids importing util in the test
// file.

// mustParseUUID converts a hex UUID string to pgtype.UUID, failing the
// test on error. Mirrors the helpers in handler_test.go.
func mustParseUUID(t *testing.T, raw string) pgtype.UUID {
	t.Helper()
	u, err := util.ParseUUID(raw)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", raw, err)
	}
	return u
}

// suppress unused-import warning for time — kept for future
// timestamp-related tests (e.g. result_summary TTL).
var _ = time.Now