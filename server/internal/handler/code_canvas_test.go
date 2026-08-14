// Package handler — code_canvas_test.go (0.5.18 M4)
//
// DB-free tests for the code_canvas issue-bound artifact surface. The
// render + loopback + validation seams are extracted as pure functions
// so the wire outcomes (400 / 502 / 503) can be pinned without a running
// PostgreSQL. The DB-backed happy path (create persists a row, list
// returns it newest-first) needs a live server; see the note in
// handler_test.go / run manually with a PG container.

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestCodeCanvasValidateInput(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		code     string
		language string
		wantCode string
		wantLang string
		wantErr  bool
	}{
		{"empty code is rejected", "", "python", "", "", true},
		{"whitespace-only code is rejected", "   \n\t ", "python", "", "", true},
		{"oversize code is rejected", strings.Repeat("a", codeCanvasMaxCodeBytes+1), "python", "", "", true},
		{"max code is accepted", strings.Repeat("a", codeCanvasMaxCodeBytes), "python", strings.Repeat("a", codeCanvasMaxCodeBytes), "python", false},
		{"empty language defaults to text", "print(1)", "", "print(1)", codeCanvasDefaultLanguage, false},
		{"whitespace language defaults to text", "print(1)", "  ", "print(1)", codeCanvasDefaultLanguage, false},
		{"language is preserved", "print(1)", "python", "print(1)", "python", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, lang, err := validateCodeCanvasInput(tc.code, tc.language)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if code != tc.wantCode || lang != tc.wantLang {
				t.Errorf("validateCodeCanvasInput = (%q, %q), want (%q, %q)", code, lang, tc.wantCode, tc.wantLang)
			}
		})
	}
}

func TestCodeCanvasRenderSuccess(t *testing.T) {
	t.Parallel()
	const canvas = "<html><body>canvas</body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/render" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(canvas))
	}))
	defer srv.Close()

	got, err := renderCodeCanvas(context.Background(), srv.URL, "print(1)", "python")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != canvas {
		t.Errorf("renderCodeCanvas = %q, want %q", got, canvas)
	}
}

func TestCodeCanvasRenderNon2xx(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := renderCodeCanvas(context.Background(), srv.URL, "print(1)", "python")
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
	// The handler maps this error to 502 (writeError, StatusBadGateway).
	if !strings.Contains(err.Error(), "http 500") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "http 500")
	}
}

func TestCodeCanvasLoopbackURLUnregistered(t *testing.T) {
	t.Parallel()
	experimentalLoopback.Lock()
	experimentalLoopback.registry = nil
	experimentalLoopback.Unlock()

	// An unregistered URL is the handler's 503 condition.
	if got := codeCanvasLoopbackURL(); got != "" {
		t.Errorf("codeCanvasLoopbackURL = %q, want empty (service not ready)", got)
	}
}

// TestCodeCanvasArtifactRoundTrip is a DB-backed happy path: create two
// artifacts for a fixture issue and read them back newest-first. Requires
// migration 239 (code_canvas_artifact) to be applied to the test DB.
func TestCodeCanvasArtifactRoundTrip(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, creator_type, creator_id, title)
		VALUES ($1, 'member', $2, $3)
		RETURNING id
	`, testWorkspaceID, testUserID, "code canvas fixture").Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID)
	})

	create := func(code, lang, html string) string {
		t.Helper()
		row, err := testHandler.Queries.CreateCodeCanvasArtifact(ctx, db.CreateCodeCanvasArtifactParams{
			WorkspaceID: parseUUID(testWorkspaceID),
			IssueID:     parseUUID(issueID),
			Code:        code,
			Language:    lang,
			Html:        html,
		})
		if err != nil {
			t.Fatalf("create artifact: %v", err)
		}
		return uuidToString(row.ID)
	}

	first := create("print(1)", "python", "<html>one</html>")
	// created_at is the transaction timestamp; a tiny gap makes the
	// newest-first assertion deterministic rather than tie-prone.
	time.Sleep(2 * time.Millisecond)
	second := create("print(2)", "python", "<html>two</html>")

	rows, err := testHandler.Queries.ListCodeCanvasArtifactsByIssue(ctx, db.ListCodeCanvasArtifactsByIssueParams{
		IssueID: parseUUID(issueID),
		Limit:   100,
	})
	if err != nil {
		t.Fatalf("list artifacts: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("list artifacts = %d rows, want 2", len(rows))
	}
	if got := uuidToString(rows[0].ID); got != second {
		t.Errorf("newest-first order broken: rows[0].ID = %s, want %s", got, second)
	}
	if got := uuidToString(rows[1].ID); got != first {
		t.Errorf("rows[1].ID = %s, want %s", got, first)
	}
	if rows[0].Code != "print(2)" || rows[0].Html != "<html>two</html>" {
		t.Errorf("rows[0] = code %q html %q, want print(2)/<html>two</html>", rows[0].Code, rows[0].Html)
	}
}

