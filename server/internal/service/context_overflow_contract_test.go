package service

import (
	"testing"

	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

// TestContextOverflowResumeAndRetryContract pins the two ledger memberships a
// context-exhausted run must satisfy (upstream #6366 / #6422 / GH #6402):
//
//   - The reason IS resume-unsafe: GetLastTaskSession / GetLastChatTaskSession
//     and the chat-session inheritance gate must never hand the next run the
//     saturated session, or every later trigger replays the overflow.
//   - The reason is NOT auto-retryable: retryableReasons excludes agent-side
//     errors by design, and a context-overflow task may have executed tools, so
//     re-running it has no idempotency key.
func TestContextOverflowResumeAndRetryContract(t *testing.T) {
	if !resumeUnsafeFailureReason(string(taskfailure.ReasonAgentContextOverflow)) {
		t.Fatalf("%q must be resume-unsafe so the dead session is retired",
			taskfailure.ReasonAgentContextOverflow)
	}
	if retryableReasons[string(taskfailure.ReasonAgentContextOverflow)] {
		t.Fatalf("%q must not be auto-retryable", taskfailure.ReasonAgentContextOverflow)
	}
}
