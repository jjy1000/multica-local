package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// 0.5.114 critique gate: a completed claude_science_lab research task
// schedules exactly one review task for the critique reviewer; the
// reviewer's own completion schedules nothing; non-lab issues schedule
// nothing. Runs against the shared handler DB harness (service has no
// DB bootstrap of its own) — TaskService only needs Queries here, the
// critique enqueue path never touches the tx starter.
func TestClaudeLabCritique_AutoEnqueue(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test harness unavailable (needs DATABASE_URL)")
	}
	wsUUID := mustParseUUID(t, testWorkspaceID)
	ctx := context.Background()

	if _, err := testPool.Exec(ctx,
		`DELETE FROM issue WHERE workspace_id = $1 AND title = $2`,
		wsUUID, "lab-test-"+t.Name()); err != nil {
		t.Fatalf("pre-clean fixture issue: %v", err)
	}

	memberID := mustCreateTestMember(t, wsUUID)
	researchID := mustCreateTestAgent(t, wsUUID, "research", memberID)
	critiqueID := mustCreateTestAgent(t, wsUUID, "critique", memberID)
	issueID := mustCreateTestLabIssue(t, wsUUID, memberID, researchID)
	plainID := mustCreatePlainIssueForCritiqueTest(t, wsUUID, memberID, researchID)

	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE issue_id IN ($1, $2)`, issueID, plainID)
		_, _ = testPool.Exec(ctx, `DELETE FROM issue WHERE id IN ($1, $2)`, issueID, plainID)
		_, _ = testPool.Exec(ctx, `DELETE FROM agent WHERE id IN ($1, $2)`, researchID, critiqueID)
	})

	svc := &service.TaskService{Queries: testHandler.Queries}
	result, _ := json.Marshal(map[string]string{"output": "The measured half-life was 12.4s vs the claimed 12.0s."})
	researchTask := db.AgentTaskQueue{ID: testUUIDForCritique(1), AgentID: researchID, IssueID: issueID}

	// 1. research completion on the lab issue → exactly one critique task
	svc.MaybeEnqueueClaudeLabCritique(ctx, researchTask, result)
	assertCritiqueTaskCount(t, ctx, issueID, critiqueID, 1)

	// 2. a second completion while the review is pending coalesces (unique
	//    index / pending check)
	svc.MaybeEnqueueClaudeLabCritique(ctx, researchTask, result)
	assertCritiqueTaskCount(t, ctx, issueID, critiqueID, 1)

	// 3. loop cut: critique's own completion schedules nothing new
	var critiqueTaskID pgtype.UUID
	if err := testPool.QueryRow(ctx,
		`SELECT id FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2 LIMIT 1`,
		issueID, critiqueID).Scan(&critiqueTaskID); err != nil {
		t.Fatalf("load critique task: %v", err)
	}
	svc.MaybeEnqueueClaudeLabCritique(ctx, db.AgentTaskQueue{ID: critiqueTaskID, AgentID: critiqueID, IssueID: issueID}, result)
	assertCritiqueTaskCount(t, ctx, issueID, critiqueID, 1)

	// 4. handoff note carries the research digest
	var handoff pgtype.Text
	if err := testPool.QueryRow(ctx,
		`SELECT handoff_note FROM agent_task_queue WHERE id = $1`, critiqueTaskID).Scan(&handoff); err != nil {
		t.Fatalf("load handoff note: %v", err)
	}
	if !handoff.Valid || !strings.Contains(handoff.String, "12.4s") {
		t.Errorf("handoff note must carry the output digest, got: %q", handoff.String)
	}

	// 5. non-lab issue → no critique task ever
	plainTask := db.AgentTaskQueue{ID: testUUIDForCritique(2), AgentID: researchID, IssueID: plainID}
	svc.MaybeEnqueueClaudeLabCritique(ctx, plainTask, result)
	assertCritiqueTaskCount(t, ctx, plainID, critiqueID, 0)
}

func mustCreatePlainIssueForCritiqueTest(t *testing.T, wsID, creator, agent pgtype.UUID) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO issue (workspace_id, creator_id, creator_type, title, status, assignee_id, assignee_type, number)
		VALUES ($1, $2, 'member', $3, 'todo', $4, 'agent', 987655)
		RETURNING id`,
		wsID, creator, "critique-plain-"+t.Name(), agent).Scan(&id)
	if err != nil {
		t.Fatalf("create plain issue: %v", err)
	}
	return id
}

func testUUIDForCritique(b byte) pgtype.UUID {
	var u pgtype.UUID
	u.Valid = true
	u.Bytes[0] = b
	return u
}

func assertCritiqueTaskCount(t *testing.T, ctx context.Context, issueID, critiqueID pgtype.UUID, want int) {
	t.Helper()
	var n int
	if err := testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2`,
		issueID, critiqueID).Scan(&n); err != nil {
		t.Fatalf("count critique tasks: %v", err)
	}
	if n != want {
		t.Fatalf("critique tasks for issue: got %d want %d", n, want)
	}
}
