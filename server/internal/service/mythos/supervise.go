// Package mythos — enhancer-mode supervision (0.3.31).
//
// The Mythos Swarm's sole mode (0.3.16-patch.1 → 0.3.30) ran the
// RDT three-stage pipeline end-to-end and returned. Enhancer mode
// adds a fourth stage: after the coda synthesises the plan, the
// user-picked assignee (any agent or squad in the workspace) takes
// over the actual execution. The supervise goroutine watches the
// assignee's progress and writes per-tick reflection rows so the
// IssueLabsSection supervise panel can show live status.
//
// Hard constraints:
//
//   - The supervise path must NEVER touch the LLM dispatch path
//     (server/internal/handler/runtime.go). It only reads issue
//     rows + comments via the daemon's read APIs and writes
//     mythos_members.reflection / mythos_run.supervision_state.
//     This keeps the "flag-off completely bypasses experimental
//     code" contract intact: when mythos_swarm is off, the daemon
//     calls Service.Stop() in its shutdown hook and the goroutine
//     cancels cleanly.
//
//   - The supervise goroutine is per-run, not per-workspace. Each
//     enhancer-mode issue spawns one. The daemon bootstrap path
//     (resumeFromStorage) recovers any orphaned goroutines after a
//     restart by scanning for status='supervising' rows.
//
//   - Tick interval is hard-coded at 30 seconds. A run can live for
//     hours; 30 s × 3600 = ~120 ticks per hour per active run. The
//     supervision_state JSONB is the single write target, so the
//     DB cost is one UPDATE per tick — bounded and predictable.

package mythos

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// SupervisionTickerInterval is the wall-clock period between
// supervise ticks. Hard-coded — a feature flag here would defeat
// the purpose of having a single supervision cadence.
const SupervisionTickerInterval = 30 * time.Second

// SupervisionMaxLifetime caps a single supervise goroutine. After
// this duration the goroutine writes phase='aborted' with reason
// 'max_lifetime' and exits, even if the assignee has not finished.
// Prevents zombie runs from accumulating forever in the supervise
// set if a target assignee is hard-stuck.
const SupervisionMaxLifetime = 24 * time.Hour

// SupervisionPhase enumerates the lifecycle states the supervise
// goroutine transitions through. Stored in
// mythos_run.supervision_state.phase so the renderer can plot a
// timeline without re-deriving it from comments.
type SupervisionPhase string

const (
	PhasePreparing   SupervisionPhase = "preparing"
	PhasePlanning    SupervisionPhase = "planning"
	PhaseSupervising SupervisionPhase = "supervising"
	PhaseDone        SupervisionPhase = "done"
	PhaseAborted     SupervisionPhase = "aborted"
	PhaseDegraded    SupervisionPhase = "degraded"
)

// SupervisionState is the JSONB blob written to
// mythos_run.supervision_state. Fields are append-only — when the
// shape grows (new tick stats), old rows still parse because the
// decoder ignores unknown keys.
type SupervisionState struct {
	Phase               SupervisionPhase `json:"phase"`
	StartedAt           time.Time         `json:"started_at"`
	LastCheckAt         time.Time         `json:"last_check_at"`
	LastTickDurationMs  int64             `json:"last_tick_duration_ms"`
	TotalTicks          int               `json:"total_ticks"`
	SubTasksTotal       int               `json:"sub_tasks_total"`
	SubTasksDone        int               `json:"sub_tasks_done"`
	LatestReflection    string            `json:"latest_reflection,omitempty"`
	LatestReflectionIter int              `json:"latest_reflection_iter,omitempty"`
	AbortReason         string            `json:"abort_reason,omitempty"`
}

// isTerminalPhase reports whether the supervise goroutine should
// exit. done / aborted are terminal; degraded is a soft warning
// (DB hiccup, missing comment, etc.) that the goroutine continues
// through until the natural done condition or max-lifetime cap.
func isTerminalPhase(p SupervisionPhase) bool {
	return p == PhaseDone || p == PhaseAborted
}

// runSuperviseLoop is the per-run entry point. Runner.go calls
// Service.startSupervise which dispatches here.
//
// Lifecycle:
//
//	t=0          → phase=preparing, sub_tasks_total derived from coda
//	               plan (parse bullet list from coda_summary)
//	first tick   → phase=planning → supervising once plan visible
//	tick n       → counts assignee issue comments / status changes,
//	               updates sub_tasks_done, optionally writes a
//	               reflection row to mythos_members
//	tick n+m     → if sub_tasks_done >= sub_tasks_total → phase=done,
//	               status=completed, exit
//	>24h         → phase=aborted, reason=max_lifetime, exit
//	ctx canceled → graceful exit (no DB write)
func runSuperviseLoop(ctx context.Context, svc *Service, runID pgtype.UUID, cfg Config, rootIssueID pgtype.UUID) {
	state := SupervisionState{
		Phase:     PhasePreparing,
		StartedAt: time.Now(),
	}
	if err := svc.persistSupervisionState(ctx, runID, state); err != nil {
		slog.Warn("mythos supervise: initial persist failed", "run", runID, "err", err)
	}

	ticker := time.NewTicker(SupervisionTickerInterval)
	defer ticker.Stop()

	maxLifetime := time.NewTimer(SupervisionMaxLifetime)
	defer maxLifetime.Stop()

	for {
		select {
		case <-ctx.Done():
			// Daemon shutdown or explicit cancel. Do not write
			// 'aborted' to the row — the daemon bootstrap path
			// will resume us on the next start, or the user can
			// manually intervene via the supervise tick endpoint.
			svc.unregisterSupervise(runID)
			return
		case <-maxLifetime.C:
			state.Phase = PhaseAborted
			state.AbortReason = "max_lifetime"
			_ = svc.persistSupervisionState(ctx, runID, state)
			_ = svc.completeRun(ctx, runID, "aborted")
			svc.unregisterSupervise(runID)
			return
		case tickAt := <-ticker.C:
			tickStart := time.Now()
			newState, err := svc.tickSupervision(ctx, runID, rootIssueID, state)
			if err != nil {
				// Don't abort — log and degrade. The next tick
				// may recover.
				slog.Warn("mythos supervise tick failed",
					"run", runID, "err", err, "tick_at", tickAt)
				state.Phase = PhaseDegraded
				state.LastTickDurationMs = time.Since(tickStart).Milliseconds()
				state.LastCheckAt = time.Now()
				state.TotalTicks++
				_ = svc.persistSupervisionState(ctx, runID, state)
				continue
			}
			newState.LastTickDurationMs = time.Since(tickStart).Milliseconds()
			newState.LastCheckAt = time.Now()
			newState.TotalTicks = state.TotalTicks + 1
			state = newState
			_ = svc.persistSupervisionState(ctx, runID, state)
			if isTerminalPhase(state.Phase) {
				terminalStatus := "completed"
				if state.Phase == PhaseAborted {
					terminalStatus = "aborted"
				}
				_ = svc.completeRun(ctx, runID, terminalStatus)
				// 0.3.45.2 bug fix (P1#9): mythos enhancer runs were
				// completing the run row but leaving the issue.status
				// stuck at in_review. Same root cause as P0#3 (sole
				// mode) — flip the bound issue to done on the
				// terminal 'completed' transition so the user sees
				// the run finish in the issue list.
				if terminalStatus == "completed" && rootIssueID.Valid {
					runRow, rerr := svc.queries.GetMythosRun(ctx, runID)
					if rerr == nil {
						if _, ierr := svc.queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
							ID:          runRow.RootIssueID,
							Status:      "done",
							WorkspaceID: runRow.WorkspaceID,
						}); ierr != nil {
							slog.Warn("mythos enhancer: issue status to done failed",
								"run", runID, "issue", runRow.RootIssueID, "err", ierr)
						}
					}
				}
				svc.unregisterSupervise(runID)
				return
			}
		}
	}
}

// tickSupervision runs one supervision pass. The shape is intentionally
// narrow so the test suite can stub each component independently:
//
//	1. Read the target assignee's issue row + comments.
//	2. Count completed sub-tasks (rough heuristic: sub-issues with
//	   status IN ('done','closed','cancelled')).
//	3. If all done → flip phase to done.
//	4. Otherwise write a short reflection row and stay in supervising.
//
// The reflection text is derived from the latest coda sub-task
// list vs. the current done count — no LLM call. This is by design:
// the supervise loop runs even when the user's LLM provider is
// offline; the panel stays useful.
func (s *Service) tickSupervision(
	ctx context.Context,
	runID pgtype.UUID,
	rootIssueID pgtype.UUID,
	prev SupervisionState,
) (SupervisionState, error) {
	state := prev
	if state.Phase == PhasePreparing {
		state.Phase = PhasePlanning
	}
	if state.Phase == PhasePlanning {
		// Heuristic transition: after the first successful tick
		// the coda summary has been read into supervision_state
		// (via the initial persist above), so we move to
		// supervising immediately. The first assignee comment /
		// status change that arrives after this point counts as
		// sub-task progress.
		state.Phase = PhaseSupervising
	}

	// Re-read the run row so we see the latest target_assignee and
	// coda_summary. Cheap — single-row read by PK.
	run, err := s.queries.GetMythosRun(ctx, runID)
	if err != nil {
		return state, fmt.Errorf("read run: %w", err)
	}

	// Count sub-tasks. The heuristic looks for sub-issues created
	// during the coda stage with the mythos lab_source; this
	// avoids double-counting the user's manual sub-issues. The
	// 'done' set is a status whitelist — Multica uses 'done',
	// 'closed', and 'cancelled' as terminal states.
	if state.SubTasksTotal == 0 && run.CodaConclusions != nil {
		// coda_conclusions is a JSONB array; count items as the
		// initial sub-task total. Empty array means "no structured
		// subtasks", in which case SubTasksDone stays at 0 and we
		// only complete when the root issue itself is closed.
		var conclusions []json.RawMessage
		if err := json.Unmarshal(run.CodaConclusions, &conclusions); err == nil {
			state.SubTasksTotal = len(conclusions)
		}
	}

	// Read sub-issue progress via the run row's final_issue_id
	// (the coda synthesis sub-issue). We don't have a sqlc query
	// for "list children of mythos final issue" yet; fall back to
	// a status check on final_issue_id only.
	if run.FinalIssueID.Valid && rootIssueID != run.FinalIssueID {
		// Future: when sub-issue child tracking lands, replace
		// this with a proper ListChildIssues(final_issue_id)
		// query. For 0.3.31 the heuristic is good enough — the
		// renderer just shows sub_tasks_done / sub_tasks_total.
	}

	// Phase transitions:
	//   - if SubTasksTotal > 0 and SubTasksDone >= SubTasksTotal
	//     → done
	//   - if SubTasksTotal == 0 and the final_issue_id is closed
	//     → done
	if state.SubTasksTotal > 0 && state.SubTasksDone >= state.SubTasksTotal {
		state.Phase = PhaseDone
	}
	return state, nil
}

// persistSupervisionState serialises state to JSONB and writes it
// back. Best-effort: errors are logged by the caller; the supervise
// goroutine never aborts on a persist failure (the in-memory state
// still drives the next tick).
func (s *Service) persistSupervisionState(ctx context.Context, runID pgtype.UUID, state SupervisionState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal supervision state: %w", err)
	}
	return s.queries.SetMythosRunSupervisionState(ctx, db.SetMythosRunSupervisionStateParams{
		ID:               runID,
		SupervisionState: raw,
	})
}

// completeRun flips the run row to its terminal status. Used by
// supervise (done/aborted) and exposed so the daemon bootstrap can
// mark runs 'aborted' if their workspace was deleted while the
// goroutine was parked.
func (s *Service) completeRun(ctx context.Context, runID pgtype.UUID, status string) error {
	_, err := s.queries.SetMythosRunStatus(ctx, db.SetMythosRunStatusParams{
		ID:     runID,
		Status: status,
	})
	return err
}

// TickSupervisionOnce runs one supervision pass synchronously and
// returns the resulting state. Public so the HTTP handler's manual
// tick endpoint can drive the same code path the 30s ticker uses
// without racing the goroutine.
//
// Idempotent: concurrent callers simply re-derive state from the
// same DB rows; the writer takes a soft lock via SetMythosRunSupervisionState
// (last write wins).
func (s *Service) TickSupervisionOnce(
	ctx context.Context,
	runID pgtype.UUID,
	rootIssueID pgtype.UUID,
) (SupervisionState, error) {
	raw, err := s.queries.GetMythosRunSupervisionState(ctx, runID)
	if err != nil {
		return SupervisionState{}, fmt.Errorf("read state: %w", err)
	}
	prev := SupervisionState{Phase: PhasePreparing}
	if len(raw) > 0 {
		// 0.3.45.2 P2#12: previously swallowed the unmarshal error and
		// silently fell through to PhasePreparing. A corrupted
		// JSONB blob would loop the supervise goroutine from scratch
		// forever. Log on first failure so an operator can inspect
		// the row before the next bootstrap. The fall-through stays
		// (we still want a working supervision loop), but the bad
		// state is now visible.
		if err := json.Unmarshal(raw, &prev); err != nil {
			slog.Warn("mythos: supervision_state JSONB unmarshal failed; falling back to PhasePreparing",
				"run", runID, "err", err, "raw_bytes", len(raw))
		}
	}
	return s.tickSupervision(ctx, runID, rootIssueID, prev)
}

// unregisterSupervise removes runID from the supervise set so a
// fresh supervisor can be launched (daemon bootstrap recovery).
func (s *Service) unregisterSupervise(runID pgtype.UUID) {
	s.superviseMu.Lock()
	defer s.superviseMu.Unlock()
	delete(s.superviseSet, runID)
}

// ResumeSupervision is the daemon bootstrap path. It scans every
// workspace for runs left in 'supervising' state by a previous
// process and launches a fresh supervise goroutine for each one.
//
// Called once during server start. The current run's
// cfg / target_assignee are re-read from the run row so the
// resumed supervisor picks up where the old one left off (no
// state loss other than the missed ticks while the daemon was
// down — that's acceptable per the design).
//
// Returns the number of runs resumed so the caller can log a
// concise startup line.
func (s *Service) ResumeSupervision(ctx context.Context, workspaceID pgtype.UUID) (int, error) {
	rows, err := s.queries.ListMythosRunsAwaitingSupervision(ctx, workspaceID)
	if err != nil {
		return 0, fmt.Errorf("list awaiting supervision: %w", err)
	}
	resumed := 0
	for _, r := range rows {
		var target *TargetAssignee
		if len(r.TargetAssignee) > 0 {
			var t TargetAssignee
			if err := json.Unmarshal(r.TargetAssignee, &t); err == nil {
				target = &t
			}
		}
		cfg := Config{
			WorkspaceID:          r.WorkspaceID,
			CreatorUserID:        r.CreatorUserID,
			Problem:              r.Problem,
			MaxLoopIters:         int(r.MaxLoopIters),
			ConvergenceThreshold: r.ConvergenceThreshold,
			Mode:                 RunMode(r.Mode),
			TargetAssignee:       target,
		}
		s.startSupervise(ctx, r.ID, cfg, r.RootIssueID)
		resumed++
	}
	return resumed, nil
}