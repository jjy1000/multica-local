package experimental

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

// TestSwarmGCRunSweepsUntilStopped pins the 0.5.39 boot-wiring fix.
// Pre-fix, SwarmGC.Start took a caller context and router.go passed
// the boot-scoped bootCtx (30s timeout + defer cancel); Run exited on
// ctx.Done() ~8ms after router setup and swarm cleanup never ran —
// the same dormant-GC class as the 0.5.25 runtime_gc fix. The fix
// removed the context parameter entirely (Start/Run take no ctx,
// mirroring runtime_gc), so the loop cannot be bound to a boot-scoped
// context. This test asserts the sweep branch is actually reachable:
// with a 20ms interval, Run must tick ≥ 2 sweeps — it FAILS on code
// where the loop exits without sweeping (pre-fix: "got 0 sweeps").
func TestSwarmGCRunSweepsUntilStopped(t *testing.T) {
	g := NewSwarmGC(SwarmGCConfig{
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
	t.Fatalf("swarm_gc did not sweep: got %d sweeps in 2s (loop likely exited at start)",
		g.sweepCount.Load())
}
