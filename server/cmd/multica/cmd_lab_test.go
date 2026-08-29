package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
)

// TestResolveLabFlagKey pins the <lab> → flag_key normalization: a bare slug
// gets the "user_" prefix, an already-prefixed key passes through, a
// built-in catalog key (e.g. "semantica") passes through unchanged so
// `multica lab delegate semantica "..."` lands on lab_source="semantica"
// (not "user_semantica"), and input whitespace is trimmed before prefixing.
func TestResolveLabFlagKey(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"bare slug gets user_ prefix", "my-lab", "user_my-lab"},
		{"already-prefixed passes through", "user_my-lab", "user_my-lab"},
		{"whitespace trimmed", "  my-lab  ", "user_my-lab"},
		{"built-in catalog key passes through", "semantica", "semantica"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveLabFlagKey(c.in); got != c.want {
				t.Fatalf("resolveLabFlagKey(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestDeriveDelegateTitle covers the title fallback, first-line extraction, and
// the rune-safe 80-rune truncation with ellipsis.
func TestDeriveDelegateTitle(t *testing.T) {
	// First line wins; the rest is dropped.
	if got := deriveDelegateTitle("Run a war-game\nwith details"); got != "Run a war-game" {
		t.Fatalf("first-line extraction = %q", got)
	}
	// Whitespace-only input falls back to the default title.
	if got := deriveDelegateTitle("   "); got != "Delegated lab run" {
		t.Fatalf("empty fallback = %q", got)
	}
	// Long CJK input truncates at 80 runes (not bytes) + a single ellipsis.
	long := strings.Repeat("汉", 100)
	got := deriveDelegateTitle(long)
	if n := len([]rune(got)); n != 81 {
		t.Fatalf("truncated title has %d runes, want 81 (got %q)", n, got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated title should end with ellipsis: %q", got)
	}
}

// TestLatestTask verifies the poll helper picks the most recent created_at,
// falling back to the last element when timestamps are equal or missing.
func TestLatestTask(t *testing.T) {
	tasks := []map[string]any{
		{"id": "old", "created_at": "2026-08-13T00:00:00Z"},
		{"id": "new", "created_at": "2026-08-13T00:01:00Z"},
	}
	if got := latestTask(tasks)["id"]; got != "new" {
		t.Fatalf("latest = %v, want new", got)
	}
	if got := latestTask([]map[string]any{{"id": "a"}, {"id": "b"}})["id"]; got != "b" {
		t.Fatalf("equal/missing ts fallback = %v, want b", got)
	}
}

// TestExtractTaskOutput covers the result.output envelope read and its
// graceful degradation on missing / non-map result payloads.
func TestExtractTaskOutput(t *testing.T) {
	if got := extractTaskOutput(map[string]any{"result": map[string]any{"output": "the deliverable"}}); got != "the deliverable" {
		t.Fatalf("output = %q", got)
	}
	if got := extractTaskOutput(map[string]any{}); got != "" {
		t.Fatalf("missing result = %q, want empty", got)
	}
	if got := extractTaskOutput(map[string]any{"result": "not a map"}); got != "" {
		t.Fatalf("non-map result = %q, want empty", got)
	}
}

// TestLabDelegateValidateIssueStatus pins the status validation the delegate
// flow depends on (a non-backlog status so the run dispatches). The core
// validator itself is shared with the issue commands and covered there too.
func TestLabDelegateValidateIssueStatus(t *testing.T) {
	// MUL-6243: validateIssueStatus is now format-only. The 7 built-ins all
	// pass (they're well-formed keys), and any custom well-formed key
	// ("ready_to_merge", "in_qa") passes locally too — the server's Resolve()
	// is the source of truth for "is this a known status in this workspace".
	for _, ok := range []string{"backlog", "todo", "in_progress", "in_review", "done", "blocked", "cancelled", "ready_to_merge", "in_qa"} {
		if err := validateIssueStatus(ok); err != nil {
			t.Errorf("validateIssueStatus(%q) = %v, want nil", ok, err)
		}
	}
	// A malformed key (contains a space) is the kind of input the local guard
	// still rejects — the server never sees it.
	if err := validateIssueStatus("not a status"); err == nil {
		t.Error("validateIssueStatus(\"not a status\") = nil, want error")
	}
}

// newDelegateTestClient builds a real cli.APIClient pointed at the given
// httptest server — the concrete client struct has no interface seam, but its
// BaseURL + HTTPClient fields are injectable, which is enough to drive
// waitForDelegatedResult against a fake task-runs endpoint.
func newDelegateTestClient(srv *httptest.Server) *cli.APIClient {
	return &cli.APIClient{BaseURL: srv.URL, HTTPClient: srv.Client()}
}

// TestWaitForDelegatedResult covers the four poll outcomes: completed, failed,
// timeout, and the fast-fail when no run is dispatched within the grace window
// (the 30s default is shortened via the package-level var).
func TestWaitForDelegatedResult(t *testing.T) {
	t.Run("completed returns the agent output", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": "task-1", "status": "completed", "created_at": "2026-08-13T00:00:00Z",
					"result": map[string]any{"output": "the deliverable"}},
			})
		}))
		defer srv.Close()
		res, err := waitForDelegatedResult(newDelegateTestClient(srv), "issue-1", "user_my-lab", 5*time.Second, 10*time.Millisecond)
		if err != nil {
			t.Fatalf("completed: %v", err)
		}
		if res.Status != "completed" || res.Output != "the deliverable" || res.TaskID != "task-1" {
			t.Fatalf("res = %+v", res)
		}
	})

	t.Run("failed surfaces the error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": "task-1", "status": "failed", "created_at": "2026-08-13T00:00:00Z", "error": "boom"},
			})
		}))
		defer srv.Close()
		_, err := waitForDelegatedResult(newDelegateTestClient(srv), "issue-1", "user_my-lab", 5*time.Second, 10*time.Millisecond)
		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("failed err = %v, want boom", err)
		}
	})

	t.Run("running forever times out", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": "task-1", "status": "running", "created_at": "2026-08-13T00:00:00Z"},
			})
		}))
		defer srv.Close()
		_, err := waitForDelegatedResult(newDelegateTestClient(srv), "issue-1", "user_my-lab", 150*time.Millisecond, 15*time.Millisecond)
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("timeout err = %v, want timed out", err)
		}
	})

	t.Run("no dispatch within grace fails fast", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		}))
		defer srv.Close()
		oldGrace := labDelegateNoTaskGrace
		labDelegateNoTaskGrace = 100 * time.Millisecond
		t.Cleanup(func() { labDelegateNoTaskGrace = oldGrace })
		_, err := waitForDelegatedResult(newDelegateTestClient(srv), "issue-1", "user_my-lab", 5*time.Second, 10*time.Millisecond)
		if err == nil || !strings.Contains(err.Error(), "no run was dispatched") {
			t.Fatalf("grace err = %v, want no run was dispatched", err)
		}
	})
}

// TestTruncateDelegateOutput pins the parent-comment output cap (0.5.88
// delegation loop). Short output passes through verbatim; long output is
// rune-capped at delegateOutputMaxChars + the "…(truncated)" suffix —
// rune-based so CJK results never split mid-character.
func TestTruncateDelegateOutput(t *testing.T) {
	short := "forecast: 42% ±3"
	if got := truncateDelegateOutput(short); got != short {
		t.Fatalf("short output must pass through verbatim, got %q", got)
	}

	long := strings.Repeat("a", delegateOutputMaxChars+500)
	got := truncateDelegateOutput(long)
	if !strings.HasSuffix(got, "…(truncated)") {
		t.Fatalf("long output must end with the truncation suffix, got suffix %q", got[len(got)-20:])
	}
	if n := len([]rune(strings.TrimSuffix(got, "…(truncated)"))); n != delegateOutputMaxChars {
		t.Fatalf("capped output carries %d runes, want %d", n, delegateOutputMaxChars)
	}

	// Exactly at the cap: no suffix.
	exact := strings.Repeat("b", delegateOutputMaxChars)
	if got := truncateDelegateOutput(exact); got != exact {
		t.Fatalf("output at the cap must be unchanged")
	}

	// CJK: 2500 Han runes cap to 2000 runes + suffix, never invalid UTF-8.
	cjk := strings.Repeat("汉", 2500)
	got = truncateDelegateOutput(cjk)
	if n := len([]rune(strings.TrimSuffix(got, "…(truncated)"))); n != delegateOutputMaxChars {
		t.Fatalf("capped CJK output carries %d runes, want %d", n, delegateOutputMaxChars)
	}
}
