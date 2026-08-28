package experimental

import (
	"testing"
)

// TestCatalogAutoDispatchContract (0.5.81 plan §P3+§P4) pins the real
// catalog's AutoDispatch literal values for the two labs whose defaults
// were flipped in this release. The pins live in catalog_test.go (not
// registry_test.go::TestAutoDispatchFlagBehavior, which exercises the
// AutoDispatch HELPER on a synthetic slice) so a future catalog edit
// that accidentally re-enables pythia_oracle auto-launch or re-opts
// claude_science_lab out of the standard 0.3.46 dispatch path trips
// the regression here at build time.
//
// - pythia_oracle → AutoDispatch must be *false (opt-out): the per-issue
//   10-round SSE forecast loop is too expensive to auto-fire on every
//   lab_source flip. The user triggers forecasts explicitly via the
//   per-issue Pythia panel's "Run forecast" button.
//
// - claude_science_lab → AutoDispatch must be nil-or-*true (default
//   trigger): the standard 0.3.46 contract applies (assignee rewrite +
//   enqueue). The "Run research" button stays as a manual re-trigger
//   for retries.
//
// NOT t.Parallel(): the catalog slice is a package-global and this test
// reads it directly. Other parallel tests in this package do the same;
// serial execution is intentional.
func TestCatalogAutoDispatchContract(t *testing.T) {
	cases := []struct {
		key        string
		wantFalse  bool   // AutoDispatch must be *false
		wantTrue   bool   // AutoDispatch must be nil or *true
		wantReason string // surfaced in the failure message
	}{
		{
			key:        "pythia_oracle",
			wantFalse:  true,
			wantReason: "0.5.81 P3: pythia_oracle must opt out of auto-dispatch (10 SSE rounds on every create is too expensive)",
		},
		{
			key:        "claude_science_lab",
			wantTrue:   true,
			wantReason: "0.5.81 P4: claude_science_lab must follow the default 0.3.46 auto-dispatch contract (assignee rewrite + enqueue)",
		},
		{
			// 0.5.82 WL2: timesfm pins BOTH the AutoDispatch *false and
			// (below) the verbatim flag-key literal "timesfm" per the
			// flag-key string duplication law — the same literal is
			// hand-copied into install_timesfm.go, router.go, the
			// manifest, run_loopback wiring, and migration 275's CHECK.
			key:        "timesfm",
			wantFalse:  true,
			wantReason: "0.5.82 WL2: timesfm CPU inference takes seconds-minutes per call; runs stay manual/retry-driven, never auto-fired on a lab_source flip",
		},
		{
			// 0.5.83 WL3: causal_graph is opt-in the same way — the Tier
			// A/B recorders cost writes on every enqueue/complete once
			// enabled, so they must never wake from a lab_source flip.
			// The verbatim "causal_graph" literal is hand-copied into
			// router.go's gate, lock.go's SourceCausalGraph, migration
			// 279's CHECK, the manifest, and the desktop manager
			// descriptor.
			key:        "causal_graph",
			wantFalse:  true,
			wantReason: "0.5.83 WL3: causal_graph Tier A/B recorders add writes to the task enqueue/complete paths; they are flag-gated and must never auto-fire",
		},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			f, ok := findCatalogEntry(c.key)
			if !ok {
				t.Fatalf("catalog entry %q not found", c.key)
			}
			if c.wantFalse {
				if f.AutoDispatch == nil || *f.AutoDispatch {
					t.Fatalf("AutoDispatch(%q) = %v, want *false — %s", c.key, f.AutoDispatch, c.wantReason)
				}
			}
			if c.wantTrue {
				if f.AutoDispatch != nil && !*f.AutoDispatch {
					t.Fatalf("AutoDispatch(%q) = *false, want nil or *true — %s", c.key, c.wantReason)
				}
			}
		})
	}
}

// findCatalogEntry returns the Flag literal for key from Catalog. The
// test uses it to surface a clear "entry missing" failure distinct
// from an AutoDispatch value failure.
func findCatalogEntry(key string) (Flag, bool) {
	for _, f := range Catalog {
		if f.Key == key {
			return f, true
		}
	}
	return Flag{}, false
}

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

// TestCatalogInteractionModelContract (0.5.86) pins the 独立工作型 vs
// 辅助协作型 classification literals. The same strings are consumed by
// handler/issue.go's create/update assignee-lock gates (via
// IsAssigneeModelLab) and mirrored to the client through
// ExperimentalFlagResponse.interaction_model →
// packages/core/types/experimental.ts — an accidental reclassification
// here would silently re-allow (or hard-block) manual assignees on
// every bound issue.
//
// Pins:
//   - assignee (独立工作型): the lab's leader owns the assignee slot.
//   - auxiliary (辅助协作型): trace/visualize-only, never an assignee.
//   - swarm_topology keeps InteractionModelAssignee for legacy bound
//     issues even though HideFromIssueLabPicker freezes NEW bindings
//     (0.5.86 swarm consolidation — mythos_swarm is the single 蜂群 lab).
//   - chat_pin_ui / code_canvas / user plugins stay unclassified
//     (legacy coexist behavior — no lock, no auxiliary semantics).
//
// NOT t.Parallel(): reads the package-global Catalog directly, matching
// the other catalog tests in this file.
func TestCatalogInteractionModelContract(t *testing.T) {
	byKey := make(map[string]string, len(Catalog))
	for _, f := range Catalog {
		if prev, dup := byKey[f.Key]; dup {
			t.Fatalf("duplicate catalog key %q (prev interaction_model=%q)", f.Key, prev)
		}
		byKey[f.Key] = f.InteractionModel
		switch f.InteractionModel {
		case "", InteractionModelAssignee, InteractionModelAuxiliary:
		default:
			t.Errorf("Catalog[%q].InteractionModel = %q, want \"\", %q or %q",
				f.Key, f.InteractionModel, InteractionModelAssignee, InteractionModelAuxiliary)
		}
	}

	assigneeWant := []string{
		"claude_science_lab", "pythia_oracle", "mythos_swarm",
		"swarm_topology", "semantica", "timesfm",
	}
	for _, key := range assigneeWant {
		if got := byKey[key]; got != InteractionModelAssignee {
			t.Errorf("Catalog[%q].InteractionModel = %q, want %q (独立工作型)",
				key, got, InteractionModelAssignee)
		}
		if !IsAssigneeModelLab(key) {
			t.Errorf("IsAssigneeModelLab(%q) = false, want true", key)
		}
	}

	auxiliaryWant := []string{"causal_graph", "llm_wiki_bridge"}
	for _, key := range auxiliaryWant {
		if got := byKey[key]; got != InteractionModelAuxiliary {
			t.Errorf("Catalog[%q].InteractionModel = %q, want %q (辅助协作型)",
				key, got, InteractionModelAuxiliary)
		}
		if !IsAuxiliaryModelLab(key) {
			t.Errorf("IsAuxiliaryModelLab(%q) = false, want true", key)
		}
		if IsAssigneeModelLab(key) {
			t.Errorf("IsAssigneeModelLab(%q) = true, want false — auxiliary labs must never lock an assignee", key)
		}
	}

	legacyWant := []string{"chat_pin_ui", "code_canvas"}
	for _, key := range legacyWant {
		if got := byKey[key]; got != "" {
			t.Errorf("Catalog[%q].InteractionModel = %q, want \"\" (legacy coexist — unclassified labs keep manual assignees)", key, got)
		}
	}

	// 0.5.86 swarm consolidation: frozen for NEW bindings while the
	// legacy mutex keeps protecting existing ones.
	if !byKeyExists(byKey, "swarm_topology") {
		t.Fatal("swarm_topology literal removed from catalog — forward-only law violation")
	}

	// Unknown keys and the dynamic user-plugin layer fall through to
	// the empty (legacy) model.
	if got := InteractionModelOf("no_such_flag"); got != "" {
		t.Errorf("InteractionModelOf(unknown) = %q, want \"\"", got)
	}
}

func byKeyExists(byKey map[string]string, key string) bool {
	_, ok := byKey[key]
	return ok
}
