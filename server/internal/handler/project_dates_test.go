package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// decodeProject pulls a ProjectResponse out of a recorder, failing the test
// on a non-expected status or a decode error.
func decodeProject(t *testing.T, w *httptest.ResponseRecorder, wantStatus int) ProjectResponse {
	t.Helper()
	if w.Code != wantStatus {
		t.Fatalf("expected status %d, got %d: %s", wantStatus, w.Code, w.Body.String())
	}
	var p ProjectResponse
	if err := json.NewDecoder(w.Body).Decode(&p); err != nil {
		t.Fatalf("decode ProjectResponse: %v", err)
	}
	return p
}

// Project start_date / due_date follow the same calendar-day contract as the
// issue dates: create echoes them, GET persists them, an update with the key
// present but empty clears the date while an absent key leaves it untouched.
//
// Mirrors upstream project_dates_test.go (MUL-4513). Local-only adaptions:
//   - Uses the pgtype.Date ↔ *string round-trip via util.ParseCalendarDate /
//     dateToPtr (same machinery as issue.start_date).
//   - Cleans up the row via t.Cleanup so the test does not require a global
//     delete at the end of the run.
func TestProjectStartDueDateLifecycle(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	// Create with both dates set.
	w := httptest.NewRecorder()
	testHandler.CreateProject(w, newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title":      "0.3.44 project dates",
		"start_date": "2026-03-01",
		"due_date":   "2026-03-31",
	}))
	created := decodeProject(t, w, http.StatusCreated)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, created.ID)
	})
	if created.StartDate == nil || *created.StartDate != "2026-03-01" {
		t.Fatalf("create start_date = %v, want 2026-03-01", created.StartDate)
	}
	if created.DueDate == nil || *created.DueDate != "2026-03-31" {
		t.Fatalf("create due_date = %v, want 2026-03-31", created.DueDate)
	}

	// GET persists both dates.
	w = httptest.NewRecorder()
	getReq := withURLParam(newRequest("GET", "/api/projects/"+created.ID, nil), "id", created.ID)
	testHandler.GetProject(w, getReq)
	got := decodeProject(t, w, http.StatusOK)
	if got.StartDate == nil || *got.StartDate != "2026-03-01" {
		t.Fatalf("GET start_date = %v, want 2026-03-01", got.StartDate)
	}
	if got.DueDate == nil || *got.DueDate != "2026-03-31" {
		t.Fatalf("GET due_date = %v, want 2026-03-31", got.DueDate)
	}

	// Update with the key present but the value as "" clears the date.
	w = httptest.NewRecorder()
	updReq := withURLParam(newRequest("PUT", "/api/projects/"+created.ID, map[string]any{
		"start_date": "",
		"due_date":   "",
	}), "id", created.ID)
	testHandler.UpdateProject(w, updReq)
	cleared := decodeProject(t, w, http.StatusOK)
	if cleared.StartDate != nil {
		t.Fatalf("update clear: start_date = %v, want nil", *cleared.StartDate)
	}
	if cleared.DueDate != nil {
		t.Fatalf("update clear: due_date = %v, want nil", *cleared.DueDate)
	}

	// Update with an absent key leaves the column untouched (set it again
	// first so we can prove the untouched branch, not just the cleared state).
	w = httptest.NewRecorder()
	updReq = withURLParam(newRequest("PUT", "/api/projects/"+created.ID, map[string]any{
		"start_date": "2026-04-01",
		"due_date":   "2026-04-30",
	}), "id", created.ID)
	testHandler.UpdateProject(w, updReq)
	if w.Code != http.StatusOK {
		t.Fatalf("update set both: status = %d, body = %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	updReq = withURLParam(newRequest("PUT", "/api/projects/"+created.ID, map[string]any{
		"title": "0.3.44 project dates (renamed)",
	}), "id", created.ID)
	testHandler.UpdateProject(w, updReq)
	untouched := decodeProject(t, w, http.StatusOK)
	if untouched.StartDate == nil || *untouched.StartDate != "2026-04-01" {
		t.Fatalf("untouched start_date = %v, want 2026-04-01", untouched.StartDate)
	}
	if untouched.DueDate == nil || *untouched.DueDate != "2026-04-30" {
		t.Fatalf("untouched due_date = %v, want 2026-04-30", untouched.DueDate)
	}
}

// TestProjectStartDueDateInvalidFormat verifies the handler returns a clean
// 400 with a parseable message instead of crashing the request. The DB
// already accepted YYYY-MM-DD, so this guards against accidental relaxation
// of the input format.
func TestProjectStartDueDateInvalidFormat(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	w := httptest.NewRecorder()
	testHandler.CreateProject(w, newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title":      "bad-date",
		"start_date": "03/01/2026",
	}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed start_date, got %d: %s", w.Code, w.Body.String())
	}
}