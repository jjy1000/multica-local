-- Mythos Swarm storage queries. See server/migrations/149_mythos_run.up.sql
-- for the table shape. Forward-only — .down.sql is the only rollback path.

-- name: CreateMythosRun :one
INSERT INTO mythos_run (
    workspace_id,
    creator_user_id,
    problem,
    max_loop_iters,
    convergence_threshold
) VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetMythosRun :one
SELECT * FROM mythos_run WHERE id = $1;

-- name: SetMythosRunStatus :one
UPDATE mythos_run
SET status = @status,
    completed_at = CASE WHEN @status IN ('completed','aborted','failed') THEN now() ELSE completed_at END
WHERE id = @id::uuid
RETURNING *;

-- name: SetMythosRunRootIssue :exec
UPDATE mythos_run SET root_issue_id = $2 WHERE id = $1;

-- name: SetMythosRunFinalIssue :exec
UPDATE mythos_run SET final_issue_id = $2 WHERE id = $1;

-- name: AdvanceMythosRunLoop :exec
UPDATE mythos_run
SET current_loop = @current_loop::int,
    convergence_history = @convergence_history::jsonb
WHERE id = @id::uuid;

-- name: ListMythosRunsByWorkspace :many
SELECT * FROM mythos_run
WHERE workspace_id = $1
ORDER BY started_at DESC
LIMIT $2;

-- name: SetMythosRunExtensionAgents :exec
-- 0.3.29: persist the user-picked extra loop participants as a JSONB
-- UUID array. Empty array clears the field. The runner validates
-- each entry is a real agent in the active workspace before calling
-- this — the SQL side does no further checks.
UPDATE mythos_run
SET extension_agent_ids = @extension_agent_ids::jsonb
WHERE id = @id::uuid;

-- name: SetMythosRunSelfOptimization :exec
-- 0.3.29: persist the self-reflection on/off flag. Stored verbatim
-- (no validation); the runner reads it as a boolean.
UPDATE mythos_run
SET self_optimization_enabled = @self_optimization_enabled
WHERE id = @id::uuid;

-- name: SetMythosRunCodaConclusions :exec
-- 0.3.29: persist the coda agent's structured conclusions JSONB
-- array. Caller provides the JSONB bytes; the runner does no
-- schema validation (the coda agent is trusted to emit well-
-- shaped strings).
UPDATE mythos_run
SET coda_conclusions = @coda_conclusions::jsonb
WHERE id = @id::uuid;

-- name: CreateMythosMember :one
INSERT INTO mythos_members (
    run_id,
    agent_id,
    role,
    iteration
) VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListMythosMembersByRun :many
SELECT * FROM mythos_members
WHERE run_id = $1
ORDER BY iteration ASC, role ASC;

-- name: SetMythosMemberResult :exec
UPDATE mythos_members SET result_issue_id = $2 WHERE id = $1;

-- name: SetMythosMemberReflection :exec
-- 0.3.29: write the coda agent's per-iteration reflection onto the
-- matching loop member row. Nullable so the runner can clear it
-- by passing '' and we turn that into NULL inside the runner.
-- 0.3.31: also write reflection_iter so supervise can pick the
-- latest reflection deterministically under concurrent writes.
UPDATE mythos_members
SET reflection = @reflection,
    reflection_iter = @reflection_iter
WHERE id = @id;

-- name: ListExtensionAgentUUIDs :many
-- 0.3.29: read the extension_agent_ids JSONB column back as a slice
-- of UUID strings. The Go side json.Unmarshal's the JSONB to a
-- []string via the pgx codec; this query just returns the column.
SELECT extension_agent_ids
FROM mythos_run
WHERE id = $1;

-- name: SetMythosRunMode :exec
-- 0.3.31: persist the dual-mode flag ('sole'|'enhancer') at run
-- creation. The runner passes the value through from Config.Mode;
-- the SQL side does no validation beyond the table CHECK.
UPDATE mythos_run
SET mode = @mode
WHERE id = @id::uuid;

-- name: SetMythosRunTargetAssignee :exec
-- 0.3.31: persist the enhancer-mode target (JSONB {type, id}). The
-- runner marshals the struct; SQL stores the bytes verbatim.
UPDATE mythos_run
SET target_assignee = @target_assignee::jsonb
WHERE id = @id::uuid;

-- name: SetMythosRunSupervisionState :exec
-- 0.3.31: supervise goroutine writes a fresh snapshot every tick.
-- JSONB blob; the runner/scheduler never reads individual keys via
-- SQL, it always json.Unmarshal's the whole struct.
UPDATE mythos_run
SET supervision_state = @supervision_state::jsonb
WHERE id = @id::uuid;

-- name: GetMythosRunSupervisionState :one
-- 0.3.31: supervise ticker reads the current snapshot to decide
-- whether to continue / terminate. One row, one column.
SELECT supervision_state
FROM mythos_run
WHERE id = $1;

-- name: ListMythosRunsAwaitingSupervision :many
-- 0.3.31: daemon bootstrap recovery. Finds runs that were left in
-- 'supervising' state by a previous daemon process so they can be
-- re-scheduled. workspace_id param scopes to the active workspace.
SELECT *
FROM mythos_run
WHERE workspace_id = $1
  AND mode = 'enhancer'
  AND status = 'supervising'
ORDER BY started_at ASC;

-- name: ListStalledMythosRunsForGC :many
-- 0.5.87 async-engine unification (swarm orchestrator port): mirrors
-- ListStalledSwarmRunsForGC (swarm_run.sql). ResumeSupervision only
-- re-adopts status='supervising' rows; a 'running' row orphaned by a
-- mid-pipeline restart has no recovery path and can never terminate —
-- the exact zombie class migration 283 reaped one-shot on the swarm
-- side and the dev-record's "stuck running since 08-24" rows here.
-- Two stall clocks, one per status:
--   - 'running' (sole pipeline / pre-coda enhancer): no supervision
--     heartbeat exists, so started_at is the only honest clock. The
--     in-process RDT pipeline is bounded well under 24h (MaxLoopIters
--     hard cap 5 × DefaultWaitTimeout + the 5min coda deadline), and
--     24h also matches the supervise goroutine's own max-lifetime.
--   - 'supervising': the live goroutine refreshes
--     supervision_state.last_check_at every 30s tick, so a heartbeat
--     staler than 1h means the goroutine died without a resume. The
--     1h window is ~120 ticks — a healthy goroutine (fresh after boot
--     resume within 30s) can never be reaped by a 6h-interval sweep.
--     COALESCE covers rows whose state predates the t=0 heartbeat
--     stamp; started_at keeps those on the conservative clock.
SELECT *
FROM mythos_run
WHERE status IN ('running', 'supervising')
  AND (
    (status = 'running'
     AND started_at < now() - interval '24 hours')
    OR
    (status = 'supervising'
     AND COALESCE((supervision_state ->> 'last_check_at')::timestamptz, started_at) < now() - interval '1 hour')
  )
ORDER BY started_at ASC
LIMIT $1;

-- name: ListMythosRunsByIssueAndWorkspace :many
-- 0.3.31: GET /api/issues/{id}/mythos-runs lookup for the IssueLabsSection
-- supervise panel. Filters by root_issue_id + workspace_id and returns
-- the freshest 5 rows so a long-running enhancer issue does not grow
-- an unbounded list. The panel reads the first row only.
SELECT *
FROM mythos_run
WHERE root_issue_id = $1
  AND workspace_id = $2
ORDER BY started_at DESC
LIMIT 5;
