package handler

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// dupRaceFixture creates a workspace-invocable agent and an issue assigned to it.
func dupRaceFixture(t *testing.T, agentName string, issueNumber int) (agentID, issueID, runtimeID string) {
	t.Helper()
	ctx := context.Background()
	agentID = createHandlerTestAgent(t, agentName, nil)
	if err := testPool.QueryRow(ctx, `SELECT runtime_id::text FROM agent WHERE id = $1`, agentID).Scan(&runtimeID); err != nil {
		t.Fatalf("load runtime: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position, assignee_type, assignee_id)
		VALUES ($1, 'dup-enqueue-race fixture', 'in_progress', 'none', $2, 'member', $3, 0, 'agent', $4)
		RETURNING id::text
	`, testWorkspaceID, testUserID, issueNumber, agentID).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID)
	})
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM comment WHERE issue_id = $1`, issueID) })
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })
	return agentID, issueID, runtimeID
}

func insertDupRaceComment(t *testing.T, issueID, content, age string) string {
	t.Helper()
	// These race cases compete for the same thread slot.
	var parentID *string
	if err := testPool.QueryRow(context.Background(), `SELECT (SELECT id::text FROM comment WHERE issue_id=$1 AND parent_id IS NULL ORDER BY created_at, id LIMIT 1)`, issueID).Scan(&parentID); err != nil {
		t.Fatalf("lookup thread parent: %v", err)
	}
	var id string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type, created_at, parent_id)
		VALUES ($1, $2, 'member', $3, $4, 'comment', now() - $5::interval, $6)
		RETURNING id::text
	`, issueID, testWorkspaceID, testUserID, content, age, parentID).Scan(&id); err != nil {
		t.Fatalf("insert comment: %v", err)
	}
	return id
}

func commentCovered(t *testing.T, issueID, agentID, commentID, statusFilter string) bool {
	t.Helper()
	var ok bool
	if err := testPool.QueryRow(context.Background(), `
		SELECT $3::uuid = trigger_comment_id OR $3::uuid = ANY(coalesced_comment_ids)
		FROM agent_task_queue
		WHERE issue_id = $1 AND agent_id = $2 AND status = $4
	`, issueID, agentID, commentID, statusFilter).Scan(&ok); err != nil {
		t.Fatalf("check covered %s: %v", commentID, err)
	}
	return ok
}

// TestCommentEnqueueRaceQueuedWinnerFoldsLoser: when the lost-race winner is
// still QUEUED, the losing comment is folded into it (the merge makes the newer
// comment the trigger and pushes the prior trigger into coalesced), so the
// single run covers both. Coalesced outcome, one pending task, and no warning /
// constraint-name leak.
//
// Run once per trigger source that can lose this race on an issue whose agent is
// the assignee. Both enqueues now normalize the unique violation into the
// ErrDuplicatePendingTask sentinel (upstream b8d03bc3f, MUL-7326), so the loser
// coalesces instead of surfacing blocked/internal_error — which dropped the
// comment's instruction outright: the comment-creation path has no obligation
// hand-off, and a losing comment usually PREDATES the winning task row, so
// completion reconcile's created_at window cannot pick it up either.
//
// Fork port note: upstream's file grew to eight tests across the MUL-4302
// review rounds (#5958 lineage) with a dbfx fixture package and a
// resolveCommentTriggerEnqueue state machine the fork never ported. This port
// carries only the table-driven test this commit (b8d03bc3f) added, adapted to
// the fork's enqueueCommentAgentTriggers signature and plain-SQL fixtures.
func TestCommentEnqueueRaceQueuedWinnerFoldsLoser(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	cases := []struct {
		name          string
		agentName     string
		issueNumber   int
		source        commentAgentTriggerSource
		enqueueWinner func(ctx context.Context, issue db.Issue, agentID, commentID pgtype.UUID) error
	}{
		{
			name:        "mention",
			agentName:   "dup-race-queued",
			issueNumber: 999311,
			source:      commentTriggerSourceMentionAgent,
			enqueueWinner: func(ctx context.Context, issue db.Issue, agentID, commentID pgtype.UUID) error {
				_, err := testHandler.TaskService.EnqueueTaskForMention(ctx, issue, agentID, commentID)
				return err
			},
		},
		{
			name:        "issue_assignee",
			agentName:   "dup-race-assignee",
			issueNumber: 999316,
			source:      commentTriggerSourceIssueAssignee,
			enqueueWinner: func(ctx context.Context, issue db.Issue, _, commentID pgtype.UUID) error {
				_, err := testHandler.TaskService.EnqueueTaskForIssue(ctx, issue, commentID)
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			agentID, issueID, _ := dupRaceFixture(t, tc.agentName, tc.issueNumber)
			agentUUID := util.MustParseUUID(agentID)
			issue, err := testHandler.Queries.GetIssue(ctx, util.MustParseUUID(issueID))
			if err != nil {
				t.Fatalf("load issue: %v", err)
			}
			agent, err := testHandler.Queries.GetAgent(ctx, agentUUID)
			if err != nil {
				t.Fatalf("load agent: %v", err)
			}

			winnerCommentID := insertDupRaceComment(t, issueID, "first instruction", "6 minutes")
			if err := tc.enqueueWinner(ctx, issue, agentUUID, util.MustParseUUID(winnerCommentID)); err != nil {
				t.Fatalf("enqueue winning task: %v", err)
			}
			loserCommentID := insertDupRaceComment(t, issueID, "second distinct instruction", "1 minute")

			trigger := commentAgentTrigger{Agent: agent, Source: tc.source}
			// Member actor, matching the production CreateComment shape
			// (daemon.go's reconcile passes the comment author the same way);
			// actor resolution is not what this test exercises.
			results := testHandler.enqueueCommentAgentTriggers(ctx, issue, util.MustParseUUID(loserCommentID), []commentAgentTrigger{trigger}, "member", testUserID, testUserID)

			// Guards the service hunk: the lost race must coalesce, not block.
			if res := results[agentID]; res.status != DispatchCoalesced {
				t.Fatalf("queued-winner race: got status %q reason %q, want coalesced", res.status, res.reason)
			}
			if !commentCovered(t, issueID, agentID, loserCommentID, "queued") {
				t.Fatal("losing comment was NOT folded into the queued winner — its instruction would be dropped")
			}
			if !commentCovered(t, issueID, agentID, winnerCommentID, "queued") {
				t.Fatal("winner comment is no longer covered after the fold")
			}
			if n := pendingTaskCountForAgentIssue(t, issueID, agentID); n != 1 {
				t.Fatalf("pending task count = %d, want exactly 1", n)
			}
		})
	}
}
