-- Swarm topology storage queries. See server/migrations/241_swarm_topology.up.sql
-- for the table shapes. Forward-only — .down.sql is the only rollback path.
--
-- Naming convention mirrors mythos_run.sql (server-side --name: Comment :verb).
-- All queries take an explicit workspace_id or swarm_run_id filter for
-- membership-g enforcement (the handler layer adds the workspace filter; the
-- sqlc layer doesn't see membership — it just sees the row keys).

-- name: CreateSwarmRun :one
INSERT INTO swarm_run (
    workspace_id,
    creator_user_id,
    root_issue_id,
    problem,
    topology_spec,
    max_runtime_hours
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetSwarmRun :one
SELECT * FROM swarm_run WHERE id = $1;

-- name: GetSwarmRunByRootIssue :one
-- Reverse lookup for issue detail page (one swarm per issue by UNIQUE idx).
-- Used by the orchestrator's ResumeSupervision on daemon bootstrap and by
-- the issue detail header pill on render.
SELECT * FROM swarm_run WHERE root_issue_id = $1;

-- name: SetSwarmRunStatus :one
-- Mirrors mythos_run SetMythosRunStatus semantics: terminal status auto-stamps
-- completed_at; non-terminal leaves it NULL.
UPDATE swarm_run
SET status = @status::text,
    completed_at = CASE WHEN @status::text IN ('completed','aborted','failed')
                       THEN now() ELSE completed_at END
WHERE id = @id::uuid
RETURNING *;

-- name: SetSwarmRunPhase :one
-- Phase advance (research → design → ... → done). Done is terminal and
-- must be paired with a status flip to 'completed' by the orchestrator.
UPDATE swarm_run
SET current_phase = @current_phase::text
WHERE id = @id::uuid
RETURNING *;

-- name: SetSwarmRunTopology :exec
-- Re-write topology_spec after the leader's first planning tick.
-- Used by the leader during the preparing → planning transition.
UPDATE swarm_run
SET topology_spec = @topology_spec::jsonb
WHERE id = @id::uuid;

-- name: RecordSwarmInterrupt :one
-- Stamp the user interrupt timestamp + reason on the swarm_run row.
-- A separate row is also written to swarm_interrupt for audit.
UPDATE swarm_run
SET interrupted_at = now(),
    interrupt_reason = @interrupt_reason::text
WHERE id = @id::uuid
RETURNING *;

-- name: ListActiveSwarmRuns :many
-- Daemon bootstrap recovery: scan for runs in any non-terminal status so
-- the orchestrator can ResumeSupervision. Mirrors mythos supervise.go:405.
SELECT * FROM swarm_run
WHERE status IN ('preparing','planning','running','monitoring')
ORDER BY started_at ASC;

-- name: ListSwarmRunsByWorkspace :many
-- Past runs panel (mirrors mythos-view.tsx:507 PastRunsPanel).
SELECT * FROM swarm_run
WHERE workspace_id = $1
ORDER BY started_at DESC
LIMIT $2;

-- name: ListCompletedSwarmRunsForGC :many
-- swarm_gc candidate query: terminal status + older than 7d + not yet
-- archived. Mirrors runtime_gc.go:174 ListExperimentalClaudeRuntimeSessionsExpired.
-- Single-param LIMIT pattern (sqlc requires sequential $1/$2 numbering for
-- composite queries; the batch size lives in Go caller config).
SELECT * FROM swarm_run
WHERE status IN ('completed','aborted','failed')
  AND completed_at < now() - INTERVAL '7 days'
ORDER BY completed_at ASC
LIMIT $1;

-- name: DeleteSwarmRun :exec
-- GC final unlink (after tarGz archive). Called only after the sentinel
-- pattern completes (cf. runtime_gc.go archiveOne).
DELETE FROM swarm_run WHERE id = $1;

-- name: CreateSwarmRole :one
INSERT INTO swarm_role (
    swarm_run_id,
    agent_id,
    role_name,
    role_instructions,
    parent_role_id,
    depends_on
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetSwarmRole :one
SELECT * FROM swarm_role WHERE id = $1;

-- name: ListSwarmRolesByRun :many
-- Topology graph renderer + orchestrator's per-tick DAG walk.
SELECT * FROM swarm_role
WHERE swarm_run_id = $1
ORDER BY created_at ASC;

-- name: ListReadySwarmRolesByRun :many
-- Orchestrator's "what's eligible to claim now" query. The DAG walk
-- uses this + swarm_role.parent_role_id status to enforce ordering.
SELECT * FROM swarm_role
WHERE swarm_run_id = $1
  AND status IN ('ready','running')
ORDER BY created_at ASC;

-- name: SetSwarmRoleStatus :one
UPDATE swarm_role
SET status = @status::text,
    current_step = COALESCE(@current_step::text, current_step),
    last_heartbeat_at = CASE WHEN @status::text IN ('ready','running','idle')
                            THEN now() ELSE last_heartbeat_at END
WHERE id = @id::uuid
RETURNING *;

-- name: TouchSwarmRoleHeartbeat :exec
-- Per-tick liveness signal. Called by the daemon each time it observes
-- the role's agent_task_queue row progressing.
UPDATE swarm_role
SET last_heartbeat_at = now()
WHERE id = $1;

-- name: ArchiveSwarmRolesByRun :exec
-- GC sweep: bulk flip role rows to 'archived' before deleting the
-- agent rows they reference. Soft-deletes preserve the FK for the
-- retention window (7d archive + 30d trash, mirrors runtime_gc).
UPDATE swarm_role SET status = 'archived' WHERE swarm_run_id = $1;

-- name: CreateSwarmRoleMessage :one
INSERT INTO swarm_role_message (
    swarm_run_id,
    from_role_id,
    to_role_id,
    content,
    type
) VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListSwarmRoleMessagesByRun :many
-- Per-run message stream (UI + orchestrator). Capped at 200 most recent
-- to keep payload reasonable for long-running swarms.
SELECT * FROM swarm_role_message
WHERE swarm_run_id = $1
ORDER BY created_at DESC
LIMIT $2;

-- name: MarkSwarmRoleMessageRead :exec
-- Per-role read receipt. Appends (role_id, read_at) to read_by.
UPDATE swarm_role_message
SET read_by = read_by || jsonb_build_array(jsonb_build_object(
    'role_id', @role_id::uuid,
    'read_at', to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
))
WHERE id = @id::uuid;

-- name: CreateSwarmInterrupt :one
-- Audit row written alongside swarm_run.interrupted_at + reason.
INSERT INTO swarm_interrupt (
    swarm_run_id,
    user_id,
    kind,
    payload
) VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListSwarmInterruptsByRun :many
-- Retrospective analysis + UI audit panel.
SELECT * FROM swarm_interrupt
WHERE swarm_run_id = $1
ORDER BY created_at DESC;

-- name: CountActiveRolesByRun :one
-- Orchestrator's "what's still doing work" check (used by phase advance).
SELECT COUNT(*) FROM swarm_role
WHERE swarm_run_id = $1
  AND status IN ('ready','running','idle');

-- name: CountCompletedRolesByRun :one
-- Phase advance gate: all non-archived roles must be 'completed' before
-- the orchestrator can transition to the next phase.
SELECT COUNT(*) FROM swarm_role
WHERE swarm_run_id = $1
  AND status = 'completed';

-- name: DeleteSwarmRoleMessagesOlderThan :exec
-- swarm_gc TTL sweep: drop messages past MessageTTL (30 days). Called
-- alongside ArchiveSwarmRolesByRun in the cleanup cascade. Mirrors the
-- runtime_gc deletion pattern.
DELETE FROM swarm_role_message
WHERE swarm_run_id = $1
  AND created_at < now() - INTERVAL '30 days';

-- name: CancelAgentTasksBySwarmRun :exec
-- Drain in-flight agent_task_queue rows when a swarm_run is aborted.
-- Called from handler.PostSwarmInterrupt (sync drain after SetSwarmRunStatus
-- flips to 'aborted') and from orchestrator.runOrchestratorLoop on terminal
-- status detect (defense-in-depth — covers the gap between user-cancel and
-- the next 30s tick).
--
-- Filters by agent_id IN (the run's role-agents) AND status IN the three
-- active states — terminal rows (completed/failed/cancelled) are untouched
-- so audit trail + history stay intact. The schema has no cancelled_at
-- column, so we just flip status; downstream readers distinguish by status.
--
-- Returns no rows (:exec) because the handler doesn't need to enumerate
-- them — the caller observes via GetSwarmRunStatus / GetAgentTaskList.
UPDATE agent_task_queue
SET status = 'cancelled'
WHERE agent_id IN (SELECT agent_id FROM swarm_role WHERE swarm_run_id = $1)
  AND status IN ('queued', 'dispatched', 'running');