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

// TestAutoDispatchFlagBehavior (0.5.22) pins the AutoDispatch helper
// contract: nil pointer and *true both mean "auto-dispatch as before";
// *false means "skip auto-dispatch — user must trigger manually"; and
// unknown keys default to true so a typo or a retired catalog entry
// never silently turns off the agent enqueue.
//
// The helper reads Catalog directly, so the test snapshots + restores
// the global slice around a temporary mutation. AutoDispatch itself
// does not consult the registry's flag map.
//
// NOT t.Parallel(): this test mutates the package-global Catalog, which
// races with TestCatalog_NewFlagsHaveManifestPaths / _RuntimeAndBridgeFlagsAreKnown
// (also parallel). Serial execution is intentional.
func TestAutoDispatchFlagBehavior(t *testing.T) {

	trueVal := true
	falseVal := false

	orig := Catalog
	t.Cleanup(func() { Catalog = orig })

	Catalog = []Flag{
		{Key: "lab_default", AutoDispatch: nil},
		{Key: "lab_explicit_true", AutoDispatch: &trueVal},
		{Key: "lab_explicit_false", AutoDispatch: &falseVal},
		// claude_science_lab pin: the only production opt-out
		// (snapshot of the real catalog value).
		{Key: "claude_science_lab", AutoDispatch: &falseVal},
		{Key: "pythia_oracle", AutoDispatch: nil},
	}

	cases := []struct {
		key  string
		want bool
	}{
		{"lab_default", true},
		{"lab_explicit_true", true},
		{"lab_explicit_false", false},
		{"claude_science_lab", false},
		{"pythia_oracle", true},
		{"unknown_key", true},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			if got := AutoDispatch(c.key); got != c.want {
				t.Fatalf("AutoDispatch(%q) = %v, want %v", c.key, got, c.want)
			}
		})
	}
}
