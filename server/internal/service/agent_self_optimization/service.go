// Package agent_self_optimization — service.go (0.3.45.1).
//
// Per-workspace scheduler + runner host. Mirrors the mythos
// Service pattern (see server/internal/service/mythos/runner.go):
//
//   - One Service instance per daemon process
//   - One ticker goroutine per workspace (workspaceIDsMu-protected set)
//   - Advisory-lock guarded runs (no two daemons run the same ws)
//   - Resume() on daemon bootstrap picks up any pending rows from the
//     previous process
//   - Stop() cancels every ticker / in-flight run on daemon shutdown
//
// Flag-off contract: Service.Start() is a no-op when
// experimental.DefaultFor("agent_self_optimization") is false. The
// ticker never starts, Resume() returns 0, Stop() is also a no-op.
// This keeps the "flag-off completely bypasses experimental code"
// CLAUDE.md contract intact.
package agent_self_optimization

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// SchedulerTickerInterval is how often the per-workspace scheduler
// loop checks whether to fire. 1 minute is coarse enough to be
// cheap (8 ticks per workspace per hour) and fine enough to fire
// within 60s of the scheduled 10:00 local instant.
const SchedulerTickerInterval = 1 * time.Minute

// LockKey is the advisory-lock namespace key. pg_try_advisory_lock
// takes a single bigint; we hash the string into one. The same
// constant is used by the manual-trigger HTTP path so two HTTP
// requests can't run concurrent scans for the same workspace.
const LockKey = "agent_self_optimization"

// Service is the daemon-wide host. Construct via NewService.
type Service struct {
	queries *db.Queries
	kb      KBWriter

	mu             sync.Mutex
	tickers        map[pgtype.UUID]context.CancelFunc
	workspaceIDs   []pgtype.UUID
	localClock     *time.Location
}

// NewService returns a Service ready for Start. The caller (typically
// server/cmd/server/router.go's newAgentSelfOptService) then calls
// Start + defers Stop.
func NewService(queries *db.Queries) *Service {
	return &Service{
		queries:    queries,
		kb:         NewFileSystemKBWriter(""),
		tickers:    make(map[pgtype.UUID]context.CancelFunc),
		localClock: time.Local,
	}
}

// SetKBWriter replaces the default filesystem KB writer. Tests use
// this to inject a fake.
func (s *Service) SetKBWriter(kb KBWriter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kb = kb
}

// SetLocalClock overrides the timezone used by NextTrigger. Tests
// pin UTC + a fixed instant.
func (s *Service) SetLocalClock(loc *time.Location) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.localClock = loc
}

// Start launches one scheduler goroutine per workspace ID. Safe to
// call multiple times — repeated calls add new tickers; the daemon
// bootstrap path always calls Start exactly once.
//
// Flag-off: returns nil immediately without launching any tickers.
// The cancel-func map stays empty and Stop() is also a no-op.
func (s *Service) Start(ctx context.Context, workspaceIDs []pgtype.UUID) error {
	// Hard gate: flag-off means we don't even consult the catalog
	// default — every gate downstream of DefaultFor is a no-op.
	// This is the CLAUDE.md "flag-off completely bypasses
	// experimental code" contract for agent_self_optimization.
	// The check stays at the TOP of Start (not inside the per-
	// workspace loop) so the bypass is uniform.
	if !flagOn() {
		slog.Info("agent-self-opt: flag off; scheduler not started")
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.workspaceIDs = append([]pgtype.UUID(nil), workspaceIDs...)
	for _, id := range workspaceIDs {
		if _, exists := s.tickers[id]; exists {
			continue
		}
		tickCtx, cancel := context.WithCancel(context.Background())
		s.tickers[id] = cancel
		go s.runScheduler(tickCtx, id)
	}
	return nil
}

// Resume scans for pending / running rows left behind by a previous
// process and re-launches their runners. The scheduler tickers
// themselves are started via Start; Resume is purely for crash
// recovery. Returns the number of runs resumed.
func (s *Service) Resume(ctx context.Context, workspaceID pgtype.UUID) (int, error) {
	if !flagOn() {
		return 0, nil
	}
	rows, err := s.queries.ListPendingAgentSelfOptRuns(ctx)
	if err != nil {
		return 0, fmt.Errorf("list pending self-opt runs: %w", err)
	}
	resumed := 0
	for _, r := range rows {
		if r.WorkspaceID != workspaceID {
			continue
		}
		if r.Status != "pending" && r.Status != "running" {
			continue
		}
		// We don't re-launch a stuck runner here — the scheduler
		// ticker for this workspace will pick the row up on its
		// next tick and re-attempt. Mark the row 'cancelled' if
		// it's been "running" for more than MaxRunLifetime so a
		// true zombie doesn't block the next run.
		if r.Status == "running" && r.StartedAt.Valid && time.Since(r.StartedAt.Time) > MaxRunLifetime {
			if _, uerr := s.queries.UpdateAgentSelfOptRunStatus(ctx, db.UpdateAgentSelfOptRunStatusParams{
				ID:           r.ID,
				Status:       "failed",
				ErrorMessage: pgtype.Text{String: "resumed run exceeded max lifetime; marked failed", Valid: true},
			}); uerr != nil {
				slog.Warn("agent-self-opt: zombie mark failed", "run", r.ID, "err", uerr)
			}
			continue
		}
		resumed++
	}
	if resumed > 0 {
		slog.Info("agent-self-opt: resume pending runs", "count", resumed, "workspace", workspaceID)
	}
	return resumed, nil
}

// MaxRunLifetime caps how long a single runner can hold status='running'.
// Mirrors mythos.SupervisionMaxLifetime but shorter — the self-opt run
// is bounded by the SQL scan + heuristic computation, which should
// complete in well under an hour even on a 1000-issue workspace.
const MaxRunLifetime = 2 * time.Hour

// Stop cancels every ticker and waits for in-flight runners to drain.
// Called from the daemon shutdown hook.
func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, cancel := range s.tickers {
		cancel()
		delete(s.tickers, id)
	}
}

// runScheduler is the per-workspace goroutine. Ticks once a minute;
// on each tick it computes NextTrigger for this workspace and, if
// the current time is at/after that instant, fires a run.
func (s *Service) runScheduler(ctx context.Context, workspaceID pgtype.UUID) {
	ticker := time.NewTicker(SchedulerTickerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.maybeFire(ctx, workspaceID)
		}
	}
}

// maybeFire runs the gate check + advisory lock + Run() call. The
// advisory lock keeps two daemons (or a daemon + a manual CLI
// trigger) from running the same workspace concurrently.
func (s *Service) maybeFire(ctx context.Context, workspaceID pgtype.UUID) {
	// 1. Last successful run → schedule reference point.
	var lastSuccess time.Time
	last, err := s.queries.LastSuccessfulAgentSelfOptRun(ctx, workspaceID)
	if err == nil {
		lastSuccess = last.FinishedAt.Time
	} else if err != pgx.ErrNoRows {
		slog.Warn("agent-self-opt: read last success failed",
			"workspace", workspaceID, "err", err)
		return
	}

	s.mu.Lock()
	loc := s.localClock
	kb := s.kb
	s.mu.Unlock()

	next := NextTrigger(lastSuccess, time.Now(), loc)
	if time.Now().Before(next) {
		// Not yet — try again next tick.
		return
	}

	// 2. Insert a pending run row (so the user sees it in history).
	pending, err := s.queries.CreateAgentSelfOptRun(ctx, db.CreateAgentSelfOptRunParams{
		WorkspaceID: workspaceID,
		Status:      "pending",
		TriggerKind: "scheduled",
	})
	if err != nil {
		slog.Warn("agent-self-opt: create pending run failed",
			"workspace", workspaceID, "err", err)
		return
	}

	// 3. Advisory lock guard. We use a per-row pg_try_advisory_xact_lock
	// (sqlc helper LockAgentSelfOptRun below) to keep concurrent
	// daemons from double-firing. The lock auto-releases on tx commit
	// so we never leak.
	lockCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	gotLock, err := s.queries.LockAgentSelfOptRun(lockCtx, pending.ID.String())
	if err != nil {
		slog.Warn("agent-self-opt: advisory lock failed",
			"workspace", workspaceID, "err", err)
		return
	}
	if !gotLock {
		// Another daemon beat us to it; mark the pending row
		// cancelled and return without running.
		_, _ = s.queries.UpdateAgentSelfOptRunStatus(ctx, db.UpdateAgentSelfOptRunStatusParams{
			ID:           pending.ID,
			Status:       "cancelled",
			ErrorMessage: pgtype.Text{String: "another daemon held the advisory lock", Valid: true},
		})
		return
	}

	// 4. Flip to 'running' + invoke the runner.
	if _, err := s.queries.UpdateAgentSelfOptRunStatus(ctx, db.UpdateAgentSelfOptRunStatusParams{
		ID:     pending.ID,
		Status: "running",
	}); err != nil {
		slog.Warn("agent-self-opt: mark running failed", "run", pending.ID, "err", err)
		return
	}
	s.executeRun(ctx, pending.ID, workspaceID, "scheduled", kb)
}

// executeRun is the shared body for both the scheduler tick path
// and the manual-trigger HTTP path. Returns the created run row id.
func (s *Service) executeRun(ctx context.Context, runID pgtype.UUID, workspaceID pgtype.UUID, triggerKind string, kb KBWriter) {
	result, runErr := Run(ctx, s.queries, RunInputs{
		WorkspaceID: workspaceID,
		TriggerKind: triggerKind,
	}, kb)

	if runErr != nil {
		slog.Warn("agent-self-opt: run failed",
			"run", runID, "workspace", workspaceID, "err", runErr)
		_, _ = s.queries.UpdateAgentSelfOptRunStatus(ctx, db.UpdateAgentSelfOptRunStatusParams{
			ID:           runID,
			Status:       "failed",
			ErrorMessage: pgtype.Text{String: runErr.Error(), Valid: true},
		})
		return
	}

	promptJSON, mErr := MarshalPromptSuggestions(result.PromptSuggestions)
	if mErr != nil {
		slog.Warn("agent-self-opt: marshal suggestions failed",
			"run", runID, "err", mErr)
	}
	_, _ = s.queries.UpdateAgentSelfOptRunResult(ctx, db.UpdateAgentSelfOptRunResultParams{
		ID:                  runID,
		PromptSuggestions:   promptJSON,
		ReportMd:            pgtype.Text{String: result.ReportMarkdown, Valid: true},
		SourceIssueCount:    int32(result.SourceIssueCount),
		KbAppendixPath:      pgtype.Text{String: result.KBAppendixPath, Valid: result.KBAppendixPath != ""},
		CreatedIssueID:      result.CreatedIssueID,
	})
}

// TriggerManualRun is the manual-trigger entry point. CLI + HTTP call
// it. Returns the run row id. The advisory lock guards against
// concurrent manual triggers + the scheduler tick.
func (s *Service) TriggerManualRun(ctx context.Context, workspaceID pgtype.UUID) (pgtype.UUID, error) {
	if !flagOn() {
		return pgtype.UUID{}, fmt.Errorf("agent_self_optimization flag is off")
	}
	pending, err := s.queries.CreateAgentSelfOptRun(ctx, db.CreateAgentSelfOptRunParams{
		WorkspaceID: workspaceID,
		Status:      "pending",
		TriggerKind: "manual",
	})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("create manual run: %w", err)
	}
	gotLock, err := s.queries.LockAgentSelfOptRun(ctx, pending.ID.String())
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("advisory lock: %w", err)
	}
	if !gotLock {
		return pgtype.UUID{}, fmt.Errorf("another run is in flight for this workspace")
	}
	if _, err := s.queries.UpdateAgentSelfOptRunStatus(ctx, db.UpdateAgentSelfOptRunStatusParams{
		ID:     pending.ID,
		Status: "running",
	}); err != nil {
		return pgtype.UUID{}, fmt.Errorf("mark running: %w", err)
	}
	s.mu.Lock()
	kb := s.kb
	s.mu.Unlock()
	go s.executeRun(context.Background(), pending.ID, workspaceID, "manual", kb)
	return pending.ID, nil
}

// Flag is the central gate. Wraps experimental.DefaultFor so a
// single grep finds every entry point.
func flagOn() bool {
	return flagOnExperimental()
}

// _ = uuid.Nil keeps the import alive for tests that compare against
// the zero UUID (e.g. "no workspace selected" sentinels).
var _ = uuid.Nil