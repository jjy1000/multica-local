package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Tests for the MCP sync surface (migration 285): the read-only mirror API
// (GET /api/mcp-sync), its redaction contract, and the task-level
// `mcp_calls` figure riding the daemon usage channel.

// TestGetMcpSyncRedactsSecrets pins the mirror's redaction contract: env and
// header VALUES never cross the API boundary — only their key names, so the
// settings tab can show what a server needs without the credentials.
func TestGetMcpSyncRedactsSecrets(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	seed := `{"name":"secretive-server","definition":` +
		`{"type":"stdio","command":"node","env":{"TAVILY_API_KEY":"sk-super-secret"},"headers":{"Authorization":"Bearer xyz"}},` +
		`"source_hash":"testhash","status":"synced"}`
	if _, err := testPool.Exec(ctx, `
		INSERT INTO mcp_sync_server (name, definition, source_hash, status)
		SELECT $1::text, ($2::jsonb)->'definition', 'testhash', 'synced'
	`, "secretive-server", seed); err != nil {
		t.Fatalf("seed mirror row: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM mcp_sync_server WHERE name = $1`, "secretive-server")
	})

	w := httptest.NewRecorder()
	testHandler.GetMcpSync(w, newRequest("GET", "/api/mcp-sync", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	body := w.Body.String()
	if strings.Contains(body, "sk-super-secret") || strings.Contains(body, "Bearer xyz") {
		t.Fatal("plaintext secret leaked through the mirror API")
	}

	var resp struct {
		Servers []struct {
			Name       string         `json:"name"`
			Definition map[string]any `json:"definition"`
			Status     string         `json:"status"`
		} `json:"servers"`
		LastError string `json:"last_error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var found *struct {
		Name       string         `json:"name"`
		Definition map[string]any `json:"definition"`
	}
	for i := range resp.Servers {
		if resp.Servers[i].Name == "secretive-server" {
			found = &struct {
				Name       string         `json:"name"`
				Definition map[string]any `json:"definition"`
			}{Name: resp.Servers[i].Name, Definition: resp.Servers[i].Definition}
		}
	}
	if found == nil {
		t.Fatal("seeded server missing from response")
	}
	env, ok := found.Definition["env"].(map[string]any)
	if !ok {
		t.Fatal("env map missing from definition")
	}
	if env["TAVILY_API_KEY"] != "********" {
		t.Fatalf("env value not masked: %v", env["TAVILY_API_KEY"])
	}
	if _, ok := found.Definition["command"]; !ok {
		t.Fatal("non-secret field lost by redaction")
	}
}

// TestReportTaskUsageStoresMcpCalls pins the daemon usage channel's
// task-level `mcp_calls` field: it persists even with an empty usage list
// (a run can invoke MCP tools without accumulating token entries), rejects
// negative values, and leaves the stored count untouched when an older
// daemon omits the field.
func TestReportTaskUsageStoresMcpCalls(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	agentID := createHandlerTestAgent(t, "McpCallsAgent", []byte("[]"))

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position)
		VALUES ($1, 'mcp-calls-issue', 'todo', 'medium', $2, 'member', 92779, 0)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })

	newTask := func() string {
		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, issue_id)
			VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), 'completed', 0, $2)
			RETURNING id
		`, agentID, issueID).Scan(&id); err != nil {
			t.Fatalf("create task: %v", err)
		}
		t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, id) })
		return id
	}

	postUsage := func(taskID string, body map[string]any) *httptest.ResponseRecorder {
		req := newRequest("POST", "/api/daemon/tasks/"+taskID+"/usage", body)
		req = withURLParam(req, "taskId", taskID)
		w := httptest.NewRecorder()
		testHandler.ReportTaskUsage(w, req)
		return w
	}

	mcpCallsOf := func(taskID string) int {
		var n int
		if err := testPool.QueryRow(ctx,
			`SELECT mcp_calls FROM agent_task_queue WHERE id = $1`, taskID).Scan(&n); err != nil {
			t.Fatalf("read mcp_calls: %v", err)
		}
		return n
	}

	// mcp_calls with an EMPTY usage list.
	taskWithCallsOnly := newTask()
	if w := postUsage(taskWithCallsOnly, map[string]any{"usage": []any{}, "mcp_calls": 7}); w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := mcpCallsOf(taskWithCallsOnly); got != 7 {
		t.Fatalf("want mcp_calls=7, got %d", got)
	}

	// mcp_calls alongside token entries.
	taskWithBoth := newTask()
	taskWithBothUsage := []map[string]any{{"provider": "claude", "model": "opus", "input_tokens": 10, "output_tokens": 5}}
	if w := postUsage(taskWithBoth, map[string]any{"usage": taskWithBothUsage, "mcp_calls": 3}); w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := mcpCallsOf(taskWithBoth); got != 3 {
		t.Fatalf("want mcp_calls=3, got %d", got)
	}

	// Omitted field leaves the stored count untouched (older daemon).
	taskWithBothUsage[0]["input_tokens"] = 11
	taskWithBothUsage[0]["output_tokens"] = 6
	if w := postUsage(taskWithBoth, map[string]any{"usage": taskWithBothUsage}); w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := mcpCallsOf(taskWithBoth); got != 3 {
		t.Fatalf("omitted field must not zero the count, got %d", got)
	}

	// Negative values are rejected outright.
	taskNegative := newTask()
	if w := postUsage(taskNegative, map[string]any{"usage": []any{}, "mcp_calls": -1}); w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if got := mcpCallsOf(taskNegative); got != 0 {
		t.Fatalf("negative value must not persist, got %d", got)
	}
}

// TestDashboardMcpCallsDaily feeds the KPI tile: only terminal tasks within
// the window contribute, and the daily rows sum to the stored per-task
// counts.
func TestDashboardMcpCallsDaily(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	agentID := createHandlerTestAgent(t, "McpDashboardAgent", []byte("[]"))

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position)
		VALUES ($1, 'mcp-dashboard-issue', 'todo', 'medium', $2, 'member', 92780, 0)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })

	newTerminalTask := func(status string, mcpCalls int) string {
		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, issue_id, started_at, completed_at, mcp_calls)
			VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), $2, 0, $3, now() - interval '1 hour', now(), $4)
			RETURNING id
		`, agentID, status, issueID, mcpCalls).Scan(&id); err != nil {
			t.Fatalf("create task: %v", err)
		}
		t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, id) })
		return id
	}
	newTerminalTask("completed", 4)
	newTerminalTask("failed", 2)
	// A non-terminal (or missing-timestamp) task must not contribute.
	newTerminalTask("completed", 0)
	var runningTask string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, issue_id, mcp_calls)
		VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), 'running', 0, $2, 99)
		RETURNING id
	`, agentID, issueID).Scan(&runningTask); err != nil {
		t.Fatalf("create running task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, runningTask) })

	w := httptest.NewRecorder()
	testHandler.GetDashboardMcpCallsDaily(w, newRequest("GET", "/api/dashboard/mcp-calls/daily?days=1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var rows []struct {
		Date     string `json:"date"`
		McpCalls int64  `json:"mcp_calls"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode rows: %v", err)
	}
	var total int64
	for _, r := range rows {
		total += r.McpCalls
	}
	if total != 6 {
		t.Fatalf("want total mcp_calls=6 (4+2, running task excluded), got %d", total)
	}
}
