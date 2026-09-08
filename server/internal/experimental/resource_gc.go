// Package experimental — resource_gc.go (0.5.105).
//
// Periodic orphan sweeper for experimental_resource_lock /
// experimental_resource_visibility. Descended from swarm_gc.go
// (0.5.22): the swarm_topology runtime was retired in 0.5.105
// (audit H3) and this is the generic half that survives — the 6h
// tick that drives SweepOrphanedExperimentalResources (0.5.60,
// audit P0-3).
//
// Claim() is idempotent per (source, type, resource_id), so every
// orphan row proves the underlying resource was hard-deleted without
// releasing its lock:
//
//   - runtime teardown cascade (DeleteArchivedAgentsByRuntime +
//     DeleteSquadsByArchivedAgentsOnRuntime inside DeleteAgentRuntime /
//     ArchiveAgentsAndDeleteRuntime) hard-deletes archived lab agents
//     and their squads;
//   - workspace delete cascades agent/squad/skill/member rows, and
//     lock.resource_id has no FK at all (migration 148).
//
// Migration 274 did the one-shot cleanup; this loop keeps the tables
// clean going forward. Inert rows are invisible to every consult site
// today, but any future count-based consumer would inherit the
// inflation, so the sweep logs only when rows moved.

package experimental

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ResourceGCConfig bundles every knob the GC loop respects.
type ResourceGCConfig struct {
	// Queries is the sweep target. A nil Queries (test-only builds)
	// makes each tick a counted no-op.
	Queries *db.Queries
	// Interval defaults to 6 hours.
	Interval time.Duration
	// Logger is the structured logger; defaults to slog.Default().
	Logger *slog.Logger
}

// ResourceGC owns the goroutine that drives
// SweepOrphanedExperimentalResources on a tick. Call Start once at
// server boot; Run is the inner loop (exported for tests).
type ResourceGC struct {
	cfg        ResourceGCConfig
	running    atomic.Bool
	stopped    chan struct{}
	stopOne    sync.Once
	sweepCount atomic.Uint64
}

// NewResourceGC builds the GC with config defaults applied.
func NewResourceGC(cfg ResourceGCConfig) *ResourceGC {
	if cfg.Interval <= 0 {
		cfg.Interval = 6 * time.Hour
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &ResourceGC{
		cfg:     cfg,
		stopped: make(chan struct{}),
	}
}

// Start launches the GC goroutine. Idempotent — re-calling is a
// no-op (the running flag short-circuits). Mirrors runtime_gc.Start:
// Start takes NO context. The loop owns context.Background internally
// (see Run), so it can never be bound to a boot-scoped caller context.
// The Stop channel is the graceful shutdown hook; main.go's shutdown
// sequence closes it.
//
// 0.5.39 fix (inherited from swarm_gc): Start previously took a
// context and router.go passed the boot-scoped bootCtx (30s timeout +
// defer cancel). Run exited on ctx.Done() ~8ms after router setup and
// cleanup never ran — the same dormant-GC class as the 0.5.25
// runtime_gc fix. The context parameter is removed entirely.
func (g *ResourceGC) Start() {
	if !g.running.CompareAndSwap(false, true) {
		g.cfg.Logger.Debug("resource_gc already running")
		return
	}
	go g.Run()
	g.cfg.Logger.Info("resource_gc started", "interval", g.cfg.Interval.String())
}

// Stop signals the loop to exit. Safe to call multiple times (the
// sync.Once guards the close). Mirrors runtime_gc.Stop semantics.
func (g *ResourceGC) Stop() {
	g.stopOne.Do(func() {
		close(g.stopped)
		g.running.Store(false)
		g.cfg.Logger.Info("resource_gc stopped")
	})
}

// Run is the inner loop, exported for tests. Tick interval is
// determined by config (default 6h). Each tick runs one orphan sweep.
//
// Exit paths are g.stopped (main.go shutdown Stop) and running=false.
// There is NO context argument — the sweep owns context.Background via
// a per-sweep timeout, so the loop cannot be bound to a short-lived
// caller context (0.5.39 fix).
func (g *ResourceGC) Run() {
	defer g.Stop()
	ticker := time.NewTicker(g.cfg.Interval)
	defer ticker.Stop()

	for g.running.Load() {
		select {
		case <-g.stopped:
			return
		case <-ticker.C:
			g.sweepCount.Add(1)
			g.sweep()
		}
	}
}

// sweepOrphanedExperimentalResources deletes experimental_resource_lock /
// experimental_resource_visibility rows whose resource was hard-deleted
// by a cascade that never releases them. Sweeper is injectable so tests
// can drive the path without *db.Queries.
func (g *ResourceGC) sweepOrphanedExperimentalResources(sweeper orphanResourceSweeper) {
	if sweeper == nil {
		return
	}
	g.sweepCount.Add(1)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	locks, visibility, err := SweepOrphanedExperimentalResources(ctx, sweeper)
	if err != nil {
		g.cfg.Logger.Warn("orphan experimental resource sweep failed", "err", err.Error())
		return
	}
	if locks > 0 || visibility > 0 {
		g.cfg.Logger.Info("orphan experimental resource sweep",
			"locks_deleted", locks, "visibility_deleted", visibility)
	}
}

// sweep runs one tick. Production callers go through the generated
// Queries; tests inject a fake via the sweeper hook.
func (g *ResourceGC) sweep() {
	if g.cfg.Queries == nil {
		// Typed-nil *db.Queries inside the orphanResourceSweeper
		// interface is non-nil, so the guard must live here — a nil
		// check inside sweepOrphanedExperimentalResources cannot see it.
		return
	}
	g.sweepOrphanedExperimentalResources(g.cfg.Queries)
}
