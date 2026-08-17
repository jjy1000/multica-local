// Package experimental — auth_token_gc.go (0.5.31).
//
// Unified background garbage collector for the three auth-token tables
// that have an `expires_at` column but no working retention GC:
//   - task_token           (migration 108; one row per agent spawn — high volume)
//   - workspace_invitation (migration 041; one row per invite — 7d TTL)
//   - daemon_token         (migration 029; one row per daemon install — long-lived)
//
// Same bug class as runtime_gc.go (0.5.25): the codebase documented
// the retention ladder in the migration comments but no GC ever swept
// the rows. They accumulated indefinitely past their `expires_at`.
// Per audit task a41b3235a93aacc6a: 3 production expires_at columns
// are dormant; this GC closes them in one unified tick.
//
// Lifecycle mirrors RuntimeGC + SemanticaGC + SwarmGC exactly:
//   1. Start() launches the loop on its own goroutine.
//   2. Stop() funnels close-once through stopOne.
//   3. Run() is exported for tests; sweepCount is an atomic.Uint64
//      so TestAuthTokenGC_RunSweepsBeforeExit catches the eager-close
//      regression (RuntimeGC pre-0.5.25 pattern).
//
// Two sibling tables — verification_code (migration 009) and
// personal_access_tokens (migration 011) — are TRUNCATEd by migration
// 249 instead of swept here: both have no insert path in this fork
// (per CLAUDE.md "Localized fork" contract: SendCode/VerifyCode/
// GoogleLogin return 410 Gone; cloud PAT removed), so rows are pure
// legacy residue and the GC contract doesn't apply.

package experimental

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	defaultAuthTokenGCInterval        = 6 * time.Hour
	defaultAuthTokenGCOperationTimeout = 15 * time.Second
)

// AuthTokenGCConfig bundles every knob the GC loop respects.
type AuthTokenGCConfig struct {
	// Queries is required in production. Tests may pass nil and the
	// sweep short-circuits via the nil-guard, mirroring RuntimeGC's
	// pattern (TestRuntimeGC_Run_DoesNotPanic). Without this guard a
	// test-only build that forgot to wire Queries would NPE on first
	// sweep.
	Queries *db.Queries
	// Interval defaults to 6 hours. The three tables are
	// low-traffic relative to the 30-day / 7-day / long-lived
	// retention windows; sub-hourly cadence is wasteful.
	Interval time.Duration
	// OperationTimeout overrides the per-table sweep timeout
	// (default 15s). One slow DELETE on a single table cannot
	// starve the others — each gets its own sub-context.
	OperationTimeout time.Duration
	// Logger is the structured logger; defaults to slog.Default().
	Logger *slog.Logger
}

// AuthTokenGC owns the goroutine that sweeps the three auth-token
// tables on a tick. Call Start once at server boot; Run is the inner
// loop (exported for tests).
type AuthTokenGC struct {
	cfg     AuthTokenGCConfig
	running atomic.Bool
	stopped chan struct{}
	stopOne sync.Once
	// sweepCount increments every time sweep() is invoked. Exposed
	// for tests so a regression of the Run() loop body (pre-0.5.25
	// eager-close bug class) is caught the same way
	// TestRuntimeGC_RunSweepsBeforeExit does. Production ignores it.
	sweepCount atomic.Uint64
}

// NewAuthTokenGC builds the GC with config defaults applied.
func NewAuthTokenGC(cfg AuthTokenGCConfig) *AuthTokenGC {
	if cfg.Interval <= 0 {
		cfg.Interval = defaultAuthTokenGCInterval
	}
	if cfg.OperationTimeout <= 0 {
		cfg.OperationTimeout = defaultAuthTokenGCOperationTimeout
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &AuthTokenGC{
		cfg:     cfg,
		stopped: make(chan struct{}),
	}
}

// Start launches the GC loop on its own goroutine. Idempotent —
// re-calling is a no-op (the running flag short-circuits).
func (g *AuthTokenGC) Start() {
	if !g.running.CompareAndSwap(false, true) {
		return
	}
	go g.Run()
}

// Stop signals the loop to halt. Safe to call before Start; safe
// to call multiple times. The actual close-once is funnelled through
// stopOne so the loop body's defer close can't race a Stop that's
// already closed the channel. Mirrors RuntimeGC.Stop semantics.
func (g *AuthTokenGC) Stop() {
	if !g.running.Load() {
		return
	}
	g.running.Store(false)
	g.stopOne.Do(func() {
		if g.stopped != nil {
			close(g.stopped)
		}
	})
}

// Run is the loop body. Exported so tests can drive it directly
// without spawning a goroutine. Sweeps at cfg.Interval; returns
// when stopped is closed or running flips to false.
func (g *AuthTokenGC) Run() {
	t := time.NewTicker(g.cfg.Interval)
	defer t.Stop()

	// One immediate sweep on boot so backlogs start draining right
	// away. If Start() is racing a Stop() that arrives before the
	// first tick, the sweep still completes — it's idempotent and
	// bounded by the per-table OperationTimeout.
	g.sweep()

	for g.running.Load() {
		select {
		case <-t.C:
			g.sweep()
		case <-g.stopped:
			return
		}
	}
}

// sweep runs one pass over all three tables. Each table gets its
// own sub-context with OperationTimeout so a slow DELETE on one
// table can't starve the others. Errors are logged at Warn (one
// failed table must not abort the rest of the sweep).
func (g *AuthTokenGC) sweep() {
	g.sweepCount.Add(1)
	if g.cfg.Queries == nil {
		g.cfg.Logger.Debug("auth_token_gc disabled; no queries wired")
		return
	}

	// task_token — highest volume of the three (one row per agent
	// spawn; see migration 108). 30-day retention baked into the
	// table's expires_at insert path.
	subCtx, cancel := context.WithTimeout(context.Background(), g.cfg.OperationTimeout)
	if err := g.cfg.Queries.DeleteExpiredTaskTokens(subCtx); err != nil {
		g.cfg.Logger.Warn("auth_token_gc task_token sweep failed", "err", err.Error())
	}
	cancel()

	// workspace_invitation — moderate volume (one row per invite
	// attempt; 7d TTL per migration 041). Concurrent with other
	// sweeps via a fresh sub-context.
	subCtx, cancel = context.WithTimeout(context.Background(), g.cfg.OperationTimeout)
	if err := g.cfg.Queries.DeleteExpiredWorkspaceInvitations(subCtx); err != nil {
		g.cfg.Logger.Warn("auth_token_gc workspace_invitation sweep failed", "err", err.Error())
	}
	cancel()

	// daemon_token — long-lived (one row per daemon install; see
	// migration 029). Sub-context again for timeout isolation.
	subCtx, cancel = context.WithTimeout(context.Background(), g.cfg.OperationTimeout)
	if err := g.cfg.Queries.DeleteExpiredDaemonTokens(subCtx); err != nil {
		g.cfg.Logger.Warn("auth_token_gc daemon_token sweep failed", "err", err.Error())
	}
	cancel()
}

// SweepCount returns the cumulative sweep invocation count. Tests
// use this to assert the loop body actually fires (mirrors
// RuntimeGC.sweepCount, 0.5.25 fix precedent).
func (g *AuthTokenGC) SweepCount() uint64 { return g.sweepCount.Load() }