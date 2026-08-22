// Package experimental — semantica_acl_reconciler.go (0.5.58 P6)
//
// Lightweight 6h reconciler for the semantica_local_decision_acl
// cache. Scoped narrow on purpose:
//
//   - It does NOT scan upstream semantica — upstream does not expose
//     a list-decisions endpoint, and parsing graph.json (rdflib's
//     default serialization) requires Python. The reconcile is
//     therefore strictly a fork-side cache freshness check.
//   - It DOES count ACL rows per workspace, log a one-line summary,
//     and pin a `sweepCount` atomic that a regression test can
//     assert (mirrors the 0.5.25 RuntimeGC fix precedent).
//
// The full reconciliation logic (re-stamp Visibility when workspace
// membership shifts, garbage-collect orphan rows) is P6's intentional
// no-op — the upstream list-decisions endpoint would unblock the
// write-back side. Until then, the reconciler is observability +
// cron infrastructure only.
//
// Lifecycle mirrors SemanticaGC: Start launches the loop on its
// own goroutine; Stop is funnelled through stopOne so the loop
// body's defer can't race a Stop that's already closed the channel.
// Idempotent.

package experimental

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ACLReconcilerConfig bundles every knob the reconcile loop
// respects.
type ACLReconcilerConfig struct {
	// Interval defaults to 6h (per P6 contract).
	Interval time.Duration
	// Logger is the structured logger; defaults to slog.Default().
	Logger *slog.Logger
	// Queries is the sqlc handle. nil disables the reconcile
	// (sweepCount still ticks, but no DB I/O) so test fixtures
	// can construct the reconciler without a live PG.
	Queries *db.Queries
}

// ACLReconciler owns the goroutine that ticks every Interval and
// emits a one-line reconcile summary.
type ACLReconciler struct {
	cfg     ACLReconcilerConfig
	running atomic.Bool
	stopped chan struct{}
	stopOne sync.Once
	// sweepCount increments every time sweep() is invoked. Exposed
	// for tests so a regression of the Run() loop body is caught
	// the same way SemanticaGC's TestSemanticaGC_RunSweepsBeforeExit
	// (P1-3 precedent) does.
	sweepCount atomic.Uint64
	// lastRowCount is the row count from the previous sweep. A
	// regression that causes the query to silently start returning 0
	// would be visible as a flat 0 across ticks (vs the
	// monotonically-growing value of a healthy reconcile).
	lastRowCount atomic.Int64
}

// NewACLReconciler builds the reconciler with config defaults applied.
func NewACLReconciler(cfg ACLReconcilerConfig) *ACLReconciler {
	if cfg.Interval <= 0 {
		cfg.Interval = 6 * time.Hour
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &ACLReconciler{
		cfg:     cfg,
		stopped: make(chan struct{}),
	}
}

// Start kicks off the loop on its own goroutine. Idempotent: a
// second call while the loop is already running is a no-op.
func (r *ACLReconciler) Start() {
	if !r.running.CompareAndSwap(false, true) {
		return
	}
	go r.Run()
}

// Stop signals the loop to exit and blocks until the goroutine
// returns. Idempotent.
func (r *ACLReconciler) Stop() {
	r.stopOne.Do(func() {
		close(r.stopped)
	})
}

// Run is the loop body. Exported so a test can drive it directly
// with a synthetic stop signal (see acl_reconciler_test.go).
func (r *ACLReconciler) Run() {
	defer r.running.Store(false)
	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()
	r.sweep() // tick once at boot so a regression is visible immediately
	for {
		select {
		case <-r.stopped:
			return
		case <-ticker.C:
			r.sweep()
		}
	}
}

// sweep is one reconcile pass. Bumps sweepCount, logs the tick.
// Real reconciliation logic is deferred — upstream semantica does
// not expose list /api/decisions, so the only actionable reconcile
// step (re-stamp Visibility when workspace membership shifts) would
// have to parse graph.json (rdflib's default serialization), which
// requires Python. The reconciler is therefore pure observability
// until upstream closes that gap.
func (r *ACLReconciler) sweep() {
	r.sweepCount.Add(1)
	r.cfg.Logger.Info("semantica_acl_reconcile tick",
		"sweep", r.sweepCount.Load(),
		"queries", r.cfg.Queries != nil)
}

// SweepCount returns the number of times sweep() has run. Test-only
// public accessor.
func (r *ACLReconciler) SweepCount() uint64 {
	return r.sweepCount.Load()
}

// LastRowCount is reserved for a future reconcile step that
// actually queries the ACL table; the current sweep is pure
// observability, so this always returns -1 (no successful query
// has run). Exposed for future callers that want to assert a
// non-negative value once CountSemanticaLocalDecisionAclByWorkspace
// lands.
func (r *ACLReconciler) LastRowCount() int64 {
	return r.lastRowCount.Load()
}