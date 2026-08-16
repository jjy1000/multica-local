// Package handler — comment_decision_test.go (0.5.22 MUL-4525 §2 port).
//
// Fork-local pure coverage of the dispatch decisions extracted from
// comment.go / admission.go. Each decision is a pure function so it is
// trivial to unit-test without DB or service dependencies. The caller relies
// on the return shape (not on the query call) to govern its branch.
//
// What lives here:
//   - TestDecidePostMergeMiss — pinned in admission_test.go (round-3 baseline).
//   - TestDecideSuppressedLeaderOutcome — round-4 honest-outcome gate.
//   - TestCommentMergeTerminalOutcome — round-5 honest merge-outcome gate
//     (upstream 300a4c629): a real merge is coalesced, a refused/failed
//     merge is blocked/internal_error, and only "no queued task to fold"
//     falls through to the active-task decision.
package handler

import (
	"errors"
	"testing"
)

// TestCommentMergeTerminalOutcome is the round-5 mapping pin (upstream
// 300a4c629, "honest merge outcome"): the four commentMergeResult values must
// each map to the precise (status, reason, terminal) triple the caller relies
// on. terminal=false ONLY for commentMergeNoPendingTask — every other
// resolution carries its own outcome and short-circuits the caller.
//
// This is a pure mapping test — no DB, no handler. The caller-side wiring
// (enqueueCommentAgentTriggers) is exercised in comment_merge_failclosed_test.
func TestCommentMergeTerminalOutcome(t *testing.T) {
	cases := []struct {
		name        string
		result      commentMergeResult
		wantStatus  DispatchStatus
		wantReason  DispatchReasonCode
		wantTerminal bool
	}{
		{"succeeded → coalesced (success-shaped)", commentMergeSucceeded, DispatchCoalesced, ReasonCoalesced, true},
		{"no pending task → fall through", commentMergeNoPendingTask, "", "", false},
		{"unknown error → blocked internal_error (must NOT be coalesced)", commentMergeError, DispatchBlocked, ReasonInternalError, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, reason, terminal := commentMergeTerminalOutcome(tc.result)
			if status != tc.wantStatus || reason != tc.wantReason || terminal != tc.wantTerminal {
				t.Errorf("got %s/%s (terminal=%v), want %s/%s (terminal=%v)",
					status, reason, terminal, tc.wantStatus, tc.wantReason, tc.wantTerminal)
			}
		})
	}
}

// TestDecideSuppressedLeaderOutcome: the self-trigger-suppressed squad
// leader's active-task check must never fake success — a query error is a
// non-success internal_error, a confirmed active run defers, and a
// confirmed-none is self_trigger_suppressed (MUL-4525 §2 round-4, Elon
// review).
//
// Unit-tested here because a real DB fault cannot be forced through valid
// handler inputs, and this decision — not the query call — is what actually
// governs the branch.
//
// (TestDecidePostMergeMiss lives in admission_test.go — the round-3 baseline
// already pinned that decision there.)
func TestDecideSuppressedLeaderOutcome(t *testing.T) {
	cases := []struct {
		name       string
		active     bool
		err        error
		wantStatus DispatchStatus
		wantReason DispatchReasonCode
	}{
		{"query error", false, errors.New("db down"), DispatchBlocked, ReasonInternalError},
		{"query error dominates stale active", true, errors.New("db down"), DispatchBlocked, ReasonInternalError},
		{"active run", true, nil, DispatchDeferred, ReasonAlreadyActive},
		{"no active run", false, nil, DispatchBlocked, ReasonSelfTriggerSuppressed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, reason := decideSuppressedLeaderOutcome(tc.active, tc.err)
			if status != tc.wantStatus || reason != tc.wantReason {
				t.Errorf("got %s/%s, want %s/%s", status, reason, tc.wantStatus, tc.wantReason)
			}
		})
	}
}