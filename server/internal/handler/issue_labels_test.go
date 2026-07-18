package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// TestCreateIssueResponseOmitsLabels pins the LOCAL contract that the create
// response OMITS the `labels` field entirely (nil pointer → omitted via
// `omitempty`). This is the deliberate local design: create handlers
// intentionally skip the labelsByIssue bulk-load to save a round-trip on the
// hot path. The client merge then preserves whatever labels are already in
// cache (server/internal/handler/issue.go:70-76).
//
// Adapted from upstream TestCreateIssueAttachesLabelsAtomically: the local
// CreateIssueRequest has no `label_ids` body field, and the local create
// path does not pre-attach labels. Labels are attached via a follow-up
// UpdateIssue call (covered in TestUpdateIssueAttachesLabelsOmitsLabels
// below).
func TestCreateIssueResponseOmitsLabels(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	w := httptest.NewRecorder()
	testHandler.CreateIssue(w, newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":    "0.3.44 labels-on-create " + uuid.NewString()[:8],
		"status":   "todo",
		"priority": "low",
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var issue IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&issue); err != nil {
		t.Fatalf("decode: %v", err)
	}
	t.Cleanup(func() { deleteTestIssue(t, issue.ID) })

	// Per the local contract (server/internal/handler/issue.go:70-76), the
	// create response must NOT include a `labels` field at all — the pointer
	// is nil and the JSON tag is `omitempty`. The client merge preserves
	// whatever labels are already in cache.
	if issue.Labels != nil {
		t.Fatalf("create response must OMIT labels field (pointer nil + omitempty); got %v", *issue.Labels)
	}
}

// TestIssueLabelAttachDetach_FullFlow pins the LOCAL flow:
//   1. Create an issue (no labels, response omits labels field).
//   2. Create two labels.
//   3. POST /api/issues/{id}/labels twice (AttachLabel endpoint) attaches both.
//   4. GetIssue surfaces both labels in the response.
//
// This is the local equivalent of upstream's TestCreateIssueAttachesLabelsAtomically,
// adapted to the LOCAL routing: there is no `label_ids` body field on
// CreateIssue / UpdateIssue — label attachment goes through the dedicated
// POST /api/issues/{id}/labels + DELETE /api/issues/{id}/labels/{labelId}
// routes, NOT inline on the issue write paths.
func TestIssueLabelAttachDetach_FullFlow(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	// Step 1: create issue
	w := httptest.NewRecorder()
	testHandler.CreateIssue(w, newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":    "0.3.44 labels-attach " + uuid.NewString()[:8],
		"status":   "todo",
		"priority": "low",
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var issue IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&issue); err != nil {
		t.Fatalf("decode: %v", err)
	}
	t.Cleanup(func() { deleteTestIssue(t, issue.ID) })

	// Step 2: create two labels
	labelA := createTestLabel(t, "la-"+uuid.NewString()[:8])
	labelB := createTestLabel(t, "lb-"+uuid.NewString()[:8])

	// Step 3: attach via dedicated endpoint
	for _, labelID := range []string{labelA, labelB} {
		w = httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/"+issue.ID+"/labels", map[string]any{
			"label_id": labelID,
		})
		req = withURLParam(req, "id", issue.ID)
		testHandler.AttachLabel(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("AttachLabel %s: expected 200, got %d: %s", labelID, w.Code, w.Body.String())
		}
	}

	// Confirm DB rows
	if count := countIssueLabelsForIssue(t, issue.ID); count != 2 {
		t.Fatalf("issue_to_label rows after attach = %d, want 2", count)
	}

	// Step 4: GetIssue surfaces both labels
	w = httptest.NewRecorder()
	getReq := withURLParam(newRequest("GET", "/api/issues/"+issue.ID, nil), "id", issue.ID)
	testHandler.GetIssue(w, getReq)
	if w.Code != http.StatusOK {
		t.Fatalf("GetIssue: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var fetched IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&fetched); err != nil {
		t.Fatalf("decode fetched: %v", err)
	}
	if fetched.Labels == nil {
		t.Fatal("GetIssue must include labels (list/detail paths load them)")
	}
	got := map[string]bool{}
	for _, l := range *fetched.Labels {
		got[l.ID] = true
	}
	if !got[labelA] || !got[labelB] {
		t.Fatalf("GetIssue labels: expected %s + %s, got %v", labelA, labelB, got)
	}

	// Step 5: detach one via DELETE
	w = httptest.NewRecorder()
	detReq := newRequest("DELETE", "/api/issues/"+issue.ID+"/labels/"+labelA, nil)
	detReq = withURLParams(detReq, "id", issue.ID, "labelId", labelA)
	testHandler.DetachLabel(w, detReq)
	if w.Code != http.StatusOK {
		t.Fatalf("DetachLabel: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if count := countIssueLabelsForIssue(t, issue.ID); count != 1 {
		t.Fatalf("issue_to_label rows after detach = %d, want 1", count)
	}
}

// createTestLabel seeds an issue-scoped label and registers cleanup.
func createTestLabel(t *testing.T, name string) string {
	t.Helper()
	w := httptest.NewRecorder()
	testHandler.CreateLabel(w, newRequest("POST", "/api/labels", map[string]any{
		"name":  name,
		"color": "#ef4444",
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateLabel %q: expected 201, got %d: %s", name, w.Code, w.Body.String())
	}
	var created LabelResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode label: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue_label WHERE id = $1`, created.ID)
	})
	return created.ID
}

// countIssueLabelsForIssue counts issue_to_label rows for the given issue.
func countIssueLabelsForIssue(t *testing.T, issueID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM issue_to_label WHERE issue_id = $1`, issueID,
	).Scan(&n); err != nil {
		t.Fatalf("count issue_to_label: %v", err)
	}
	return n
}

// deleteTestIssue is defined in issue_batch_test.go (shared helper).