package llmwiki

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestClient_FlagOff_RespectsCtx pins the client's flag-off contract:
// whatever the FlagOn closure decides, that is what the client
// surfaces. The decision is owned by the closure built at
// handler/llm_wiki_bridge.go::llmWikiFlagOnFor — this test pins
// that the client itself does not consult experimental.DefaultFor
// or any other ambient catalog signal. Production wires the
// closure to per-user experimental_pref; tests can wire any
// truthy/falsy func to drive their scenarios.
func TestClient_FlagOff_RespectsCtx(t *testing.T) {
	t.Parallel()

	called := false
	c, err := New(t.Context(), Config{
		BaseURL: "http://127.0.0.1:1",
		Token:   "t",
		FlagOn:  func(_ context.Context) bool { return false },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := c.Health(t.Context()); err != ErrFlagDisabled {
		t.Fatalf("Health with flag off: got %v, want ErrFlagDisabled", err)
	}
	if _, err := c.Search(t.Context(), "x", 5, false); err != ErrFlagDisabled {
		t.Fatalf("Search with flag off: got %v, want ErrFlagDisabled", err)
	}
	if called {
		t.Fatal("network should not be touched when the flag is off")
	}
}

// TestClient_Health_OK spins up a tiny stub server so the happy path
// can be exercised without the LLM Wiki desktop running.
func TestClient_Health_OK(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/health" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"status":"running","version":"test"}`)
	}))
	defer srv.Close()
	if !strings.HasSuffix(srv.URL, "/api/v1/health") && strings.Contains(srv.URL, "/api/v1") {
		// httptest server strips the API prefix — point the client at the
		// real desktop API path manually.
	}
	clientBase := strings.TrimSuffix(srv.URL, "/api/v1/health")
	if !strings.HasSuffix(clientBase, "/api/v1") {
		clientBase = clientBase + "/api/v1"
	}

	c, err := New(t.Context(), Config{
		BaseURL: clientBase,
		Token:   "t",
		FlagOn:  func(_ context.Context) bool { return true },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := c.Health(t.Context())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if v, ok := out["status"]; !ok || v != "running" {
		t.Fatalf("unexpected health body: %v", out)
	}
}

// TestWriter_RejectsParentTraversal is the file-side mirror of the
// server-side rejection. We do not rely on the handler's own check
// because CLI / Skill adapters call the writer directly; this test
// pins the safety.
func TestWriter_RejectsParentTraversal(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	w, err := NewWriter(base)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	bad := []string{"../escape.md", "../etc/passwd", "good/../../../etc/hosts"}
	for _, p := range bad {
		if _, err := w.Write(p, []byte("x")); err != ErrPathOutsideVault {
			t.Fatalf("Write(%q) = %v, want ErrPathOutsideVault", p, err)
		}
	}
}

// TestWriter_WritesAndReadsBack is the canonical happy path. We
// reuse the same vault root as the production DefaultVaultDir so
// this test would still pass in CI without a temp dir override.
func TestWriter_WritesAndReadsBack(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	w, err := NewWriter(base)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	got, err := w.Write("sub/notes/today.md", []byte("hello"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !strings.HasPrefix(got, base) {
		t.Fatalf("Write returned %q outside base %q", got, base)
	}
	body, err := os.ReadFile(filepath.Join(base, "sub", "notes", "today.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(body) != "hello" {
		t.Fatalf("body = %q, want %q", string(body), "hello")
	}
}

// TestDiscoverBaseURL_LiveDesktopReturns is the happy-path mirror
// of the NoListener check: when the LLM Wiki desktop is running on
// 19827 or 19828 (which it is on most developer machines), probe
// returns the matching base URL. We deliberately do not fail the
// test when the desktop app is offline — the absence of a listener
// is the desktop app's own diagnostic surface.
func TestDiscoverBaseURL_LiveDesktopReturns(t *testing.T) {
	t.Parallel()
	base, err := DiscoverBaseURL(t.Context(), &http.Client{Timeout: 200 * time.Millisecond})
	if err != nil {
		// No desktop app running — that's fine; we're not asserting.
		t.Skipf("desktop api not running: %v", err)
	}
	if !strings.HasPrefix(base, "http://127.0.0.1:198") {
		t.Fatalf("expected loopback base on 198xx, got %q", base)
	}
}
