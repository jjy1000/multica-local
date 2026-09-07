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

// Regression for the 0.5.102 board finding: `fetchFirstPages` fans out one
// listIssues request per status category and renders the returned `total`
// as the column badge. ListIssues only honoured the singular `status`
// param, so every bucket carried the workspace-wide total and the same
// global first page — the board showed a "92" badge over an empty column
// body. These tests pin the MUL-6409 filter set (status_category /
// statuses / assignee_filters / creator_filters) on the plain list
// endpoint, mirroring ListGroupedIssues.
//
// Totals are asserted through actors (member/creator) that are unique to
// this test, so concurrent rows from other tests in the shared workspace
// cannot leak into the count.
func TestListIssuesStatusCategoryAndActorFilters(t *testing.T) {
	ctx := context.Background()

	suffix := time.Now().UnixNano()
	createMember := func(name string) string {
		t.Helper()
		var userID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO "user" (name, email)
			VALUES ($1, $2)
			RETURNING id
		`, name, fmt.Sprintf("list-filters-%d-%s@multica.ai", suffix, name)).Scan(&userID); err != nil {
			t.Fatalf("create user %s: %v", name, err)
		}
		t.Cleanup(func() {
			_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
		})
		if _, err := testPool.Exec(ctx, `
			INSERT INTO member (workspace_id, user_id, role)
			VALUES ($1, $2, 'member')
		`, testWorkspaceID, userID); err != nil {
			t.Fatalf("create member %s: %v", name, err)
		}
		return userID
	}

	memberA := createMember("List Filters A")
	memberB := createMember("List Filters B")

	createIssue := func(title, status string, assigneeType *string, assigneeID *string, creatorID string) string {
		t.Helper()
		var number int32
		if err := testPool.QueryRow(ctx, `
			UPDATE workspace
			SET issue_counter = GREATEST(
				issue_counter,
				(SELECT COALESCE(MAX(number), 0) FROM issue WHERE workspace_id = $1)
			) + 1
			WHERE id = $1
			RETURNING issue_counter
		`, testWorkspaceID).Scan(&number); err != nil {
			t.Fatalf("next issue number: %v", err)
		}

		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO issue (
				workspace_id, title, description, status, priority,
				assignee_type, assignee_id, creator_type, creator_id,
				position, number
			)
			VALUES ($1, $2, NULL, $3, 'none', $4, $5, 'member', $6, $7, $8)
			RETURNING id
		`, testWorkspaceID, title, status, assigneeType, assigneeID, creatorID, float64(number), number).Scan(&id); err != nil {
			t.Fatalf("create issue %q: %v", title, err)
		}
		t.Cleanup(func() {
			_, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, id)
		})
		return id
	}

	strPtr := func(s string) *string { return &s }

	// Two todo issues for A (one per category probe), one review issue for
	// A, one todo issue for B created by B.
	createIssue("List filters A todo one", "todo", strPtr("member"), &memberA, memberA)
	createIssue("List filters A todo two", "todo", strPtr("member"), &memberA, memberA)
	createIssue("List filters A review", "in_review", strPtr("member"), &memberA, memberA)
	createIssue("List filters B todo", "todo", strPtr("member"), &memberB, memberB)

	fetch := func(t *testing.T, query string) (int, []IssueResponse) {
		t.Helper()
		path := fmt.Sprintf("/api/issues?workspace_id=%s&%s", testWorkspaceID, query)
		w := httptest.NewRecorder()
		testHandler.ListIssues(w, newRequest("GET", path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("ListIssues(%s): expected 200, got %d: %s", query, w.Code, w.Body.String())
		}
		var resp struct {
			Issues []IssueResponse `json:"issues"`
			Total  int             `json:"total"`
		}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode list response: %v", err)
		}
		return resp.Total, resp.Issues
	}

	// status_category narrows the bucket and `total` reflects it — this is
	// the exact request shape the board fan-out sends.
	total, issues := fetch(t, fmt.Sprintf("status_category=todo&assignee_filters=member:%s", memberA))
	if total != 2 || len(issues) != 2 {
		t.Fatalf("status_category=todo member A: expected total=2 len=2, got total=%d len=%d", total, len(issues))
	}

	total, _ = fetch(t, fmt.Sprintf("status_category=in_review&assignee_filters=member:%s", memberA))
	if total != 1 {
		t.Fatalf("status_category=in_review member A: expected total=1, got %d", total)
	}

	// statuses (raw keys, comma-separated) narrows the same way.
	total, _ = fetch(t, fmt.Sprintf("statuses=todo,in_review&assignee_filters=member:%s&limit=1", memberA))
	if total != 3 {
		t.Fatalf("statuses=todo,in_review member A: expected total=3, got %d", total)
	}

	// `total` is the filtered count, not the page size — with limit=1 the
	// body holds 1 row while total stays 3 (pagination awareness).
	total, issues = fetch(t, fmt.Sprintf("statuses=todo,in_review&assignee_filters=member:%s&limit=1", memberA))
	if total != 3 || len(issues) != 1 {
		t.Fatalf("paged fetch: expected total=3 len=1, got total=%d len=%d", total, len(issues))
	}

	// creator_filters narrows on the creator relation.
	total, _ = fetch(t, fmt.Sprintf("status_category=todo&creator_filters=member:%s", memberB))
	if total != 1 {
		t.Fatalf("status_category=todo creator B: expected total=1, got %d", total)
	}

	// assignee_filters accepts the multi-actor OR form.
	total, _ = fetch(t, fmt.Sprintf(
		"status_category=todo&assignee_filters=member:%s,member:%s",
		memberA, memberB,
	))
	if total != 3 {
		t.Fatalf("status_category=todo multi-assignee: expected total=3, got %d", total)
	}
}
