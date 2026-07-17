package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/experimental"
)

// TestListExperimentalFlags_DefaultValues pins the "no override ⇒ default"
// semantic. The handler is called by a freshly-instrumented test user who
// has never toggled anything, so every flag's Enabled must equal the
// catalog DefaultVal. Any future flip in catalog defaults will surface
// here first — intentional, since these are the values the Labs UI shows
// on first visit.
func TestListExperimentalFlags_DefaultValues(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	w := httptest.NewRecorder()
	r := newRequest("GET", "/api/experimental-flags", nil)
	testHandler.ListExperimentalFlags(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	var resp ExperimentalFlagsListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Flags) == 0 {
		t.Fatal("expected at least one catalog flag (chat_pin_ui)")
	}
	// Catalog length pins: every catalog entry must be exposed AND the
	// response must not silently drop or duplicate any flag. A future
	// catalog edit will surface here first.
	if len(resp.Flags) != len(experimental.Catalog) {
		t.Fatalf("response returned %d flags, catalog has %d — handler may be filtering or duplicating",
			len(resp.Flags), len(experimental.Catalog))
	}

	// Every catalog flag must appear exactly once with Enabled=DefaultEnabled.
	for _, cf := range experimental.Catalog {
		var got *ExperimentalFlagResponse
		for i := range resp.Flags {
			if resp.Flags[i].Key == cf.Key {
				got = &resp.Flags[i]
				break
			}
		}
		if got == nil {
			t.Fatalf("catalog flag %q missing from response", cf.Key)
		}
		if got.DefaultEnabled != cf.DefaultVal {
			t.Fatalf("flag %q: response default_enabled=%v, catalog=%v",
				cf.Key, got.DefaultEnabled, cf.DefaultVal)
		}
		if got.Enabled != cf.DefaultVal {
			t.Fatalf("flag %q: response enabled=%v, expected catalog default %v (no override)",
				cf.Key, got.Enabled, cf.DefaultVal)
		}
	}
}

// TestUpdateExperimentalFlag_TogglesAndPersists exercises the full
// write→read round-trip: PATCH flips the pref, GET returns the new value.
// This is the integration test for the optimistic Labs UI flow.
func TestUpdateExperimentalFlag_TogglesAndPersists(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	// Flip chat_pin_ui on for the test user, then verify GET reflects it.
	w := httptest.NewRecorder()
	r := newRequest("PATCH", "/api/experimental-flags/chat_pin_ui", map[string]any{
		"enabled": true,
	})
	r = withURLParam(r, "key", "chat_pin_ui")
	testHandler.UpdateExperimentalFlag(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d (body=%s)", w.Code, w.Body.String())
	}
	t.Cleanup(func() {
		// Reset to false so other tests see the canonical default.
		reset := httptest.NewRecorder()
		resetR := newRequest("PATCH", "/api/experimental-flags/chat_pin_ui", map[string]any{
			"enabled": false,
		})
		resetR = withURLParam(resetR, "key", "chat_pin_ui")
		testHandler.UpdateExperimentalFlag(reset, resetR)
	})

	w = httptest.NewRecorder()
	r = newRequest("GET", "/api/experimental-flags", nil)
	testHandler.ListExperimentalFlags(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on follow-up GET, got %d (body=%s)", w.Code, w.Body.String())
	}

	var resp ExperimentalFlagsListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, f := range resp.Flags {
		if f.Key == "chat_pin_ui" && !f.Enabled {
			t.Fatalf("expected chat_pin_ui enabled=true after PATCH, got false")
		}
	}
}

// TestUpdateExperimentalFlag_RejectsUnknownKey is the safety net that keeps
// the labs UI from accidentally producing rows no flag definition can
// render. Without this check, a typo or stale client could land a row in
// experimental_pref that the user then has no UI affordance to clear.
func TestUpdateExperimentalFlag_RejectsUnknownKey(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	w := httptest.NewRecorder()
	r := newRequest("PATCH", "/api/experimental-flags/nonexistent_flag", map[string]any{
		"enabled": true,
	})
	r = withURLParam(r, "key", "nonexistent_flag")
	testHandler.UpdateExperimentalFlag(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown flag key, got %d (body=%s)", w.Code, w.Body.String())
	}
}

// TestListExperimentalFlags_RequiresAuth ensures the handler refuses
// unauthenticated callers. The middleware normally injects X-User-ID; we
// strip that header here to simulate a request that bypassed auth.
func TestListExperimentalFlags_RequiresAuth(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/experimental-flags", nil)
	// Intentionally do not set X-User-ID — requireUserID must reject.
	testHandler.ListExperimentalFlags(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when X-User-ID missing, got %d (body=%s)", w.Code, w.Body.String())
	}
}