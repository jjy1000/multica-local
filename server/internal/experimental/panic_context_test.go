package experimental

import (
	"errors"
	"testing"
)

func TestSetAndPopPanicFlagContext(t *testing.T) {
	// Ensure a clean slot before and after — parallel-safe via
	// swap-and-clear semantics.
	defer PopPanicFlagContext()

	SetPanicFlagContext("mythos_swarm", "mythos.Run")
	key, ctx, ok := PopPanicFlagContext()
	if !ok {
		t.Fatal("PopPanicFlagContext returned !ok after SetPanicFlagContext")
	}
	if key != "mythos_swarm" || ctx != "mythos.Run" {
		t.Errorf("got (%q, %q), want (mythos_swarm, mythos.Run)", key, ctx)
	}
	// Pop again — should return ok=false (slot was cleared).
	_, _, ok = PopPanicFlagContext()
	if ok {
		t.Error("PopPanicFlagContext should return !ok on second call")
	}
}

func TestWithPanicFlagContextRetainsOnReturn(t *testing.T) {
	defer PopPanicFlagContext()

	WithPanicFlagContext("claude_science_lab", "install", func() {
		// Intentionally do NOT PopPanicFlagContext here — Pop is
		// destructive and would consume the slot we're testing.
		// The previous test popped + asserted inside fn, then popped
		// again outside and asserted the second Pop returned !ok.
		// Under the H2 contract the slot is retained on return, so
		// a single Pop after the closure must see the same value.
	})
	// H2 (audit 2026-09-06): the slot is intentionally NOT cleared on
	// return. The slot is overwritten by the next SetPanicFlagContext
	// call regardless, so a stale slot on a success-path return is
	// harmless. The previous test asserted a clear-on-return; that
	// contract was the LIFO-defer bug the audit fixed.
	key, ctx, ok := PopPanicFlagContext()
	if !ok || key != "claude_science_lab" || ctx != "install" {
		t.Errorf("slot must be retained on return so the outer recover() sentinel can attribute it; got (%q, %q, ok=%v), want claude_science_lab/install/true", key, ctx, ok)
	}
}

func TestWithPanicFlagContextRetainsOnPanic(t *testing.T) {
	defer PopPanicFlagContext()
	defer func() {
		_ = recover() // swallow the deliberate panic
		// H2 (audit 2026-09-06): the slot MUST be retained past a
		// panic so the outer recover() sentinel in cmd/server/main.go
		// (which runs AFTER the package-level defer unwinds) can read
		// it for blacklist attribution. The previous test pinned the
		// LIFO-defer bug — clearing the slot in a defer would run
		// BEFORE the outer recover() and silently empty the
		// attribution slot.
		key, ctx, ok := PopPanicFlagContext()
		if !ok || key != "pythia_oracle" || ctx != "brief" {
			t.Errorf("slot must be retained past panic for sentinel attribution; got (%q, %q, ok=%v), want pythia_oracle/brief/true", key, ctx, ok)
		}
	}()

	WithPanicFlagContext("pythia_oracle", "brief", func() {
		panic("simulated pythia panic")
	})
}

func TestPopEmptyReturnsFalse(t *testing.T) {
	defer PopPanicFlagContext()
	// Already-cleared slot returns false without panicking.
	_, _, ok := PopPanicFlagContext()
	if ok {
		t.Error("PopPanicFlagContext on empty slot returned ok=true")
	}
}

func TestPanicContextSurvivesErrorReturn(t *testing.T) {
	defer PopPanicFlagContext()
	// 0.5.6: the test no longer uses the `agent_self_optimization`
	// string literal — that flag is removed from the catalog and
	// the magic string is no longer meaningful here. The pop/push
	// contract is the same regardless of the payload string.
	SetPanicFlagContext("product_level_placeholder", "scheduler skip")
	// Return an error via errors.New (non-panic). Slot must persist.
	if err := errors.New("non-panic error"); err == nil {
		t.Fatal("test bug")
	}
	key, ctx, ok := PopPanicFlagContext()
	if !ok || key != "product_level_placeholder" || ctx != "scheduler skip" {
		t.Errorf("got (%q, %q, ok=%v), want product_level_placeholder/scheduler skip/true", key, ctx, ok)
	}
}
