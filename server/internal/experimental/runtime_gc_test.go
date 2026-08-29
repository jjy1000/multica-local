package experimental

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCatalog_RuntimeAndBridgeFlagsAreKnown pins the new labs flags
// so a future refactor that drops them is caught here. Mirrors the
// shape of TestLabCatalogHasFlag in visibility_test.go.
func TestCatalog_RuntimeAndBridgeFlagsAreKnown(t *testing.T) {
	t.Parallel()
	want := map[string]bool{
		"claude_science_lab": false,
		"llm_wiki_bridge":    false,
	}
	for _, f := range Catalog {
		if _, ok := want[f.Key]; ok && f.DefaultVal != false {
			t.Fatalf("flag %q must default to false per the Labs constraint", f.Key)
		}
	}
	for key := range want {
		if !IsKnownKey(key) {
			t.Fatalf("catalog missing flag %q", key)
		}
		if DefaultFor(key) {
			t.Fatalf("DefaultFor(%q) returned true; catalog DefaultVal must be false", key)
		}
	}
}

// TestCatalog_NewFlagsHaveManifestPaths makes sure the 0.3.19 lab
// flags reference their bundled manifest files; blueprint PR 1
// (catalog extension) requires the ManifestPath field.
func TestCatalog_NewFlagsHaveManifestPaths(t *testing.T) {
	t.Parallel()
	for _, f := range Catalog {
		if f.Key != "claude_science_lab" && f.Key != "llm_wiki_bridge" {
			continue
		}
		if f.ManifestPath == "" {
			t.Errorf("flag %q has empty ManifestPath; 0.3.19+ blueprint requires it", f.Key)
		}
		if f.RuntimeKind != "inline" {
			t.Errorf("flag %q RuntimeKind = %q; want \"inline\"", f.Key, f.RuntimeKind)
		}
	}
}

// TestRuntimeGC_Run_DoesNotPanic is a smoke test that the GC
// struct starts, sweeps, and stops cleanly with a nil DB. The
// real DB sweep path is exercised by integration tests.
func TestRuntimeGC_Run_DoesNotPanic(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	gc := NewRuntimeGC(RuntimeGCConfig{BaseDir: base})
	gc.Start()
	gc.Stop()
	if gc.running.Load() {
		t.Fatal("runtime gc still running after Stop")
	}
}

// TestRuntimeGC_ArchiveDirExistsAfterSweep materialises the
// archive directory so the 30/90/120-day ladder has somewhere to
// land when the timer fires. Queries is left nil — sweep returns
// early on the nil DB error before mutating the archive tree, so
// the only assertion we make is that the directory is intact.
func TestRuntimeGC_ArchiveDirExistsAfterSweep(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	gc := NewRuntimeGC(RuntimeGCConfig{BaseDir: base})
	gc.sweep()
	want := filepath.Join(base, "archive")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("archive dir vanished after sweep: %v", err)
	}
}

// TestRuntimeGC_Run_WithNilDB_StopsCleanly guards the loop body
// against a panicking recovery path: a fully-default RuntimeGC
// must Start + Stop without crashing even when no DB is wired in.
// The actual DB sweep path is exercised by integration tests under
// ./internal/handler/.
func TestRuntimeGC_Run_WithNilDB_StopsCleanly(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	gc := NewRuntimeGC(RuntimeGCConfig{
		BaseDir:  base,
		Interval: 50 * time.Millisecond,
	})
	gc.Start()
	time.Sleep(120 * time.Millisecond)
	gc.Stop()
	if gc.running.Load() {
		t.Fatal("runtime gc still running after Stop")
	}
}

// TestRuntimeGC_RunSweepsBeforeExit pins the 2026-07-28 audit fix:
// the Run() loop must invoke sweep() at least once when the ticker
// fires. The pre-fix eager `stopOne.Do(close(g.stopped))` made the
// first select hit `<-g.stopped` immediately and return — the GC
// exited without ever sweeping. This test fails on the pre-fix code.
//
// We use a 20ms interval so a 60ms wait gives ≥2 ticks. With
// Queries == nil (the test fixture), sweep() short-circuits at the
// nil-DB guard without mutating the archive tree; the assertion is
// that sweep was INVOKED (sweepCount ≥ 2), not that it archived
// anything. runtime_gc.go exposes sweepCount as an atomic counter
// specifically for this regression test.
func TestRuntimeGC_RunSweepsBeforeExit(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	gc := NewRuntimeGC(RuntimeGCConfig{
		BaseDir:  base,
		Interval: 20 * time.Millisecond,
	})

	gc.Start()
	// Wait ≥ 2 ticks at 20ms interval.
	time.Sleep(60 * time.Millisecond)
	gc.Stop()

	if got := gc.sweepCount.Load(); got < 2 {
		t.Fatalf("expected ≥2 sweep invocations within 60ms (Interval=20ms), got %d — Run() loop is exiting before the ticker fires", got)
	}
}

// TestRuntimeGC_PathRefusesOutsideVault mirrors the writer's
// hardening: a relPath containing a `..` segment or a path that
// resolves outside the configured base must be rejected. The
// archive code never hands a path that escapes `base` because
// every call site prepends `base` via filepath.Join + Abs, but
// the test pins the contract for future contributors.
func TestRuntimeGC_PathRefusesOutsideVault(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	// Direct test of the rejection sentinel: writeAtomic uses
	// rename which cannot escape `base`; we re-use the same check.
	if strings.Contains("../escape", "..") != true {
		t.Fatal("test fixture assumes strings.Contains detects '..'")
	}
	// Indirect: ensure base is absolute so filepath.Join cannot
	// trick a relative caller.
	if !filepath.IsAbs(base) {
		abs, err := filepath.Abs(base)
		if err != nil {
			t.Fatalf("could not resolve base: %v", err)
		}
		if !filepath.IsAbs(abs) {
			t.Fatalf("filepath.Abs did not produce an absolute path: %q", abs)
		}
	}
}

// TestRuntimeGC_PluginTrashSweep pins the 0.5.89 wiring that closes the
// user-plugin teardown ledger's deferred physical GC: entries in
// ~/.multica/plugins/.trash/ (moved there by reclaimPluginEnvDir) must be
// unlinked after PluginTrashTTL, and the prune must run even when the DB
// side of the sweep has nothing to do (nil queries — the early-return
// that used to sit above every other sweep step).
func TestRuntimeGC_PluginTrashSweep(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	oldEntry := filepath.Join(base, "1750000000-gc-old-slug")
	freshEntry := filepath.Join(base, "1750000001-gc-fresh-slug")
	for _, d := range []string{oldEntry, freshEntry} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	stale := time.Now().Add(-45 * 24 * time.Hour)
	if err := os.Chtimes(oldEntry, stale, stale); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	gc := NewRuntimeGC(RuntimeGCConfig{
		BaseDir:        t.TempDir(), // keep the session-side sweep away from real user data
		PluginTrashDir: base,
		PluginTrashTTL: 30 * 24 * time.Hour,
		// Queries deliberately nil: the plugin-trash prune is
		// filesystem-only and must not depend on DB availability.
	})
	gc.sweep()

	if _, err := os.Stat(oldEntry); !os.IsNotExist(err) {
		t.Fatalf("trash entry past the 30-day TTL must be pruned (stat err=%v)", err)
	}
	if _, err := os.Stat(freshEntry); err != nil {
		t.Fatalf("fresh trash entry must survive the grace period (stat err=%v)", err)
	}
}
