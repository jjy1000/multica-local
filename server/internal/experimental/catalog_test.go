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
