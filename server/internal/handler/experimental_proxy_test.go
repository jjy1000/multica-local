package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/experimental"
)

// TestIsLoopbackUpstreamURL pins the SSRF guard on the experimental
// upstream-registration endpoint: only http(s) URLs targeting a
// loopback host may be registered as a reverse-proxy upstream. Any
// external host must be rejected so the same-origin proxy can never
// be turned into an open proxy.
func TestIsLoopbackUpstreamURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{"ipv4 loopback with port", "http://127.0.0.1:33333", true},
		{"ipv4 loopback subnet", "http://127.0.0.5:8080/health", true},
		{"ipv6 loopback", "http://[::1]:9000", true},
		{"localhost hostname", "http://localhost:8088", true},
		{"https loopback", "https://127.0.0.1:8443", true},
		{"external host", "http://attacker.example/leak", false},
		{"external ip", "http://10.0.0.1:80", false},
		{"public ip", "http://93.184.216.34", false},
		{"metadata endpoint", "http://169.254.169.254/latest/meta-data", false},
		{"non-http scheme", "file:///etc/passwd", false},
		{"gopher scheme", "gopher://127.0.0.1:70", false},
		{"empty string", "", false},
		{"garbage", "not a url at all ::::", false},
		{"host-only no scheme", "127.0.0.1:8090", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLoopbackUpstreamURL(tt.url); got != tt.want {
				t.Errorf("isLoopbackUpstreamURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

// TestUpstreamRegisterKeyMirror pins the engine-key lifecycle: a
// registration carrying a key must populate BOTH mirrors (the package-level
// experimentalLoopback.apiKeys map the direct engine callers read via
// oracleEngineKey, and h.ExperimentalFlagAPIKeys the reverse-proxy
// Director consults); an empty-key registration and a DELETE unregister
// must clear BOTH, so a key never outlives the URL it was registered with.
// The pythia loopback token gate (0.5.122 security batch) authenticates
// with this key — a stale surviving key would keep authenticating callers
// against an engine the manager no longer owns.
func TestUpstreamRegisterKeyMirror(t *testing.T) {
	const service = "pythia_oracle"
	h := &Handler{ExperimentRegistry: experimental.NewRegistry()}
	t.Cleanup(func() {
		SetExperimentalLoopbackURL(service, "")
		experimentalLoopback.Lock()
		delete(experimentalLoopback.apiKeys, service)
		experimentalLoopback.Unlock()
		h.ExperimentalFlagAPIKeysMu.Lock()
		delete(h.ExperimentalFlagAPIKeys, service)
		h.ExperimentalFlagAPIKeysMu.Unlock()
	})

	register := func(key string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		body := map[string]any{"service": service, "url": "http://127.0.0.1:33333"}
		if key != "" {
			body["key"] = key
		}
		h.upstreamRegister(w, newRequest(http.MethodPost, "/__experimental/upstream", body))
		return w
	}

	// Registration with a key populates both mirrors.
	w := register("engine-secret-1")
	if w.Code != http.StatusNoContent {
		t.Fatalf("register: expected 204, got %d: %s", w.Code, w.Body.String())
	}
	if got := oracleEngineKey(); got != "engine-secret-1" {
		t.Errorf("oracleEngineKey() = %q after keyed register, want engine-secret-1", got)
	}
	if got := h.ExperimentalFlagAPIKeys[service]; got != "engine-secret-1" {
		t.Errorf("ExperimentalFlagAPIKeys[%s] = %q after keyed register, want engine-secret-1", service, got)
	}

	// An empty-key registration clears both mirrors (re-registration in
	// anonymous mode must not leave the old key behind).
	w = register("")
	if w.Code != http.StatusNoContent {
		t.Fatalf("re-register without key: expected 204, got %d: %s", w.Code, w.Body.String())
	}
	if got := oracleEngineKey(); got != "" {
		t.Errorf("oracleEngineKey() = %q after empty-key register, want empty", got)
	}
	if got := h.ExperimentalFlagAPIKeys[service]; got != "" {
		t.Errorf("ExperimentalFlagAPIKeys[%s] = %q after empty-key register, want empty", service, got)
	}

	// A keyed registration followed by DELETE clears both mirrors.
	w = register("engine-secret-2")
	if w.Code != http.StatusNoContent {
		t.Fatalf("re-register with key: expected 204, got %d: %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	h.upstreamUnregister(w, withURLParam(
		httptest.NewRequest(http.MethodDelete, "/__experimental/upstream/"+service, nil),
		"service", service,
	))
	if w.Code != http.StatusNoContent {
		t.Fatalf("unregister: expected 204, got %d: %s", w.Code, w.Body.String())
	}
	if got := oracleEngineKey(); got != "" {
		t.Errorf("oracleEngineKey() = %q after unregister, want empty", got)
	}
	if got := h.ExperimentalFlagAPIKeys[service]; got != "" {
		t.Errorf("ExperimentalFlagAPIKeys[%s] = %q after unregister, want empty", service, got)
	}
}
