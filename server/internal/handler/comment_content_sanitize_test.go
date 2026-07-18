package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCreateComment_StripsNullBytesInsteadOf500 pins the fix for GH #5388.
//
// A comment whose content carries a byte PostgreSQL's TEXT type cannot store —
// most commonly an embedded NUL (SQLSTATE 22021) that survives a JSON round
// trip from `--content-file` — must post successfully with the offending byte
// stripped, not fail the INSERT with an opaque 500 the CLI renders as a
// generic "server unavailable" (and then retries forever).
//
// Mirrors upstream comment_content_sanitize_test.go verbatim.
func TestCreateComment_StripsNullBytesInsteadOf500(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	issueID := createTestIssue(t, "null-byte comment fixture (GH #5388)", "todo", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })

	w := httptest.NewRecorder()
	r := newRequest("POST", "/api/issues/"+issueID+"/comments", map[string]any{
		"content": "diagnosis body\x00 with a stray NUL byte",
	})
	r = withURLParam(r, "id", issueID)

	testHandler.CreateComment(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateComment with NUL byte: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	got, _ := body["content"].(string)
	if strings.ContainsRune(got, '\x00') {
		t.Fatalf("stored content still contains a NUL byte: %q", got)
	}
	if want := "diagnosis body with a stray NUL byte"; got != want {
		t.Fatalf("stored content: expected %q (NUL stripped), got %q", want, got)
	}
}

// TestCreateComment_AllNulContentRejected verifies the strip-then-recheck
// branch: a comment whose content is ENTIRELY NUL bytes must surface as
// 400 "content is required", not 201 with an empty string silently written.
// This guards against an off-by-one in the sanitize step.
func TestCreateComment_AllNulContentRejected(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	issueID := createTestIssue(t, "all-NUL comment fixture", "todo", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })

	w := httptest.NewRecorder()
	r := newRequest("POST", "/api/issues/"+issueID+"/comments", map[string]any{
		"content": "\x00\x00\x00",
	})
	r = withURLParam(r, "id", issueID)

	testHandler.CreateComment(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("all-NUL content: expected 400 'content is required', got %d: %s", w.Code, w.Body.String())
	}
}

// TestUpdateComment_StripsNullBytesInsteadOf500 mirrors the create-path test
// on the edit path. The fix touches both — preview parity requires it.
func TestUpdateComment_StripsNullBytesInsteadOf500(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	issueID := createTestIssue(t, "null-byte comment edit fixture", "todo", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })

	// Create a normal comment first.
	w := httptest.NewRecorder()
	r := newRequest("POST", "/api/issues/"+issueID+"/comments", map[string]any{
		"content": "original",
	})
	r = withURLParam(r, "id", issueID)
	testHandler.CreateComment(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("seed comment: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var seed map[string]any
	if err := json.NewDecoder(w.Body).Decode(&seed); err != nil {
		t.Fatalf("decode seed: %v", err)
	}
	commentID, _ := seed["id"].(string)

	// Now update with a NUL byte in the body.
	w = httptest.NewRecorder()
	updReq := newRequest("PUT", "/api/comments/"+commentID, map[string]any{
		"content": "edited body\x00 with stray NUL",
	})
	updReq = withURLParam(updReq, "commentId", commentID)
	testHandler.UpdateComment(w, updReq)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateComment with NUL byte: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got, _ := body["content"].(string)
	if strings.ContainsRune(got, '\x00') {
		t.Fatalf("updated content still contains a NUL byte: %q", got)
	}
	if want := "edited body with stray NUL"; got != want {
		t.Fatalf("updated content: expected %q, got %q", want, got)
	}
}