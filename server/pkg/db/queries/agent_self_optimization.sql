-- 0.3.45.1: agent_self_optimization run lifecycle queries.
--
-- All read paths return the full row set so the renderer / CLI / debug
-- surfaces can render without a second round-trip. Writes use COALESCE
-- (sqlc.narg) for partial-update symmetry with the rest of the issue
-- surface (see UpdateIssue in issue.sql).

-- name: CreateAgentSelfOptRun :one
-- Insert a new run row. status defaults to 'pending'; trigger_kind must
-- be one of the CHECK-allowed values (the caller validates). Returns
-- the full row including server-side defaults (id, started_at).
INSERT INTO agent_self_opt_run (
    workspace_id, status, trigger_kind
) VALUES (
    $1, $2, $3
)
RETURNING *;

-- name: GetAgentSelfOptRun :one
-- Single-row read used by the lab history view's detail panel and by
-- Resume() to fetch the canonical row for a recovered run.
SELECT *
FROM agent_self_opt_run
WHERE id = $1;

-- name: ListAgentSelfOptRunsByWorkspace :many
-- Paginated newest-first listing for /experimental/self-opt-history.
-- The workspace_id predicate makes the tenant invariant a SQL-layer
-- guarantee (mirrors the same defense-in-depth used in ListIssues).
SELECT *
FROM agent_self_opt_run
WHERE workspace_id = $1
ORDER BY started_at DESC
LIMIT $2 OFFSET $3;

-- name: ListPendingAgentSelfOptRuns :many
-- Daemon bootstrap scan for Resume(). Uses the partial index
-- idx_self_opt_run_pending to keep the read cheap.
SELECT *
FROM agent_self_opt_run
WHERE status IN ('pending', 'running')
ORDER BY started_at ASC;

-- name: UpdateAgentSelfOptRunStatus :one
-- Status transition. Pass through to the CHECK constraint; the runner
-- wraps this in advisory-lock-protected state transitions to prevent
-- concurrent updates from two daemons.
UPDATE agent_self_opt_run
SET status = $2,
    finished_at = CASE WHEN $2 IN ('done', 'failed', 'cancelled')
                       THEN now() ELSE finished_at END,
    error_message = $3
WHERE id = $1
RETURNING *;

-- name: UpdateAgentSelfOptRunResult :one
-- Result finalization: caller has the LLM output and the created
-- self-opt issue id. Writes report_md, prompt_suggestions, source
-- count, kb_appendix_path, and created_issue_id in one UPDATE so the
-- history view's row is consistent on first render.
UPDATE agent_self_opt_run
SET prompt_suggestions = $2,
    report_md = $3,
    source_issue_count = $4,
    kb_appendix_path = $5,
    created_issue_id = $6,
    status = 'done',
    finished_at = now()
WHERE id = $1
RETURNING *;

-- name: LastSuccessfulAgentSelfOptRun :one
-- Scheduler input: when was the last successful run for this workspace?
-- Returns sql.ErrNoRows if no run has ever succeeded (the runner
-- treats that as "fire immediately on the next eligible window").
SELECT *
FROM agent_self_opt_run
WHERE workspace_id = $1
  AND status = 'done'
ORDER BY finished_at DESC
LIMIT 1;

-- name: ListDoneIssuesForSelfOpt :many
-- Done-issue scan used by the self-opt runner. Filter chain (every
-- clause honors sqlc.narg so the runner can opt-out by passing nil):
--
--   1. workspace_id   — tenant guard
--   2. status='done'  — only completed tasks (per user spec)
--   3. lab_source IS NULL — exclude lab-bound issues (the runner
--      creates lab_source='agent_self_optimization' issues which are
--      therefore invisible to this scan)
--   4. updated_at >= since — bounded by Service-supplied window
--   5. assignee_id NOT IN (...) — exclude any agent hidden by a
--      Labs flag (visibility table). Empty array is a no-op via
--      the "AND (sqlc.narg IS NULL OR ...)" guard.
--   6. agent name NOT IN (...) — hardcoded safety net for the
--      mythos_*/claude_science*/pythia_oracle agent names that
--      pre-date the visibility table.
--   7. LIMIT — bounded by the runner's MaxIssuesPerRun constant.
SELECT i.id, i.workspace_id, i.title, i.status, i.priority,
       i.assignee_type, i.assignee_id, i.updated_at,
       a.name AS assignee_name
FROM issue i
LEFT JOIN agent a
  ON i.assignee_type = 'agent' AND a.id = i.assignee_id
WHERE i.workspace_id = $1
  AND i.status = 'done'
  AND i.lab_source IS NULL
  AND i.updated_at >= $2
  AND ($3::uuid[] IS NULL OR cardinality($3::uuid[]) = 0
       OR (i.assignee_type = 'agent' AND i.assignee_id NOT IN (
            SELECT unnest($3::uuid[]))))
  AND ($4::text[] IS NULL OR cardinality($4::text[]) = 0
       OR (i.assignee_type = 'agent' AND i.assignee_id NOT IN (
            SELECT id FROM agent WHERE name = ANY($4::text[]))))
ORDER BY i.updated_at DESC
LIMIT $5;

-- name: LockAgentSelfOptRun :one
-- pg_try_advisory_xact_lock(hashtextextended(run_id::text, 0)) — non-
-- blocking. Returns true when this caller now holds the lock; false
-- when another tx holds it. The lock auto-releases on commit/rollback.
-- The Service uses it to keep two daemons (or a daemon + a manual
-- CLI trigger) from running the same workspace concurrently. The
-- lock key is hashed off the run id so every run row has its own
-- lock domain — no global bottleneck.
SELECT pg_try_advisory_xact_lock(hashtextextended($1::text, 0)) AS locked;

-- name: ListOptedInUsers :many
-- 0.3.45.2: per-user opt-in scan used by the Service.Start() boot
-- path. Returns every user_id that has an enabled=true row in
-- experimental_pref for the agent_self_optimization flag key.
-- The daemon turns this list × every workspace into a per-(user,
-- workspace) scheduler ticker so a single opted-in user with one
-- workspace gets one ticker, and three opted-in users in one
-- workspace get three tickers (each checks flagOnForUser on every
-- tick and self-skips on opt-out).
--
-- Deduplicated at the SQL layer with DISTINCT so a user who toggled
-- the flag multiple times is only counted once. The Service trusts
-- the flagOnForUser() re-check on every tick before launching work,
-- so this list is "who opted in at boot" — not "who is currently
-- opted in". Toggling the flag while the daemon is running does not
-- require a restart.
SELECT DISTINCT user_id
FROM experimental_pref
WHERE flag_key = 'agent_self_optimization'
  AND enabled = TRUE;

-- name: GetExperimentalPrefEnabled :one
-- 0.3.45.2: per-tick re-check used by flagOnForUser(). Returns the
-- enabled column directly so the Service avoids importing the full
-- experimental_pref row just to read a bool. sqlc.ErrNoRows means
-- "no opt-in row" → caller treats as opted-out.
SELECT enabled
FROM experimental_pref
WHERE user_id = $1
  AND flag_key = $2;
-- name: UpdateAgentSelfOptRunDeferred :one
-- 0.5.2: park a run whose source data is too thin. status='deferred',
-- deferred_until = the retry window after which the scheduler may
-- re-attempt it. The run stays visible in history with a hint.
UPDATE agent_self_opt_run
SET status = 'deferred',
    deferred_reason = $2,
    deferred_until = $3,
    data_count = $4,
    finished_at = now()
WHERE id = $1
RETURNING *;

-- name: LatestDeferredAgentSelfOptRun :one
-- 0.5.2: the scheduler consults this before firing a new run — if the
-- latest run for the workspace is still deferred (retry window open),
-- the ticker skips rather than stacking another run.
SELECT *
FROM agent_self_opt_run
WHERE workspace_id = $1
  AND status = 'deferred'
ORDER BY started_at DESC
LIMIT 1;

-- name: CountActiveAgentSelfOptRuns :one
-- 0.5.2: how many runs are pending or running for a workspace. The
-- scheduler + manual trigger consult this before creating a new run so
-- concurrent triggers (catch-up tick + manual button + Resume) cannot
-- stack runs that race on the issue-number unique constraint.
SELECT COUNT(*)
FROM agent_self_opt_run
WHERE workspace_id = $1
  AND status IN ('pending', 'running');

-- name: ListChangedSkillsForSelfOpt :many
-- 0.5.3: skill-content scan for the self-opt runner. Skills are optimizable
-- subjects when they have non-empty content AND were touched within the
-- scan window (updated_at >= since). content is the trainable text (the
-- SKILL.md body); description stays a non-trainable summary.
SELECT s.id, s.workspace_id, s.name, s.description, s.content, s.updated_at
FROM skill s
WHERE s.workspace_id = $1
  AND s.content <> ''
  AND s.updated_at >= $2
ORDER BY s.updated_at DESC
LIMIT $3;

-- name: ListChangedSquadsForSelfOpt :many
-- 0.5.3: squad scan for the self-opt runner. Squad instructions (migration
-- 088) are the trainable text; description is a non-trainable summary.
-- Only squads touched within the window are candidates.
SELECT s.id, s.workspace_id, s.name, s.description, s.instructions, s.updated_at
FROM squad s
WHERE s.workspace_id = $1
  AND s.instructions <> ''
  AND s.updated_at >= $2
ORDER BY s.updated_at DESC
LIMIT $3;

-- name: ListChangedAutopilotsForSelfOpt :many
-- 0.5.3: autopilot scan for the self-opt runner. The trainable text is the
-- issue_title_template (what the autopilot generates) + description (its
-- operating brief). Only autopilots that produced at least one completed
-- run within the window are candidates — an autopilot that never fired has
-- no behavioral evidence to optimize on.
SELECT a.id, a.workspace_id, a.title, a.description, a.issue_title_template,
       a.updated_at
FROM autopilot a
WHERE a.workspace_id = $1
  AND a.status <> 'archived'
  AND a.updated_at >= $2
  AND EXISTS (
      SELECT 1 FROM autopilot_run r
      WHERE r.autopilot_id = a.id
        AND r.status = 'completed'
        AND r.completed_at >= $2
  )
ORDER BY a.updated_at DESC
LIMIT $3;
