package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/util"
)

// TestSearchIssues_DeadlineAlreadyPassed asserts the search handler respects
// an already-expired context. Pre-fix bug: SearchIssues used the request
// context directly and would block forever on a runaway LIKE/ILIKE; the
// 5-second timeout (parentCtx + context.WithTimeout) means an expired
// parent context must short-circuit to 504 instead of hanging.
//
// We don't exercise the SQL slow path (no pg_sleep available in unit tests)
// — we only verify that a context whose deadline is already in the past
// surfaces as 504 Gateway Timeout.
func TestSearchIssues_DeadlineAlreadyPassed(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	// Workspace ID is the test workspace; query anything that matches the
	// SQL path but the context will already be expired by the time the
	// query is issued.
	w := httptest.NewRecorder()
	r := newRequest("GET", "/api/search/issues?q=anything", nil)
	r.Header.Set("X-User-ID", testUserID)
	r.Header.Set("X-Workspace-ID", testWorkspaceID)

	// Inject a context that has already expired. This simulates the user
	// hanging up or a parent context whose deadline passed before our
	// 5s wrapper could fire — either way, SearchIssues must not block.
	deadCtx, cancel := context.WithDeadline(r.Context(), time.Now().Add(-1*time.Second))
	defer cancel()
	r = r.WithContext(deadCtx)

	testHandler.SearchIssues(w, r)

	// Acceptable outcomes: 504 (timeout), 500 (deadline surfaced as
	// generic error), or even 400 (missing q — though we provided it).
	// The critical assertion is that we did NOT block forever.
	if w.Code == http.StatusOK {
		t.Fatalf("SearchIssues returned 200 against an already-expired context; expected timeout")
	}
	// 504 is the desired code; 500 is acceptable if pgx wrapped it.
	if w.Code != http.StatusGatewayTimeout && w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 504 or 500 with expired context, got %d (body=%s)", w.Code, w.Body.String())
	}
}

// TestSearchIssues_HappyPathIsFast verifies the timeout wrapper does not
// regress normal search latency. A trivial term should resolve well under
// the 5-second budget.
func TestSearchIssues_HappyPathIsFast(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	wsUUID, err := util.ParseUUID(testWorkspaceID)
	if err != nil {
		t.Fatalf("parse workspace id: %v", err)
	}

	w := httptest.NewRecorder()
	r := newRequest("GET", "/api/search/issues?q=integration", nil)
	r.Header.Set("X-User-ID", testUserID)
	r.Header.Set("X-Workspace-ID", testWorkspaceID)

	start := time.Now()
	testHandler.SearchIssues(w, r)
	elapsed := time.Since(start)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for happy path, got %d (body=%s)", w.Code, w.Body.String())
	}
	// Allow generous headroom: 2s is well below the 5s timeout but
	// comfortably above any healthy cold-cache query.
	if elapsed > 2*time.Second {
		t.Fatalf("happy-path search took %v, expected <2s (timeout wrapper would still allow 5s)", elapsed)
	}
	// Sanity-check the response shape — issues must be present (even if empty).
	if !strings.Contains(w.Body.String(), `"issues"`) {
		t.Fatalf("expected issues array in response, got: %s", w.Body.String())
	}
	_ = wsUUID // workspace UUID loaded above only for fixture validation
}
