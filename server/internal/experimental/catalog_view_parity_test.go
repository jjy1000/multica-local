// Package experimental — catalog_view_parity_test.go (0.5.105, audit H2).
//
// The lab view maps live in four places that cannot import each other:
//
//	server/internal/experimental/catalog.go   (this catalog — source of truth)
//	packages/views/issues/components/issue-labs-section.tsx  (FLAG_ROUTE_SUFFIX)
//	apps/web/app/[workspaceSlug]/(dashboard)/experimental/page.tsx (web LABS cards)
//	apps/desktop/src/renderer/src/routes.tsx  (desktop lab routes)
//
// Go tests cannot import TS, so the two sides pin the key set from
// opposite ends: this test fails when a flag is added to or removed
// from Catalog, and the TS-side length pin in
// issue-labs-section.test.tsx fails when FLAG_ROUTE_SUFFIX changes.
// Either failure must be resolved by reconciling ALL FOUR lists by
// hand. This converts the historical silent-failure mode (0.5.81
// semantica, 0.5.82 timesfm, 0.5.83 causal_graph each shipped with a
// missing view row) into a loud test failure.
package experimental

import (
	"slices"
	"testing"
)

func TestCatalogFlagViewParity(t *testing.T) {
	want := []string{
		// VERBATIM key list — keep in sync with FLAG_ROUTE_SUFFIX
		// (packages/views/issues/components/issue-labs-section.tsx),
		// web LABS, and desktop routes.tsx when this changes.
		"causal_graph",
		"chat_pin_ui",
		"claude_science_lab",
		"code_canvas",
		"llm_wiki_bridge",
		"mythos_swarm",
		"pythia_oracle",
		"semantica",
		"swarm_topology", // Frozen tombstone only (0.5.105, audit H3)
		"timesfm",
	}

	got := make([]string, 0, len(Catalog))
	for _, f := range Catalog {
		got = append(got, f.Key)
	}
	slices.Sort(got)
	slices.Sort(want)

	if !slices.Equal(got, want) {
		added, removed := diffStrings(want, got)
		t.Fatalf(`catalog flag keys drifted from the pinned view-parity list.
  added to catalog:   %v
  removed from catalog: %v
Reconcile ALL FOUR lab view lists in the same commit:
  1. server/internal/experimental/catalog.go (this change)
  2. packages/views/issues/components/issue-labs-section.tsx (FLAG_ROUTE_SUFFIX + its TS length pin)
  3. apps/web/app/[workspaceSlug]/(dashboard)/experimental/page.tsx (web LABS cards)
  4. apps/desktop/src/renderer/src/routes.tsx (+ manager-factory static descriptors)`,
			added, removed)
	}
}

func diffStrings(expected, got []string) (added, removed []string) {
	for _, g := range got {
		if !slices.Contains(expected, g) {
			added = append(added, g)
		}
	}
	for _, w := range expected {
		if !slices.Contains(got, w) {
			removed = append(removed, w)
		}
	}
	return added, removed
}
