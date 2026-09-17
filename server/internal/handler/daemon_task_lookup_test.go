package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// The daemon interrupts a running agent the moment a task-status poll answers
// `404 task not found` (shouldInterruptAgent → isTaskNotFoundError). So that
// body is a kill signal, and only a lookup that completed and found no task row
// may produce it. Transient failures and authorization misses must stay distinct.

// lookupFaultPool fails one named query and passes everything else through, so
// a single link in the optional source chain can be made to fail while the
// task row itself stays readable.
type lookupFaultPool struct {
	db.DBTX
	query  string
	called bool
}

func (f *lookupFaultPool) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	if strings.Contains(query, "-- name: "+f.query+" :one") {
		f.called = true
		return &mockRow{err: context.DeadlineExceeded}
	}
	return f.DBTX.QueryRow(ctx, query, args...)
}

// seedLookupAgent returns (agentID, runtimeID) for a fresh agent in the test
// workspace.
func seedLookupAgent(t *testing.T, name string) (agentID, runtimeID string) {
	t.Helper()
	agentID = createHandlerTestAgent(t, name, nil)
	if err := testPool.QueryRow(context.Background(),
		`SELECT runtime_id::text FROM agent WHERE id = $1`, agentID).Scan(&runtimeID); err != nil {
		t.Fatalf("load runtime: %v", err)
	}
	return agentID, runtimeID
}

// seedLookupIssue creates a minimal issue and registers its cleanup.
func seedLookupIssue(t *testing.T, title string) string {
	t.Helper()
	var issueID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type)
		VALUES ($1, $2, 'todo', 'medium', $3, 'member')
		RETURNING id::text
	`, testWorkspaceID, title, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })
	return issueID
}

// seedLookupChatSession creates a minimal chat session owned by the agent.
func seedLookupChatSession(t *testing.T, agentID, title string) string {
	t.Helper()
	var chatID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, status)
		VALUES ($1, $2, $3, $4, 'active')
		RETURNING id::text
	`, testWorkspaceID, agentID, testUserID, title).Scan(&chatID); err != nil {
		t.Fatalf("seed chat session: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE id = $1`, chatID) })
	return chatID
}

// seedLookupAutopilotRun creates a paused autopilot with a running run and
// returns (autopilotID, runID).
func seedLookupAutopilotRun(t *testing.T, agentID, title string) (string, string) {
	t.Helper()
	var apID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO autopilot (workspace_id, title, assignee_type, assignee_id, status, execution_mode, created_by_type, created_by_id)
		VALUES ($1, $2, 'agent', $3, 'paused', 'run_only', 'member', $4)
		RETURNING id::text
	`, testWorkspaceID, title, agentID, testUserID).Scan(&apID); err != nil {
		t.Fatalf("seed autopilot: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM autopilot WHERE id = $1`, apID) })
	var runID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO autopilot_run (autopilot_id, source, status)
		VALUES ($1, 'manual', 'running')
		RETURNING id::text
	`, apID).Scan(&runID); err != nil {
		t.Fatalf("seed autopilot run: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM autopilot_run WHERE id = $1`, runID) })
	return apID, runID
}

// seedLookupTask inserts a running agent_task_queue row (outside the pending
// unique index) with the given extra columns, and registers its cleanup.
func seedLookupTask(t *testing.T, agentID, runtimeID string, extraColumns []string, extraValues ...any) string {
	t.Helper()
	cols := append([]string{"agent_id", "runtime_id", "status", "started_at"}, extraColumns...)
	placeholders := []string{"$1", "$2", "'running'", "now()"}
	for i := range extraColumns {
		placeholders = append(placeholders, fmt.Sprintf("$%d", i+3))
	}
	query := fmt.Sprintf(`INSERT INTO agent_task_queue (%s) VALUES (%s) RETURNING id::text`,
		strings.Join(cols, ", "), strings.Join(placeholders, ", "))
	var taskID string
	args := append([]any{agentID, runtimeID}, extraValues...)
	if err := testPool.QueryRow(context.Background(), query, args...).Scan(&taskID); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })
	return taskID
}

// TestGetTaskStatus_DoesNotResolveSourceWorkspace pins the hot-path contract:
// status polling authorizes through the owning agent's workspace and never
// follows optional issue / chat / autopilot links.
func TestGetTaskStatus_DoesNotResolveSourceWorkspace(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := seedLookupAgent(t, "MUL-7259 lookup agent")
	issueID := seedLookupIssue(t, "MUL-7259 lookup issue")
	chatID := seedLookupChatSession(t, agentID, "MUL-7259 lookup chat")
	_, runID := seedLookupAutopilotRun(t, agentID, "MUL-7259 lookup autopilot")

	for _, tc := range []struct{ query, column, id string }{
		{"GetIssue", "issue_id", issueID},
		{"GetChatSession", "chat_session_id", chatID},
		{"GetAutopilotRun", "autopilot_run_id", runID},
		{"GetAutopilot", "autopilot_run_id", runID},
	} {
		t.Run(tc.query, func(t *testing.T) {
			taskID := seedLookupTask(t, agentID, runtimeID, []string{tc.column}, tc.id)
			fault := &lookupFaultPool{DBTX: testPool, query: tc.query}
			h := &Handler{Queries: db.New(fault), TaskService: &service.TaskService{Queries: db.New(fault)}}
			req := newDaemonTokenRequest(http.MethodGet, "/api/daemon/tasks/"+taskID+"/status", nil, testWorkspaceID, "test-daemon")
			req = withURLParam(req, "taskId", taskID)

			w := httptest.NewRecorder()
			h.GetTaskStatus(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status poll: got %d: %s", w.Code, w.Body.String())
			}
			if fault.called {
				t.Fatalf("status poll unexpectedly executed %s", tc.query)
			}
			var response map[string]string
			if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response["status"] != "running" {
				t.Fatalf("status = %q, want running", response["status"])
			}
		})
	}
}

// TestGetTaskStatus_TaskRowPresenceContract distinguishes a genuinely missing
// task from a surviving task whose optional source row has gone away.
func TestGetTaskStatus_TaskRowPresenceContract(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := seedLookupAgent(t, "MUL-7259 absent agent")

	t.Run("task row missing", func(t *testing.T) {
		missing := uuid.NewString()
		req := newDaemonTokenRequest(http.MethodGet, "/api/daemon/tasks/"+missing+"/status", nil, testWorkspaceID, "test-daemon")
		req = withURLParam(req, "taskId", missing)
		w := httptest.NewRecorder()
		testHandler.GetTaskStatus(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("missing task: got %d: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "task not found") {
			t.Fatalf("a genuinely missing task must still interrupt the daemon: %s", w.Body.String())
		}
	})

	t.Run("optional source missing", func(t *testing.T) {
		// agent_task_queue.issue_id is ON DELETE CASCADE, so an issue task can
		// never outlive its issue. chat_session_id is ON DELETE SET NULL, so a
		// chat task can survive its source. The status endpoint authorizes that
		// row through its owning agent instead of treating the optional source
		// as task identity.
		chatID := seedLookupChatSession(t, agentID, "MUL-7259 doomed chat")
		taskID := seedLookupTask(t, agentID, runtimeID, []string{"chat_session_id"}, chatID)
		if _, err := testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE id = $1`, chatID); err != nil {
			t.Fatalf("delete chat session: %v", err)
		}

		req := newDaemonTokenRequest(http.MethodGet, "/api/daemon/tasks/"+taskID+"/status", nil, testWorkspaceID, "test-daemon")
		req = withURLParam(req, "taskId", taskID)
		w := httptest.NewRecorder()
		testHandler.GetTaskStatus(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("source-missing task: got %d: %s", w.Code, w.Body.String())
		}
		var response map[string]string
		if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if response["status"] != "running" {
			t.Fatalf("status = %q, want running", response["status"])
		}
	})
}

// TestGetTaskStatus_ForeignWorkspace_Returns404 pins the permission boundary:
// splitting lookup failures out of the 404 must not turn a cross-workspace task
// into a distinguishable response. A foreign task and a missing one look alike.
func TestGetTaskStatus_ForeignWorkspace_Returns404(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := seedLookupAgent(t, "MUL-7259 foreign agent")
	issueID := seedLookupIssue(t, "MUL-7259 foreign issue")
	taskID := seedLookupTask(t, agentID, runtimeID, []string{"issue_id"}, issueID)

	otherWorkspace := uuid.NewString()
	req := newDaemonTokenRequest(http.MethodGet, "/api/daemon/tasks/"+taskID+"/status", nil, otherWorkspace, "other-daemon")
	req = withURLParam(req, "taskId", taskID)
	w := httptest.NewRecorder()
	testHandler.GetTaskStatus(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("foreign workspace: got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "task not found") {
		t.Fatalf("authorization miss must not carry the daemon's deletion signal: %s", w.Body.String())
	}
}
