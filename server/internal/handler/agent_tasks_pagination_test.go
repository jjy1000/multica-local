package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// History pagination (upstream MUL-7685): ListAgentTasks is a bounded keyset
// read, not an unbounded dump. The visible-history predicate runs BEFORE
// LIMIT so hidden escalation fallbacks can never end a page early, and the
// cursor carries (created_at, id) so tied timestamps order by descending id.

func createHistoryAgent(t *testing.T, name string) string {
	t.Helper()
	var agentID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id,
			instructions, custom_env, custom_args
		)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb,
		        $3, 'workspace', 1, $4, '', '{}'::jsonb, '[]'::jsonb)
		RETURNING id
	`, testWorkspaceID, name, handlerTestRuntimeID(t), testUserID).Scan(&agentID); err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
	})
	return agentID
}

// insertHistoryTask inserts one agent_task_queue row from a column map and
// returns its id. Every caller cleans up via the returned id.
func insertHistoryTask(t *testing.T, cols map[string]any) string {
	t.Helper()
	query := `INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, created_at`
	values := []any{cols["agent_id"], cols["runtime_id"], cols["status"], int32(0), cols["created_at"]}
	placeholders := "$1, $2, $3, $4, $5"
	for _, key := range []string{"started_at", "completed_at", "escalation_for_task_id", "id"} {
		v, ok := cols[key]
		if !ok {
			continue
		}
		values = append(values, v)
		placeholders += fmt.Sprintf(", $%d", len(values))
		switch key {
		case "started_at":
			query += ", started_at"
		case "completed_at":
			query += ", completed_at"
		case "escalation_for_task_id":
			query += ", escalation_for_task_id"
		case "id":
			query += ", id"
		}
	}
	query += `) VALUES (` + placeholders + `) RETURNING id`
	var id string
	if err := testPool.QueryRow(context.Background(), query, values...).Scan(&id); err != nil {
		t.Fatalf("insert history task: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, id)
	})
	return id
}

func readHistoryPage(t *testing.T, agentID, query string, wantStatus int) ([]AgentTaskResponse, string) {
	t.Helper()
	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodGet, "/api/agents/"+agentID+"/tasks"+query, nil), "id", agentID)
	testHandler.ListAgentTasks(w, req)
	if w.Code != wantStatus {
		t.Fatalf("ListAgentTasks%s: expected %d, got %d: %s", query, wantStatus, w.Code, w.Body.String())
	}
	if wantStatus != http.StatusOK {
		return nil, ""
	}
	var tasks []AgentTaskResponse
	if err := json.NewDecoder(w.Body).Decode(&tasks); err != nil {
		t.Fatalf("decode tasks: %v", err)
	}
	return tasks, w.Header().Get(HeaderAgentTasksNextCursor)
}

func TestListAgentTasksPagination(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID := handlerTestRuntimeID(t)
	agentID := createHistoryAgent(t, "paged history agent")
	otherAgentID := createHistoryAgent(t, "other history agent")
	// Equal, sub-second timestamps exercise the tie breaker and lossless cursor.
	createdAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	visible := map[string]bool{}
	for i := 0; i < 205; i++ {
		id := insertHistoryTask(t, map[string]any{
			"agent_id": agentID, "runtime_id": runtimeID, "status": "completed", "created_at": createdAt,
		})
		visible[id] = true
	}
	var parentID string
	for id := range visible {
		parentID = id
		break
	}
	insertHistoryTask(t, map[string]any{
		"agent_id": otherAgentID, "runtime_id": runtimeID, "status": "completed", "created_at": createdAt,
	})
	// These sort ahead of the visible history. Filtering after LIMIT would
	// produce an empty/short page and incorrectly make older work inaccessible.
	for _, status := range []string{"cancelled", "deferred"} {
		insertHistoryTask(t, map[string]any{
			"agent_id": agentID, "runtime_id": runtimeID, "status": status,
			"created_at": createdAt.Add(time.Second), "escalation_for_task_id": parentID,
		})
	}
	startedFallback := insertHistoryTask(t, map[string]any{
		"agent_id": agentID, "runtime_id": runtimeID, "status": "cancelled",
		"created_at": createdAt, "started_at": createdAt, "escalation_for_task_id": parentID,
	})
	visible[startedFallback] = true

	for _, query := range []string{"", "?limit=999999"} {
		tasks, cursor := readHistoryPage(t, agentID, query, http.StatusOK)
		if len(tasks) != 200 || cursor == "" {
			t.Fatalf("%s: got %d tasks, cursor %q", query, len(tasks), cursor)
		}
	}
	for _, query := range []string{"?limit=0", "?limit=-1", "?limit=abc", "?limit=999999999999999999999", "?before=bad", "?before=2026-01-01T00:00:00Z%7Cbad"} {
		readHistoryPage(t, agentID, query, http.StatusBadRequest)
	}

	seen := map[string]bool{}
	cursor := ""
	var previous AgentTaskResponse
	for page := 0; page < 100; page++ {
		tasks, next := readHistoryPage(t, agentID, "?limit=7&before="+url.QueryEscape(cursor), http.StatusOK)
		if len(tasks) > 7 {
			t.Fatal("page exceeded limit")
		}
		for _, task := range tasks {
			if !visible[task.ID] || seen[task.ID] {
				t.Fatalf("unexpected or repeated task %s", task.ID)
			}
			if previous.ID != "" && previous.ID <= task.ID {
				t.Fatalf("tied timestamps not ordered by descending id: %s then %s", previous.ID, task.ID)
			}
			seen[task.ID] = true
			previous = task
		}
		if page == 0 {
			// Newer work must not shift subsequent pages or repeat old rows.
			insertHistoryTask(t, map[string]any{
				"agent_id": agentID, "runtime_id": runtimeID, "status": "completed",
				"created_at": createdAt.Add(time.Minute),
			})
		}
		if next == "" {
			break
		}
		if next == cursor {
			t.Fatal("cursor did not advance")
		}
		cursor = next
	}
	if len(seen) != len(visible) {
		t.Fatalf("read %d of %d visible tasks", len(seen), len(visible))
	}

	// A cursor remains usable if its boundary row is deleted between requests.
	tasks, cursor := readHistoryPage(t, agentID, "?limit=1", http.StatusOK)
	if _, err := testPool.Exec(context.Background(), "DELETE FROM agent_task_queue WHERE id = $1", tasks[0].ID); err != nil {
		t.Fatal(err)
	}
	tasks, _ = readHistoryPage(t, agentID, "?limit=1&before="+url.QueryEscape(cursor), http.StatusOK)
	if len(tasks) != 1 {
		t.Fatal("deleted boundary lost the rest of history")
	}

	emptyID := createHistoryAgent(t, "empty history agent")
	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodGet, "/api/agents/"+emptyID+"/tasks", nil), "id", emptyID)
	testHandler.ListAgentTasks(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "[]\n" {
		t.Fatalf("empty history = %d %s", w.Code, w.Body.String())
	}
}

func TestAgentActivityDurationUsesAllRuns(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID := handlerTestRuntimeID(t)
	agentID := createHistoryAgent(t, "duration agent")
	now := time.Now().UTC().Truncate(time.Second)
	// More than one history page: duration must never be calculated from the
	// newest 200 rows or change when another page is opened.
	for i := 0; i < 201; i++ {
		duration := time.Minute
		if i == 0 {
			duration = 10 * time.Minute
		}
		insertHistoryTask(t, map[string]any{
			"agent_id": agentID, "runtime_id": runtimeID, "status": "completed",
			"started_at": now.Add(-duration), "completed_at": now, "created_at": now.Add(-duration),
		})
	}
	// Out-of-window, no-duration, and in-flight runs contribute nothing.
	insertHistoryTask(t, map[string]any{
		"agent_id": agentID, "runtime_id": runtimeID, "status": "completed",
		"started_at": now.Add(-32 * 24 * time.Hour), "completed_at": now.Add(-31 * 24 * time.Hour),
		"created_at": now.Add(-32 * 24 * time.Hour),
	})
	insertHistoryTask(t, map[string]any{
		"agent_id": agentID, "runtime_id": runtimeID, "status": "cancelled",
		"completed_at": now, "created_at": now,
	})
	insertHistoryTask(t, map[string]any{
		"agent_id": agentID, "runtime_id": runtimeID, "status": "failed",
		"started_at": now.Add(time.Second), "completed_at": now, "created_at": now,
	})
	insertHistoryTask(t, map[string]any{
		"agent_id": agentID, "runtime_id": runtimeID, "status": "running",
		"started_at": now, "created_at": now,
	})

	w := httptest.NewRecorder()
	testHandler.GetWorkspaceAgentActivity30d(w, newRequest(http.MethodGet, "/api/agent-activity-30d", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GetWorkspaceAgentActivity30d: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var buckets []AgentActivityBucket
	if err := json.NewDecoder(w.Body).Decode(&buckets); err != nil {
		t.Fatalf("decode buckets: %v", err)
	}
	var count int32
	var duration float64
	for _, bucket := range buckets {
		if bucket.AgentID == agentID {
			count += bucket.DurationCount
			duration += bucket.DurationMs
		}
	}
	if count != 201 || duration != 210*60000 {
		t.Fatalf("duration/count = %v/%d, want %v/201", duration, count, 210*60000)
	}
}

func TestListAgentTasksPageBoundaries(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID := handlerTestRuntimeID(t)
	for _, count := range []int{0, 1, 2, 3, 4, 6} {
		t.Run(fmt.Sprintf("%d_tasks_limit_3", count), func(t *testing.T) {
			agentID := createHistoryAgent(t, "history boundary agent")
			now := time.Now().UTC().Truncate(time.Microsecond)
			// Deliberately reverse UUID and time order: created_at must be the
			// primary sort key, not UUID order or insertion order.
			expected := make([]string, count)
			for i := 0; i < count; i++ {
				expected[i] = insertHistoryTask(t, map[string]any{
					"id":         fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1),
					"agent_id":   agentID,
					"runtime_id": runtimeID,
					"status":     "completed",
					"created_at": now.Add(-time.Duration(i) * time.Microsecond),
				})
			}
			var got []string
			before := ""
			for page := 0; page < 3; page++ {
				tasks, next := readHistoryPage(t, agentID, "?limit=3&before="+url.QueryEscape(before), http.StatusOK)
				for _, task := range tasks {
					got = append(got, task.ID)
				}
				wantMore := len(got) < count
				if (next != "") != wantMore {
					t.Fatalf("after %d/%d tasks: cursor=%q", len(got), count, next)
				}
				if !wantMore {
					break
				}
				before = next
			}
			if len(got) != count {
				t.Fatalf("read %d of %d tasks", len(got), count)
			}
			for i := range expected {
				if got[i] != expected[i] {
					t.Fatalf("position %d = %s, want %s (newest first)", i, got[i], expected[i])
				}
			}
		})
	}
}
