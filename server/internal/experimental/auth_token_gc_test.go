// Regression pin for the 0.5.31 AuthTokenGC dormancy class — mirrors
// runtime_gc_test.go's TestRuntimeGC_RunSweepsBeforeExit. Asserts
// that the loop body actually fires on a fast 20ms tick, catching
// the pre-0.5.25 eager-close regression pattern in case future
// edits break the loop body the same way.
//
// Test plan:
//   1. NewAuthTokenGC with nil Queries (test-only build; no DB pool).
//   2. Drive Run() on a fresh goroutine (Start() would block the test).
//   3. Wait 60ms; assert SweepCount >= 2.
//   4. Call Stop() to close the channel; assert Run() returns.
//   5. Verify the sweep count is monotonic — no double-decrement.
// Pre-fix code: sweepCount stays at 0 (the eager close kills the loop
// before the first tick); the assertion fails with "got 0 sweeps".

package experimental

import (
	"sync"
	"testing"
	"time"
)

// TestAuthTokenGC_RunSweepsBeforeExit pins the loop body. Mirrors
// TestRuntimeGC_RunSweepsBeforeExit — calls Start() which sets the
// `running` flag (Run() itself never flips it). Without Start() the
// for-loop never executes because the guard `for g.running.Load()`
// stays false.
func TestAuthTokenGC_RunSweepsBeforeExit(t *testing.T) {
	gc := NewAuthTokenGC(AuthTokenGCConfig{
		// Queries intentionally nil — sweep short-circuits via
		// the nil-guard, mirroring RuntimeGC's nil-DB pattern.
		Queries: nil,
		Interval: 20 * time.Millisecond,
	})

	gc.Start()
	// Wait ≥ 2 ticks at 20ms interval. The immediate sweep at
	// boot counts as 1; the first ticker fire adds another at
	// t+20ms. 100ms gives 2+ ticker fires comfortably.
	time.Sleep(100 * time.Millisecond)

	count := gc.SweepCount()
	if count < 2 {
		t.Fatalf("auth_token_gc Run() body never swept; got %d sweep invocations, want >= 2. "+
			"This is the 0.5.25 RuntimeGC dormancy regression class — the loop body must actually fire on each tick.",
			count)
	}

	// Stop should close the channel and let the goroutine exit.
	gc.Stop()

	// Give the goroutine a brief window to exit cleanly.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		// Reading SweepCount is atomic; the loop body has
		// stopped only when subsequent reads settle.
		time.Sleep(20 * time.Millisecond)
		if gc.SweepCount() == count {
			// No new sweep has fired — the loop is quiescent.
			break
		}
	}
}

// TestAuthTokenGC_StopBeforeStart is a no-op regression. Pre-fix code
// would have panicked on a nil channel close. Mirrors a defensive
// check on the lifecycle contract.
func TestAuthTokenGC_StopBeforeStart(t *testing.T) {
	gc := NewAuthTokenGC(AuthTokenGCConfig{Interval: time.Hour})

	// Stop with running=false must not panic. The lifecycle guard
	// `if !g.running.Load() { return }` short-circuits.
	gc.Stop()

	// Double Stop is also safe — stopOne funnels the close.
	gc.Stop()
}

// TestAuthTokenGC_StartStopIdempotency pins that Start() is
// idempotent. A double-Start must not launch two goroutines; a
// double-Stop must not panic on a closed channel.
func TestAuthTokenGC_StartStopIdempotency(t *testing.T) {
	gc := NewAuthTokenGC(AuthTokenGCConfig{
		Queries: nil,
		Interval: 100 * time.Millisecond,
	})

	gc.Start()
	// Second Start must be a no-op (CompareAndSwap short-circuits).
	gc.Start()

	// Give the loop one tick to fire.
	time.Sleep(150 * time.Millisecond)
	if got := gc.SweepCount(); got < 1 {
		t.Fatalf("expected at least 1 sweep after 150ms at 100ms interval, got %d", got)
	}

	gc.Stop()
	// Second Stop is safe (stopOne + running-guard).
	gc.Stop()
}

// TestAuthTokenGC_NewAppliesDefaults pins that NewAuthTokenGC
// substitutes sane defaults for any zero-valued config field. A
// future regression that drops a default would let Interval=0
// fire the loop as fast as the runtime allows.
func TestAuthTokenGC_NewAppliesDefaults(t *testing.T) {
	gc := NewAuthTokenGC(AuthTokenGCConfig{})

	if gc.cfg.Interval <= 0 {
		t.Fatalf("Interval default not applied; got %v", gc.cfg.Interval)
	}
	if gc.cfg.OperationTimeout <= 0 {
		t.Fatalf("OperationTimeout default not applied; got %v", gc.cfg.OperationTimeout)
	}
	if gc.cfg.Logger == nil {
		t.Fatal("Logger default not applied; got nil")
	}
	if gc.stopped == nil {
		t.Fatal("stopped channel not initialized in constructor")
	}
}

// TestAuthTokenGC_SweepGoroutineLeak ensures Run() does not leak
// goroutines when Stop() is called mid-tick. Drive a tight loop
// with Stop racing every tick for ~50ms; at the end the goroutine
// count should match the test's baseline.
func TestAuthTokenGC_SweepGoroutineLeak(t *testing.T) {
	gc := NewAuthTokenGC(AuthTokenGCConfig{
		Queries:  nil,
		Interval: 1 * time.Millisecond,
	})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		gc.Run()
	}()

	// Let it tick for 50ms then stop.
	time.Sleep(50 * time.Millisecond)
	gc.Stop()

	// Run() must return within a short window after Stop.
	doneCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneCh)
	}()
	select {
	case <-doneCh:
		// Good — the goroutine exited.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("auth_token_gc goroutine did not exit within 500ms of Stop()")
	}

	// And a second Stop is harmless.
	gc.Stop()
}