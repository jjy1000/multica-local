// Package mythos — stalled-run reaper (0.5.87).
//
// Async-engine unification, swarm orchestrator port. The swarm side
// has had a closed reap loop since 0.5.86 (ListStalledSwarmRunsForGC +
// the swarm_gc sweep): every non-terminal run either has a live
// orchestrator goroutine or gets failed by the GC. The mythos side had
// no equivalent — ResumeSupervision re-adopts only status='supervising'
// rows, so a 'running' row orphaned by a mid-pipeline restart can never
// terminate (the dev-record's "3 stuck running since 08-24" class).
//
// This file ports the swarm engine's three remaining patterns onto the
// supervise loop, completing the unification:
//
//   1. Stalled-run reap   (swarm_gc.go sweep → ReapStalledRuns below)
//   2. Per-tick timeout   (orchestrator.go:318 → supervise.go)
//   3. Heartbeat from t=0 (swarm_role.last_heartbeat_at →
//                          supervision_state.last_check_at, stamped on
//                          the initial persist in supervise.go)
//
// Like SwarmGC, StartStalledRunReaper takes NO context: the loop owns
// context.Background internally (the 0.5.39 lesson — a boot-scoped ctx
// made the GC exit ~8ms after router setup). Stop is folded into
// Service.Stop so the reaper dies with the supervise set on shutdown.

package mythos

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// StalledReaperInterval is the sweep cadence. Matches SwarmGCConfig's
// 6h default — slow enough to be invisible, fast enough that a stalled
// run never outlives a workday.
const StalledReaperInterval = 6 * time.Hour

// reapSweepTimeout bounds each sweep's DB calls. Mirrors the 60s
// per-sweep timeout on swarm_gc.sweep / runtime_gc.sweep.
const reapSweepTimeout = 60 * time.Second

// reapBatchLimit caps one sweep. Mirrors the swarm GC's batch cap.
const reapBatchLimit = 100

// StalledReapAbortReason is written into supervision_state.abort_reason
// when a supervising row is reaped, so the supervise panel's timeline
// shows why the run died instead of a bare 'aborted' phase.
const StalledReapAbortReason = "stalled_reap"

// reapQuerier is the narrow seam the reaper's unit tests inject so the
// stall decision + per-row handling can be exercised without a real
// DB. Production leaves Service.reapQ nil; resolveReapQuerier falls
// back to *db.Queries — the same seam shape as tickQ (supervise.go).
type reapQuerier interface {
	ListStalledMythosRunsForGC(ctx context.Context, limit int32) ([]db.MythosRun, error)
	SetMythosRunStatus(ctx context.Context, arg db.SetMythosRunStatusParams) (db.MythosRun, error)
	SetMythosRunSupervisionState(ctx context.Context, arg db.SetMythosRunSupervisionStateParams) error
}

func (s *Service) resolveReapQuerier() reapQuerier {
	if s.reaper.reapQ != nil {
		return s.reaper.reapQ
	}
	return s.queries
}

// ReapStalledRuns fails every non-terminal run the stall clocks have
// condemned (see ListStalledMythosRunsForGC for the two clocks).
// Per-row best-effort, mirroring the swarm GC's reap branch: one bad
// row must not strand the others. SetMythosRunStatus stamps
// completed_at on terminal flips, so the run's lifetime stays
// queryable after the reap. Returns the number of rows failed.
func (s *Service) ReapStalledRuns(ctx context.Context) (int, error) {
	q := s.resolveReapQuerier()
	rows, err := q.ListStalledMythosRunsForGC(ctx, reapBatchLimit)
	if err != nil {
		return 0, fmt.Errorf("list stalled mythos runs: %w", err)
	}
	reaped := 0
	for _, run := range rows {
		// Supervising rows keep their timeline: merge the abort reason
		// into the existing supervision_state so the panel renders
		// phase='aborted' + reason instead of losing tick history.
		// 'running' rows have no state blob — the status flip alone is
		// the record (we don't invent one post-hoc).
		if run.Status == "supervising" {
			if err := s.mergeStallAbort(ctx, q, run); err != nil {
				slog.Warn("mythos reaper: supervision_state abort write failed",
					"run", run.ID.String(), "err", err)
				// Non-fatal: the status flip below is the load-bearing write.
			}
		}
		if _, err := q.SetMythosRunStatus(ctx, db.SetMythosRunStatusParams{
			ID:     run.ID,
			Status: "failed",
		}); err != nil {
			slog.Warn("mythos reaper: stall-fail write failed",
				"run", run.ID.String(), "err", err)
			continue
		}
		slog.Warn("mythos reaper: stalled run failed",
			"run", run.ID.String(),
			"status_was", run.Status,
			"mode", run.Mode,
			"started_at", run.StartedAt.Time.Format(time.RFC3339))
		reaped++
	}
	return reaped, nil
}

// mergeStallAbort rewrites supervision_state with phase='aborted' +
// abort_reason=stalled_reap, preserving every other field (ticks,
// sub-task counters, started_at) so the renderer's history survives.
// A corrupt or missing blob is replaced with a minimal honest state —
// same fall-through as TickSupervisionOnce's corrupt-JSONB branch.
func (s *Service) mergeStallAbort(ctx context.Context, q reapQuerier, run db.MythosRun) error {
	state := SupervisionState{}
	if len(run.SupervisionState) > 0 {
		if err := json.Unmarshal(run.SupervisionState, &state); err != nil {
			slog.Warn("mythos reaper: supervision_state unmarshal failed; replacing with minimal state",
				"run", run.ID.String(), "err", err, "raw_bytes", len(run.SupervisionState))
			state = SupervisionState{}
		}
	}
	state.Phase = PhaseAborted
	state.AbortReason = StalledReapAbortReason
	if state.StartedAt.IsZero() {
		state.StartedAt = run.StartedAt.Time
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal stalled supervision state: %w", err)
	}
	return q.SetMythosRunSupervisionState(ctx, db.SetMythosRunSupervisionStateParams{
		ID:               run.ID,
		SupervisionState: raw,
	})
}

// StartStalledRunReaper launches the reap loop. Idempotent — re-calling
// is a no-op (the running flag short-circuits, mirroring SwarmGC.Start).
// Takes no context: the loop owns context.Background internally and
// exits via the Service.stop channel. An immediate boot sweep reaps
// rows orphaned by the previous process now rather than up to `interval`
// later (the 0.5.60 starvation lesson from the swarm GC's tick-first
// loop).
func (s *Service) StartStalledRunReaper(interval time.Duration) {
	if interval <= 0 {
		interval = StalledReaperInterval
	}
	// Lazy channel init for Service values built by struct literal
	// (tests) that bypass NewService. superviseMu serializes against
	// a concurrent stopReaper.
	s.superviseMu.Lock()
	if s.reaper.reapStop == nil {
		s.reaper.reapStop = make(chan struct{})
	}
	s.superviseMu.Unlock()
	if !s.reaper.reapRunning.CompareAndSwap(false, true) {
		slog.Debug("mythos stalled-run reaper already running")
		return
	}
	go s.runReaperLoop(interval)
	slog.Info("mythos stalled-run reaper started", "interval", interval.String())
}

// runReaperLoop is the reap tick loop. Boot sweep first, then every
// `interval`. Exit paths: the stop channel (Service.Stop at shutdown)
// and the running flag.
func (s *Service) runReaperLoop(interval time.Duration) {
	defer s.reaper.reapRunning.Store(false)
	s.reapSweep()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.reaper.reapStop:
			return
		case <-ticker.C:
			s.reapSweep()
		}
	}
}

func (s *Service) reapSweep() {
	ctx, cancel := context.WithTimeout(context.Background(), reapSweepTimeout)
	defer cancel()
	reaped, err := s.ReapStalledRuns(ctx)
	if err != nil {
		slog.Warn("mythos stalled-run reaper sweep failed", "err", err)
		return
	}
	if reaped > 0 {
		slog.Info("mythos stalled-run reaper sweep", "reaped", reaped)
	}
}

// stopReaper closes the reap stop channel exactly once. Called from
// Service.Stop (runner.go) so shutdown stays one call. Nil-safe for
// Service values that never started the reaper.
func (s *Service) stopReaper() {
	if s.reaper.reapStop == nil {
		return
	}
	s.reaper.reapStopOnce.Do(func() { close(s.reaper.reapStop) })
}

// reapFields bundles the reaper's sync state. Kept as a struct so the
// Service definition in runner.go gains one field instead of four.
type reapFields struct {
	// reapStop is closed by stopReaper on shutdown. Created in
	// NewService so Stop is safe even if the reaper never started.
	reapStop chan struct{}
	// reapStopOnce guards the close (Stop may be called twice).
	reapStopOnce sync.Once
	// reapRunning makes StartStalledRunReaper idempotent.
	reapRunning atomic.Bool
	// reapQ is the test seam; nil in production (see reapQuerier).
	reapQ reapQuerier
}
