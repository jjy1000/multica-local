// Package causalgraph — maintenance ticker (0.5.83 WL3 S2, roadmap
// §3.3 item 15). GC-style loop mirroring SemanticaGC
// (internal/experimental/semantica_gc.go), but DB-backed:
//
//   - stale-marking: causal_node rows not observed for 30 days go
//     status='stale'. Constraint/assumption nodes are EXEMPT — they
//     are the long-lived anchors (issue roots especially: nothing
//     touches them after creation, TouchCausalNode has no callers
//     yet, and the issue icon popup reads status='active').
//   - suggested GC: unconfirmed tier-D proposals older than 30 days
//     are deleted. Rejected tombstones (mig 280) are the audit trail
//     and are kept FOREVER — they are what keeps proposers silent.
//
// Orphan reconcile needs no pass: every FK in causal_node /
// causal_edge is ON DELETE CASCADE (migs 277-278), so issue or node
// deletion cannot strand rows.
//
// Lifecycle mirrors SemanticaGC exactly: Start launches the loop on
// its own goroutine and takes NO context (0.5.39 regression law);
// Stop is funnelled through stopOne; Run is exported for tests; an
// atomic sweepCount lets tests assert the loop body fired. Wired in
// cmd/server/main.go: started beside the scheduler, stopped in the
// after-HTTP-drain shutdown chain (same order contract as the
// SemanticaGC / SwarmGC Stop() calls).
package causalgraph

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CausalMaintenanceConfig bundles every knob the ticker respects.
type CausalMaintenanceConfig struct {
	// Interval defaults to 24 hours (roadmap: daily cadence is enough).
	Interval time.Duration
	// NodeStaleAfter defaults to 30 days (roadmap §3.3 item 15).
	NodeStaleAfter time.Duration
	// SuggestedRetention defaults to 30 days.
	SuggestedRetention time.Duration
	// SweepTimeout bounds one sweep. Defaults to 60s.
	SweepTimeout time.Duration
	// Logger defaults to slog.Default().
	Logger *slog.Logger
}

func (c CausalMaintenanceConfig) withDefaults() CausalMaintenanceConfig {
	if c.Interval <= 0 {
		c.Interval = 24 * time.Hour
	}
	if c.NodeStaleAfter <= 0 {
		c.NodeStaleAfter = 30 * 24 * time.Hour
	}
	if c.SuggestedRetention <= 0 {
		c.SuggestedRetention = 30 * 24 * time.Hour
	}
	if c.SweepTimeout <= 0 {
		c.SweepTimeout = 60 * time.Second
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	return c
}

// CausalMaintenance owns the maintenance goroutine.
type CausalMaintenance struct {
	pool *pgxpool.Pool
	cfg  CausalMaintenanceConfig

	running atomic.Bool
	stopped chan struct{}
	stopOne sync.Once
	// sweepCount increments every time sweep() runs. Tests assert on
	// it the same way TestSemanticaGC does (SemanticaGC.sweepCount
	// precedent).
	sweepCount atomic.Uint64
}

// NewCausalMaintenance builds the ticker. A nil pool is allowed for
// tests — sweeps become no-ops.
func NewCausalMaintenance(pool *pgxpool.Pool, cfg CausalMaintenanceConfig) *CausalMaintenance {
	return &CausalMaintenance{
		pool:    pool,
		cfg:     cfg.withDefaults(),
		stopped: make(chan struct{}),
	}
}

// Start launches the loop on its own goroutine. Idempotent. Takes no
// context — ownership of the lifetime belongs to Stop, not to any
// request scope (0.5.39 regression law).
func (m *CausalMaintenance) Start() {
	if !m.running.CompareAndSwap(false, true) {
		return
	}
	go m.Run()
}

// Stop signals the loop to halt. Safe before Start; safe to call
// multiple times.
func (m *CausalMaintenance) Stop() {
	if !m.running.Load() {
		return
	}
	m.running.Store(false)
	m.stopOne.Do(func() {
		if m.stopped != nil {
			close(m.stopped)
		}
	})
}

// Run is the loop body, exported so tests can drive it directly
// (mirrors SemanticaGC.Run).
func (m *CausalMaintenance) Run() {
	t := time.NewTicker(m.cfg.Interval)
	defer t.Stop()

	for m.running.Load() {
		select {
		case <-t.C:
			m.sweep()
		case <-m.stopped:
			return
		}
	}
}

// sweep runs one maintenance pass. Safe to call from tests.
func (m *CausalMaintenance) sweep() {
	m.sweepCount.Add(1)
	if m.pool == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), m.cfg.SweepTimeout)
	defer cancel()
	m.sweepOnce(ctx)
}

func (m *CausalMaintenance) sweepOnce(ctx context.Context) {
	staleCutoff := time.Now().UTC().Add(-m.cfg.NodeStaleAfter)
	gcCutoff := time.Now().UTC().Add(-m.cfg.SuggestedRetention)

	// 1. Stale-mark volatile nodes. constraint/assumption are the
	// durable kinds (issue roots are constraints) — exempt.
	staleTags, err := m.pool.Exec(ctx, `
		UPDATE causal_node SET status = 'stale'
		WHERE status = 'active'
		  AND last_observed_at < $1
		  AND type NOT IN ('constraint', 'assumption')
	`, staleCutoff)
	if err != nil {
		m.cfg.Logger.Warn("causal maintenance: stale-mark failed", "error", err)
	} else if staleTags.RowsAffected() > 0 {
		m.cfg.Logger.Info("causal maintenance: stale-marked nodes",
			"count", staleTags.RowsAffected())
	}

	// 2. GC unconfirmed suggestions. Rejected tombstones stay forever.
	gcTags, err := m.pool.Exec(ctx, `
		DELETE FROM causal_edge
		WHERE status = 'suggested' AND created_at < $1
	`, gcCutoff)
	if err != nil {
		m.cfg.Logger.Warn("causal maintenance: suggested GC failed", "error", err)
	} else if gcTags.RowsAffected() > 0 {
		m.cfg.Logger.Info("causal maintenance: expired unconfirmed suggestions",
			"count", gcTags.RowsAffected())
	}
}

// SweepCount returns the cumulative sweep invocation count (test
// surface, mirrors SemanticaGC.SweepCount).
func (m *CausalMaintenance) SweepCount() uint64 { return m.sweepCount.Load() }
