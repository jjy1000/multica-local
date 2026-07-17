package middleware

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/experimental"
)

// withBurstBlacklist sets a per-test blacklist path. Serial tests
// only — see withTempBlacklist in safety_test.go for the rationale.
func withBurstBlacklist(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "experimental-blacklist.json")
	experimental.SetBlacklistPath(path)
	t.Cleanup(func() { experimental.SetBlacklistPath(defaultBlacklistPath()) })
	return path
}

func defaultBlacklistPath() string {
	return experimental.ResolveBlacklistPath()
}

// TestBurstBreaksAfterThreshold verifies that 3 consecutive 5xx on a
// flag-attributed request marks the flag broken exactly once. The
// middleware only trusts X-Experimental-Flag on a real experimental
// path for a known catalog flag (0.3.25 forgery guard), so the test
// uses claude_science_lab on /api/experimental/... rather than a
// synthetic key on /anything.
func TestBurstBreaksAfterThreshold(t *testing.T) {
	withBurstBlacklist(t)

	cfg := BurstConfig{Threshold: 3, Window: time.Minute}
	handler := ExperimentalFlagBurst(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/api/experimental/claude-science/sessions", nil)
		req.Header.Set(ExperimentalFlagHeader, "claude_science_lab")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	entry, ok := experimental.IsBroken("claude_science_lab")
	if !ok {
		t.Fatal("flag should be broken after threshold 5xx")
	}
	if entry.Reason != experimental.Reason5xxBurst {
		t.Errorf("Reason = %q, want %q", entry.Reason, experimental.Reason5xxBurst)
	}
}

// TestBurstIgnoresForgedHeader verifies the 0.3.25 forgery guard: a
// client-set X-Experimental-Flag on a NON-experimental path (or with
// an unknown flag key) must NOT trip the breaker, even on repeated
// 5xx. Without the guard, any user could blacklist a flag by forging
// the header on an unrelated 500-prone endpoint.
func TestBurstIgnoresForgedHeader(t *testing.T) {
	withBurstBlacklist(t)

	cfg := BurstConfig{Threshold: 1, Window: time.Minute}
	handler := ExperimentalFlagBurst(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	// Real flag key, but a non-experimental path → ignored.
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/api/issues", nil)
		req.Header.Set(ExperimentalFlagHeader, "claude_science_lab")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
	if _, ok := experimental.IsBroken("claude_science_lab"); ok {
		t.Fatal("forged header on non-experimental path must not trip the breaker")
	}

	// Experimental path, but an unknown/garbage flag key → ignored.
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/api/experimental/claude-science/x", nil)
		req.Header.Set(ExperimentalFlagHeader, "not_a_real_flag")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
	if _, ok := experimental.IsBroken("not_a_real_flag"); ok {
		t.Fatal("unknown flag key must not trip the breaker")
	}
}

func TestBurstIgnores4xx(t *testing.T) {
	withBurstBlacklist(t)

	cfg := BurstConfig{Threshold: 1, Window: time.Minute}
	handler := ExperimentalFlagBurst(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadRequest)
	}))

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/api/experimental/claude-science/sessions", nil)
		req.Header.Set(ExperimentalFlagHeader, "claude_science_lab")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	if _, ok := experimental.IsBroken("claude_science_lab"); ok {
		t.Fatal("4xx responses must not trip the breaker")
	}
}

func TestBurstIgnoresUnflaggedRequests(t *testing.T) {
	withBurstBlacklist(t)

	cfg := BurstConfig{Threshold: 1, Window: time.Minute}
	handler := ExperimentalFlagBurst(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/anything", nil)
		// No header set.
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	bl, err := experimental.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(bl.Entries) != 0 {
		t.Errorf("expected no entries, got %+v", bl.Entries)
	}
}

func TestBurstOncePerWindow(t *testing.T) {
	withBurstBlacklist(t)

	cfg := BurstConfig{Threshold: 2, Window: time.Minute}
	calls := 0
	handler := ExperimentalFlagBurst(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))

	hit := func() {
		req := httptest.NewRequest("GET", "/api/experimental/claude-science/sessions", nil)
		req.Header.Set(ExperimentalFlagHeader, "claude_science_lab")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	hit()
	hit()
	hit()
	hit()
	if calls != 4 {
		t.Errorf("handler called %d times, want 4", calls)
	}

	bl, err := experimental.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	count := 0
	for _, e := range bl.Entries {
		if e.FlagKey == "claude_science_lab" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("entries for flag = %d, want exactly 1 (no spam writes)", count)
	}
}

func TestBurstContextField(t *testing.T) {
	withBurstBlacklist(t)

	cfg := BurstConfig{Threshold: 1, Window: time.Minute}
	handler := ExperimentalFlagBurst(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))

	req := httptest.NewRequest("GET", "/api/experimental/claude-science/sessions", nil)
	req.Header.Set(ExperimentalFlagHeader, "claude_science_lab")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	entry, ok := experimental.IsBroken("claude_science_lab")
	if !ok {
		t.Fatal("flag should be broken")
	}
	if !strings.Contains(entry.Context, "/api/experimental/claude-science/sessions") {
		t.Errorf("Context should record the request path, got %q", entry.Context)
	}
}

func TestBurstConcurrentRequestsCountCorrectly(t *testing.T) {
	withBurstBlacklist(t)

	cfg := BurstConfig{Threshold: 4, Window: time.Minute}
	handler := ExperimentalFlagBurst(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/api/experimental/claude-science/sessions", nil)
			req.Header.Set(ExperimentalFlagHeader, "claude_science_lab")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
		}()
	}
	wg.Wait()

	entry, ok := experimental.IsBroken("claude_science_lab")
	if !ok {
		t.Fatal("flag should be broken after concurrent burst")
	}
	if entry.Reason != experimental.Reason5xxBurst {
		t.Errorf("Reason = %q, want %q", entry.Reason, experimental.Reason5xxBurst)
	}
}
