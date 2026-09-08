package experimental

import (
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

// TestResourceGCRunSweepsUntilStopped pins the 0.5.39 boot-wiring fix,
// inherited from swarm_gc when the swarm_topology runtime was retired
// (0.5.105). Pre-fix, GC.Start took a caller context and router.go
// passed the boot-scoped bootCtx (30s timeout + defer cancel); Run
// exited on ctx.Done() ~8ms after router setup and cleanup never ran.
// Start/Run take no ctx, so the loop cannot be bound to a boot-scoped
// context. With a 20ms interval, Run must tick ≥ 2 sweeps — it FAILS
// on code where the loop exits without sweeping.
func TestResourceGCRunSweepsUntilStopped(t *testing.T) {
	g := NewResourceGC(ResourceGCConfig{
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Interval: 20 * time.Millisecond,
	})
	g.Start()
	defer g.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if g.sweepCount.Load() >= 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("resource_gc did not sweep: got %d sweeps in 2s (loop likely exited at start)",
		g.sweepCount.Load())
}

func TestResourceGCSweepNilQueriesIsNoOp(t *testing.T) {
	g := NewResourceGC(ResourceGCConfig{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	// nil Queries → no sweeper → no panic, no count.
	g.sweep()
	if g.sweepCount.Load() != 0 {
		t.Fatalf("nil-Queries sweep counted %d, want 0", g.sweepCount.Load())
	}
}

func TestResourceGCSweepCountsAndSurvivesError(t *testing.T) {
	g := NewResourceGC(ResourceGCConfig{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	failing := &fakeOrphanSweeper{lockErr: errors.New("boom")}
	g.sweepOrphanedExperimentalResources(failing)
	if g.sweepCount.Load() != 1 {
		t.Fatalf("failed sweep not counted: got %d, want 1", g.sweepCount.Load())
	}
	if failing.lockCalls != 1 {
		t.Fatalf("sweeper calls = %d, want 1", failing.lockCalls)
	}

	ok := &fakeOrphanSweeper{locks: 3, vis: 2}
	g.sweepOrphanedExperimentalResources(ok)
	if g.sweepCount.Load() != 2 {
		t.Fatalf("second sweep not counted: got %d, want 2", g.sweepCount.Load())
	}
}
