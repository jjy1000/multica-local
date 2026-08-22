// Package experimental — semantica_acl_reconciler_test.go (0.5.58 P6)
//
// Regression test for the ACL reconciler loop. Asserts the
// tick-once-at-boot + interval-tick contract by calling sweep()
// via a fast-tick loop and checking SweepCount moves.
//
// Mirrors TestRuntimeGC_RunSweepsBeforeExit (0.5.25 RuntimeGC fix)
// and TestSemanticaGC_RunSweepsBeforeExit (0.5.30 P1-3). The
// pre-fix bug for both was an eager close-of-stopped or a Start()
// never wired — both produced sweepCount=0; the post-fix code
// produces sweepCount>=2 within a few ticks.
package experimental

import (
	"log/slog"
	"testing"
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestACLReconciler_RunSweepsBeforeExit(t *testing.T) {
	r := NewACLReconciler(ACLReconcilerConfig{
		Interval: 20 * time.Millisecond,
		Logger:   slog.Default(),
		Queries:  &db.Queries{}, // nil-quiescent path; no DB I/O attempted
	})
	r.Start()
	defer r.Stop()
	// Wait long enough for at least 3 ticks: boot sweep + 2 interval sweeps.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r.SweepCount() >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := r.SweepCount(); got < 3 {
		t.Errorf("expected sweepCount >= 3 after 2s, got %d (loop body unreachable?)", got)
	}
}

func TestACLReconciler_StopIsIdempotent(t *testing.T) {
	r := NewACLReconciler(ACLReconcilerConfig{
		Interval: time.Hour, // long enough that no auto-tick fires during the test
		Logger:   slog.Default(),
	})
	r.Start()
	r.Stop()
	r.Stop() // second call must not panic (close-of-closed-channel)
}

func TestACLReconciler_StartIsIdempotent(t *testing.T) {
	r := NewACLReconciler(ACLReconcilerConfig{
		Interval: time.Hour,
		Logger:   slog.Default(),
	})
	r.Start()
	r.Start() // second call must not leak a second goroutine
	r.Stop()
}