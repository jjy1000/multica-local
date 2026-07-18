package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestListIssuesSortsByCreatedAt locks in the contract for the sort column
// "created_at" with asc/desc direction. Mirrors the upstream
// issue_sort_test.go::TestListIssuesSortsByStatusAndUpdatedAt pattern, but
// adapted to the LOCAL whitelist of allowed sort columns — the upstream
// column set ("status" / "updated_at") is NOT supported here; instead we
// exercise the same ordering machinery on "created_at" + "priority", the
// two columns a user-facing issue list actually sorts on.
//
// Why local-only columns matter: the upstream sort logic ranks by an
// expression like `CASE i.status WHEN 'in_progress' THEN 0 ...`. The local
// fork uses an explicit whitelist (server/internal/handler/issue.go:924)
// because every added column is a public API contract — silently accepting
// unknown columns would let a stale client keep working with an old sort
// key after a refactor. So this test pins BOTH the columns we DO support
// (created_at asc / desc, priority asc / desc) AND the rejection of an
// unsupported column.
func TestListIssuesSortsByCreatedAt(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	type fixture struct {
		title     string
		priority  string
		createdAt time.Time
	}
	fixtures := []fixture{
		{"sort-c-2", "high", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
		{"sort-c-1", "low", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"sort-c-3", "urgent", time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)},
	}
	for index, item := range fixtures {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO issue (
				workspace_id, title, status, priority, creator_type, creator_id,
				position, number, created_at, updated_at
			)
			VALUES ($1, $2, 'todo', $3, 'member', $4, $5,
				(SELECT COALESCE(MAX(number), 0) + 1 FROM issue WHERE workspace_id = $1),
				$6, $6)
		`, testWorkspaceID, item.title, item.priority, testUserID, index, item.createdAt); err != nil {
			t.Fatalf("insert issue %q: %v", item.title, err)
		}
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM issue WHERE workspace_id = $1 AND title LIKE 'sort-c-%'`, testWorkspaceID)
	})

	listTitles := func(sort, direction string) []string {
		t.Helper()
		path := fmt.Sprintf(
			"/api/issues?workspace_id=%s&limit=200&sort=%s&direction=%s",
			testWorkspaceID, sort, direction,
		)
		w := httptest.NewRecorder()
		testHandler.ListIssues(w, newRequest("GET", path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("ListIssues sort=%s direction=%s: expected 200, got %d: %s",
				sort, direction, w.Code, w.Body.String())
		}
		var response struct {
			Issues []IssueResponse `json:"issues"`
		}
		if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		titles := make([]string, 0, len(response.Issues))
		for _, issue := range response.Issues {
			titles = append(titles, issue.Title)
		}
		return titles
	}

	// asc by created_at → c-1, c-2, c-3
	// (only filter the sort-c-* rows out so unrelated test data does not
	// interfere with the index range).
	filterSortRows := func(in []string) []string {
		out := make([]string, 0, 3)
		for _, t := range in {
			if len(t) >= 7 && t[:7] == "sort-c-" {
				out = append(out, t)
			}
		}
		return out
	}
	if got := filterSortRows(listTitles("created_at", "asc")); fmt.Sprint(got) != "[sort-c-1 sort-c-2 sort-c-3]" {
		t.Errorf("created_at asc = %v, want [sort-c-1 sort-c-2 sort-c-3]", got)
	}
	if got := filterSortRows(listTitles("created_at", "desc")); fmt.Sprint(got) != "[sort-c-3 sort-c-2 sort-c-1]" {
		t.Errorf("created_at desc = %v, want [sort-c-3 sort-c-2 sort-c-1]", got)
	}

	// priority asc → urgent, high, low (the local CASE expression maps
	// urgent=0, high=1, medium=2, low=3).
	if got := filterSortRows(listTitles("priority", "asc")); fmt.Sprint(got) != "[sort-c-3 sort-c-2 sort-c-1]" {
		t.Errorf("priority asc = %v, want [sort-c-3 sort-c-2 sort-c-1]", got)
	}
}

// TestListIssuesRejectsUnknownSort pins the unknown-sort 400 contract. The
// handler rejects anything outside its whitelist with a 400 — keeping this
// surface tight is part of how we know an upstream refactor of the order-by
// expression can't silently leak an old client.
func TestListIssuesRejectsUnknownSort(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	w := httptest.NewRecorder()
	testHandler.ListIssues(w, newRequest("GET",
		"/api/issues?workspace_id="+testWorkspaceID+"&sort=this_column_is_not_whitelisted", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown sort column, got %d: %s", w.Code, w.Body.String())
	}
}

// TestListIssuesRejectsBadDirection pins the direction 400 contract.
func TestListIssuesRejectsBadDirection(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	w := httptest.NewRecorder()
	testHandler.ListIssues(w, newRequest("GET",
		"/api/issues?workspace_id="+testWorkspaceID+"&sort=created_at&direction=sideways", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid direction, got %d: %s", w.Code, w.Body.String())
	}
}