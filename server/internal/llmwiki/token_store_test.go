package llmwiki

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// redirectHome points homeFn at a temp directory for the duration of
// the test and restores it afterwards. These tests mutate package
// globals, so none of them may call t.Parallel.
func redirectHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := homeFn
	homeFn = func() (string, error) { return dir, nil }
	t.Cleanup(func() { homeFn = old })
	return dir
}

// TestDiscoverToken_UserStoreAndPrecedence pins the discovery chain
// end to end against real files in a redirected home: the Multica
// user store wins over the app's app-state, the app-state wins over
// legacy layouts, and the env var overrides everything.
func TestDiscoverToken_UserStoreAndPrecedence(t *testing.T) {
	home := redirectHome(t)
	t.Setenv("LLM_WIKI_API_TOKEN", "")

	// Nothing on disk → actionable error, not a silent empty token.
	if _, err := DiscoverToken(t.Context()); err == nil {
		t.Fatal("DiscoverToken with empty home: expected error")
	} else if !strings.Contains(err.Error(), "Settings → API + MCP") {
		t.Fatalf("error should point at the app settings + paste flow, got: %v", err)
	}

	// Legacy layout alone resolves.
	legacy := filepath.Join(home, ".llm-wiki", "auth.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(`{"token":"tok-legacy"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if tok, err := DiscoverToken(t.Context()); err != nil || tok != "tok-legacy" {
		t.Fatalf("legacy discovery: tok=%q err=%v", tok, err)
	}
	if src := TokenSource(); src != "legacy" {
		t.Fatalf("TokenSource = %q, want legacy", src)
	}

	// v0.6.x app-state outranks legacy.
	appDir := filepath.Join(home, "Library", "Application Support", "com.llmwiki.app")
	if err := os.MkdirAll(appDir, 0o700); err != nil {
		t.Fatal(err)
	}
	appState := filepath.Join(appDir, "app-state.json")
	if err := os.WriteFile(appState, []byte(`{"apiConfig":{"token":"tok-app"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if tok, _ := DiscoverToken(t.Context()); tok != "tok-app" {
		t.Fatalf("app-state should outrank legacy, got %q", tok)
	}
	if src := TokenSource(); src != "app" {
		t.Fatalf("TokenSource = %q, want app", src)
	}

	// The Multica user store outranks everything on disk.
	if err := SaveUserToken("tok-pasted"); err != nil {
		t.Fatalf("SaveUserToken: %v", err)
	}
	if tok, _ := DiscoverToken(t.Context()); tok != "tok-pasted" {
		t.Fatalf("user store should be most authoritative, got %q", tok)
	}
	if src := TokenSource(); src != "user" {
		t.Fatalf("TokenSource = %q, want user", src)
	}

	// Env var beats every file source.
	t.Setenv("LLM_WIKI_API_TOKEN", "tok-env")
	if tok, _ := DiscoverToken(t.Context()); tok != "tok-env" {
		t.Fatalf("env should win, got %q", tok)
	}
	if src := TokenSource(); src != "env" {
		t.Fatalf("TokenSource = %q, want env", src)
	}
}

// TestClearUserToken_FallsBackToAppState pins that clearing the
// Multica store (the Labs 页 "清除" button / CLI --clear) drops
// discovery back to the app-side sources instead of going tokenless.
func TestClearUserToken_FallsBackToAppState(t *testing.T) {
	home := redirectHome(t)
	t.Setenv("LLM_WIKI_API_TOKEN", "")

	if err := SaveUserToken("tok-pasted"); err != nil {
		t.Fatal(err)
	}
	appDir := filepath.Join(home, "Library", "Application Support", "com.llmwiki.app")
	if err := os.MkdirAll(appDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "app-state.json"), []byte(`{"apiConfig":{"token":"tok-app"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ClearUserToken(); err != nil {
		t.Fatalf("ClearUserToken: %v", err)
	}
	// Clearing an already-absent store is a no-op success.
	if err := ClearUserToken(); err != nil {
		t.Fatalf("second ClearUserToken: %v", err)
	}
	if tok, _ := DiscoverToken(t.Context()); tok != "tok-app" {
		t.Fatalf("after clear, discovery should fall back to app-state, got %q", tok)
	}
}

// TestSaveUserToken_RejectsEmpty pins the guard so the HTTP handler
// can map an empty paste to 400 instead of writing a useless store.
func TestSaveUserToken_RejectsEmpty(t *testing.T) {
	redirectHome(t)
	if err := SaveUserToken("   "); err == nil {
		t.Fatal("SaveUserToken(whitespace): expected error")
	}
}
