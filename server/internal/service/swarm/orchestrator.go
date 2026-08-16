// Package swarm — orchestrator goroutine (0.5.21).
//
// The orchestrator owns the per-swarm_run tick loop. Lifecycle:
//
//   Start(ctx, runID)
//     → registerOrchestrator(runID, cancel)
//     → go runOrchestratorLoop(ctx, runID, state)
//
//   runOrchestratorLoop
//     ticker := 30s
//     maxLifetime := 72h
//     for {
//       select {
//       case <-ctx.Done():    return  // graceful shutdown
//       case <-maxLifetime.C: return  // hard cap
//       case <-ticker.C:
//         tick(ctx, runID, state)   // phase advance + heartbeat + role check
//       }
//     }
//
//   tick (per-iteration)
//     1. Read swarm_run + role set
//     2. If terminal status → unregisterOrchestrator + return
//     3. Tick each role: heartbeat touch, idle detect, fail-after-N
//     4. Phase advance gate: count(completed roles in current phase)
//        == total → advance swarm_run.current_phase
//     5. If current_phase == PhaseDone → set status='completed'
//
//   ResumeOrchestration (daemon bootstrap)
//     ListActiveSwarmRuns() → for each, Start a fresh goroutine.
//     Mirrors mythos/supervise.go::ResumeSupervision.
//
//   Stop (server shutdown, wired in cmd/server/main.go)
//     cancel all registered ctxs → runOrchestratorLoop exits cleanly
//     via the ctx.Done() branch.

package swarm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// OrchestratorQuerier is the narrow seam the orchestrator uses to talk
// to the DB. Lets unit tests stub the DB without spinning up a full
// sqlc + pgx stack. Mirrors mythos/supervise.go:294
// tickSupervisionQuerier.
type OrchestratorQuerier interface {
	GetSwarmRun(ctx context.Context, id pgtype.UUID) (db.SwarmRun, error)
	SetSwarmRunStatus(ctx context.Context, arg db.SetSwarmRunStatusParams) (db.SwarmRun, error)
	SetSwarmRunPhase(ctx context.Context, arg db.SetSwarmRunPhaseParams) (db.SwarmRun, error)
	SetSwarmRunPaused(ctx context.Context, arg db.SetSwarmRunPausedParams) (db.SwarmRun, error)
	ListSwarmRolesByRun(ctx context.Context, swarmRunID pgtype.UUID) ([]db.SwarmRole, error)
	ListReadySwarmRolesByRun(ctx context.Context, swarmRunID pgtype.UUID) ([]db.SwarmRole, error)
	ListActiveSwarmRuns(ctx context.Context) ([]db.SwarmRun, error)
	SetSwarmRoleStatus(ctx context.Context, arg db.SetSwarmRoleStatusParams) (db.SwarmRole, error)
	TouchSwarmRoleHeartbeat(ctx context.Context, id pgtype.UUID) error
	CountActiveRolesByRun(ctx context.Context, swarmRunID pgtype.UUID) (int64, error)
	CountCompletedRolesByRun(ctx context.Context, swarmRunID pgtype.UUID) (int64, error)
	RecordSwarmInterrupt(ctx context.Context, arg db.RecordSwarmInterruptParams) (db.SwarmRun, error)

	// FIX 2 (0.5.22): bootstrap writes + enqueue. The orchestrator
	// creates one role-agent per spec.Roles entry, the matching
	// swarm_role row, and (via CreateAgentTask) the agent_task_queue
	// dispatch row when a role's parent is ready.
	CreateAgent(ctx context.Context, arg db.CreateAgentParams) (db.Agent, error)
	CreateSwarmRole(ctx context.Context, arg db.CreateSwarmRoleParams) (db.SwarmRole, error)
	GetSwarmRole(ctx context.Context, id pgtype.UUID) (db.SwarmRole, error)
	CreateAgentTask(ctx context.Context, arg db.CreateAgentTaskParams) (db.AgentTaskQueue, error)

	// FIX 4 (0.5.22): runtime binding pre-check before enqueue. A
	// role-agent with no online runtime can never be claimed by a
	// daemon (claim is keyed on runtime_id), so enqueueReadyRole skips
	// the dispatch + writes an error message when this returns false.
	AgentHasOnlineRuntime(ctx context.Context, agentID pgtype.UUID) (bool, error)

	// FIX 3 (0.5.22): coda summary comment on root_issue_id.
	CreateComment(ctx context.Context, arg db.CreateCommentParams) (db.Comment, error)
	CreateSwarmRoleMessage(ctx context.Context, arg db.CreateSwarmRoleMessageParams) (db.SwarmRoleMessage, error)
	ListSwarmRoleMessagesByRun(ctx context.Context, arg db.ListSwarmRoleMessagesByRunParams) ([]db.SwarmRoleMessage, error)

	// CancelAgentTasksBySwarmRun drains in-flight agent_task_queue rows
	// owned by the run's role-agents when the run is aborted. Called by
	// the handler on user-cancel (sync drain) and by the orchestrator's
	// tick loop on terminal-status detect (defense-in-depth).
	CancelAgentTasksBySwarmRun(ctx context.Context, swarmRunID pgtype.UUID) error
}

// Service owns the registered orchestrators + the goroutines that
// drive them. Constructed once at server boot (cmd/server/main.go) and
// shared across handlers. Mirrors mythos.Service shape.
type Service struct {
	q   OrchestratorQuerier
	log *slog.Logger

	mu      sync.Mutex
	running map[pgtype.UUID]*Orchestrator
}

// NewService builds a swarm Service with the DB seam + a structured
// logger. Caller is responsible for calling Stop() at shutdown.
func NewService(q OrchestratorQuerier, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		q:       q,
		log:     log,
		running: make(map[pgtype.UUID]*Orchestrator),
	}
}

// StartOrchestrator launches a goroutine that ticks the 5-phase machine
// for the given swarm_run. Idempotent: re-calling on a run that
// already has a live orchestrator is a no-op (the existing goroutine
// keeps running; the new call returns nil).
//
// Called from:
//   - install_swarm.go after Bootstrap completes (status flips to
//     'running').
//   - ResumeOrchestration at daemon bootstrap for each active run.
func (s *Service) StartOrchestrator(ctx context.Context, runID pgtype.UUID) error {
	s.mu.Lock()
	if _, exists := s.running[runID]; exists {
		s.mu.Unlock()
		s.log.Debug("swarm orchestrator already running", "run_id", runID.String())
		return nil
	}
	run, err := s.q.GetSwarmRun(ctx, runID)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("get swarm run: %w", err)
	}
	if isTerminal(run.Status) {
		s.mu.Unlock()
		s.log.Debug("swarm orchestrator skipped — terminal status",
			"run_id", runID.String(), "status", run.Status)
		return nil
	}

	loopCtx, cancel := context.WithCancel(context.Background())
	orch := &Orchestrator{
		RunID:         runID,
		WorkspaceID:   run.WorkspaceID,
		Cancel:        cancel,
		StartedAt:     time.Now(),
		MaxRuntimeHrs: run.MaxRuntimeHours,
	}
	s.running[runID] = orch
	s.mu.Unlock()

	go s.runOrchestratorLoop(loopCtx, runID, run.MaxRuntimeHours)
	s.log.Info("swarm orchestrator started",
		"run_id", runID.String(),
		"max_runtime_hours", run.MaxRuntimeHours)
	return nil
}

// Stop cancels every registered orchestrator. Wired to the server
// shutdown sequence in cmd/server/main.go so a graceful shutdown
// drains in-flight goroutines via the ctx.Done() branch.
//
// Mirrors mythos.Service.Stop() at supervise.go:387 (note the method
// is named Stop, not Resume — the daemon bootstrap path is
// ResumeOrchestration).
func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for runID, orch := range s.running {
		orch.Cancel()
		delete(s.running, runID)
	}
	s.log.Info("swarm orchestrator service stopped")
}

// ResumeOrchestration is the daemon bootstrap path. Scans every
// non-terminal swarm_run and launches a fresh orchestrator for each.
// Mirrors mythos/supervise.go::ResumeSupervision (line 405).
//
// Per-workspace call: the handler loops every active workspace. The
// service is shared; the orchestrator set is global. The map is keyed
// by run_id so concurrent calls across workspaces deduplicate
// correctly.
func (s *Service) ResumeOrchestration(ctx context.Context, workspaceID pgtype.UUID) (int, error) {
	runs, err := s.q.ListActiveSwarmRuns(ctx)
	if err != nil {
		return 0, fmt.Errorf("list active swarm runs: %w", err)
	}
	started := 0
	for _, run := range runs {
		if run.WorkspaceID != workspaceID {
			continue
		}
		if err := s.StartOrchestrator(ctx, run.ID); err != nil {
			s.log.Warn("swarm orchestrator resume failed",
				"run_id", run.ID.String(), "err", err.Error())
			continue
		}
		started++
	}
	if started > 0 {
		s.log.Info("swarm orchestrator resume complete",
			"workspace_id", workspaceID.String(), "started", started)
	}
	return started, nil
}

// runOrchestratorLoop is the per-run tick loop. Mirrors
// mythos/supervise.go:115 runSuperviseLoop shape.
func (s *Service) runOrchestratorLoop(ctx context.Context, runID pgtype.UUID, maxRuntimeHrs int32) {
	ticker := time.NewTicker(OrchestratorTickerInterval)
	defer ticker.Stop()

	maxLifetime := time.NewTimer(time.Duration(maxRuntimeHrs) * time.Hour)
	defer maxLifetime.Stop()

	for {
		select {
		case <-ctx.Done():
			s.log.Debug("swarm orchestrator loop exit (ctx cancelled)",
				"run_id", runID.String())
			s.unregister(runID)
			return

		case <-maxLifetime.C:
			s.log.Warn("swarm orchestrator loop exit (max lifetime)",
				"run_id", runID.String())
			// Hard cap: write failure status + record interrupt.
			s.markFailed(ctx, runID, "max_lifetime")
			s.unregister(runID)
			return

		case <-ticker.C:
			if err := s.tick(ctx, runID); err != nil {
				if errors.Is(err, errTerminalStatus) {
					s.log.Info("swarm orchestrator loop exit (terminal)",
						"run_id", runID.String())
					s.unregister(runID)
					return
				}
				s.log.Warn("swarm orchestrator tick failed",
					"run_id", runID.String(), "err", err.Error())
				// Non-terminal error: keep ticking. The next tick
				// will retry; transient DB errors are not fatal.
			}
		}
	}
}

// errTerminalStatus signals runOrchestratorLoop to exit. Returned by
// tick when the DB row's status has flipped to a terminal value
// (between ticks, e.g. via an explicit user cancel).
var errTerminalStatus = errors.New("swarm run reached terminal status")

// tick runs one supervision pass. The shape mirrors
// mythos/supervise.go:198 tickSupervision — read DB state, decide,
// write DB state. The orchestrator never holds state outside the
// goroutine; tick reads from the DB on every call.
func (s *Service) tick(ctx context.Context, runID pgtype.UUID) error {
	run, err := s.q.GetSwarmRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("get swarm run: %w", err)
	}

	// Already terminal? Caller flipped status between ticks (e.g.
	// via user cancel). Exit cleanly.
	if isTerminal(run.Status) {
		return errTerminalStatus
	}

	// FIX 2 (0.5.22): a paused run skips phase advance + task enqueue.
	// The user paused the run; keep the goroutine ticking (so a resume
	// is picked up on the next 30s cycle) but do nothing else — roles
	// stay in their current state and no new agent_task_queue rows are
	// written while paused.
	if run.IsPaused {
		return nil
	}

	// FIX 2 (0.5.22): bootstrap roles from topology_spec on every
	// tick. Idempotent: skips roles that already exist by name. The
	// leader fills topology_spec during the planning phase; before
	// that the spec is empty and bootstrapFromSpec is a no-op.
	if err := s.bootstrapFromSpec(ctx, runID); err != nil {
		s.log.Warn("swarm bootstrap from spec failed",
			"run_id", runID.String(), "err", err.Error())
		// Non-fatal: next tick retries. Transient DB errors must
		// not crash the orchestrator.
	}

	roles, err := s.q.ListSwarmRolesByRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("list roles: %w", err)
	}

	now := time.Now()

	// Per-role lifecycle: heartbeat touch + idle detection + fail
	// after RoleIdleTickLimit consecutive idle ticks.
	for _, role := range roles {
		if RoleStatus(role.Status) == RoleCompleted || RoleStatus(role.Status) == RoleFailed || RoleStatus(role.Status) == RoleArchived {
			continue
		}
		if err := s.tickRole(ctx, runID, role, now); err != nil {
			s.log.Warn("swarm role tick failed",
				"run_id", runID.String(), "role_id", role.ID.String(), "err", err.Error())
		}
		// FIX 2 (0.5.22): enqueue ready roles whose parent is done.
		// Only ready roles are eligible (running means an in-flight
		// dispatch exists; completed/failed are terminal).
		if RoleStatus(role.Status) == RoleReady {
			if err := s.enqueueReadyRole(ctx, run, role); err != nil {
				s.log.Warn("swarm role enqueue failed",
					"run_id", runID.String(), "role_id", role.ID.String(), "err", err.Error())
			}
		}
	}

	// Phase advance gate. Mirrors mythos supervise completion
	// determination (2026-07-28 audit fix).
	if err := s.advancePhase(ctx, runID, run, roles); err != nil {
		return fmt.Errorf("advance phase: %w", err)
	}

	return nil
}

// tickRole runs one role's lifecycle step. Detects heartbeat timeout
// (no heartbeat in HeartbeatTimeout → mark 'idle'), and escalates
// 'idle' to 'failed' after RoleIdleTickLimit consecutive idle ticks.
func (s *Service) tickRole(ctx context.Context, runID pgtype.UUID, role db.SwarmRole, now time.Time) error {
	// Heartbeat touch on running roles.
	if RoleStatus(role.Status) == RoleRunning {
		if err := s.q.TouchSwarmRoleHeartbeat(ctx, role.ID); err != nil {
			return fmt.Errorf("heartbeat touch: %w", err)
		}
		return nil
	}

	// Idle detection: if the role is in 'ready' or 'idle' and hasn't
	// seen a heartbeat in HeartbeatTimeout, increment its idle
	// counter; escalate to 'failed' at RoleIdleTickLimit.
	if RoleStatus(role.Status) == RoleReady || RoleStatus(role.Status) == RoleIdle {
		if !role.LastHeartbeatAt.Valid {
			// No heartbeat yet — just touching parent_role state. Skip
			// idle detection on this tick.
			return nil
		}
		idleSince := now.Sub(role.LastHeartbeatAt.Time)
		if idleSince < HeartbeatTimeout {
			// Not yet idle. Heartbeat touch so the role survives
			// the next tick.
			if err := s.q.TouchSwarmRoleHeartbeat(ctx, role.ID); err != nil {
				return fmt.Errorf("heartbeat touch (idle check): %w", err)
			}
			return nil
		}
		// Idle threshold crossed.
		nextStatus := RoleIdle
		if idleSince > HeartbeatTimeout*time.Duration(RoleIdleTickLimit) {
			nextStatus = RoleFailed
		}
		_, err := s.q.SetSwarmRoleStatus(ctx, db.SetSwarmRoleStatusParams{
			ID:          role.ID,
			Status:      string(nextStatus),
			CurrentStep: "heartbeat timeout",
		})
		if err != nil {
			return fmt.Errorf("set role status: %w", err)
		}
		s.log.Warn("swarm role status flipped",
			"run_id", runID.String(),
			"role_id", role.ID.String(),
			"role_name", role.RoleName,
			"from", string(role.Status),
			"to", string(nextStatus))
	}
	return nil
}

// advancePhase checks the completion gate for the current phase and
// advances swarm_run.current_phase when every role in the phase is
// 'completed'. When the phase reaches PhaseDone, flips the run to
// status='completed'.
//
// The completion gate counts ALL roles (regardless of parent_role_id
// topology) — the orchestrator's Phase advance gate is "every role
// completed", not "every leaf role completed", because the leader
// authored the topology_spec with explicit dependency edges. The DAG
// walk (separate from this gate) handles per-role claim ordering.
func (s *Service) advancePhase(ctx context.Context, runID pgtype.UUID, run db.SwarmRun, roles []db.SwarmRole) error {
	// If any role is in 'failed', the whole phase is failed. Fail
	// fast so the user sees the issue rather than waiting for the
	// 72h cap.
	for _, role := range roles {
		if RoleStatus(role.Status) == RoleFailed {
			if _, err := s.q.SetSwarmRunStatus(ctx, db.SetSwarmRunStatusParams{
				ID:     runID,
				Status: string(StatusFailed),
			}); err != nil {
				return fmt.Errorf("set failed status: %w", err)
			}
			_, _ = s.q.RecordSwarmInterrupt(ctx, db.RecordSwarmInterruptParams{
				ID:              runID,
				InterruptReason: "one or more roles failed",
			})
			s.log.Warn("swarm run failed (role escalation)",
				"run_id", runID.String())
			return nil
		}
	}

	totalRoles := int64(len(roles))
	completedRoles, err := s.q.CountCompletedRolesByRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("count completed: %w", err)
	}
	if completedRoles < totalRoles {
		// Not every role has completed yet.
		return nil
	}

	// All roles completed. Advance phase.
	currentPhase := SwarmPhase(run.CurrentPhase)
	next := NextPhase(currentPhase)

	if next == PhaseDone {
		// Terminal: flip status to 'completed'.
		if _, err := s.q.SetSwarmRunStatus(ctx, db.SetSwarmRunStatusParams{
			ID:     runID,
			Status: string(StatusCompleted),
		}); err != nil {
			return fmt.Errorf("set completed status: %w", err)
		}
		// FIX 3 (0.5.22): post the coda summary as a system comment
		// on the root issue, mirroring mythos's runCoda write at
		// router.go:951-957. Best-effort: a comment write failure
		// does not roll back the terminal status flip.
		if err := s.writeCompletionSummary(ctx, run); err != nil {
			s.log.Warn("swarm coda summary write failed",
				"run_id", runID.String(), "err", err.Error())
		}
		s.log.Info("swarm run completed",
			"run_id", runID.String(), "phases_walked", len(PhaseOrder)-1)
		return nil
	}

	// Non-terminal advance: set current_phase to next, reset all
	// roles to 'ready' so they can be re-enqueued in the next
	// phase (the leader may have different per-phase role sets in
	// future; for now every role runs through every phase).
	if _, err := s.q.SetSwarmRunPhase(ctx, db.SetSwarmRunPhaseParams{
		ID:           runID,
		CurrentPhase: string(next),
	}); err != nil {
		return fmt.Errorf("set phase: %w", err)
	}
	for _, role := range roles {
		if _, err := s.q.SetSwarmRoleStatus(ctx, db.SetSwarmRoleStatusParams{
			ID:          role.ID,
			Status:      string(RoleReady),
			CurrentStep: fmt.Sprintf("phase %s → %s", currentPhase, next),
		}); err != nil {
			return fmt.Errorf("reset role status: %w", err)
		}
	}
	s.log.Info("swarm phase advanced",
		"run_id", runID.String(),
		"from", string(currentPhase),
		"to", string(next))
	return nil
}

// markFailed writes the failed status + records an interrupt for
// audit. Used by the max-lifetime hard cap.
func (s *Service) markFailed(ctx context.Context, runID pgtype.UUID, reason string) {
	if _, err := s.q.SetSwarmRunStatus(ctx, db.SetSwarmRunStatusParams{
		ID:     runID,
		Status: string(StatusFailed),
	}); err != nil {
		s.log.Warn("swarm markFailed status write failed",
			"run_id", runID.String(), "err", err.Error())
	}
	if _, err := s.q.RecordSwarmInterrupt(ctx, db.RecordSwarmInterruptParams{
		ID:              runID,
		InterruptReason: reason,
	}); err != nil {
		s.log.Warn("swarm markFailed interrupt write failed",
			"run_id", runID.String(), "err", err.Error())
	}
}

// bootstrapFromSpec (FIX 2, 0.5.22) creates one role-agent + one
// swarm_role row per spec.Roles entry that the leader authored into
// swarm_run.topology_spec. Idempotent: it lists existing roles by
// name and skips any that already exist, so re-running on every tick
// (or after a partial failure) is safe.
//
// No-op when the spec is empty — the leader fills topology_spec
// during the preparing → planning transition, and the bootstrap only
// fires once the spec has at least one role. This matches the
// install-time pattern documented in multica-creating-swarms
// SKILL.md Phase 1 (the leader authors, the orchestrator bootstraps).
//
// Skill bindings for each role-agent are NOT written here. The
// multica-creating-swarms leader authors agent_skill rows as part of
// its own execution; the orchestrator only owns agent + swarm_role
// creation. Adding role.Skills to RoleSpec is a future schema
// decision (requires widening JSONB spec) — for now we defer to the
// leader's pre-bootstrap skill writes.
//
// If the run is still in 'preparing' or 'planning' after bootstrap,
// we flip it to 'running' so the orchestrator's tick can pick up the
// newly-created ready roles.
func (s *Service) bootstrapFromSpec(ctx context.Context, runID pgtype.UUID) error {
	run, err := s.q.GetSwarmRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("get swarm run: %w", err)
	}
	if isTerminal(run.Status) {
		return nil
	}
	spec, err := TopologySpecFromJSON(run.TopologySpec)
	if err != nil {
		return fmt.Errorf("decode topology spec: %w", err)
	}
	if len(spec.Roles) == 0 {
		return nil
	}
	// Validate the spec before writing any rows — a malformed
	// topology_spec at this point is a leader bug, not a runtime
	// condition we should paper over.
	if verr := ValidateTopologySpec(spec); verr != nil {
		return fmt.Errorf("invalid topology spec: %w", verr)
	}

	existing, err := s.q.ListSwarmRolesByRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("list existing roles: %w", err)
	}
	existingByName := make(map[string]db.SwarmRole, len(existing))
	for _, r := range existing {
		existingByName[r.RoleName] = r
	}

	// Pre-build a name → id index for parent-role resolution. New
	// roles created in this bootstrap pass also contribute (so a
	// spec with ordering by index can resolve its parent's ID even
	// before the parent's row commits).
	nameToID := make(map[string]pgtype.UUID, len(existing))
	for _, r := range existing {
		nameToID[r.RoleName] = r.ID
	}

	for _, roleSpec := range spec.Roles {
		if _, ok := existingByName[roleSpec.Name]; ok {
			continue // already bootstrapped on a prior tick
		}

		// Create the role-agent. RuntimeID is intentionally zero —
		// the daemon claims the agent via the normal task-queue
		// path and binds a runtime then. CustomArgs MUST be a JSON
		// array (audit 2026-08-06; migration 238 repairs historical
		// rows that stored `{}` here).
		agent, aerr := s.q.CreateAgent(ctx, db.CreateAgentParams{
			WorkspaceID:        run.WorkspaceID,
			Name:               "swarm_role_" + roleSpec.Name,
			Description:        fmt.Sprintf("Swarm role agent: %s", roleSpec.Name),
			AvatarUrl:          pgtype.Text{},
			RuntimeMode:        "local",
			RuntimeConfig:      []byte(`{}`),
			RuntimeID:          pgtype.UUID{},
			Visibility:         "workspace",
			MaxConcurrentTasks: 1,
			OwnerID:            run.CreatorUserID,
			Instructions:       roleSpec.Instructions,
			CustomEnv:          []byte(`{}`),
			CustomArgs:         []byte(`[]`),
			McpConfig:          []byte(`{}`),
			Model:              pgtype.Text{String: roleSpec.RuntimeModelHint, Valid: roleSpec.RuntimeModelHint != ""},
			ThinkingLevel:      pgtype.Text{},
			SystemKey:          pgtype.Text{},
		})
		if aerr != nil {
			return fmt.Errorf("create role-agent %q: %w", roleSpec.Name, aerr)
		}

		// Resolve parent_role_id by name. The leader's spec is
		// authoritative — we honour ParentRoleName even if the
		// declared parent is itself a leaf node in the DAG (the
		// leader decides whether the DAG is a chain or a fan-out).
		parentID := pgtype.UUID{}
		if roleSpec.ParentRoleName != "" {
			if id, ok := nameToID[roleSpec.ParentRoleName]; ok {
				parentID = id
			} else {
				s.log.Warn("swarm bootstrap: parent_role_name unresolved",
					"run_id", runID.String(),
					"role", roleSpec.Name,
					"parent", roleSpec.ParentRoleName)
			}
		}

		depsJSON := []byte(`[]`)
		if len(roleSpec.DependsOn) > 0 {
			d, merr := json.Marshal(roleSpec.DependsOn)
			if merr != nil {
				return fmt.Errorf("marshal depends_on for %q: %w", roleSpec.Name, merr)
			}
			depsJSON = d
		}

		swarmRole, rerr := s.q.CreateSwarmRole(ctx, db.CreateSwarmRoleParams{
			SwarmRunID:       runID,
			AgentID:          agent.ID,
			RoleName:         roleSpec.Name,
			RoleInstructions: roleSpec.Instructions,
			ParentRoleID:     parentID,
			DependsOn:        depsJSON,
		})
		if rerr != nil {
			return fmt.Errorf("create swarm_role %q: %w", roleSpec.Name, rerr)
		}
		nameToID[roleSpec.Name] = swarmRole.ID

		// Flip created → ready so the next tick's enqueue pass can
		// claim it (only top-of-DAG roles will actually enqueue
		// until their parents complete, but the state flip is the
		// same for all roles).
		if _, serr := s.q.SetSwarmRoleStatus(ctx, db.SetSwarmRoleStatusParams{
			ID:          swarmRole.ID,
			Status:      string(RoleReady),
			CurrentStep: "bootstrapped by orchestrator",
		}); serr != nil {
			return fmt.Errorf("set role ready %q: %w", roleSpec.Name, serr)
		}
		s.log.Info("swarm role bootstrapped",
			"run_id", runID.String(),
			"role_name", roleSpec.Name,
			"agent_id", agent.ID.String(),
			"role_id", swarmRole.ID.String())
	}

	// If we got here, bootstrap made progress (spec was non-empty
	// and at least one new role was created). Flip the run from
	// preparing/planning → running so the issue header pill flips
	// to the live state.
	if run.Status == string(StatusPreparing) || run.Status == string(StatusPlanning) {
		if _, serr := s.q.SetSwarmRunStatus(ctx, db.SetSwarmRunStatusParams{
			ID:     runID,
			Status: string(StatusRunning),
		}); serr != nil {
			return fmt.Errorf("set run running: %w", serr)
		}
		s.log.Info("swarm run status flipped to running",
			"run_id", runID.String(),
			"from", run.Status)
	}
	return nil
}

// enqueueReadyRole (FIX 2, 0.5.22) creates an agent_task_queue row
// for a ready role, provided its parent (if any) has reached
// 'completed'. Marks the role 'running' so the next tick does not
// re-enqueue. The daemon claims the new task via the normal
// task-queue trigger; progress flows back via SetSwarmRoleStatus.
//
// No-op for roles whose parent is not yet complete — the DAG
// topological walk falls out of the orchestrator's 30s tick: a role
// re-attempts enqueue on every tick until its parent flips to
// 'completed'.
func (s *Service) enqueueReadyRole(ctx context.Context, run db.SwarmRun, role db.SwarmRole) error {
	if role.ParentRoleID.Valid {
		parent, err := s.q.GetSwarmRole(ctx, role.ParentRoleID)
		if err != nil {
			return fmt.Errorf("get parent role: %w", err)
		}
		if parent.Status != string(RoleCompleted) {
			return nil // not ready yet; wait for the next tick
		}
	}

	// FIX 4 (0.5.22): runtime binding pre-check. Enqueueing a task for
	// a role-agent whose runtime_id does not resolve to an online
	// daemon runtime guarantees a silent stall — the daemon claims
	// tasks BY runtime_id, so a NULL/offline runtime can never claim
	// the row. Write an error message and leave the role 'ready' so the
	// next tick retries once a runtime is bound.
	hasRuntime, err := s.q.AgentHasOnlineRuntime(ctx, role.AgentID)
	if err != nil {
		return fmt.Errorf("check role-agent runtime: %w", err)
	}
	if !hasRuntime {
		if _, merr := s.q.CreateSwarmRoleMessage(ctx, db.CreateSwarmRoleMessageParams{
			SwarmRunID: run.ID,
			FromRoleID: pgtype.UUID{},
			ToRoleID:   role.ID,
			Content:    fmt.Sprintf("role-agent %s has no bound daemon runtime", role.RoleName),
			Type:       string(MessageError),
		}); merr != nil {
			return fmt.Errorf("write runtime-missing message: %w", merr)
		}
		s.log.Warn("swarm role enqueue skipped — no bound daemon runtime",
			"run_id", run.ID.String(),
			"role_id", role.ID.String(),
			"role_name", role.RoleName)
		return nil
	}

	_, err = s.q.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID:        role.AgentID,
		RuntimeID:      pgtype.UUID{}, // daemon binds on claim
		IssueID:        run.RootIssueID,
		Priority:       50,
		TriggerSummary: pgtype.Text{String: fmt.Sprintf("[swarm] %s (phase %s)", role.RoleName, run.CurrentPhase), Valid: true},
		HandoffNote:    pgtype.Text{String: role.RoleInstructions, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("create agent task: %w", err)
	}

	if _, err := s.q.SetSwarmRoleStatus(ctx, db.SetSwarmRoleStatusParams{
		ID:          role.ID,
		Status:      string(RoleRunning),
		CurrentStep: "enqueued — awaiting claim",
	}); err != nil {
		return fmt.Errorf("set role running: %w", err)
	}
	s.log.Info("swarm role enqueued",
		"run_id", run.ID.String(),
		"role_id", role.ID.String(),
		"role_name", role.RoleName,
		"phase", run.CurrentPhase)
	return nil
}

// writeCompletionSummary (FIX 3, 0.5.22) posts the coda summary as a
// system comment on root_issue_id when the run reaches PhaseDone.
// Mirrors the mythos coda write pattern referenced at router.go:951
// ("posts the coda summary as an issue comment"). Synthesises:
//
//   - role counters (total / completed / failed)
//   - the most recent role_message rows (capped at 50) as a short
//     digest so the user has a glance-able record
//   - the phase walk length so the reader sees "ran 4 phases"
//
// AuthorType is "system" (zero AuthorID — the convention from
// issue_child_done.go:170-178 for system-authored comments on
// issues). Failure is non-fatal; the orchestrator already wrote
// status='completed' before calling this, so a comment write miss
// does not roll back the terminal flip.
func (s *Service) writeCompletionSummary(ctx context.Context, run db.SwarmRun) error {
	roles, err := s.q.ListSwarmRolesByRun(ctx, run.ID)
	if err != nil {
		return fmt.Errorf("list roles for summary: %w", err)
	}
	totalRoles := len(roles)
	completedRoles := 0
	failedRoles := 0
	for _, r := range roles {
		switch RoleStatus(r.Status) {
		case RoleCompleted:
			completedRoles++
		case RoleFailed:
			failedRoles++
		}
	}

	messages, err := s.q.ListSwarmRoleMessagesByRun(ctx, db.ListSwarmRoleMessagesByRunParams{
		SwarmRunID: run.ID,
		Limit:      50,
	})
	if err != nil {
		// Non-fatal: continue with an empty message list so the
		// user still gets the role-counter summary.
		s.log.Warn("swarm coda: list messages failed",
			"run_id", run.ID.String(), "err", err.Error())
		messages = nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[swarm coda] Run completed across %d phases.", len(PhaseOrder)-1)
	fmt.Fprintf(&b, "\n\nRoles: %d total — %d completed, %d failed.",
		totalRoles, completedRoles, failedRoles)
	if len(messages) > 0 {
		fmt.Fprintf(&b, "\n\nLast %d message(s) captured for audit:", len(messages))
		for _, m := range messages {
			fmt.Fprintf(&b, "\n- [%s] %s", m.Type, truncateForSummary(m.Content, 200))
		}
	}

	_, err = s.q.CreateComment(ctx, db.CreateCommentParams{
		IssueID:      run.RootIssueID,
		WorkspaceID:  run.WorkspaceID,
		AuthorType:   "system",
		AuthorID:     pgtype.UUID{}, // zero UUID: system-authored convention
		Content:      b.String(),
		Type:         "system",
		ParentID:     pgtype.UUID{},
		SourceTaskID: pgtype.UUID{},
	})
	if err != nil {
		return fmt.Errorf("create coda comment: %w", err)
	}
	s.log.Info("swarm coda summary posted",
		"run_id", run.ID.String(),
		"issue_id", run.RootIssueID.String())
	return nil
}

// truncateForSummary clips a message body to keep the coda comment
// readable. The 200-char cap leaves room for ~50 messages in the
// payload without the comment becoming a wall of text.
func truncateForSummary(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// unregister removes an orchestrator from the running map. Called on
// every loop exit (ctx cancel, terminal status, max lifetime).
func (s *Service) unregister(runID pgtype.UUID) {
	s.mu.Lock()
	delete(s.running, runID)
	s.mu.Unlock()
}

// isTerminal returns true for any swarm status that ends the run.
// Mirrors the CHECK constraint in migration 241.
func isTerminal(status string) bool {
	switch SwarmStatus(status) {
	case StatusCompleted, StatusAborted, StatusFailed:
		return true
	}
	return false
}

// TopologySpecFromJSON decodes swarm_run.topology_spec into a typed
// shape. The orchestrator reads this on bootstrap + on every tick.
// Used by install_swarm.go after the leader authors the spec.
func TopologySpecFromJSON(raw []byte) (TopologySpec, error) {
	var spec TopologySpec
	if len(raw) == 0 {
		return spec, nil
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		return spec, fmt.Errorf("decode topology spec: %w", err)
	}
	return spec, nil
}

// ValidateTopologySpec enforces the MaxSwarmRoles cap and the DAG
// shape invariants (no cycles, no missing parent references). Called
// by the orchestrator at bootstrap; rejects the bootstrap attempt
// with an error if the spec is invalid.
func ValidateTopologySpec(spec TopologySpec) error {
	if len(spec.Roles) == 0 {
		return errors.New("topology spec has no roles")
	}
	if len(spec.Roles) > MaxSwarmRoles {
		return fmt.Errorf("topology spec has %d roles (cap %d)",
			len(spec.Roles), MaxSwarmRoles)
	}
	names := make(map[string]bool, len(spec.Roles))
	for _, role := range spec.Roles {
		if role.Name == "" {
			return errors.New("role missing name")
		}
		if names[role.Name] {
			return fmt.Errorf("duplicate role name %q", role.Name)
		}
		names[role.Name] = true
	}
	// DAG check: every parent_role_name / depends_on entry must
	// resolve to a declared role. We do a simple linear scan; the
	// topology is bounded at MaxSwarmRoles=6 so O(n^2) is fine.
	for _, role := range spec.Roles {
		if role.ParentRoleName != "" && !names[role.ParentRoleName] {
			return fmt.Errorf("role %q parent %q not declared",
				role.Name, role.ParentRoleName)
		}
		for _, dep := range role.DependsOn {
			if !names[dep] {
				return fmt.Errorf("role %q depends on undeclared role %q",
					role.Name, dep)
			}
			if dep == role.Name {
				return fmt.Errorf("role %q self-dependency", role.Name)
			}
		}
	}
	return nil
}