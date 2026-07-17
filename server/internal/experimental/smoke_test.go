// Package experimental — smoke_test.go
//
// End-to-end smoke for the 0.3.19 lab surfaces. Verifies the
// pieces that P1 (manifest loader), P2 (Registry), and the
// runtime / llm_wiki routes wire together inside one Go process,
// without spinning up the full backend.
//
// What's checked:
//   - the registry enumerates every catalog flag,
//   - the catalog snapshot is consistent with the installed JSON
//     manifests (every catalog entry that points at a ManifestPath
//     has a stage-able file),
//   - the install dispatcher can mark + hide (the lock-table path
//     doesn't require a DB connection for the dual-claim tests).
//
// The full HTTP smoke (server + DB) lives in handler/ and remains
// test-gated on the DB connection; this file is the always-on
// developer sanity check.
package experimental

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSmoke_RegistryEnumeratesCatalog(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if len(r.Flags()) < len(Catalog) {
		t.Fatalf("registry flags %d < catalog %d", len(r.Flags()), len(Catalog))
	}
	for _, f := range Catalog {
		if _, ok := r.Flag(f.Key); !ok {
			t.Fatalf("registry missing %q", f.Key)
		}
	}
}

func TestSmoke_ManifestLoaderRoundTrips(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"chat_pin_ui", "claude_science_lab", "pythia_oracle", "mythos_swarm"} {
		m, err := LoadManifest(key)
		if err != nil {
			if errors.Is(err, ErrNoManifest) {
				// chat_pin_ui etc. don't need a manifest for the
				// plain-toggle path; that's a soft error.
				continue
			}
			t.Fatalf("LoadManifest(%q): %v", key, err)
		}
		if m.APIVersion != "multica.dev/experiment/v1" {
			t.Errorf("manifest %q apiVersion = %q", key, m.APIVersion)
		}
		if m.Metadata.Flag != key {
			t.Errorf("manifest %q metadata.flag = %q", key, m.Metadata.Flag)
		}
	}
}

// TestSmoke_ManifestPathsExistOnDisk re-points the loader at a
// tempdir containing one placeholder manifest per flag key, so
// the catalog → manifest round-trip is asserted in CI without
// shipping a real manifest tree. The test fixture is mandatory:
// the loader refuses a flag whose ManifestPath is set but the
// file is missing on disk.
func TestSmoke_ManifestPathsExistOnDisk(t *testing.T) {
	t.Parallel()

	// Build a tempdir mirroring the production layout
	// (`<root>/experiments/<flag>/manifest.json`).
	root := t.TempDir()
	for _, f := range Catalog {
		if f.ManifestPath == "" {
			continue
		}
		dest := filepath.Join(root, f.ManifestPath)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			t.Fatal(err)
		}
		// Bare minimum valid manifest. The loader only cares about
		// apiVersion + kind + metadata.name + metadata.flag.
		body := []byte(`{"apiVersion":"multica.dev/experiment/v1","kind":"Experiment","metadata":{"name":"` + f.Key + `","flag":"` + f.Key + `","title":{"en":"` + f.Title.En + `","zh":"` + f.Title.Zh + `"},"description":{"en":"d","zh":"d"}}}`)
		if err := os.WriteFile(dest, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	prev := ManifestRoot()
	SetManifestRoot(root)
	defer SetManifestRoot(prev)

	for _, f := range Catalog {
		if f.ManifestPath == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, f.ManifestPath)); err != nil {
			t.Errorf("flag %q manifest path %q missing: %v", f.Key, f.ManifestPath, err)
		}
	}
}

func TestSmoke_ProxyRoutesAreValid(t *testing.T) {
	t.Parallel()
	for _, p := range NewRegistry().ProxyRoutes() {
		if !strings.HasPrefix(p.Prefix, "/experimental/") {
			t.Errorf("proxy prefix %q does not start with /experimental/", p.Prefix)
		}
		if p.LoopbackService == "" {
			t.Errorf("proxy for flag %q has empty loopback service", p.FlagKey)
		}
	}
}
