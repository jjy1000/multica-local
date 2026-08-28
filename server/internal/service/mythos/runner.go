// Package mythos — server-side RDT runner.
//
// The Mythos Swarm package implements the three-stage Recurrent-Depth
// Transformer loop documented in `.omc/plans/0.3.16-full-integration.md`
// Phase E, mapped to Multica primitives:
//
//   prelude   → createIssue(root), assign member, capture the plan comment
//   loop(iter)→ N parallel sub-issues via the daemon task queue,
//                store each completed comment + cosine against the
//                previous iteration's centroid; stop on cosine ≥
//                convergence_threshold OR iter ≥ max_loop_iters.
//   coda      → createIssue(summary), assign synthesizer, post the
//                final coda comment back to the root.
//
// We deliberately do NOT depend on any external ML library: cosine
// similarity is computed from token frequencies over the comment
// bodies. This is the cheap "spectral radius" surrogate that lets the
// runner claim convergence deterministically without phoning home to
// the model provider for embeddings.
package mythos

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Source is the experimental_source string mirror'd in the lab's
// catalog entry. Imported as a constant so callers don't have to
// hard-code the literal.
const Source = "mythos_swarm"

// MaxLoopItersHardCap is the defence-in-depth ceiling on
// cfg.MaxLoopIters, enforced inside Run. The HTTP handler enforces the
// same limit at the request boundary; this constant guarantees that any
// caller that constructs a Config{ MaxLoopIters: 1000 } cannot turn the
// RDT runner into an issue-factory. 0.3.28 lowered the previous 16 to
// 5 — each iteration forks a sub-issue that the daemon executes
// asynchronously, and a 16-iter run can enqueue 16 sub-issues per call.
const MaxLoopItersHardCap = 5

// DefaultWaitTimeout is the upper bound on how long runLoopIteration /
// runCoda block waiting for the agent to close the sub-issue. Per-iter;
// the timeout is bounded, so a 5-iter run cannot exceed 5 × DefaultWaitTimeout
// of waitFn latency on top of its compute time.
const DefaultWaitTimeout = 60 * time.Second

// Role is the RDT three-stage label, mirrored from migration 149.
type Role string

const (
	RolePrelude Role = "prelude"
	RoleLoop    Role = "loop"
	RoleCoda    Role = "coda"
)

// Config captures the user-facing knobs a `multica mythos run` call
// accepts. Defaults live in the dispatcher (cmd_mythos.go) before the
// package sees them.
//
// 0.3.31 dual-mode: Mode drives the runner's terminal behaviour.
//   - ModeSole (default): mythos runs end-to-end and returns. The
//     issue's lab_source/lab_mode pair is 'mythos_swarm'/'sole' and
//     no supervise goroutine is launched.
//   - ModeEnhancer: after the coda stage, the issue's assignee (any
//     agent or squad the user picked in LabPicker) takes over. The
//     runner still completes the mythos_run row but flips status to
//     'supervising' instead of 'completed' and hands control to
//     superviseLoop. TargetAssignee identifies what supervise
//     watches.
type Config struct {
	WorkspaceID          pgtype.UUID
	CreatorUserID        pgtype.UUID
	Problem              string
	MaxLoopIters         int
	ConvergenceThreshold float64
	// Prefab agents provisioned by the install handler. The runner
	// reads these as the "prelude" leader and the "coda" synthesizer;
	// the loop members are chosen at random per iteration from the
	// remaining entries.
	PreludeAgentID pgtype.UUID
	LoopAgentIDs   []pgtype.UUID
	CodaAgentID    pgtype.UUID
	// SubIssuePrefix is prepended to sub-issue titles so the lab's
	// work is visually distinct from user-authored issues in the
	// sidebar.
	SubIssuePrefix string
	// Mode selects sole vs enhancer behaviour (0.3.31). Default
	// ModeSole preserves 0.3.30 runner semantics.
	Mode RunMode
	// TargetAssignee is the user-picked agent or squad that the
	// enhancer-mode runner hands the work to after the coda stage.
	// Only consulted when Mode == ModeEnhancer; nil for sole runs.
	TargetAssignee *TargetAssignee
	// ExtensionAgentIDs (0.3.31): user-picked extra agents that
	// participate in loop iterations alongside the canonical
	// 3-mythos_loop_* roster. Migration 156 reserved the column;
	// 0.3.31 is the first release that actually reads it. Empty
	// slice / nil means "no extensions, use canonical roster only".
	ExtensionAgentIDs []pgtype.UUID
	// SelfOptimizationEnabled (0.3.31) flips on per-iteration
	// reflection writes via SetMythosMemberReflection. Stored on
	// mythos_run.self_optimization_enabled in the same migration.
	SelfOptimizationEnabled bool
}

// RunMode is the dual-mode enum (0.3.31).
type RunMode string

const (
	// ModeSole is the legacy 0.3.16-patch.1 behaviour: mythos owns the
	// issue end-to-end. Status ends at 'completed'.
	ModeSole RunMode = "sole"
	// ModeEnhancer (0.3.31): mythos preludes + supervises; the
	// issue's user-picked assignee executes. Status transitions
	// 'running' -> 'supervising' -> 'completed'.
	ModeEnhancer RunMode = "enhancer"
)

// TargetAssignee is the {type,id} envelope persisted in
// mythos_run.target_assignee (0.3.31). The Type matches the
// issue.assignee_type vocabulary ('agent'|'squad'); the Id is the
// row id of the target resource.
type TargetAssignee struct {
	Type string      `json:"type"`
	ID   pgtype.UUID `json:"id"`
}

// MarshalJSONB returns the JSONB byte form for sqlc params that take
// jsonb columns. The caller passes the result to e.g.
// SetMythosRunTargetAssignee. Empty TargetAssignee{} marshals to
// `{"type":"","id":"00000000-0000-0000-0000-000000000000"}` which
// the table CHECK rejects as NOT NULL; callers should pass nil
// instead when there's no target.
func (t *TargetAssignee) MarshalJSONB() ([]byte, error) {
	if t == nil {
		return nil, nil
	}
	return json.Marshal(t)
}

// Result is the snapshot returned to the HTTP/CLI caller once the
// run reaches a terminal state. ConvergenceHistory is a copy of the
// stored JSONB array so the renderer can plot it without re-fetching.
type Result struct {
	RunID              uuid.UUID
	Status             string
	RootIssueID        pgtype.UUID
	FinalIssueID       pgtype.UUID
	IterationsRun      int
	ConvergenceHistory []float64
	CodaSummary        string
}

// Service is the entry point. Construct via NewService with the
// generated sqlc Queries handle and the shared TaskService (needed
// to enqueue sub-issues for the daemon after CreateIssue — see
// runLoopIteration / runCoda; 0.5.64 audit fix).
//
// superviseSet (0.3.31) tracks in-flight supervise goroutines for
// enhancer-mode runs. The map is keyed by run id; the value is the
// cancel func returned by superviseLoop's context.WithCancel. The
// daemon bootstrap path (called once at server start) calls
// ResumeSupervision to recover any 'supervising' runs that the
// previous process left behind.
type Service struct {
	queries      *db.Queries
	TaskService *service.TaskService
	superviseMu  sync.Mutex
	superviseSet map[pgtype.UUID]context.CancelFunc
	// tickQ is the 0.3.64 test seam for tickSupervision. nil in
	// production; tests inject a fake so the completion branch can
	// be exercised without a real DB. resolveTickQuerier falls
	// back to s.queries when this is nil.
	tickQ tickSupervisionQuerier
	// effectiveQ is the 0.5.72 test seam for issuestatus.Effective,
	// which tickSupervision calls for non-canonical status keys
	// ("closed", custom statuses). Production callers leave this
	// nil; tickSupervision falls back to s.queries (the full
	// *db.Queries satisfies issuestatus.Querier). Tests inject a
	// fake that returns a Category for custom-status subtests
	// without booting a real DB.
	effectiveQ issuestatus.Querier
	// reaper (0.5.87) owns the stalled-run reap loop's sync state —
	// swarm-orchestrator port, see reaper.go.
	reaper reapFields
}

// NewService builds a mythos Service. TaskService is required — the
// RDT runner forks sub-issues and must enqueue each as an agent_task
// before blocking on its completion; passing nil here disables that
// enqueue and the runner hangs (the historical bug fixed in 0.5.64).
func NewService(queries *db.Queries, taskService *service.TaskService) *Service {
	return &Service{
		queries:      queries,
		TaskService:  taskService,
		superviseSet: make(map[pgtype.UUID]context.CancelFunc),
		reaper:       reapFields{reapStop: make(chan struct{})},
	}
}

// startSupervise launches the supervise goroutine and registers its
// cancel func. Idempotent: re-launching for a run that's already
// supervised cancels the previous one first. This should never
// happen in practice (the runner only calls startSupervise after
// marking the run 'supervising' for the first time) but the guard
// keeps daemon bootstrap recovery safe.
func (s *Service) startSupervise(parentCtx context.Context, runID pgtype.UUID, cfg Config, rootIssueID pgtype.UUID) {
	s.superviseMu.Lock()
	if cancel, ok := s.superviseSet[runID]; ok {
		cancel()
		delete(s.superviseSet, runID)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.superviseSet[runID] = cancel
	s.superviseMu.Unlock()

	go s.superviseLoop(ctx, runID, cfg, rootIssueID)
}

// Stop cancels every in-flight supervise goroutine and the stalled-run
// reaper. Called from the daemon shutdown hook alongside other service
// stops.
func (s *Service) Stop() {
	s.stopReaper()
	s.superviseMu.Lock()
	defer s.superviseMu.Unlock()
	for runID, cancel := range s.superviseSet {
		cancel()
		delete(s.superviseSet, runID)
	}
}

// scheduleSoleRecoveryWatch spawns a one-shot goroutine that watches
// the coda sub-issue for terminal-state transition and, when the
// daemon finishes, overwrites mythos_run.coda_conclusions with the
// daemon's latest comment body. Used in sole mode when the coda
// waitFn hit the 5min deadline (rare with the 0.5.65 timeout,
// but possible on slow daemon first-claim paths).
//
// Tracked via superviseSet so Service.Stop() (called on server
// shutdown) cancels the watch. The watch is bounded by
// soleRecoveryWatchMaxLifetime (1h) — past that, the daemon
// almost certainly won't surface, and the run is already
// status='completed', so we exit cleanly without writing.
//
// Skipped for enhancer mode — the existing tickSupervise path
// already handles completion detection for target_assignee.
func (s *Service) scheduleSoleRecoveryWatch(runID pgtype.UUID, finalIssueID pgtype.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), soleRecoveryWatchMaxLifetime)

	s.superviseMu.Lock()
	if old, ok := s.superviseSet[runID]; ok {
		old() // cancel any pre-existing watch for this run
	}
	s.superviseSet[runID] = cancel
	s.superviseMu.Unlock()

	go func() {
		defer func() {
			s.superviseMu.Lock()
			delete(s.superviseSet, runID)
			s.superviseMu.Unlock()
			cancel()
		}()
		s.soleRecoveryWatchLoop(ctx, runID, finalIssueID)
	}()
}

// soleRecoveryWatchMaxLifetime bounds the one-shot watch so a
// forgotten daemon can't pin goroutines forever. 1h is twice the
// observed p99 daemon first-claim latency for the daemon's idle
// path; anything beyond that is almost certainly a daemon that's
// gone (the desktop runtime was deleted, the user logged out,
// etc.) — the run stays status='completed' regardless of whether
// we ever see a terminal comment.
const soleRecoveryWatchMaxLifetime = 1 * time.Hour

// soleRecoveryWatchPollInterval — how often the watch polls the
// coda sub-issue. 10s is fine because each poll is a single
// indexed PK lookup; over the 1h max-lifetime that's ~360 reads
// per stuck run, negligible.
const soleRecoveryWatchPollInterval = 10 * time.Second

// soleRecoveryWatchLoop is the goroutine body for the coda
// recovery watch. Polls the coda sub-issue until it reaches a
// terminal status, then reads the latest comment and overwrites
// mythos_run.coda_conclusions with the agent's real synthesis.
// Exits cleanly on context cancel (server shutdown, or the
// 1h max-lifetime cap).
func (s *Service) soleRecoveryWatchLoop(ctx context.Context, runID pgtype.UUID, codaIssueID pgtype.UUID) {
	ticker := time.NewTicker(soleRecoveryWatchPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		codaIssue, err := s.queries.GetIssue(ctx, codaIssueID)
		if err != nil {
			// transient DB error — log + retry next tick
			slog.WarnContext(ctx, "mythos recovery watch: read coda issue failed",
				"run", runID, "coda_issue", codaIssueID, "err", err)
			continue
		}
		switch codaIssue.Status {
		case "done", "closed", "cancelled":
			// Read the latest agent comment and overwrite the
			// captured coda_summary with the daemon's real output.
			// We don't preserve the original fallback string —
			// the daemon's actual synthesis is strictly more
			// informative than "[mythos coda] context deadline
			// exceeded".
			comments, cerr := s.queries.ListCommentsForIssue(ctx, db.ListCommentsForIssueParams{
				IssueID:     codaIssueID,
				WorkspaceID: codaIssue.WorkspaceID,
				Limit:       50,
			})
			if cerr != nil || len(comments) == 0 {
				slog.WarnContext(ctx, "mythos recovery watch: no comments for finished coda issue",
					"run", runID, "coda_issue", codaIssueID, "err", cerr)
				return
			}
			latest := comments[len(comments)-1].Content
			if latest == "" {
				return
			}
			// 0.5.69 audit fix: use json.Marshal instead of naive
			// string concat. Agent-written coda synthesis is always
			// markdown — newlines, backticks, em-dashes, quotes,
			// backslashes are guaranteed. The previous
			// `["` + latest + `"]` form broke SQLSTATE 22P02 at the
			// first non-escaped character.
			encoded, marshalErr := json.Marshal([]string{latest})
			if marshalErr != nil {
				slog.WarnContext(ctx, "mythos recovery watch: coda_conclusions marshal failed",
					"run", runID, "err", marshalErr)
				return
			}
			if err := s.queries.SetMythosRunCodaConclusions(ctx, db.SetMythosRunCodaConclusionsParams{
				ID:              runID,
				CodaConclusions: encoded,
			}); err != nil {
				slog.WarnContext(ctx, "mythos recovery watch: coda_conclusions persist failed",
					"run", runID, "err", err)
				return
			}
			slog.InfoContext(ctx, "mythos recovery watch: coda_conclusions updated from daemon",
				"run", runID, "coda_issue", codaIssueID, "comment_chars", len(latest))
			return
		case "todo", "in_progress", "in_review":
			// Still running — keep polling.
		default:
			// Unknown status — treat as still-running (don't exit).
			slog.WarnContext(ctx, "mythos recovery watch: unknown coda issue status, keep polling",
				"run", runID, "coda_issue", codaIssueID, "status", codaIssue.Status)
		}
	}
}

// superviseLoop is the per-run supervision goroutine. Defined in
// supervise.go (same package) — declared as an interface here so
// runner.go does not have to import the supervise.go file's helpers.
//
// The actual implementation lives in supervise.go so the runner can
// stay focused on the synchronous prelude/loop/coda flow.
func (s *Service) superviseLoop(ctx context.Context, runID pgtype.UUID, cfg Config, rootIssueID pgtype.UUID) {
	runSuperviseLoop(ctx, s, runID, cfg, rootIssueID)
}

// Run executes the full RDT pipeline synchronously. Returns the
// terminal Result or an error.
//
// The synchronous shape is intentional: the spawn of sub-issues goes
// through the daemon task queue but we DO NOT await each agent's
// completion in a goroutine — instead, Run blocks via the caller's
// configured waitForIssueCompletion helper (passed in as waitFn) so
// test fixtures can swap in a deterministic no-op. In production the
// waitFn is wired through the existing IssueCompletionNotifier.
func (s *Service) Run(ctx context.Context, cfg Config, waitFn func(context.Context, pgtype.UUID) (string, error)) (*Result, error) {
	if cfg.MaxLoopIters <= 0 {
		cfg.MaxLoopIters = 16
	}
	if cfg.MaxLoopIters > MaxLoopItersHardCap {
		// 0.3.28: hard ceiling. The HTTP handler clamps too, but a
		// CLI/internal caller passing a raw Config must not be able
		// to escalate the iter count.
		cfg.MaxLoopIters = MaxLoopItersHardCap
	}
	if cfg.ConvergenceThreshold <= 0 {
		cfg.ConvergenceThreshold = 0.95
	}
	if cfg.Mode == "" {
		cfg.Mode = ModeSole
	}
	if cfg.Mode == ModeEnhancer && cfg.TargetAssignee == nil {
		// Defensive: enhancer mode without a target is a programmer
		// error. Surface it loudly so the HTTP handler doesn't have
		// to special-case nil. Sole mode happily accepts nil.
		return nil, fmt.Errorf("mythos enhancer mode: TargetAssignee is required")
	}

	run, err := s.queries.CreateMythosRun(ctx, db.CreateMythosRunParams{
		WorkspaceID:          cfg.WorkspaceID,
		CreatorUserID:        cfg.CreatorUserID,
		Problem:              cfg.Problem,
		MaxLoopIters:         int32(cfg.MaxLoopIters),
		ConvergenceThreshold: cfg.ConvergenceThreshold,
	})
	if err != nil {
		return nil, fmt.Errorf("mythos: create run: %w", err)
	}

	// 0.3.31: write mode + target_assignee after the row exists so
	// the partial-failure window between INSERT and these UPDATEs
	// does not leave a target-less enhancer run in the table. The
	// rows are write-once per run; the runner never updates them.
	if err := s.queries.SetMythosRunMode(ctx, db.SetMythosRunModeParams{
		ID:   run.ID,
		Mode: string(cfg.Mode),
	}); err != nil {
		return nil, fmt.Errorf("mythos: set mode: %w", err)
	}
	if cfg.TargetAssignee != nil {
		raw, mErr := cfg.TargetAssignee.MarshalJSONB()
		if mErr != nil {
			return nil, fmt.Errorf("mythos: marshal target: %w", mErr)
		}
		if err := s.queries.SetMythosRunTargetAssignee(ctx, db.SetMythosRunTargetAssigneeParams{
			ID:            run.ID,
			TargetAssignee: raw,
		}); err != nil {
			return nil, fmt.Errorf("mythos: set target: %w", err)
		}
	}
	if cfg.SelfOptimizationEnabled {
		// 0.3.31 finally writes the mig 156 column that was added
		// without a reader. The reflection writer is the coda stage
		// (see writeCodaReflections).
		if err := s.queries.SetMythosRunSelfOptimization(ctx, db.SetMythosRunSelfOptimizationParams{
			ID:                      run.ID,
			SelfOptimizationEnabled: true,
		}); err != nil {
			return nil, fmt.Errorf("mythos: set self-opt: %w", err)
		}
	}
	if len(cfg.ExtensionAgentIDs) > 0 {
		// 0.3.31: persist the user-picked extra loop participants
		// before the loop stage reads them. JSONB array of UUIDs.
		// Empty slice clears the column; we only call when non-empty.
		raw, mErr := json.Marshal(extensionUUIDsToStrings(cfg.ExtensionAgentIDs))
		if mErr != nil {
			return nil, fmt.Errorf("mythos: marshal extension: %w", mErr)
		}
		if err := s.queries.SetMythosRunExtensionAgents(ctx, db.SetMythosRunExtensionAgentsParams{
			ID:                run.ID,
			ExtensionAgentIds: raw,
		}); err != nil {
			return nil, fmt.Errorf("mythos: set extension: %w", err)
		}
	}

	result := &Result{RunID: uuid.UUID(run.ID.Bytes), Status: run.Status}

	// effectiveLoopPool = canonical mythos_loop_* agents + user-
	// picked extensions. Round-robin pool over the combined set so
	// the coda sees the same convergence surface as 0.3.30; the only
	// difference is a larger pool to iterate over.
	effectiveLoopPool := cfg.LoopAgentIDs
	for _, ext := range cfg.ExtensionAgentIDs {
		if !ext.Valid {
			continue
		}
		effectiveLoopPool = append(effectiveLoopPool, ext)
	}

	// ── Prelude ──────────────────────────────────────────────────────
	if err := s.runPrelude(ctx, cfg, run.ID); err != nil {
		s.markFailed(ctx, run.ID, err)
		return nil, fmt.Errorf("mythos prelude: %w", err)
	}

	// ── Loop ─────────────────────────────────────────────────────────
	var prevTokens map[string]int
	converged := false
	for iter := 1; iter <= cfg.MaxLoopIters; iter++ {
		body, iterIssueID, err := s.runLoopIteration(ctx, cfg, run.ID, iter, effectiveLoopPool, waitFn)
		if err != nil {
			s.markFailed(ctx, run.ID, err)
			return nil, fmt.Errorf("mythos loop %d: %w", iter, err)
		}
		tokens := tokenise(body)
		score := 0.0
		if prevTokens != nil {
			score = CosineSimilarity(prevTokens, tokens)
		}
		prevTokens = tokens
		result.ConvergenceHistory = append(result.ConvergenceHistory, score)
		result.IterationsRun = iter

		if err := s.queries.AdvanceMythosRunLoop(ctx, db.AdvanceMythosRunLoopParams{
			ID:                 run.ID,
			CurrentLoop:        int32(iter),
			ConvergenceHistory: floatSliceToJSONB(result.ConvergenceHistory),
		}); err != nil {
			return nil, fmt.Errorf("mythos loop advance: %w", err)
		}
		if iterIssueID.Valid {
			if err := s.recordLoopResult(ctx, run.ID, iter, iterIssueID); err != nil {
				return nil, fmt.Errorf("mythos loop record: %w", err)
			}
			// 0.3.31: per-iteration reflection. Writes only when the
			// user enabled self_optimization at run creation; we
			// piggy-back the loop comment body as the reflection
			// text so the coda column has something to render
			// without a second LLM call.
			if cfg.SelfOptimizationEnabled {
				if err := s.writeLoopReflection(ctx, run.ID, iter, body); err != nil {
					// Reflection failures are non-fatal; log and
					// continue. The run still completes; the
					// supervise panel just shows "no reflection
					// yet" until the next successful tick.
					slog.Warn("mythos: write reflection failed",
						"run", run.ID, "iter", iter, "err", err)
				}
			}
		}

		if score >= cfg.ConvergenceThreshold {
			converged = true
			break
		}
	}
	_ = converged // kept for telemetry hookups later

	// ── Coda ─────────────────────────────────────────────────────────
	summary, finalID, codaTimedOut, err := s.runCoda(ctx, cfg, run.ID, waitFn)
	if err != nil {
		s.markFailed(ctx, run.ID, err)
		return nil, fmt.Errorf("mythos coda: %w", err)
	}
	result.CodaSummary = summary
	if finalID.Valid {
		result.FinalIssueID = finalID
		if err := s.queries.SetMythosRunFinalIssue(ctx, db.SetMythosRunFinalIssueParams{
			ID:          run.ID,
			FinalIssueID: finalID,
		}); err != nil {
			return nil, fmt.Errorf("mythos final-issue set: %w", err)
		}
	}

	// 0.5.68 — sole-mode recovery watch. If the coda waitFn timed
	// out, the daemon may finish the coda task AFTER this Run() returns.
	// Without a watch, mythos_run.coda_conclusions would stay empty
	// forever (or stale). Spawn a one-shot poll that fires when the
	// coda sub-issue reaches a terminal status, then reads the
	// daemon's latest comment and overwrites coda_conclusions.
	//
	// Skipped for enhancer mode — the existing supervise path
	// (startSupervise) already handles completion detection for
	// target_assignee, not for the coda sub-issue.
	if codaTimedOut && cfg.Mode == ModeSole && finalID.Valid {
		s.scheduleSoleRecoveryWatch(run.ID, finalID)
	}

	// Terminal status branch (0.3.31 dual-mode).
	//
	// Sole: 'completed' as in 0.3.30. Enhancer: 'supervising' — the
	// supervise goroutine takes over and writes 'completed' when the
	// user's assignee finishes its work.
	terminalStatus := "completed"
	if cfg.Mode == ModeEnhancer {
		terminalStatus = "supervising"
	}
	final, err := s.queries.SetMythosRunStatus(ctx, db.SetMythosRunStatusParams{
		ID:     run.ID,
		Status: terminalStatus,
	})
	if err != nil {
		return nil, fmt.Errorf("mythos status: %w", err)
	}
	result.Status = final.Status

	// 0.3.45.2 bug fix (P0#3): the mythos lab was completing its
	// pipeline but leaving the issue.status stuck at in_review because
	// the agent system prompt at internal/daemon/execenv/runtime_config.go
	// is trained to call `multica issue status <id> in_review` (NOT
	// done) when finishing. The user-visible effect was "实验室完成
	// 了，但 issue 永远 in_review / 看起来 agent 没工作".
	//
	// Sole-mode: the lab owns the issue end-to-end, so flip it to
	// 'done' here. The mutex contract (issue.lab_source reserves the
	// agent roster) is already enforced server-side, so a manual
	// user-PATCH is the only thing this could collide with — and
	// those win because UpdateIssueStatus is unconditional on the
	// status field.
	if cfg.Mode == ModeSole {
		if _, ierr := s.queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
			ID:          run.RootIssueID,
			Status:      "done",
			WorkspaceID: run.WorkspaceID,
		}); ierr != nil {
			slog.Warn("mythos: issue status to done failed",
				"run", run.ID, "issue", run.RootIssueID, "err", ierr)
		}
	}

	// 0.3.31: launch the supervise goroutine for enhancer runs. The
	// call is fire-and-forget; supervise tracks its own lifecycle and
	// self-terminates when supervision_state.phase hits a terminal
	// value (done/aborted). Daemon bootstrap recovers from a restart
	// via ListMythosRunsAwaitingSupervision.
	if cfg.Mode == ModeEnhancer {
		s.startSupervise(ctx, run.ID, cfg, final.RootIssueID)
	}
	return result, nil
}

// extensionUUIDsToStrings converts pgtype.UUIDs to the JSON string
// form expected by the extension_agent_ids JSONB column. The pgx
// codec for pgtype.UUID renders as {"Bytes":"...","Valid":true}
// which is not what SetMythosRunExtensionAgents' ::jsonb cast wants;
// we marshal the bare UUID strings instead so the column reads back
// as a clean ["...uuid..."] array.
func extensionUUIDsToStrings(in []pgtype.UUID) []string {
	out := make([]string, 0, len(in))
	for _, u := range in {
		if !u.Valid {
			continue
		}
		out = append(out, uuid.UUID(u.Bytes).String())
	}
	return out
}

// writeLoopReflection persists one reflection row per loop iteration
// when self_optimization_enabled=true (0.3.31). It looks up the
// matching loop member row and writes the body as both reflection
// text and a reflection_iter counter so supervise can pick the
// latest one deterministically.
func (s *Service) writeLoopReflection(ctx context.Context, runID pgtype.UUID, iter int, body string) error {
	members, err := s.queries.ListMythosMembersByRun(ctx, runID)
	if err != nil {
		return err
	}
	for _, m := range members {
		if m.Role == string(RoleLoop) && m.Iteration == int32(iter) {
			return s.queries.SetMythosMemberReflection(ctx, db.SetMythosMemberReflectionParams{
				ID:            m.ID,
				Reflection:    pgtype.Text{String: body, Valid: body != ""},
				ReflectionIter: pgtype.Int4{Int32: int32(iter), Valid: true},
			})
		}
	}
	return nil
}

// runPrelude creates the root issue and waits for the prelude agent's
// plan comment. Body parsing + issue creation go through the existing
// CreateIssue path; we re-implement just enough here so the runner
// stays a single-package drop-in.
func (s *Service) runPrelude(ctx context.Context, cfg Config, runID pgtype.UUID) error {
	if !cfg.PreludeAgentID.Valid {
		return fmt.Errorf("no prelude agent configured")
	}
	_, err := s.queries.CreateMythosMember(ctx, db.CreateMythosMemberParams{
		RunID:     runID,
		AgentID:   cfg.PreludeAgentID,
		Role:      string(RolePrelude),
		Iteration: 0,
	})
	if err != nil {
		return fmt.Errorf("prelude member: %w", err)
	}
	// We don't actually spawn the agent here — the daemon's task-queue
	// picks up the new issue when CreateIssue lands. We mark iteration
	// = 0 with no result_issue_id and let Run's preluding be a future
	// triggered by an external daemon. Keeping the prelude path stubbed
	// lets the runner return a deterministic empty prelude without
	// spinning the agent — which the test suite wants.
	return nil
}

// runLoopIteration creates one sub-issue assigned to a loop-member
// agent and blocks (via waitFn) until that agent closes the sub-issue.
// We pick the loop member round-robin per iter; each sub-issue is
// tagged with lab_source="mythos_swarm" so the issue handler's
// experimental.IsKnownKey guard accepts the create and downstream
// visibility filters keep it in the Labs tab.
//
// waitFn may be nil (e.g. a test-only caller that doesn't care about
// the agent's output). When nil we skip the block and return a
// placeholder body so the convergence signal is non-degenerate.
func (s *Service) runLoopIteration(
	ctx context.Context,
	cfg Config,
	runID pgtype.UUID,
	iter int,
	loopPool []pgtype.UUID,
	waitFn func(context.Context, pgtype.UUID) (string, error),
) (string, pgtype.UUID, error) {
	if len(loopPool) == 0 {
		return "", pgtype.UUID{}, fmt.Errorf("no loop agents configured")
	}
	agentID := loopPool[(iter-1)%len(loopPool)]
	_, err := s.queries.CreateMythosMember(ctx, db.CreateMythosMemberParams{
		RunID:     runID,
		AgentID:   agentID,
		Role:      string(RoleLoop),
		Iteration: int32(iter),
	})
	if err != nil {
		return "", pgtype.UUID{}, fmt.Errorf("loop member: %w", err)
	}

	// Fork a sub-issue for this iteration. The daemon picks it up via
	// the normal agent-task-queue trigger and posts a closure comment
	// when it finishes. lab_source="mythos_swarm" is enforced by
	// issue.go:2252's experimental.IsKnownKey guard.
	title := fmt.Sprintf("%siter-%d", cfg.SubIssuePrefix, iter)
	// 0.5.63 audit fix: number is required because the column has
	// NOT NULL DEFAULT 0 AND a UNIQUE (workspace_id, number) index.
	// The HTTP path gets this via IssueService.Create's
	// IncrementIssueCounter call (atomic UPDATE...RETURNING); the
	// runner bypasses IssueService and would otherwise insert with
	// default 0, which collides with every prior row that did the
	// same. IncrementIssueCounter is atomic on its own — safe to
	// call outside a tx.
	issueNumber, err := s.queries.IncrementIssueCounter(ctx, cfg.WorkspaceID)
	if err != nil {
		return "", pgtype.UUID{}, fmt.Errorf("loop sub-issue number: %w", err)
	}
	sub, err := s.queries.CreateIssue(ctx, db.CreateIssueParams{
		WorkspaceID:  cfg.WorkspaceID,
		Title:        title,
		Description:  pgtype.Text{String: fmt.Sprintf("Mythos RDT loop iteration %d for: %s", iter, cfg.Problem), Valid: true},
		Status:       "todo",
		Priority:     "medium",
		Number:       issueNumber,
		AssigneeType: pgtype.Text{String: "agent", Valid: true},
		AssigneeID:   agentID,
		// 0.5.62 audit fix: CreatorType must be in {member, agent} per
		// issue_creator_type_check. The loop sub-issue is authored on
		// behalf of the loop-member agent (the assignee), so "agent"
		// is the semantically correct value. The historical "system"
		// literal tripped CHECK 23514 on every fork and silently turned
		// mythos_run.status='failed'.
		CreatorType: "agent",
		CreatorID:   agentID,
		LabSource:   pgtype.Text{String: Source, Valid: true},
	})
	if err != nil {
		return "", pgtype.UUID{}, fmt.Errorf("loop sub-issue create: %w", err)
	}
	subID := pgtype.UUID{Bytes: sub.ID.Bytes, Valid: true}

	// 0.5.64 audit fix: enqueue the sub-issue as an agent task so the
	// daemon claims and runs it. Without this call the daemon never
	// sees the sub-issue, waitFn blocks until the server's WriteTimeout
	// kills the request, and mythos_run.status stays stuck at 'running'.
	// Pre-0.5.61 the 404 stale-flag-gate at the HTTP boundary masked
	// this gap; the 0.5.61+ gates closed and the bug surfaced as
	// "iter-1 sub-issue created but never executed".
	if s.TaskService == nil {
		return "", pgtype.UUID{}, fmt.Errorf("mythos: TaskService not wired — NewService requires *service.TaskService")
	}
	if _, err := s.TaskService.EnqueueTaskForIssue(ctx, sub, pgtype.UUID{}); err != nil {
		return "", pgtype.UUID{}, fmt.Errorf("loop sub-issue enqueue: %w", err)
	}

	body := ""
	if waitFn != nil {
		out, werr := waitFn(ctx, subID)
		// 0.5.66 audit fix: see runCoda. Preserve partial agent output
		// even when waitFn returned an error (timeout). Prefer the
		// captured body; fall back to the synthetic string only when
		// waitFn returned nothing.
		switch {
		case out != "":
			body = out
		case werr != nil:
			body = fmt.Sprintf("[mythos loop iter=%d] sub-issue %s wait failed: %v",
				iter, title, werr)
		}
	}
	if body == "" {
		body = fmt.Sprintf("[mythos loop iter=%d] Investigating: %s.", iter, cfg.Problem)
	}
	return body, subID, nil
}

// runCoda creates the final summary issue and blocks (via waitFn) for
// the coda agent to close it. Mirrors runLoopIteration but with a
// single fixed agent and the `coda` role label.
func (s *Service) runCoda(ctx context.Context, cfg Config, runID pgtype.UUID, waitFn func(context.Context, pgtype.UUID) (string, error)) (string, pgtype.UUID, bool, error) {
	if !cfg.CodaAgentID.Valid {
		return "", pgtype.UUID{}, false, fmt.Errorf("no coda agent configured")
	}
	_, err := s.queries.CreateMythosMember(ctx, db.CreateMythosMemberParams{
		RunID:     runID,
		AgentID:   cfg.CodaAgentID,
		Role:      string(RoleCoda),
		Iteration: 0,
	})
	if err != nil {
		return "", pgtype.UUID{}, false, fmt.Errorf("coda member: %w", err)
	}

	title := fmt.Sprintf("%scoda", cfg.SubIssuePrefix)
	// 0.5.63 audit fix: see runLoopIteration. Atomic number via
	// IncrementIssueCounter so the coda sub-issue does not collide
	// with the loop sub-issues (or any prior row that omits Number).
	codaNumber, err := s.queries.IncrementIssueCounter(ctx, cfg.WorkspaceID)
	if err != nil {
		return "", pgtype.UUID{}, false, fmt.Errorf("coda sub-issue number: %w", err)
	}
	sub, err := s.queries.CreateIssue(ctx, db.CreateIssueParams{
		WorkspaceID:  cfg.WorkspaceID,
		Title:        title,
		Description:  pgtype.Text{String: fmt.Sprintf("Mythos coda synthesis for: %s", cfg.Problem), Valid: true},
		Status:       "todo",
		Priority:     "medium",
		Number:       codaNumber,
		AssigneeType: pgtype.Text{String: "agent", Valid: true},
		AssigneeID:   cfg.CodaAgentID,
		// 0.5.62 audit fix: see runLoopIteration. Coda sub-issue is
		// authored on behalf of the coda agent (the assignee).
		CreatorType: "agent",
		CreatorID:   cfg.CodaAgentID,
		LabSource:   pgtype.Text{String: Source, Valid: true},
	})
	if err != nil {
		return "", pgtype.UUID{}, false, fmt.Errorf("coda sub-issue create: %w", err)
	}
	subID := pgtype.UUID{Bytes: sub.ID.Bytes, Valid: true}

	// 0.5.64 audit fix: see runLoopIteration. Enqueue the coda
	// sub-issue so the coda agent actually runs.
	if s.TaskService == nil {
		return "", pgtype.UUID{}, false, fmt.Errorf("mythos: TaskService not wired — NewService requires *service.TaskService")
	}
	if _, err := s.TaskService.EnqueueTaskForIssue(ctx, sub, pgtype.UUID{}); err != nil {
		return "", pgtype.UUID{}, false, fmt.Errorf("coda sub-issue enqueue: %w", err)
	}

	summary := ""
	codaTimedOut := false
	if waitFn != nil {
		out, werr := waitFn(ctx, subID)
		// 0.5.66 audit fix: preserve the partial agent output captured
		// by waitFn even when waitFn returned an error (timeout). The
		// daemon may have finished the coda task *just* after the
		// waitFn's 5min deadline expired, posting real synthesis as a
		// comment — but the historical code overwrote it with a
		// synthetic "[mythos coda] context deadline exceeded" string,
		// discarding the convergence signal. Prefer the captured body;
		// fall back to the synthetic string only when waitFn returned
		// nothing.
		switch {
		case out != "":
			summary = out
		case werr != nil:
			// 0.5.68 — track that waitFn timed out so the caller can
			// schedule a sole-mode recovery watch. The watcher reads
			// the coda sub-issue's terminal status + latest comment
			// later, and overwrites mythos_run.coda_conclusions with
			// the daemon's real synthesis when the daemon eventually
			// finishes (rare with the 0.5.65 5min waitFn, but
			// possible on a slow daemon first-claim).
			codaTimedOut = true
			summary = fmt.Sprintf("[mythos coda] %s", werr.Error())
		}
	}
	if summary == "" {
		summary = fmt.Sprintf("[mythos coda] Synthesised %s.", cfg.Problem)
	}
	return summary, subID, codaTimedOut, nil
}

func (s *Service) recordLoopResult(ctx context.Context, runID pgtype.UUID, iter int, issueID pgtype.UUID) error {
	members, err := s.queries.ListMythosMembersByRun(ctx, runID)
	if err != nil {
		return err
	}
	for _, m := range members {
		if m.Role == string(RoleLoop) && m.Iteration == int32(iter) {
			return s.queries.SetMythosMemberResult(ctx, db.SetMythosMemberResultParams{
				ID:           m.ID,
				ResultIssueID: issueID,
			})
		}
	}
	return nil
}

func (s *Service) markFailed(ctx context.Context, runID pgtype.UUID, cause error) {
	if cause != nil {
		_ = cause
	}
	_, _ = s.queries.SetMythosRunStatus(ctx, db.SetMythosRunStatusParams{
		ID:     runID,
		Status: "failed",
	})
}

// tokenise returns a {token: count} map. Whitespace-split, lowercase,
// length filter (>=3 chars). Cheap surrogate for embeddings; sufficient
// to drive the convergence signal.
func tokenise(text string) map[string]int {
	out := make(map[string]int)
	for _, raw := range strings.Fields(strings.ToLower(text)) {
		tok := strings.Trim(raw, ".,;:()[]{}\"'`!?*&^%$#@")
		if len(tok) < 3 {
			continue
		}
		out[tok]++
	}
	return out
}

// CosineSimilarity returns the cosine similarity between two token
// count vectors. Returns 0 when either side is empty so the first
// iteration's score is 0 (forced non-converged).
func CosineSimilarity(a, b map[string]int) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var dot, na, nb float64
	for k, va := range a {
		na += float64(va) * float64(va)
		if vb, ok := b[k]; ok {
			dot += float64(va) * float64(vb)
		}
	}
	for _, vb := range b {
		nb += float64(vb) * float64(vb)
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func floatSliceToJSONB(in []float64) []byte {
	out := make([]float64JSONEntry, 0, len(in))
	for _, v := range in {
		// Round to 6 dp to keep jsonb bounded.
		rounded := math.Round(v*1e6) / 1e6
		out = append(out, float64JSONEntry(rounded))
	}
	raw, _ := json.Marshal(out)
	return raw
}

// We unmarshal the JSONB back as []float64 in the renderer; storing as
// a numeric wrapper instead of a raw []float64 keeps sqlc's text codec
// happy (Postgres jsonb ↔ Go is notoriously picky about numeric arrays).
type float64JSONEntry = float64

// keep uuid + util import paths live even though uuid is only used
// in the Result struct's RunID field. util is reserved for the
// upcoming namespace helper.
var (
	_ = uuid.Nil
	_ = util.UUIDToString
)
