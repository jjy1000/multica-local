// Package swarm — orchestrator + lifecycle for the swarm topology
// feature (0.5.21).
//
// A swarm is a self-organising multi-agent system that the
// orchestrator bootstraps on demand, runs through a 5-phase machine
// (research → design → implement → review → done), and tears down
// when the run reaches terminal status. The orchestrator mirrors
// mythos/supervise.go's tick + 24h cap pattern but is per-role
// (each role-agent has its own heartbeat) rather than per-issue.
//
// Hard constraints (mirrors mythos hard-constraints block):
//
//   - The orchestrator never touches the LLM dispatch path
//     (server/internal/handler/runtime.go). It only enqueues work
//     via existing PATCH /api/issues/{id}/assignee paths and reads
//     agent_task_queue status. Flag-off blocks NEW runs at the HTTP
//     boundary but does not cancel in-flight role-agents — those
//     self-terminate on swarm_run terminal status or the 72h cap.
//
//   - The orchestrator is per-swarm_run, not per-workspace. Each
//     swarm_run spawns one. Daemon bootstrap (ResumeSupervision)
//     recovers orphaned orchestrators by scanning for non-terminal
//     swarm_run rows.
//
//   - Tick interval is hard-coded at 30 seconds (mirrors
//     mythos SupervisionTickerInterval). Phase advance + heartbeat
//     are the only writes per tick — bounded and predictable.
//
//   - Max 6 roles per swarm (MaxSwarmRoles=6, anti-pattern #1).
//     Enforced both at install (Phase 1) and at runtime bootstrap
//     (the leader author rejects any topology_spec > 6 roles).
//
// Anti-patterns explicitly avoided (per OSS survey + Anthropic Jun
// 2025 "Built a multi-agent research system"):
//  1. No spawn-50 (MaxSwarmRoles=6 cap).
//  2. No last-handoff-wins race (every state = SQL row write).
//  3. No manager self-delegation loop (parent_role_id one-way).
//  4. No group-chat deadlock (explicit phase machine + terminal
//     status; never "wait for someone to say done").
//  5. No checkpoint bloat (checkpoint only at phase boundaries).
//  6. No cross-swarm memory leak (per-swarm scoping, GC reclaims).
package swarm

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// OrchestratorTickerInterval is the wall-clock period between
// orchestrator ticks. Hard-coded — a feature flag here would defeat
// the purpose of having a single supervision cadence (mirrors mythos
// SupervisionTickerInterval at 30s).
const OrchestratorTickerInterval = 30 * time.Second

// OrchestratorMaxLifetime caps a single orchestrator goroutine. After
// this duration the orchestrator writes status='failed' with reason
// 'max_lifetime' and exits, even if some roles have not finished.
// Prevents zombie swarms from accumulating forever.
const OrchestratorMaxLifetime = 72 * time.Hour

// MaxSwarmRoles caps the number of role-agents a single swarm can
// author. Anti-pattern #1 (Anthropic Jun 2025 — spawn-50 antipattern).
// Enforced at install (Phase 1) + runtime bootstrap (leader rejects
// topology_spec > MaxSwarmRoles entries).
const MaxSwarmRoles = 6

// HeartbeatTimeout is the per-role idle threshold. If a role-agent's
// last_heartbeat_at is older than this, the orchestrator marks the
// role 'idle' (waiting on parent_role or stuck). Mirrors Mythos
// supervise 30s tick; longer because per-role work may legitimately
// pause (e.g. waiting for a sibling role to finish).
const HeartbeatTimeout = 5 * time.Minute

// RoleIdleTickLimit is the number of consecutive 'idle' ticks before
// the orchestrator escalates the role to 'failed'. Prevents zombie
// roles from accumulating without a terminal state.
const RoleIdleTickLimit = 10

// ArchiveTTL is the retention window for completed/aborted/failed
// swarm_run rows before swarm_gc archives them. Mirrors runtime_gc
// 30-day session TTL; shortened to 7 days because swarms are
// long-lived by default and the archive size grows faster.
const ArchiveTTL = 7 * 24 * time.Hour

// TrashTTL is the final-unlink TTL after tarGz archival. Mirrors
// runtime_gc 120-day trash.
const TrashTTL = 30 * 24 * time.Hour

// MessageTTL is how long swarm_role_message rows survive. Shorter
// than ArchiveTTL — messages are audit data, not state. After 30
// days they're tarballed with the run archive.
const MessageTTL = 30 * 24 * time.Hour

// SwarmPhase enumerates the lifecycle states the orchestrator
// transitions through. Stored in swarm_run.current_phase.
type SwarmPhase string

const (
	PhaseResearch  SwarmPhase = "research"
	PhaseDesign    SwarmPhase = "design"
	PhaseImplement SwarmPhase = "implement"
	PhaseReview    SwarmPhase = "review"
	PhaseDone      SwarmPhase = "done"
)

// SwarmStatus enumerates the lifecycle states of the swarm_run row.
// Mirrors mythos_run.status CHECK but with 'preparing' (leader about
// to bootstrap) + 'planning' (leader analysing + drafting role plan)
// prepended to the running/monitoring/completed/aborted/failed set.
//
// 0.5.22 audit fix (P1-7): 'planning' and 'monitoring' are reserved
// enum values accepted by the SQL CHECK but with zero writers in the
// current fork. The orchestrator reads 'planning' at orchestrator.go
// to detect bootstrap-mid-transition; 'monitoring' is reserved for a
// future "all roles enqueued, awaiting daemon progress" state that the
// current 5-phase machine collapses into 'running'. Forward-only per
// CLAUDE.md invariant — the enum stays accepted by the CHECK so a
// future writer can land without a migration.
type SwarmStatus string

const (
	StatusPreparing   SwarmStatus = "preparing"
	StatusPlanning    SwarmStatus = "planning"
	StatusRunning     SwarmStatus = "running"
	StatusMonitoring  SwarmStatus = "monitoring"
	StatusCompleted   SwarmStatus = "completed"
	StatusAborted     SwarmStatus = "aborted"
	StatusFailed      SwarmStatus = "failed"
)

// RoleStatus enumerates the lifecycle states of each swarm_role row.
type RoleStatus string

const (
	RoleCreated   RoleStatus = "created"
	RoleReady     RoleStatus = "ready"
	RoleRunning   RoleStatus = "running"
	RoleIdle      RoleStatus = "idle"
	RoleCompleted RoleStatus = "completed"
	RoleFailed    RoleStatus = "failed"
	RoleArchived  RoleStatus = "archived"
)

// MessageType enumerates the message types stored in swarm_role_message.
type MessageType string

const (
	MessageInstruction     MessageType = "instruction"
	MessageProgress        MessageType = "progress"
	MessageRequestHelp     MessageType = "request_help"
	MessageHumanInterrupt  MessageType = "human_interrupt"
	MessageCompletion      MessageType = "completion"
	MessageError           MessageType = "error"
)

// InterruptKind enumerates the kinds of user-initiated interrupts.
type InterruptKind string

const (
	InterruptPause         InterruptKind = "pause"
	InterruptResume        InterruptKind = "resume"
	InterruptCancel        InterruptKind = "cancel"
	InterruptRedirect      InterruptKind = "redirect"
	InterruptInjectMessage InterruptKind = "inject_message"
)

// PhaseOrder is the canonical phase advance sequence. The orchestrator
// uses this to walk from one phase to the next on the completion gate.
// Index 4 (PhaseDone) is terminal — phase advance stops here.
var PhaseOrder = []SwarmPhase{
	PhaseResearch,
	PhaseDesign,
	PhaseImplement,
	PhaseReview,
	PhaseDone,
}

// NextPhase returns the next phase in PhaseOrder, or PhaseDone if
// already at the terminal phase. Used by the orchestrator's phase
// advance gate.
func NextPhase(current SwarmPhase) SwarmPhase {
	for i, p := range PhaseOrder {
		if p == current && i+1 < len(PhaseOrder) {
			return PhaseOrder[i+1]
		}
	}
	return PhaseDone
}

// RoleSpec is the leader-authored shape that flows into swarm_role
// rows at bootstrap. The leader authors these from the issue body
// during the 'planning' phase; the orchestrator persists them in
// 'running'.
type RoleSpec struct {
	Name             string   `json:"name"`              // "researcher", "coder", ...
	Instructions     string   `json:"instructions"`      // role-specific runtime contract
	ParentRoleName   string   `json:"parent_role_name"`  // empty = top of DAG
	DependsOn        []string `json:"depends_on"`        // role_name list
	RuntimeModelHint string   `json:"runtime_model_hint"` // optional; passed to agent create
}

// TopologySpec is the leader-authored swarm shape, persisted to
// swarm_run.topology_spec. The orchestrator reads this at every tick
// to walk the DAG topologically.
type TopologySpec struct {
	Roles []RoleSpec `json:"roles"`
	Notes string     `json:"notes"` // leader's reasoning / plan summary
}

// Orchestrator owns the per-swarm_run goroutine that ticks the
// 5-phase machine. One Orchestrator per active swarm_run. Lifecycle
// follows mythos/supervise.go's pattern: registered on bootstrap,
// unregistered on terminal status, recovered on daemon startup via
// ResumeOrchestration.
type Orchestrator struct {
	RunID         pgtype.UUID
	WorkspaceID   pgtype.UUID
	Cancel        context.CancelFunc
	StartedAt     time.Time
	MaxRuntimeHrs int32
}

// State is the orchestrator's in-memory working set for one tick.
// Persisted across ticks via swarm_run.current_phase + each
// swarm_role.last_heartbeat_at; the orchestrator never holds state
// outside the goroutine.
type State struct {
	Phase        SwarmPhase
	CurrentTick  int64
	IdleTickSeen map[pgtype.UUID]int // role_id -> consecutive idle ticks
}