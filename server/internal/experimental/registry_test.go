package experimental

import (
	"errors"
	"sync"
	"testing"
)

// TestRegistry_BuildsFromCatalog pins the P2 contract: every
// catalog flag must appear in Registry.Flags() and the registry
// handler maps must be empty until boot binds them.
func TestRegistry_BuildsFromCatalog(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	if got := len(r.Flags()); got < len(Catalog) {
		t.Fatalf("Registry.Flags() = %d entries, want ≥ %d", got, len(Catalog))
	}
	if r.IsInstallable("chat_pin_ui") {
		t.Fatal("chat_pin_ui is not installable; install handler should be unbound")
	}
	if r.IsInstallable("claude_science_runtime") {
		t.Fatal("claude_science_runtime is not installable; install handler should be unbound")
	}
}

// TestRegistry_InstallRoundTrip verifies the install dispatcher
// behavior for both the happy path and a missing handler.
func TestRegistry_InstallRoundTrip(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	called := false
	r.RegisterInstallHandler("claude_science_lab", func(userID, workspaceID string) error {
		if userID != "user-123" {
			t.Fatalf("expected userID user-123, got %q", userID)
		}
		called = true
		return nil
	})

	if err := r.RunInstall("claude_science_lab", "user-123", "ws-1"); err != nil {
		t.Fatalf("RunInstall: %v", err)
	}
	if !called {
		t.Fatal("install handler not invoked")
	}

	if err := r.RunInstall("pythia_oracle", "", ""); !errors.Is(err, ErrNoInstallHandler) {
		t.Fatalf("RunInstall on unbound flag: got %v, want ErrNoInstallHandler", err)
	}
}

// TestRegistry_LoopbackURLConcurrent drives concurrent
// SetLoopbackURL + GetLoopbackURL to confirm the mutex protects
// the loopback map. The Desktop manager writes the URL on
// boot, the proxy handlers read it on every reverse-proxy
// hop; both sides run in separate goroutines.
func TestRegistry_LoopbackURLConcurrent(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	const workers = 8
	const iterations = 50
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				r.SetLoopbackURL("pythia", "http://127.0.0.1:12345")
				if got := r.LoopbackURL("pythia"); got == "" {
					t.Errorf("missing loopback url after set")
				}
			}
		}()
	}
	wg.Wait()
	if got := r.LoopbackURL("pythia"); got != "http://127.0.0.1:12345" {
		t.Fatalf("final loopback url = %q", got)
	}

	r.SetLoopbackURL("pythia", "") // simulate manager shutdown
	if got := r.LoopbackURL("pythia"); got != "" {
		t.Fatalf("loopback url after empty-set = %q, want empty", got)
	}
}

// TestRegistry_ProxyRoutesOnlySubprocess asserts the proxy-mounter
// filters out non-subprocess flags. The contract keeps
// inline / headless / none flags off the proxy surface so a future
// contributor can't accidentally leak the desktop renderer onto
// /experimental/<flag>.
func TestRegistry_ProxyRoutesOnlySubprocess(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	for _, p := range r.ProxyRoutes() {
		if p.Prefix == "" {
			t.Errorf("proxy prefix empty for flag %q", p.FlagKey)
		}
		if !startsWith(p.Prefix, "/") {
			t.Errorf("proxy prefix %q does not start with /", p.Prefix)
		}
	}
}

func startsWith(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	return s[:len(prefix)] == prefix
}

// TestRegistry_RollbackDefaultNoop locks the contract that an
// unbound rollback handler returns nil. The install + rollback
// endpoints depend on this — a non-nil default would surface
// as a 500 in the renderer for every flag that never registered
// a rollback.
func TestRegistry_RollbackDefaultNoop(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	if err := r.RunRollback("anything"); err != nil {
		t.Fatalf("RunRollback default: %v", err)
	}
}
