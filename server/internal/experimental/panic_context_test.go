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

func TestWithPanicFlagContextClearsOnReturn(t *testing.T) {
	defer PopPanicFlagContext()

	func() {
		WithPanicFlagContext("claude_science_lab", "install", func() {
			key, _, ok := PopPanicFlagContext()
			if !ok || key != "claude_science_lab" {
				t.Fatalf("inside WithPanicFlagContext: got (%q, ok=%v), want claude_science_lab/true", key, ok)
			}
		})
	}()
	// After the closure returns, the slot must be cleared so a
	// later panic on a different code path is NOT attributed to
	// claude_science.
	_, _, ok := PopPanicFlagContext()
	if ok {
		t.Error("WithPanicFlagContext should clear the slot on return")
	}
}

func TestWithPanicFlagContextClearsOnPanic(t *testing.T) {
	defer PopPanicFlagContext()
	defer func() {
		_ = recover() // swallow the deliberate panic
		if _, _, ok := PopPanicFlagContext(); ok {
			t.Error("slot must be cleared even when fn panics")
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
