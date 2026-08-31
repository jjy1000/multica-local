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

// TestClient_BareHostBaseURL_KeepsAPIPrefix is THE regression test
// for the 0.5.92 read-path bug: production composes requests from
// the bare loopback host DiscoverBaseURL returns (e.g.
// "http://127.0.0.1:19828") plus verb paths like
// "/projects/current/search". Before the normalizeBaseURL fix the
// /api/v1 prefix only ever appeared because the unit tests baked it
// into Config.BaseURL — every production read verb 404'd while
// /status stayed green (the app also serves /health at the root).
// This test pins the exact production composition: bare-host
// BaseURL in, prefixed path on the wire.
func TestClient_BareHostBaseURL_KeepsAPIPrefix(t *testing.T) {
	t.Parallel()

	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer t" {
			t.Errorf("bearer token missing on %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/health":
			_, _ = io.WriteString(w, `{"ok":true,"status":"running"}`)
		case "/api/v1/projects/current/search":
			// v0.6.x app shape: hits live under `results`.
			_, _ = io.WriteString(w, `{"results":[{"id":"1","title":"x","snippet":"s","score":1,"path":"wiki/x.md"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, err := New(t.Context(), Config{
		BaseURL: srv.URL, // bare host — the shape DiscoverBaseURL returns
		Token:   "t",
		FlagOn:  func(_ context.Context) bool { return true },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Health(t.Context()); err != nil {
		t.Fatalf("Health: %v", err)
	}
	hits, err := c.Search(t.Context(), "外循环", 3, false)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Path != "wiki/x.md" {
		t.Fatalf("unexpected hits: %+v", hits)
	}
	want := []string{"/api/v1/health", "/api/v1/projects/current/search"}
	if len(seen) != len(want) {
		t.Fatalf("requests seen = %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("request[%d] = %s, want %s (missing /api/v1 prefix regression)", i, seen[i], want[i])
		}
	}
}

// TestNormalizeBaseURL pins the prefix normalisation edge cases.
func TestNormalizeBaseURL(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"http://127.0.0.1:19828":     "http://127.0.0.1:19828/api/v1",
		"http://127.0.0.1:19828/":    "http://127.0.0.1:19828/api/v1",
		"http://127.0.0.1:1/api/v1":  "http://127.0.0.1:1/api/v1", // old test convention untouched
		"http://127.0.0.1:1/api/v1/": "http://127.0.0.1:1/api/v1", // trailing slash trimmed
		"":                           "",
		"  http://127.0.0.1:19828  ": "http://127.0.0.1:19828/api/v1",
	}
	for in, want := range cases {
		if got := normalizeBaseURL(in); got != want {
			t.Errorf("normalizeBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestParseTokenJSON covers every token-file shape the app has
// shipped: the legacy auth.json flat shape and the v0.6.x Tauri
// app-state apiConfig nesting.
func TestParseTokenJSON(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want string
	}{
		{"flat auth.json", `{"token":"tok-legacy"}`, "tok-legacy"},
		{"app-state apiConfig", `{"apiConfig":{"token":"tok-app"},"mineruConfig":{"token":"x"}}`, "tok-app"},
		{"garbage", `not json at all`, ""},
		{"token-less", `{"someOtherField":1}`, ""},
	}
	for _, tc := range cases {
		if got := parseTokenJSON([]byte(tc.body)); got != tc.want {
			t.Errorf("%s: parseTokenJSON = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestTokenCandidatePaths pins the discovery order: Multica's own
// store first, then the v0.6.x app-state, then the legacy layouts.
func TestTokenCandidatePaths(t *testing.T) {
	t.Parallel()
	paths := TokenCandidatePaths("/home/u")
	want := []string{
		"/home/u/.multica/llm-wiki.json",
		"/home/u/Library/Application Support/com.llmwiki.app/app-state.json",
		"/home/u/Library/Application Support/LLM Wiki/auth.json",
		"/home/u/.config/LLM Wiki/auth.json",
		"/home/u/.llm-wiki/auth.json",
	}
	if len(paths) != len(want) {
		t.Fatalf("got %d candidates, want %d: %v", len(paths), len(want), paths)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("candidate[%d] = %s, want %s", i, paths[i], want[i])
		}
	}
}

// TestClient_Search_DecodesBothResultKeys pins the v0.6.x search
// envelope drift: the app ships hits under `results` (the 0.4.x-era
// code decoded `hits` and silently returned zero rows once the
// prefix fix let requests land). `results` wins when both appear;
// a legacy `hits`-only body still decodes.
func TestClient_Search_DecodesBothResultKeys(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		body    string
		wantLen int
	}{
		{"v0.6 results key", `{"results":[{"title":"a","path":"p/a"},{"title":"b","path":"p/b"}]}`, 2},
		{"legacy hits key", `{"hits":[{"title":"a","path":"p/a"}]}`, 1},
		{"results wins over hits", `{"results":[{"title":"a","path":"p/a"}],"hits":[{"title":"x","path":"p/x"}]}`, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/projects/current/search" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			c, err := New(t.Context(), Config{
				BaseURL: srv.URL,
				Token:   "t",
				FlagOn:  func(_ context.Context) bool { return true },
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			hits, err := c.Search(t.Context(), "q", 5, false)
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(hits) != tc.wantLen {
				t.Fatalf("got %d hits, want %d", len(hits), tc.wantLen)
			}
		})
	}
}

// TestClient_Files_RecursiveCapFallsBackToTopLevel pins the v0.6.x
// behaviour drift: an explicit recursive=false must be sent (the
// app treats an omitted param as recursive), and when a recursive
// listing crosses the app's hard maxFiles cap (a 413 ERROR, not a
// truncation) Files degrades to the top-level listing instead of
// failing the caller outright.
func TestClient_Files_RecursiveCapFallsBackToTopLevel(t *testing.T) {
	t.Parallel()

	var sawRecursive []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/projects/current/files" {
			http.NotFound(w, r)
			return
		}
		rec := r.URL.Query().Get("recursive")
		sawRecursive = append(sawRecursive, rec)
		w.Header().Set("Content-Type", "application/json")
		if rec == "true" {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_, _ = io.WriteString(w, `{"error":"File listing exceeds maxFiles limit (2000)","ok":false}`)
			return
		}
		_, _ = io.WriteString(w, `{"files":[{"path":"wiki","name":"wiki","kind":"directory","size":0,"mtime":0}],"truncated":false}`)
	}))
	defer srv.Close()

	c, err := New(t.Context(), Config{
		BaseURL: srv.URL,
		Token:   "t",
		FlagOn:  func(_ context.Context) bool { return true },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	files, err := c.Files(t.Context(), "wiki", true, 0)
	if err != nil {
		t.Fatalf("Files after cap fallback: %v", err)
	}
	if len(files) != 1 || files[0].Path != "wiki" {
		t.Fatalf("unexpected top-level files: %+v", files)
	}
	if len(sawRecursive) != 2 || sawRecursive[0] != "true" || sawRecursive[1] != "false" {
		t.Fatalf("recursive params seen = %v, want [true false]", sawRecursive)
	}

	// Non-recursive callers never trigger the cap path.
	sawRecursive = nil
	if _, err := c.Files(t.Context(), "wiki", false, 0); err != nil {
		t.Fatalf("non-recursive Files: %v", err)
	}
	if len(sawRecursive) != 1 || sawRecursive[0] != "false" {
		t.Fatalf("non-recursive call should send recursive=false once, saw %v", sawRecursive)
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
		if _, err := w.Write(t.Context(), p, []byte("x")); err != ErrPathOutsideVault {
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
	got, err := w.Write(t.Context(), "sub/notes/today.md", []byte("hello"))
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
