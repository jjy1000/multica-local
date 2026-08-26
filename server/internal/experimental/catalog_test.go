package experimental

import (
	"testing"
)

// TestAllFlagKeysReturnsEveryCatalogEntry pins the contract that AllFlagKeys
// iterates every Catalog entry exactly once. 0.3.26 callers (notably
// ListAgents' visibility loop) rely on a fresh slice so they can mutate
// without disturbing the catalog.
func TestAllFlagKeysReturnsEveryCatalogEntry(t *testing.T) {
	keys := AllFlagKeys()

	if len(keys) != len(Catalog) {
		t.Fatalf("AllFlagKeys() returned %d entries, want %d (Catalog size)",
			len(keys), len(Catalog))
	}

	seen := make(map[string]bool, len(keys))
	for _, k := range keys {
		if k == "" {
			t.Fatalf("AllFlagKeys contains an empty key")
		}
		if seen[k] {
			t.Fatalf("AllFlagKeys contains duplicate key %q", k)
		}
		seen[k] = true
		if !IsKnownKey(k) {
			t.Fatalf("AllFlagKeys returned %q which IsKnownKey rejects", k)
		}
	}

	// Every key is unique and matches IsKnownKey — the contract round-trips.
	for _, f := range Catalog {
		if !seen[f.Key] {
			t.Errorf("Catalog contains %q but AllFlagKeys omitted it", f.Key)
		}
	}
}

// TestAllFlagKeysReturnsFreshSlice pins that the returned slice can be
// safely mutated without affecting subsequent calls.
func TestAllFlagKeysReturnsFreshSlice(t *testing.T) {
	first := AllFlagKeys()
	firstCopy := append([]string(nil), first...)

	// mutate
	if len(first) > 0 {
		first[0] = "tampered"
	}
	// re-fetch
	second := AllFlagKeys()
	if len(second) != len(firstCopy) {
		t.Fatalf("AllFlagKeys length changed after mutation: %d -> %d",
			len(first), len(second))
	}
	for i := range second {
		if second[i] != firstCopy[i] {
			t.Fatalf("AllFlagKeys()[%d] = %q after tampering, want %q",
				i, second[i], firstCopy[i])
		}
	}
}

// TestSemanticaCatalogNoInlineSidebar pins the 0.5.72 lab-integration
// PR-6 refactor: semantica's sidebar entry moved from the catalog's
// inline SidebarRow literal into the manifest's spec.entry_points.sidebar
// (single source of truth). The catalog literal must be empty so any
// future change goes through the manifest, not a Go code edit.
func TestSemanticaCatalogNoInlineSidebar(t *testing.T) {
	for _, f := range Catalog {
		if f.Key != "semantica" {
			continue
		}
		if len(f.Sidebar) != 0 {
			t.Fatalf("semantica catalog entry still carries %d inline SidebarRow(s); want 0 (sidebar is manifest-owned): %+v",
				len(f.Sidebar), f.Sidebar)
		}
		return
	}
	t.Fatal("semantica catalog entry not found")
}

// TestSemanticaSidebarFromManifest pins the wire-shape contract:
// Registry.SidebarEntries("semantica") must return exactly the row
// the manifest's spec.entry_points.sidebar declares, with the same
// Key/LabelKey/Route the previous inline SidebarRow carried. This is
// a byte-equal regression pin for the PR-6 inline-to-manifest
// migration — if the manifest key/label_key/route drift, the GET
// /api/experimental-flags response shifts and the renderer's nav hook
// breaks.
//
// NOT t.Parallel(): setManifestRootForTest mutates the loader's root,
// and TestLoadManifestResolvesAllFlags in manifest_test.go writes to
// the same package-global. Pattern matches the other manifest-loading
// tests in this package.
func TestSemanticaSidebarFromManifest(t *testing.T) {
	const flagKey = "semantica"

	prev := setManifestRootForTest(t, devResourcesRoot(t))
	t.Cleanup(prev)

	r := NewRegistry()
	got := r.SidebarEntries(flagKey)
	if len(got) != 1 {
		t.Fatalf("SidebarEntries(%q) returned %d entries, want 1: %+v", flagKey, len(got), got)
	}
	e := got[0]
	if e.Key != "experimental_semantica" {
		t.Errorf("SidebarEntries(%q).Key = %q, want %q", flagKey, e.Key, "experimental_semantica")
	}
	if e.FlagKey != flagKey {
		t.Errorf("SidebarEntries(%q).FlagKey = %q, want %q", flagKey, e.FlagKey, flagKey)
	}
	if e.LabelKey != "experimental_semantica" {
		t.Errorf("SidebarEntries(%q).LabelKey = %q, want %q", flagKey, e.LabelKey, "experimental_semantica")
	}
	if e.Route != "/experimental/semantica-explorer" {
		t.Errorf("SidebarEntries(%q).Route = %q, want %q", flagKey, e.Route, "/experimental/semantica-explorer")
	}
}
