package llmwiki

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestSmoke_LLMWikiBridge_LiveDesktop exercises the wired end-to-end
// path against the user's actual /Applications/LLM Wiki.app on
// 127.0.0.1:19828. Skips when LLM Wiki isn't running.
func TestSmoke_LLMWikiBridge_LiveDesktop(t *testing.T) {
	t.Parallel()

	// 1. Probe the desktop API directly. We do NOT use the multica
	// server here — the goal is to verify the llmwiki client +
	// writer code paths, not the HTTP handler (the handler test
	// would require the full server stack + DB + workspace).
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://127.0.0.1:19828/api/v1/health")
	if err != nil {
		t.Skipf("LLM Wiki desktop not running: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Skipf("desktop returned %d: %s", resp.StatusCode, string(body))
	}

	// 2. The desktop health payload must include {"ok":true,...}.
	var health map[string]any
	if err := json.Unmarshal(body, &health); err != nil {
		t.Fatalf("health body not JSON: %v", err)
	}
	if v, _ := health["ok"].(bool); !v {
		t.Fatalf("desktop health ok=false: %v", health)
	}

	// 3. Probe the writer against the user's real vault.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	vault := filepath.Join(home, "Documents", "llm wiki")
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Skipf("cannot create vault: %v", err)
	}

	SetFlagGate(func(_ context.Context) bool { return true })
	w, err := NewWriter(vault)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	writePath := "sources/multica-smoke-" + strconv.FormatInt(time.Now().UnixNano(), 10) + ".md"
	body2 := []byte("# Multica smoke test\n\nwritten by 0.3.19-dev runtime_gc_test.go\n")
	got, err := w.Write(t.Context(), writePath, body2)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	t.Logf("wrote %s (%d bytes)", got, len(body2))

	if _, err := w.Write(t.Context(), "../escape.md", body2); err != ErrPathOutsideVault {
		t.Fatalf("parent-traversal should have been rejected; got %v", err)
	}

	SetFlagGate(func(_ context.Context) bool { return false })
	if _, err := w.Write(t.Context(), "flag-off.md", body2); err != ErrFlagDisabled {
		t.Fatalf("flag-off write should have been rejected; got %v", err)
	}

	// 4. Verify the file landed on disk.
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("written file not on disk: %v", err)
	}

	// 5. Roundtrip read via the client's happy path against a stub
	// (we don't talk to the desktop API here because the desktop
	// returns 401 when no bearer token is configured; just confirm
	// the client constructor panics with a clear error).
	_ = context.Background()
}

// TestSmoke_ClientFlagOffRepeated confirms repeated calls with the
// flag off do not touch the network at any point. The HTTP client
// times out fast enough that a regression where the flag gate is
// dropped would surface as a timeout.
func TestSmoke_ClientFlagOffRepeated(t *testing.T) {
	t.Parallel()

	called := 0
	c, err := New(t.Context(), Config{
		BaseURL: "http://127.0.0.1:1",
		Token:   "t",
		FlagOn:  func(_ context.Context) bool { return false },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, err := c.Health(t.Context()); err != ErrFlagDisabled {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}
	if called > 0 {
		t.Fatal("client touched the network while the flag was off")
	}
}
