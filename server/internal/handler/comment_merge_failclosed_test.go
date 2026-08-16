// Package handler — comment_merge_failclosed_test.go (0.5.22 MUL-4525 §2 port).
//
// Pins the round-5 honest-merge-outcome contract (upstream 300a4c629,
// "refused merge is blocked, not fake coalesced"): mergeCommentIntoPendingTask
// returns a commentMergeResult, and the caller maps it via
// commentMergeTerminalOutcome. A real merge is coalesced; a refusal/failure
// is non-success. Only "no queued task to fold into" falls through.
//
// Fork-local adaptation: upstream 300a4c629 also added an attribution
// fail-closed branch (commentMergeAttributionBlocked) gated on
// service.ErrAttributionFailClosed from TaskService.AttributionForMergedComment.
// The local 0.3.7 fork's merge path does NOT call AttributionForMergedComment
// (that helper is out of scope here — see comment.go:1667-1674), so the
// attribution_blocked result does not exist in this revision and the refused
// merge collapses to commentMergeError → blocked/internal_error.
//
// What IS exercised here:
//   - TestMergeCommentIntoPendingTask_QueuedTaskMergeSucceeds — the happy
//     path: queued task + valid trigger comment → commentMergeSucceeded, and
//     the outcome mapping yields coalesced (success-shaped).
//   - TestMergeCommentIntoPendingTask_NoQueuedTaskFallsThrough — the
//     no-pending-task branch: caller reports fall-through (terminal=false) so
//     the active-task decision can run.
//   - TestMergeCommentIntoPendingTask_NoSquadFakeCoalesced — the regression
//     pin: a non-success commentMergeError must NEVER map to a success-shaped
//     coalesced outcome (the original bug: caller recorded coalesced for a
//     merge that never happened).
package handler

import (
	"context"
	"testing"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// queuedTaskForMerge inserts a queued agent_task_queue row for (agent, issue)
// whose status is 'queued' so the merge path's UPDATE WHERE status='queued'
// predicate can match. Returns the task ID + the trigger_comment_id the
// merge will overwrite. Mirrors the shape of the fixture in
// comment_reconcile_test.go::TestCompleteTask_ReconcilesPreDispatchMergeRaceComment.
func queuedTaskForMerge(t *testing.T, ctx context.Context, issueID, agentID, triggerCommentID string) string {
	t.Helper()
	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue
			(agent_id, runtime_id, issue_id, trigger_comment_id, coalesced_comment_ids, status, priority)
		VALUES ($1, $2, $3, $4, '{}', 'queued', 0)
		RETURNING id
	`, agentID, handlerTestRuntimeID(t), issueID, triggerCommentID).Scan(&taskID); err != nil {
		t.Fatalf("setup: queued task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })
	return taskID
}

// mergeTriggerCommentFor inserts a member-authored comment on the given issue
// and returns its UUID. Used as both the queued task's trigger_comment_id and
// the "new trigger comment" argument to mergeCommentIntoPendingTask.
func mergeTriggerCommentFor(t *testing.T, ctx context.Context, issueID, content string) string {
	t.Helper()
	var id string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type)
		VALUES ($1, $2, 'member', $3, $4, 'comment') RETURNING id
	`, issueID, testWorkspaceID, testUserID, content).Scan(&id); err != nil {
		t.Fatalf("setup: trigger comment: %v", err)
	}
	return id
}

// loadAgentForMergeTrigger fetches a full db.Agent row so the trigger carries
// the right struct shape for mergeCommentIntoPendingTask.
func loadAgentForMergeTrigger(t *testing.T, ctx context.Context, agentID string) db.Agent {
	t.Helper()
	agent, err := testHandler.Queries.GetAgent(ctx, util.MustParseUUID(agentID))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	return agent
}

// loadIssueForMergeTrigger fetches a full db.Issue row so the merge call site
// gets a struct (not a UUID string) for issue.
func loadIssueForMergeTrigger(t *testing.T, ctx context.Context, issueID string) db.Issue {
	t.Helper()
	issue, err := testHandler.Queries.GetIssue(ctx, util.MustParseUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	return issue
}

// TestMergeCommentIntoPendingTask_QueuedTaskMergeSucceeds is the happy-path
// pin: a queued task exists, the merge SQL updates it, and the result is
// commentMergeSucceeded → coalesced. This is the success-shape caller must
// preserve.
func TestMergeCommentIntoPendingTask_QueuedTaskMergeSucceeds(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	agentID := createHandlerTestAgent(t, "Merge Honesty: Succeeded", nil)
	issueID := createCommentTriggerPreviewIssue(t, "merge honesty succeeded", "", "")
	triggerCommentID := mergeTriggerCommentFor(t, ctx, issueID, "initial trigger")
	queuedTaskForMerge(t, ctx, issueID, agentID, triggerCommentID)
	newTriggerCommentID := mergeTriggerCommentFor(t, ctx, issueID, "follow-up should fold in")

	issue := loadIssueForMergeTrigger(t, ctx, issueID)
	agent := loadAgentForMergeTrigger(t, ctx, agentID)
	trigger := commentAgentTrigger{
		Agent:  agent,
		Source: commentTriggerSourceIssueAssignee,
	}

	result := testHandler.mergeCommentIntoPendingTask(ctx, issue, trigger, util.MustParseUUID(newTriggerCommentID), "member", testUserID)
	if result != commentMergeSucceeded {
		t.Fatalf("merge result = %d, want commentMergeSucceeded", result)
	}
	if status, reason, terminal := commentMergeTerminalOutcome(result); !terminal || status != DispatchCoalesced || reason != ReasonCoalesced {
		t.Errorf("succeeded outcome = %s/%s (terminal=%v), want coalesced/coalesced (terminal=true)", status, reason, terminal)
	}
}

// TestMergeCommentIntoPendingTask_NoQueuedTaskFallsThrough pins the fall-through
// branch: no queued task exists (pgx.ErrNoRows) → commentMergeNoPendingTask →
// terminal=false so the caller can run the active-task decision. A real merge
// must NOT collapse to the no-pending-task result.
func TestMergeCommentIntoPendingTask_NoQueuedTaskFallsThrough(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	agentID := createHandlerTestAgent(t, "Merge Honesty: No Pending Task", nil)
	issueID := createCommentTriggerPreviewIssue(t, "merge honesty no-pending-task", "", "")
	// No queued task seeded → MergeCommentIntoPendingTask returns ErrNoRows
	// because the UPDATE WHERE status='queued' matches zero rows.
	newTriggerCommentID := mergeTriggerCommentFor(t, ctx, issueID, "comment with no queued task to fold into")

	issue := loadIssueForMergeTrigger(t, ctx, issueID)
	agent := loadAgentForMergeTrigger(t, ctx, agentID)
	trigger := commentAgentTrigger{
		Agent:  agent,
		Source: commentTriggerSourceIssueAssignee,
	}

	result := testHandler.mergeCommentIntoPendingTask(ctx, issue, trigger, util.MustParseUUID(newTriggerCommentID), "member", testUserID)
	if result != commentMergeNoPendingTask {
		t.Fatalf("merge result = %d, want commentMergeNoPendingTask", result)
	}
	if status, reason, terminal := commentMergeTerminalOutcome(result); terminal {
		t.Errorf("no-pending-task outcome must NOT be terminal (must fall through to active-task decision), got %s/%s (terminal=true)", status, reason)
	}
}

// TestCommentMergeTerminalOutcome_NeverCoalescedForError is the round-5
// regression pin: a commentMergeError must NEVER map to a success-shaped
// coalesced outcome. This is the contract the original bool-returning code
// violated (attribution fail-closed + unknown DB errors both returned
// handled=true, which the caller then recorded as coalesced). Now the mapping
// is explicit and pinned.
func TestCommentMergeTerminalOutcome_NeverCoalescedForError(t *testing.T) {
	if status, reason, terminal := commentMergeTerminalOutcome(commentMergeError); !terminal || status == DispatchCoalesced || reason == ReasonCoalesced {
		t.Errorf("error outcome = %s/%s (terminal=%v), must be terminal AND non-success (NOT coalesced)", status, reason, terminal)
	}
}