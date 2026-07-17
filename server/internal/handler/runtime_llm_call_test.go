package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/multica-ai/multica/server/internal/auth"
)

// signLoopbackJWT mints a short-lived HS256 token signed with the same
// secret the production middleware uses. Kept in this test file because
// no other package wants a test-only signer.
func signLoopbackJWT(t *testing.T) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "user-loopback-test",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	signed, err := tok.SignedString(auth.JWTSecret())
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return signed
}

// stubProvider installs a fake provider CLI on $PATH for the duration of
// the test and returns a function that restores the previous PATH. The
// fake writes the prompt it received on stdin to capturePath (so tests
// can assert on the exact message) and prints a canned response.
//
// Tests must NOT rely on a real claude / codex / cursor-agent binary —
// we deliberately avoid pulling model providers into the unit-test
// dependency tree.
func stubProvider(t *testing.T, response, capturePath string) func() {
	t.Helper()
	dir := t.TempDir()
	binPath := filepath.Join(dir, "claude")
	// `tee` so the script keeps the prompt on stdout (some CLIs print
	// it back); we just care about side-effects here. The capture
	// file's directory must exist before the script runs, so the
	// caller passes a path inside t.TempDir().
	if err := os.MkdirAll(filepath.Dir(capturePath), 0o755); err != nil {
		t.Fatalf("mkdir capture: %v", err)
	}
	script := "#!/usr/bin/env bash\n" +
		"set -e\n" +
		"tee \"" + capturePath + "\" >/dev/null\n" +
		"echo \"" + response + "\"\n"
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake provider: %v", err)
	}
	prev := os.Getenv("PATH")
	if err := os.Setenv("PATH", dir+string(os.PathListSeparator)+prev); err != nil {
		t.Fatalf("set PATH: %v", err)
	}
	return func() { _ = os.Setenv("PATH", prev) }
}

func TestLLMCallHandler(t *testing.T) {
	// Tests use t.Setenv, so the parent test cannot use t.Parallel.
	// Subtests stay parallel-safe because each one's body either
	// avoids Setenv or restores PATH in a way that doesn't race with
	// sibling subtests (PATH is process-global; we serialize env
	// mutations into the "502" and "happy path" subtests only).

	send := func(remoteAddr string, headers map[string]string, body any) *httptest.ResponseRecorder {
		var raw []byte
		switch b := body.(type) {
		case nil:
			raw = nil
		case string:
			raw = []byte(b)
		default:
			raw, _ = json.Marshal(b)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/runtime/llm-call", bytes.NewReader(raw))
		if remoteAddr != "" {
			req.RemoteAddr = remoteAddr
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		(&Handler{}).LLMCallHandler(w, req)
		return w
	}

	t.Run("rejects non-loopback source", func(t *testing.T) {
		w := send("203.0.113.5:55555", map[string]string{
			"Authorization": "Bearer " + signLoopbackJWT(t),
		}, map[string]string{"prompt": "hi"})
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", w.Code)
		}
	})

	t.Run("rejects missing bearer", func(t *testing.T) {
		w := send("127.0.0.1:55555", nil, map[string]string{"prompt": "hi"})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
	})

	t.Run("rejects invalid bearer", func(t *testing.T) {
		w := send("127.0.0.1:55555", map[string]string{
			"Authorization": "Bearer not-a-jwt",
		}, map[string]string{"prompt": "hi"})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
	})

	t.Run("rejects empty prompt and messages", func(t *testing.T) {
		w := send("127.0.0.1:55555", map[string]string{
			"Authorization": "Bearer " + signLoopbackJWT(t),
		}, map[string]any{})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", w.Code)
		}
	})

	t.Run("502 when no provider CLI on PATH", func(t *testing.T) {
		// Force PATH to an empty dir so LookPath fails for every known
		// provider. Use a non-existent path that still parses so the
		// "directory" check doesn't fall back to system PATH via shell.
		emptyDir := t.TempDir()
		prev := os.Getenv("PATH")
		t.Setenv("PATH", emptyDir)
		t.Cleanup(func() { _ = os.Setenv("PATH", prev) })
		w := send("127.0.0.1:55555", map[string]string{
			"Authorization": "Bearer " + signLoopbackJWT(t),
		}, map[string]string{"prompt": "hello"})
		if w.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502", w.Code)
		}
		if !strings.Contains(w.Body.String(), "no provider CLI") {
			t.Fatalf("body should explain missing provider, got %q", w.Body.String())
		}
	})

	t.Run("happy path returns provider text", func(t *testing.T) {
		capturePath := filepath.Join(t.TempDir(), "stdin.txt")
		restore := stubProvider(t, "PYTHIA-OK", capturePath)
		t.Cleanup(restore)

		w := send("127.0.0.1:55555", map[string]string{
			"Authorization":  "Bearer " + signLoopbackJWT(t),
			"X-Pythia-Source": "pythia-oracle",
		}, map[string]any{
			"system":     "you are a forecaster",
			"prompt":     "what happens next",
			"max_tokens": 200,
		})
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
		}
		var resp LLMCallResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.Text != "PYTHIA-OK" {
			t.Fatalf("text = %q, want %q", resp.Text, "PYTHIA-OK")
		}
		// Sanity check: provider actually saw the system + prompt
		// rolled into a single user turn. The capture file is written
		// by the stub script.
		got, err := os.ReadFile(capturePath)
		if err != nil {
			t.Fatalf("read capture: %v", err)
		}
		body := string(got)
		if !strings.Contains(body, "you are a forecaster") {
			t.Fatalf("stdin missing system turn: %q", body)
		}
		if !strings.Contains(body, "what happens next") {
			t.Fatalf("stdin missing user turn: %q", body)
		}
	})

	t.Run("rate limit kicks in after threshold", func(t *testing.T) {
		restore := stubProvider(t, "ok", filepath.Join(t.TempDir(), "ignored.txt"))
		t.Cleanup(restore)

		// Clear any buckets left behind by sibling subtests so the
		// threshold assertion starts from a known zero. The bucket map
		// is process-global and earlier subtests may have accumulated
		// hits on 127.0.0.1.
		runtimeLLMCallBucketsMu.Lock()
		clear(runtimeLLMCallBuckets)
		runtimeLLMCallBucketsMu.Unlock()

		headers := map[string]string{"Authorization": "Bearer " + signLoopbackJWT(t)}
		for i := 0; i < runtimeLLMCallRateLimit; i++ {
			w := send("127.0.0.1:55555", headers,
				map[string]string{"prompt": "tick"})
			if w.Code != http.StatusOK {
				t.Fatalf("call %d: status = %d, want 200; body=%s", i, w.Code, w.Body.String())
			}
		}
		w := send("127.0.0.1:55555", headers,
			map[string]string{"prompt": "tick-over"})
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("over-limit call: status = %d, want 429", w.Code)
		}
	})
}

// make sure time package survives goimports even though this file only
// references it transitively today.
var _ = time.Second
var _ = bytes.NewReader