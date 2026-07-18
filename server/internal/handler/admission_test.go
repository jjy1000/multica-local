package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDecidePostMergeMiss is the upstream MUL-4525 must-fix regression. The
// active-task check governs what happens after a comment merge misses. A
// query FAILURE must fail closed — never enqueue a fresh task (duplicate
// concurrent-run risk) and never report a success. Only a confirmed active
// task defers; a confirmed-none enqueues fresh. Unit-tested here because a
// real DB fault cannot be forced through valid inputs, and the decision —
// not the query call — governs the branch.
func TestDecidePostMergeMiss(t *testing.T) {
	t.Run("query error: fail closed, non-success internal_error", func(t *testing.T) {
		status, reason, enqueueFresh := decidePostMergeMiss(false, errors.New("db down"))
		if enqueueFresh {
			t.Error("enqueueFresh = true on query error; must fail closed to avoid duplicate run")
		}
		if status != DispatchBlocked || reason != ReasonInternalError {
			t.Errorf("got %s/%s, want blocked/internal_error", status, reason)
		}
	})
	t.Run("query error dominates a stale active=true", func(t *testing.T) {
		status, _, enqueueFresh := decidePostMergeMiss(true, errors.New("db down"))
		if enqueueFresh || status != DispatchBlocked {
			t.Errorf("got status %s enqueueFresh %v, want blocked + no fresh enqueue", status, enqueueFresh)
		}
	})
	t.Run("active task: defer, no fresh enqueue", func(t *testing.T) {
		status, reason, enqueueFresh := decidePostMergeMiss(true, nil)
		if enqueueFresh || status != DispatchDeferred || reason != ReasonDeferred {
			t.Errorf("got %s/%s enqueueFresh %v, want deferred + no fresh enqueue", status, reason, enqueueFresh)
		}
	})
	t.Run("no active task: enqueue a fresh follow-up", func(t *testing.T) {
		_, _, enqueueFresh := decidePostMergeMiss(false, nil)
		if !enqueueFresh {
			t.Error("enqueueFresh = false with no active task; a fresh follow-up must run")
		}
	})
}

// TestWriteDispatchBlocked verifies the structured blocked response shape:
//   - HTTP status matches the caller-provided class (403 / 409 / etc).
//   - Body has both `error` (legacy) and `reason_code` (new) fields.
//   - The legacy `error` string is generic — does NOT leak the target's
//     identity. This is the MUL-4525 privacy contract: a blocked caller
//     must not be able to distinguish 'target exists, denied' from
//     'target does not exist'.
func TestWriteDispatchBlocked(t *testing.T) {
	cases := []struct {
		name     string
		code     DispatchReasonCode
		wantHTTP int
		wantSub  string
	}{
		{"invocation_not_allowed", ReasonInvocationNotAllowed, http.StatusForbidden, "permission"},
		{"target_unavailable", ReasonTargetUnavailable, http.StatusConflict, "unavailable"},
		{"runtime_offline", ReasonRuntimeOffline, http.StatusConflict, "offline"},
		{"attribution_blocked", ReasonAttributionBlocked, http.StatusConflict, "attributed"},
		{"already_active", ReasonAlreadyActive, http.StatusConflict, "active"},
		{"self_trigger_suppressed", ReasonSelfTriggerSuppressed, http.StatusForbidden, "suppressed"},
		{"internal_error", ReasonInternalError, http.StatusInternalServerError, "internal"},
		{"unknown reason → generic message", DispatchReasonCode("made_up"), http.StatusForbidden, "blocked"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			testHandler.writeDispatchBlocked(w, tc.wantHTTP, tc.code)
			if w.Code != tc.wantHTTP {
				t.Fatalf("status = %d, want %d", w.Code, tc.wantHTTP)
			}
			var body dispatchBlockedResponse
			if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.ReasonCode != tc.code {
				t.Errorf("reason_code = %q, want %q", body.ReasonCode, tc.code)
			}
			if body.Error == "" {
				t.Error("legacy error field is empty; clients on the old path will see nothing")
			}
			if !strings.Contains(body.Error, tc.wantSub) {
				t.Errorf("legacy error %q does not contain expected substring %q", body.Error, tc.wantSub)
			}
		})
	}
}

// TestDispatchOutcomeJSONShape pins the on-the-wire shape. Existing
// wire-compat comments claim it is 'additive on the wire: old clients that
// ignore it keep working' — that means the omitempty tags on optional
// fields MUST round-trip cleanly with no nil-pointer panics, and the
// required fields must always be present. Tests both an empty / blocked
// outcome and a queued outcome with a target ref.
func TestDispatchOutcomeJSONShape(t *testing.T) {
	t.Run("blocked outcome", func(t *testing.T) {
		o := DispatchOutcome{
			Status:     DispatchBlocked,
			ReasonCode: ReasonInvocationNotAllowed,
		}
		b, err := json.Marshal(o)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var roundtrip map[string]any
		if err := json.Unmarshal(b, &roundtrip); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if roundtrip["status"] != string(DispatchBlocked) {
			t.Errorf("status = %v, want %q", roundtrip["status"], DispatchBlocked)
		}
		if roundtrip["reason_code"] != string(ReasonInvocationNotAllowed) {
			t.Errorf("reason_code = %v, want %q", roundtrip["reason_code"], ReasonInvocationNotAllowed)
		}
		if _, ok := roundtrip["target"]; ok {
			t.Error("target must be omitted when nil (omitempty)")
		}
		if _, ok := roundtrip["task_id"]; ok {
			t.Error("task_id must be omitted when nil (omitempty)")
		}
	})

	t.Run("queued outcome with target + task id", func(t *testing.T) {
		taskID := "t_abc"
		o := DispatchOutcome{
			Status:     DispatchQueued,
			ReasonCode: ReasonQueued,
			Target:     &DispatchTarget{Type: "agent", ID: "a_1", Name: "Research"},
			TaskID:     &taskID,
		}
		b, err := json.Marshal(o)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var roundtrip map[string]any
		if err := json.Unmarshal(b, &roundtrip); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		target, ok := roundtrip["target"].(map[string]any)
		if !ok {
			t.Fatalf("target must serialize as a JSON object, got %T", roundtrip["target"])
		}
		if target["type"] != "agent" {
			t.Errorf("target.type = %v, want agent", target["type"])
		}
		if roundtrip["task_id"] != taskID {
			t.Errorf("task_id = %v, want %s", roundtrip["task_id"], taskID)
		}
	})
}

// TestDispatchReasonCodeValuesLock pins the wire values. New codes may be
// added, but renaming an existing one is a breaking wire change — clients
// switch with a default branch and never match the literal. This test
// guards the eight currently-defined codes against accidental drift.
func TestDispatchReasonCodeValuesLock(t *testing.T) {
	want := map[string]bool{
		"queued":                  true,
		"coalesced":               true,
		"deferred":                true,
		"invocation_not_allowed":  true,
		"target_unavailable":      true,
		"runtime_offline":         true,
		"attribution_blocked":     true,
		"already_active":          true,
		"self_trigger_suppressed": true,
		"internal_error":          true,
	}
	got := map[string]bool{
		string(ReasonQueued):                true,
		string(ReasonCoalesced):             true,
		string(ReasonDeferred):              true,
		string(ReasonInvocationNotAllowed):  true,
		string(ReasonTargetUnavailable):     true,
		string(ReasonRuntimeOffline):        true,
		string(ReasonAttributionBlocked):    true,
		string(ReasonAlreadyActive):         true,
		string(ReasonSelfTriggerSuppressed): true,
		string(ReasonInternalError):         true,
	}
	if len(got) != len(want) {
		t.Errorf("count mismatch: got %d codes, want %d", len(got), len(want))
	}
	for k := range got {
		if !want[k] {
			t.Errorf("unexpected reason code %q in local set", k)
		}
	}
	for k := range want {
		if !got[k] {
			t.Errorf("missing reason code %q from local set", k)
		}
	}
}