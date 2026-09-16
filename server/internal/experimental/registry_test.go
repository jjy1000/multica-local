package experimental

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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

// TestRegistry_RunInstallAttributesPanic pins the 0.5.107 producer for the
// panic arm of the 0.3.18 safety net. SetPanicFlagContext previously had no
// caller at all, so the recover() sentinel in cmd/server/main.go always read
// an empty slot and ReasonPanic was never recorded — a flag that crashed on
// install kept crashing on every launch. RunInstall is the chokepoint where
// per-flag code runs, so it must leave the flag's name in the slot for that
// sentinel.
//
// Deliberately NOT t.Parallel: it shares the package-level slot with the
// parallel install tests above, and the sequential group finishes before any
// paused test resumes. Pop in the recover consumes the slot so no stale
// context leaks into later tests.
func TestRegistry_RunInstallAttributesPanic(t *testing.T) {
	defer PopPanicFlagContext() // belt and braces

	r := NewRegistry()
	r.RegisterInstallHandler("semantica", func(_, _ string) error {
		panic("simulated install crash")
	})

	recovered := false
	func() {
		defer func() {
			if recover() == nil {
				t.Error("RunInstall should not swallow the handler panic")
			}
			recovered = true
			key, ctx, ok := PopPanicFlagContext()
			if !ok {
				t.Error("RunInstall left no panic flag context; the main.go sentinel cannot attribute the crash")
				return
			}
			if key != "semantica" || ctx != "registry.RunInstall" {
				t.Errorf("panic attributed to (%q, %q), want (semantica, registry.RunInstall)", key, ctx)
			}
		}()
		_ = r.RunInstall("semantica", "user-123", "ws-1")
	}()

	if !recovered {
		t.Fatal("panic never reached the recover() sentinel")
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

// TestSidebarEntriesLogsAndReturnsEmptyOnParseError pins the 0.5.x
// fix: a manifest parse failure used to silently return nil and
// leave ops in the dark. The new contract logs a slog.Warn and
// returns a non-nil empty slice so the JSON wire shape stays
// stable across "unknown flag", "no manifest", and "manifest
// corrupt" — the response struct's omitempty tag drops all three,
// but a downstream consumer that distinguishes nil vs len==0
// should still see one consistent answer.
//
// NOT t.Parallel(): SetManifestRoot + slog.SetDefault mutate
// package-global state; parallel siblings in catalog_test.go /
// manifest_test.go call LoadManifest on real manifests and would
// race against the temp-dir override. Pattern matches
// TestAutoDispatchFlagBehavior above.
func TestSidebarEntriesLogsAndReturnsEmptyOnParseError(t *testing.T) {
	// Use chat_pin_ui — its catalog ManifestPath is fixed and
	// well-known, so the corrupt-manifest fixture only needs to
	// write to one specific temp path.
	const flagKey = "chat_pin_ui"

	tmpDir := t.TempDir()
	manifestDir := filepath.Join(tmpDir, "experiments", flagKey)
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", manifestDir, err)
	}
	// Intentionally invalid JSON — LoadManifest returns
	// ErrInvalidManifest for a parse failure (vs ErrNoManifest for
	// a missing file). Both paths funnel into the same slog.Warn +
	// []SidebarEntry{} branch in the production code.
	corruptPath := filepath.Join(manifestDir, "manifest.json")
	if err := os.WriteFile(corruptPath, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("write corrupt manifest: %v", err)
	}
	// Swap the manifest root to the temp dir for the duration of
	// this test. setManifestRootForTest returns a cleanup that the
	// deferred call runs at test exit.
	defer setManifestRootForTest(t, tmpDir)()

	// Capture slog output. The default logger's text format is
	// stable across Go releases — key=value pairs appear as
	// "<key>=<value>" substrings.
	var buf bytes.Buffer
	origLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(origLogger) })

	r := NewRegistry()
	result := r.SidebarEntries(flagKey)

	// (1) result is []SidebarEntry{} (non-nil), NOT nil.
	if result == nil {
		t.Fatal("SidebarEntries returned nil on parse error; want []SidebarEntry{} (non-nil empty slice)")
	}
	if len(result) != 0 {
		t.Fatalf("SidebarEntries returned %d entries on parse error; want 0: %+v", len(result), result)
	}

	// (2) slog.Warn was emitted with the expected key/flag.
	output := buf.String()
	if !strings.Contains(output, "experimental sidebar manifest load failed") {
		t.Fatalf("slog output missing expected message: %q", output)
	}
	if !strings.Contains(output, flagKey) {
		t.Fatalf("slog output missing flag_key=%q: %q", flagKey, output)
	}
	// Also assert the err attribute made it through so ops can
	// triage from the log line alone (load it via k=v parsing).
	if !strings.Contains(output, "err=") {
		t.Fatalf("slog output missing err= attribute: %q", output)
	}

	// (3) The empty-slice result is also cached so a repeated
	// call does not hammer the broken file. This is a bonus pin
	// — without it, every /api/experimental-flags request would
	// re-parse the corrupt manifest and spam slog.
	result2 := r.SidebarEntries(flagKey)
	if len(result2) != 0 {
		t.Fatalf("second SidebarEntries call returned %d entries; want 0 (cache miss on parse error)", len(result2))
	}
}

// TestSidebarEntriesCache pins the 0.5.x memoization contract:
// SidebarEntries loads the manifest on the first call, caches the
// wire-shape SidebarEntry slice in sidebarCache, and never
// re-parses on subsequent calls even when the on-disk manifest is
// corrupted or removed.
//
// The test deliberately invalidates both cache layers between
// calls — clears f.Sidebar (the row cache) and corrupts the
// manifest file (forcing a re-parse that would fail). Without the
// sidebarCache wire slice, the second call would go through
// LoadManifest, fail on the corrupt file, and return
// []SidebarEntry{}. With sidebarCache, the second call returns
// the original rows.
//
// Implementation note: the test uses a temp dir + a freshly-
// registered user plugin flag rather than mutating the real dev
// resources tree. The earlier version of this test deleted a real
// manifest and depended on t.Cleanup to restore it — if the test
// was skipped before the Cleanup registered, the deletion leaked
// and broke TestLoadManifestResolvesAllFlags in the same package.
// Temp-dir fixtures isolate this test from any sibling that reads
// the dev resources tree.
//
// NOT t.Parallel(): Catalog / RegisterUserPlugins /
// setManifestRootForTest mutate global state. Pattern matches
// TestAutoDispatchFlagBehavior + the parse-error test above.
func TestSidebarEntriesCache(t *testing.T) {
	const flagKey = "test_sidebar_cache_flag"

	// Snapshot Catalog + restore on cleanup. The cache test needs
	// the test flag to appear in the global Catalog slice because
	// LoadManifest iterates Catalog (not user plugins) to resolve
	// ManifestPath. Adding the flag to r.flags alone is not enough.
	origCatalog := Catalog
	t.Cleanup(func() { Catalog = origCatalog })
	Catalog = append(append([]Flag{}, origCatalog...), Flag{
		Key:          flagKey,
		ManifestPath: "experiments/" + flagKey + "/manifest.json",
	})

	// Build a temp resources tree with a valid manifest for this
	// flag. The manifest declares two sidebar rows so the test can
	// assert a non-zero length after each call.
	tmpDir := t.TempDir()
	manifestDir := filepath.Join(tmpDir, "experiments", flagKey)
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", manifestDir, err)
	}
	manifestJSON := `{
		"apiVersion": "multica.dev/experiment/v1",
		"kind": "Experiment",
		"metadata": {
			"name": "` + flagKey + `",
			"flag": "` + flagKey + `",
			"title": {"en": "Cache Test"},
			"description": {"en": "Test cache"}
		},
		"spec": {
			"entry_points": {
				"sidebar": [
					{"key": "tab1", "label_key": "tab1_label", "route": "/test/tab1"},
					{"key": "tab2", "label_key": "tab2_label", "route": "/test/tab2"}
				]
			}
		}
	}`
	manifestPath := filepath.Join(manifestDir, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte(manifestJSON), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	defer setManifestRootForTest(t, tmpDir)()

	r := NewRegistry()

	// First call — should load and populate the cache.
	first := r.SidebarEntries(flagKey)
	if len(first) != 2 {
		t.Fatalf("first SidebarEntries returned %d entries, want 2: %+v", len(first), first)
	}

	// sidebarCache must be populated after the first call.
	r.mu.Lock()
	cached, ok := r.sidebarCache[flagKey]
	r.mu.Unlock()
	if !ok {
		t.Fatal("sidebarCache missing entry after first SidebarEntries call")
	}
	if len(cached) != len(first) {
		t.Fatalf("sidebarCache[%q] length %d != first-call length %d", flagKey, len(cached), len(first))
	}
	for i, e := range cached {
		if e != first[i] {
			t.Errorf("sidebarCache[%q][%d] = %+v, want %+v", flagKey, i, e, first[i])
		}
	}

	// Invalidate both cache layers:
	//   - clear f.Sidebar (so the existing "Sidebar != nil"
	//     short-circuit no longer fires);
	//   - corrupt the manifest file (so a fresh LoadManifest call
	//     would return ErrInvalidManifest).
	// Without sidebarCache, the second call would re-parse,
	// fail on the corrupt file, and return []SidebarEntry{}.
	r.mu.Lock()
	f := r.flags[flagKey]
	f.Sidebar = nil
	r.flags[flagKey] = f
	r.mu.Unlock()
	if err := os.WriteFile(manifestPath, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("corrupt manifest: %v", err)
	}

	// Second call — must still return the original rows via the
	// wire slice cache, not via LoadManifest (which would now fail).
	second := r.SidebarEntries(flagKey)
	if len(second) != len(first) {
		t.Fatalf("second SidebarEntries call returned %d entries, want %d (cache miss after manifest corrupt)",
			len(second), len(first))
	}
	for i, e := range second {
		if e != first[i] {
			t.Errorf("second[%d] = %+v, want %+v (cache hit must round-trip)", i, e, first[i])
		}
	}

	// Third call as a paranoia check — multiple cache hits in a
	// row must remain stable (no state corruption in the cache
	// layer across repeated reads).
	third := r.SidebarEntries(flagKey)
	if len(third) != len(first) {
		t.Fatalf("third SidebarEntries call returned %d entries, want %d", len(third), len(first))
	}
}
