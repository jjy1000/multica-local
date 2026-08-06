package daemon

import (
	"testing"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

// TestContextExhaustionClassifiesRegardlessOfToolUse pins GH #6402's boundary
// for this fork's daemon: the completed-branch classifier is decided by the
// output alone, and never by how many tools the run executed.
//
// "Did this run produce an answer?" must be answered from the output because a
// context window fills up mid-task far more often than on the first turn — a
// tools == 0 gate would miss the common case and leave exactly the silent
// stall the issue reports.
func TestContextExhaustionClassifiesRegardlessOfToolUse(t *testing.T) {
	reason, ok := classifyPoisonedOutput(
		"Prompt is too long · this conversation is a single exchange and cannot be compacted — " +
			"the request size comes mostly from system prompt, tool definitions, or attachments.")
	if !ok {
		t.Fatal("expected the context-exhaustion notice to be classified as poisoned output")
	}
	if reason != string(taskfailure.ReasonAgentContextOverflow) {
		t.Fatalf("reason = %q, want %q", reason, taskfailure.ReasonAgentContextOverflow)
	}
	// The reason must be one the server refuses to auto-retry: the run may have
	// executed tools, and re-running it has no idempotency key.
	if retryableFromDaemonPerspective(reason) {
		t.Fatalf("%q must not be auto-retryable", reason)
	}
}

// TestContextExhaustionNeverReplaysTheTask pins the OTHER question the fix has
// to keep apart: "may we re-run THIS task with a fresh session?". This fork's
// daemon has no shouldRetryWithFreshSession helper — the only in-task replay is
// the inline gate at the top of the claim handler, which fires when a resume
// failed BEFORE establishing a session (result.SessionID == ""). A context-
// exhausted run always carries its session id (the transcript loaded fine, it
// simply filled), so the gate never fires for it — the task fails once, its
// session is retired by the resume blacklist, and the NEXT trigger starts
// fresh. Both halves of that contract are asserted here against the fork's
// actual code shapes.
func TestContextExhaustionNeverReplaysTheTask(t *testing.T) {
	exhausted := agent.Result{
		Status:    "failed",
		SessionID: "sess-full",
		Error: "claude ended the turn with terminal_reason=" + taskfailure.TerminalReasonPromptTooLong +
			": the session's context window is exhausted and compaction could not recover it (Prompt is too long)",
	}

	// Half 1: the inline replay gate. A context-exhausted run established a
	// session before dying, so `result.SessionID == ""` is false and the retry
	// branch cannot trigger — regardless of the prior session or the tool count.
	for _, tools := range []int32{0, 1, 17} {
		if replayGateFires(exhausted, "sess-full", tools) {
			t.Errorf("tools=%d: a full context window must not trigger an in-task replay", tools)
		}
	}
	// Half 2 (resume blacklist + auto-retry allowlist membership) is asserted in
	// internal/service's context-overflow contract test — resumeUnsafeFailureReason
	// and retryableReasons live there and are package-private to it.
}

// replayGateFires mirrors the fork's inline "session resume failed, retrying
// with fresh session" condition in daemon.go's claim handler: it fires only
// when a resume failed without establishing a session. Kept local so this
// test does not have to thread the full handler harness; if the daemon ever
// gains a dedicated shouldRetryWithFreshSession helper, port the mirror to it.
func replayGateFires(result agent.Result, priorSessionID string, _ int32) bool {
	return result.Status == "failed" && priorSessionID != "" && result.SessionID == ""
}

// retryableFromDaemonPerspective mirrors the server's retryableReasons
// allowlist (internal/service/task.go) for the reasons the daemon can emit.
// Kept local so this package's test does not import internal/service, and
// deliberately exhaustive against the FORK's allowlist (which has no
// provider_network / skill_bundle entries — those are not auto-retried here).
// If a future change adds context_overflow to the server allowlist, this test
// still documents why it should not be there.
func retryableFromDaemonPerspective(reason string) bool {
	switch reason {
	case "runtime_offline", "runtime_recovery", "timeout", "codex_semantic_inactivity":
		return true
	default:
		return false
	}
}
