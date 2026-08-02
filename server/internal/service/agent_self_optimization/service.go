// Package agent_self_optimization — service.go (0.3.45.1 + 0.3.45.2 + 0.5.5.1 + 0.5.6).
//
// Per-workspace scheduler + runner host. Mirrors the mythos
// Service pattern (see server/internal/service/mythos/runner.go):
//
//   - One Service instance per daemon process
//   - One ticker goroutine per (workspace) pair (0.5.5.1: no longer
//     gated on per-user opt-in — every workspace that has the 2
//     self-opt autopilots installed gets a ticker)
//   - Advisory-lock guarded runs (no two daemons run the same ws)
//   - Resume() on daemon bootstrap picks up any pending rows from the
//     previous process
//   - Stop() cancels every ticker / in-flight run on daemon shutdown
//
// 0.5.5.1 gate: the per-tick `flagOnForUser` is now a stub that
// always returns true. The user-facing control moved to the
// autopilot row's own `enabled` field — `multica autopilot update
// --disabled` (or the GUI) flips each of the 2 self-opt autopilots
// individually, the same way every other autopilot is controlled.
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

// tickerKey is the map key for s.tickers. 0.5.5.1: workspace-only —
// the per-user opt-in gate is gone, so the key drops the UserID
// field. One ticker per workspace, regardless of how many users
// belong to it.
type tickerKey struct {
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

// Start launches one scheduler goroutine per workspace. 0.5.5.1: no
// longer gated on per-user opt-in (ListOptedInUsers) — the
// agent_self_optimization flag is now product-level
// (catalog.DefaultVal=true) and the per-tick flagOnForUser is a
// stub that always returns true. User control moved to the autopilot
// row's `enabled` field.
//
// Safe to call multiple times — repeated calls add new tickers; the
// daemon bootstrap path always calls Start exactly once.
//
// 0.5.5.1 boot flow:
//  1. workspaceIDs passed in by the router boot block
//  2. Spawn one goroutine per workspace. The goroutine ticks every
//     SchedulerTickerInterval and consults the per-autopilot
//     `enabled` flag (handled inside maybeFire) so disabling an
//     autopilot is the user-facing way to stop a given cadence.
//
// Flag-off / empty workspace: returns nil immediately. The boot is
// a no-op when no workspace exists.
func (s *Service) Start(ctx context.Context, workspaceIDs []pgtype.UUID) error {
	s.mu.Lock()
	s.workspaceIDs = append([]pgtype.UUID(nil), workspaceIDs...)
	s.mu.Unlock()

	if len(workspaceIDs) == 0 {
		slog.Info("agent-self-opt: no workspaces; scheduler not started")
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, wsID := range workspaceIDs {
		key := tickerKey{WorkspaceID: wsID}
		if _, exists := s.tickers[key]; exists {
			continue
		}
		tickCtx, cancel := context.WithCancel(context.Background())
		s.tickers[key] = cancel
		// 0.5.5.1: scheduler no longer takes a userID — the per-tick
		// gate is gone. maybeFire uses the autopilot's own enabled
		// field. The userID argument is now the workspace's first
		// member (or zero if none) — kept for the optimistic-lock
		// path that requires a non-zero caller identity.
		var userID pgtype.UUID
		go s.runScheduler(tickCtx, userID, wsID)
	}
	slog.Info("agent-self-opt: scheduler started",
		"workspaces", len(workspaceIDs),
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
	s.mu.Lock()
	kb := s.kb
	s.mu.Unlock()
	for _, r := range rows {
		if r.WorkspaceID != workspaceID {
			continue
		}
		if r.Status != "pending" && r.Status != "running" {
			continue
		}
		// True zombie: running for more than MaxRunLifetime. Mark
		// failed and move on — the next scheduler tick can pick a
		// fresh slot.
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
		// 0.3.45.2 bug fix (P0#2): actually re-launch the runner.
		// Background ctx so the boot-time timeout doesn't kill it.
		go s.executeRun(context.Background(), r.ID, r.WorkspaceID, r.TriggerKind, kb)
		resumed++
	}
	if resumed > 0 {
		slog.Info("agent-self-opt: resume re-launched runs", "count", resumed, "workspace", workspaceID)
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

// runScheduler is the per-workspace goroutine. Ticks once a minute;
// on each tick it delegates to maybeFire, which (0.5.5.1) consults
// the per-autopilot `enabled` field rather than the experimental
// flag. Disabling an autopilot is the user-facing way to stop a
// given cadence — there is no longer a per-user opt-in row to gate
// on.
func (s *Service) runScheduler(ctx context.Context, userID, workspaceID pgtype.UUID) {
	ticker := time.NewTicker(SchedulerTickerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
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

	// 0.5.2: deferred path — source data too thin. Persist the deferral
	// instead of a report; the scheduler re-attempts after the window.
	if result.Deferred {
		if _, derr := s.queries.UpdateAgentSelfOptRunDeferred(ctx, db.UpdateAgentSelfOptRunDeferredParams{
			ID:             runID,
			DeferredReason: pgtype.Text{String: result.DeferredReason, Valid: true},
			DeferredUntil:  pgtype.Timestamptz{Time: result.DeferredUntil, Valid: true},
			DataCount:      int32(result.DataCount),
		}); derr != nil {
			slog.Warn("agent-self-opt: deferral persist failed",
				"run", runID, "err", derr)
		}
		slog.Info("agent-self-opt: run deferred (insufficient data)",
			"run", runID, "reason", result.DeferredReason)
		return
	}

	// 0.5.2: record SkillOpt edits in the ledger (accepted + rejected both
	// count as experience for the next run).
	s.recordOptEdits(ctx, runID, workspaceID, result)

	promptJSON, mErr := MarshalPromptSuggestions(result.PromptSuggestions)
	if mErr != nil {
		slog.Warn("agent-self-opt: marshal suggestions failed",
			"run", runID, "err", mErr)
	}
	_, _ = s.queries.UpdateAgentSelfOptRunResult(ctx, db.UpdateAgentSelfOptRunResultParams{
		ID:                runID,
		PromptSuggestions: promptJSON,
		ReportMd:          pgtype.Text{String: result.ReportMarkdown, Valid: true},
		SourceIssueCount:  int32(result.SourceIssueCount),
		KbAppendixPath:    pgtype.Text{String: result.KBAppendixPath, Valid: result.KBAppendixPath != ""},
		CreatedIssueID:    result.CreatedIssueID,
	})
}

// recordOptEdits persists every instruction edit of a run into
// agent_opt_edit with its application state (applied / suggested /
// rejected) + validation score. Applied edits were already written back
// to agent.instructions by the runner; suggested edits wait for human
// confirmation; rejected edits are permanent negative experience.
func (s *Service) recordOptEdits(ctx context.Context, runID, workspaceID pgtype.UUID, result *RunResult) {
	record := func(edit InstructionEdit) {
		if !edit.AgentID.Valid {
			return
		}
		app := string(edit.Application)
		if app == "" {
			// Backward-compat default: pre-0.5.2 callers that only set
			// Accepted fall back to applied/rejected.
			if edit.Accepted {
				app = string(ApplicationApplied)
			} else {
				app = string(ApplicationRejected)
			}
		}
		var score pgtype.Numeric
		if edit.ValidationScore > 0 {
			_ = score.Scan(fmt.Sprintf("%.1f", edit.ValidationScore))
		}
		// applied_by is ONLY meaningful for applied edits (design-review d4:
		// a rejected/suggested edit recording applied_by=auto is misleading).
		// Non-applied edits write NULL (nullable column + sqlc.narg) so the
		// row passes the CHECK and the ledger records "not applied by anyone".
		var appliedBy pgtype.Text
		if edit.Application == ApplicationApplied {
			appliedBy = pgtype.Text{String: "auto", Valid: true}
		}
		var correctedTaskID pgtype.UUID
		if edit.CorrectedTaskID.Valid {
			correctedTaskID = edit.CorrectedTaskID
		}
		if _, err := s.queries.CreateAgentOptEdit(ctx, db.CreateAgentOptEditParams{
			AgentID:              edit.AgentID,
			TargetType:           edit.TargetType,
			TargetID:             edit.TargetID,
			SubjectScope:         edit.Scope,
			RunID:                runID,
			WorkspaceID:          workspaceID,
			EditType:             edit.EditType,
			BeforeText:           edit.BeforeText,
			AfterText:            edit.AfterText,
			Rationale:            pgtype.Text{String: edit.Rationale, Valid: edit.Rationale != ""},
			Accepted:             edit.Application == ApplicationApplied,
			Iteration:            1,
			Application:          app,
			ValidationScore:      score,
			ValidationReason:     pgtype.Text{String: edit.ValidationReason, Valid: edit.ValidationReason != ""},
			InstructionsSnapshot: pgtype.Text{String: edit.Snapshot, Valid: edit.Snapshot != ""},
			AppliedBy:            appliedBy,
			CorrectedTaskID:      correctedTaskID,
		}); err != nil {
			slog.Warn("agent-self-opt: edit ledger write failed",
				"run", runID, "subject", edit.TargetName, "err", err)
		}
	}
	for _, e := range result.OptEdits {
		record(e)
	}
}

// MaxRunLifetime caps how long a single runner can hold status='running'.
// Mirrors mythos.SupervisionMaxLifetime but shorter — the self-opt run
// is bounded by the SQL scan + heuristic computation, which should
// complete in well under an hour even on a 1000-issue workspace.
const MaxRunLifetime = 2 * time.Hour

// SuggestionExpiryWindow is how long an undecided suggestion may wait
// before the expiry sweep soft-archives it to 'ignored' (NOT 'rejected' —
// design-review verdict: a busy user's inaction must not poison the
// rejection buffer). ~3 weekly runs.
var SuggestionExpiryWindow = 21 * 24 * time.Hour

// expireSuggestions soft-archives suggestions older than the expiry window
// to 'ignored'. Runs once per scheduler tick (cheap, bounded). Ignored
// edits stay re-proposable with fresh validation; they never enter the
// rejection buffer.
func (s *Service) expireSuggestions(ctx context.Context) {
	rows, err := s.queries.ListExpiredSuggestedAgentOptEdits(ctx, db.ListExpiredSuggestedAgentOptEditsParams{
		CreatedAt: pgtype.Timestamptz{Time: time.Now().Add(-SuggestionExpiryWindow), Valid: true},
		Limit:     200,
	})
	if err != nil || len(rows) == 0 {
		return
	}
	ids := make([]pgtype.UUID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	if err := s.queries.UpdateAgentOptEditApplicationByIDs(ctx, db.UpdateAgentOptEditApplicationByIDsParams{
		Column1:     ids,
		Application: string(ApplicationIgnored),
	}); err != nil {
		slog.Warn("agent-self-opt: suggestion expiry sweep failed", "err", err)
		return
	}
	slog.Info("agent-self-opt: suggestions expired to ignored", "count", len(rows))
}

// maybeFire runs the gate check + advisory lock + Run() call. The
// advisory lock keeps two daemons (or a daemon + a manual CLI
// trigger) from running the same workspace concurrently.
//
// 0.3.45.2: the userID is passed for log correlation only — the
// per-user opt-in gate was already enforced by runScheduler() before
// this function was called. We re-check here defensively so a stale
// ticker that slipped past the runScheduler check still no-ops
// without firing work.
//
// 0.5.5.1: the flagOnForUser check is a stub that always returns
// true. The real per-cadence gate lives inside the autopilot row
// (`enabled` field) — disabled autopilots are filtered out before
// this function even consults the schedule.
func (s *Service) maybeFire(ctx context.Context, userID, workspaceID pgtype.UUID) {
	// 0.5.2: run the suggestion expiry sweep on the first tick of a
	// schedule so undecided suggestions degrade to 'ignored' instead of
	// rotting in the queue (design-review verdict: never expire-to-rejected).
	s.expireSuggestions(ctx)
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

	// 1b. Deferral gate (0.5.2): if the latest run is still parked as
	// 'deferred' and its retry window has not passed, skip this tick.
	// The runner parked the run because the source data was too thin;
	// re-firing now would just produce another thin report.
	deferred, derr := s.queries.LatestDeferredAgentSelfOptRun(ctx, workspaceID)
	if derr == nil && deferred.DeferredUntil.Valid && time.Now().Before(deferred.DeferredUntil.Time) {
		slog.Info("agent-self-opt: run deferred, retry window not passed",
			"workspace", workspaceID, "run", deferred.ID,
			"until", deferred.DeferredUntil.Time)
		return
	} else if derr != nil && derr != pgx.ErrNoRows {
		slog.Warn("agent-self-opt: read deferred run failed",
			"workspace", workspaceID, "err", derr)
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

	// 1c. In-flight guard (0.5.2): a catch-up tick can race a manual
	// trigger or a Resume re-launch. Stacking runs on the same workspace
	// ends in duplicate issue-number errors; skip when one is already
	// pending/running.
	active, aerr := s.queries.CountActiveAgentSelfOptRuns(ctx, workspaceID)
	if aerr != nil {
		slog.Warn("agent-self-opt: count active runs failed", "workspace", workspaceID, "err", aerr)
		return
	}
	if active > 0 {
		slog.Info("agent-self-opt: run already in flight; skip", "workspace", workspaceID, "active", active)
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
// 0.3.45.2 gate moved from process-level flagOn() to per-user
// flagOnForUser(ctx, callerUserID). 0.5.5.1: the per-user gate is a
// stub that always returns true. Manual triggers are not gated on
// the experimental flag any more — the caller is just expected to
// have a valid session. The user can still cancel the resulting run
// via the run-list endpoint.
func (s *Service) TriggerManualRun(ctx context.Context, callerUserID, workspaceID pgtype.UUID) (pgtype.UUID, error) {
	// 0.5.2 in-flight guard (same as the scheduler tick): a manual trigger
	// while a catch-up run is active would stack runs and race the issue
	// number constraint.
	active, aerr := s.queries.CountActiveAgentSelfOptRuns(ctx, workspaceID)
	if aerr != nil {
		return pgtype.UUID{}, fmt.Errorf("count active runs: %w", aerr)
	}
	if active > 0 {
		return pgtype.UUID{}, fmt.Errorf("a self-opt run is already in flight for this workspace")
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
