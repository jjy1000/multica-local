package experimental

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestCatalogEveryFlagHasManifest ensures the 0.3.19 contract: every
// catalog entry declares a ManifestPath so the registry (PR 2) and
// downstream consumers can resolve a manifest. Plain-toggle flags are
// allowed to point at a minimal manifest so the loader returns a
// successful "empty" manifest instead of ErrNoManifest at the
// registry boot path.
func TestCatalogEveryFlagHasManifest(t *testing.T) {
	seen := make(map[string]bool, len(Catalog))
	for _, f := range Catalog {
		seen[f.Key] = true
		if f.ManifestPath == "" {
			t.Errorf("flag %q has empty ManifestPath (P1 contract)", f.Key)
		}
		if !filepath.IsLocal(f.ManifestPath) {
			t.Errorf("flag %q ManifestPath %q is not a local path", f.Key, f.ManifestPath)
		}
	}
	if len(seen) < 5 {
		t.Errorf("expected at least 5 catalog flags, got %d", len(seen))
	}
}

// TestCatalogRuntimeKindIsValid asserts every flag's RuntimeKind is
// one of the four documented values. The registry dispatcher (PR 2)
// switches on this string; an unknown value is a developer bug.
func TestCatalogRuntimeKindIsValid(t *testing.T) {
	allowed := map[string]bool{
		"":           true, // plain toggle flags historically have no kind
		"none":       true,
		"inline":     true,
		"subprocess": true,
		"headless":   true,
	}
	for _, f := range Catalog {
		if !allowed[f.RuntimeKind] {
			t.Errorf("flag %q RuntimeKind %q is not in {none, inline, subprocess, headless}", f.Key, f.RuntimeKind)
		}
	}
}

// TestCatalogSubprocessHasProxyFields asserts that any flag declaring
// RuntimeKind="subprocess" also has a ProxyPrefix and LoopbackService.
// The proxy is how the renderer reaches a subprocess on the same
// origin; without these fields the experiment would only be reachable
// via direct loopback (or not at all in the renderer).
func TestCatalogSubprocessHasProxyFields(t *testing.T) {
	for _, f := range Catalog {
		if f.RuntimeKind != "subprocess" {
			continue
		}
		if f.ProxyPrefix == "" {
			t.Errorf("flag %q is subprocess but ProxyPrefix is empty", f.Key)
		}
		if f.LoopbackService == "" {
			t.Errorf("flag %q is subprocess but LoopbackService is empty", f.Key)
		}
		if f.ProxyPrefix != "" && f.ProxyPrefix[0] != '/' {
			t.Errorf("flag %q ProxyPrefix %q must start with /", f.Key, f.ProxyPrefix)
		}
	}
}

// TestLoadManifestResolvesAllFlags loads every flag's manifest from
// disk and asserts the cross-cutting schema passes. The loader is the
// only place we cross-check the manifest against the catalog key, so
// a divergence (flag key changed but manifest not updated, or vice
// versa) is caught here.
func TestLoadManifestResolvesAllFlags(t *testing.T) {
	// Point the loader at the dev resources tree for the duration of
	// the test. Production sets MULTICA_RESOURCES_DIR; tests use the
	// dev fallback by setting the manifest root explicitly.
	// Note: ManifestPath is "experiments/<flagKey>/manifest.json" —
	// root must be the resources tree, not the experiments subdir,
	// otherwise the path resolves to experiments/experiments/...
	prev := setManifestRootForTest(t, devResourcesRoot(t))
	t.Cleanup(prev)

	for _, f := range Catalog {
		if f.ManifestPath == "" {
			continue
		}
		m, err := LoadManifest(f.Key)
		if err != nil {
			t.Errorf("LoadManifest(%q) failed: %v", f.Key, err)
			continue
		}
		if m.Metadata.Flag != f.Key {
			t.Errorf("flag %q: manifest metadata.flag = %q (must match)", f.Key, m.Metadata.Flag)
		}
		if m.Metadata.Name == "" {
			t.Errorf("flag %q: manifest metadata.name is empty", f.Key)
		}
		if m.SourcePath == "" {
			t.Errorf("flag %q: manifest SourcePath is empty (loader did not record origin)", f.Key)
		}
	}
}

// TestLoadManifestErrNoManifest asserts the soft-error path: an empty
// ManifestPath returns ErrNoManifest so downstream callers can
// distinguish "no rich metadata" from "real load failure".
func TestLoadManifestErrNoManifest(t *testing.T) {
	m, err := LoadManifest("does_not_exist_in_catalog")
	if err == nil {
		t.Fatalf("expected error for unknown flag, got manifest=%+v", m)
	}
	if !errors.Is(err, ErrNoManifest) {
		t.Errorf("expected ErrNoManifest, got %v", err)
	}
}

// TestLoadManifestErrInvalidManifest asserts the hard-error path: a
// manifest whose metadata.flag does not match the catalog key is
// rejected with ErrInvalidManifest. The flag-key match is a hard
// invariant; a mismatched manifest is a developer bug.
func TestLoadManifestErrInvalidManifest(t *testing.T) {
	dir := t.TempDir()
	// Write a manifest with the wrong flag name.
	bad := `{
		"apiVersion": "multica.dev/experiment/v1",
		"kind": "Experiment",
		"metadata": {
			"name": "wrong_name",
			"flag": "this_is_not_a_real_flag",
			"title": {"en": "x", "zh": "y"},
			"description": {"en": "x", "zh": "y"},
			"default_enabled": false
		},
		"spec": {}
	}`
	// Stuff a malformed manifest under chat_pin_ui's path.
	flag := Catalog[0]
	if flag.Key != "chat_pin_ui" {
		t.Fatalf("first catalog entry changed: %q", flag.Key)
	}
	manifestDir := filepath.Join(dir, filepath.Dir(flag.ManifestPath))
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, flag.ManifestPath), []byte(bad), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	prev := setManifestRootForTest(t, dir)
	t.Cleanup(prev)

	_, err := LoadManifest(flag.Key)
	if err == nil {
		t.Fatalf("expected ErrInvalidManifest, got nil")
	}
	if !errors.Is(err, ErrInvalidManifest) {
		t.Errorf("expected ErrInvalidManifest, got %v", err)
	}
}

// devResourcesRoot walks up from the test's working directory to find
// apps/desktop/resources. Test runs from server/internal/experimental
// under `go test`, so cwd is the package dir; we walk up to the repo
// root. The fallback is informational only — TestLoadManifestResolvesAllFlags
// fails clearly if the path is wrong, no silent skip.
func devResourcesRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// Cwd is server/internal/experimental; repo root is ../../..
	repo := filepath.Clean(filepath.Join(cwd, "..", "..", ".."))
	root := filepath.Join(repo, "apps", "desktop", "resources")
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("dev resources root not found at %s: %v", root, err)
	}
	return root
}

// setManifestRootForTest overrides the loader's root and returns a
// cleanup function. Uses the same primitive as the production
// SetManifestRoot but takes testing.T so a test failure on the cleanup
// path is visible (we don't need it here, but the helper lives in
// the test file so future tests can use it).
func setManifestRootForTest(t *testing.T, path string) func() {
	t.Helper()
	rootMu.Lock()
	prev := rootSet
	rootSet = path
	rootMu.Unlock()
	return func() {
		rootMu.Lock()
		rootSet = prev
		rootMu.Unlock()
	}
}
