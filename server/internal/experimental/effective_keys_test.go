package experimental

import (
	"sort"
	"testing"
)

// TestEffectiveEnabledKeys_PickEnabledSemantics pins the merge contract
// behind the delegation briefing: a pref row overrides DefaultVal, an
// ABSENT row falls back to it. The absence branch is the one that
// matters — the 0.5.126 live verification caught the delegation brief
// rendering empty on every default-on install because its source query
// (ListEnabledFlagKeys) projects only pref rows, and a DefaultVal=true
// flag the user never toggled (claude_science_lab, pythia_oracle since
// 0.5.114) has no pref row yet is fully usable.
//
// The pins derive their fixture keys from the live Catalog (a DefaultVal
// true entry and a DefaultVal false entry) so catalog drift cannot
// silently invalidate the test — the same derivation law
// TestBatchUpdateIssuesLabAssigneeLockParity follows.
func TestEffectiveEnabledKeys_PickEnabledSemantics(t *testing.T) {
	var defaultOn, defaultOff string
	for i := range Catalog {
		if Catalog[i].Frozen {
			continue
		}
		if defaultOn == "" && Catalog[i].DefaultVal {
			defaultOn = Catalog[i].Key
		}
		if defaultOff == "" && !Catalog[i].DefaultVal {
			defaultOff = Catalog[i].Key
		}
	}
	if defaultOn == "" || defaultOff == "" {
		t.Fatalf("catalog must contain at least one DefaultVal=true and one DefaultVal=false flag (got on=%q off=%q)", defaultOn, defaultOff)
	}

	t.Run("absent row falls back to DefaultVal", func(t *testing.T) {
		got := EffectiveEnabledKeys(map[string]bool{})
		has := func(key string) bool {
			for _, k := range got {
				if k == key {
					return true
				}
			}
			return false
		}
		if !has(defaultOn) {
			t.Errorf("default-on flag %q with no pref row must be effectively enabled, got %v", defaultOn, got)
		}
		if has(defaultOff) {
			t.Errorf("default-off flag %q with no pref row must not be enabled, got %v", defaultOff, got)
		}
	})

	t.Run("present row overrides DefaultVal", func(t *testing.T) {
		got := EffectiveEnabledKeys(map[string]bool{
			defaultOn:  false, // user explicitly turned the default-on flag off
			defaultOff: true,  // user explicitly turned the default-off flag on
		})
		has := func(key string) bool {
			for _, k := range got {
				if k == key {
					return true
				}
			}
			return false
		}
		if has(defaultOn) {
			t.Errorf("explicit pref enabled=false must override DefaultVal=true for %q", defaultOn)
		}
		if !has(defaultOff) {
			t.Errorf("explicit pref enabled=true must override DefaultVal=false for %q", defaultOff)
		}
	})

	t.Run("unknown pref keys are ignored and output is sorted", func(t *testing.T) {
		got := EffectiveEnabledKeys(map[string]bool{"bogus_key": true})
		for _, k := range got {
			if k == "bogus_key" {
				t.Errorf("unknown pref key leaked into the effective set: %v", got)
			}
		}
		if !sort.StringsAreSorted(got) {
			t.Errorf("output must be sorted, got %v", got)
		}
	})
}
