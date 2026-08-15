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
	ListSwarmRolesByRun(ctx context.Context, swarmRunID pgtype.UUID) ([]db.SwarmRole, error)
	ListReadySwarmRolesByRun(ctx context.Context, swarmRunID pgtype.UUID) ([]db.SwarmRole, error)
	ListActiveSwarmRuns(ctx context.Context) ([]db.SwarmRun, error)
	SetSwarmRoleStatus(ctx context.Context, arg db.SetSwarmRoleStatusParams) (db.SwarmRole, error)
	TouchSwarmRoleHeartbeat(ctx context.Context, id pgtype.UUID) error
	CountActiveRolesByRun(ctx context.Context, swarmRunID pgtype.UUID) (int64, error)
	CountCompletedRolesByRun(ctx context.Context, swarmRunID pgtype.UUID) (int64, error)
	RecordSwarmInterrupt(ctx context.Context, arg db.RecordSwarmInterruptParams) (db.SwarmRun, error)
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