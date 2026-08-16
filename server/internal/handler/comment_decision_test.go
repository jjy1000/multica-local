package handler

import (
	"errors"
	"testing"
)

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