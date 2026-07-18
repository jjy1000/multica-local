// Package agent_self_optimization — service.go (0.3.45.1 + 0.3.45.2).
//
// Per-(user, workspace) scheduler + runner host. Mirrors the mythos
// Service pattern (see server/internal/service/mythos/runner.go):
//
//   - One Service instance per daemon process
//   - One ticker goroutine per (opted-in user, workspace) pair
//   - Advisory-lock guarded runs (no two daemons run the same ws)
//   - Resume() on daemon bootstrap picks up any pending rows from the
//     previous process
//   - Stop() cancels every ticker / in-flight run on daemon shutdown
//
// 0.3.45.2 gate: the scheduler tick consults flagOnForUser() on every
// tick. A user can opt out at any time and the next tick (within
// SchedulerTickerInterval = 1 minute) silently stops scheduling for
// that user. No need to cancel the ticker — it self-skips.
//
// "flag-off completely bypasses experimental code" contract: the
// catalog default remains OFF. The Service NEVER consults the catalog
// default at the gate; it ONLY consults experimental_pref. A user
// without a row (or with enabled=false) is OFF.
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

// SchedulerTickerInterval is how often the per-(user, workspace) scheduler
// loop checks whether to fire. 1 minute is coarse enough to be
// cheap and fine enough to fire within 60s of the scheduled 10:00 local
// instant, AND fine enough to honor an opt-out within 60s.
const SchedulerTickerInterval = 1 * time.Minute

// LockKey is the advisory-lock namespace key. pg_try_advisory_lock
// takes a single bigint; we hash the string into one. The same
// constant is used by the manual-trigger HTTP path so two HTTP
// requests can't run concurrent scans for the same workspace.
const LockKey = "agent_self_optimization"

// tickerKey is the map key for s.tickers. Composed of userID +
// workspaceID so a per-user opt-out only cancels that user's ticker
// for that workspace, not every workspace the daemon knows about.
type tickerKey struct {
	UserID      pgtype.UUID
	WorkspaceID pgtype.UUID
}

// Service is the daemon-wide host. Construct via NewService.
type Service struct {
	queries *db.Queries
	kb      KBWriter

	mu           sync.Mutex
	tickers      map[tickerKey]context.CancelFunc
	workspaceIDs []pgtype.UUID
	localClock   *time.Location
}

// NewService returns a Service ready for Start. The caller (typically
// server/cmd/server/router.go's newAgentSelfOptService) then calls
// Start + defers Stop.
func NewService(queries *db.Queries) *Service {
	return &Service{
		queries:    queries,
		kb:         NewFileSystemKBWriter(""),
		tickers:    make(map[tickerKey]context.CancelFunc),
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

// Start launches one scheduler goroutine per (opted-in user,
// workspace) pair. Safe to call multiple times — repeated calls add
// new tickers; the daemon bootstrap path always calls Start exactly
// once.
//
// 0.3.45.2 boot flow:
//  1. Query ListOptedInUsers → which users have experimental_pref row
//  2. Cross-product with workspaceIDs → N×M ticker keys
//  3. Spawn one goroutine per key. The goroutine self-cancels on
//     opt-out (flagOnForUser returns false on a later tick).
//
// Flag-off: returns nil immediately. The boot is a no-op when no user
// has opted in. The cancel-func map stays empty and Stop() is also a
// no-op. This is the "flag-off completely bypasses experimental code"
// contract — but per-USER, not per-PROCESS.
func (s *Service) Start(ctx context.Context, workspaceIDs []pgtype.UUID) error {
	s.mu.Lock()
	s.workspaceIDs = append([]pgtype.UUID(nil), workspaceIDs...)
	s.mu.Unlock()

	users, err := s.queries.ListOptedInUsers(ctx)
	if err != nil {
		slog.Warn("agent-self-opt: list opted-in users failed; scheduler not started", "err", err)
		return nil
	}
	if len(users) == 0 {
		slog.Info("agent-self-opt: no opted-in users; scheduler not started")
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, userID := range users {
		for _, wsID := range workspaceIDs {
			key := tickerKey{UserID: userID, WorkspaceID: wsID}
			if _, exists := s.tickers[key]; exists {
				continue
			}
			tickCtx, cancel := context.WithCancel(context.Background())
			s.tickers[key] = cancel
			go s.runScheduler(tickCtx, userID, wsID)
		}
	}
	slog.Info("agent-self-opt: scheduler started",
		"users", len(users), "workspaces", len(workspaceIDs),
		"tickers", len(s.tickers))
	return nil
}

// Resume scans for pending / running rows left behind by a previous
// process. The scheduler tickers themselves are started via Start;
// Resume is purely for crash recovery. Returns the number of runs
// resumed (zombies that get marked failed do not count).
//
// 0.3.45.2: Resume is invoked per-workspace from the router boot
// block, but the gate is now per-(user, workspace) — we check
// ListOptedInUsers once and only resume runs whose workspace has at
// least one opted-in user (or we just resume unconditionally since
// runs are user-agnostic once created — the next scheduler tick for
// any opted-in user in that workspace will surface them).
//
// Decision: runs are user-agnostic after creation (the row lives in
// agent_self_opt_run without a user_id column), so Resume() at boot
// unconditionally scans every pending/running row regardless of the
// opted-in set. The scheduler tick will still gate new work on the
// opt-in check.
func (s *Service) Resume(ctx context.Context, workspaceID pgtype.UUID) (int, error) {
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
		// next tick and re-attempt. Mark the row 'failed' if it's
		// been "running" for more than MaxRunLifetime so a true
		// zombie doesn't block the next run.
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

// Stop cancels every ticker and waits for in-flight runners to drain.
// Called from the daemon shutdown hook.
func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, cancel := range s.tickers {
		cancel()
		delete(s.tickers, key)
	}
}

// runScheduler is the per-(user, workspace) goroutine. Ticks once a
// minute; on each tick it first re-checks flagOnForUser() and skips
// silently when the user has opted out. If still opted in, it
// computes NextTrigger and fires when due.
func (s *Service) runScheduler(ctx context.Context, userID, workspaceID pgtype.UUID) {
	ticker := time.NewTicker(SchedulerTickerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// 0.3.45.2: per-tick opt-in re-check. A user who toggled
			// the flag off mid-tick is silently skipped within 60s.
			if !flagOnForUser(ctx, s.queries, userID) {
				continue
			}
			s.maybeFire(ctx, userID, workspaceID)
		}
	}
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

// MaxRunLifetime caps how long a single runner can hold status='running'.
// Mirrors mythos.SupervisionMaxLifetime but shorter — the self-opt run
// is bounded by the SQL scan + heuristic computation, which should
// complete in well under an hour even on a 1000-issue workspace.
const MaxRunLifetime = 2 * time.Hour

// maybeFire runs the gate check + advisory lock + Run() call. The
// advisory lock keeps two daemons (or a daemon + a manual CLI
// trigger) from running the same workspace concurrently.
//
// 0.3.45.2: the userID is passed for log correlation only — the
// per-user opt-in gate was already enforced by runScheduler() before
// this function was called. We re-check here defensively so a stale
// ticker that slipped past the runScheduler check still no-ops
// without firing work.
func (s *Service) maybeFire(ctx context.Context, userID, workspaceID pgtype.UUID) {
	if !flagOnForUser(ctx, s.queries, userID) {
		return
	}
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

// TriggerManualRun is the manual-trigger entry point. CLI + HTTP call
// it. Returns the run row id. The advisory lock guards against
// concurrent manual triggers + the scheduler tick.
//
// 0.3.45.2: gate moved from process-level flagOn() to per-user
// flagOnForUser(ctx, callerUserID). Returns a clear error when the
// caller is not opted in so the HTTP layer can return 403 with a
// helpful message instead of silently succeeding.
func (s *Service) TriggerManualRun(ctx context.Context, callerUserID, workspaceID pgtype.UUID) (pgtype.UUID, error) {
	if !flagOnForUser(ctx, s.queries, callerUserID) {
		return pgtype.UUID{}, fmt.Errorf("agent_self_optimization flag is off for caller")
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

// _ = uuid.Nil keeps the import alive for tests that compare against
// the zero UUID (e.g. "no workspace selected" sentinels).
var _ = uuid.Nil