-- Pythia per-issue forecast persistence (0.3.55) + continuation contract
-- (0.5.111, migration 290). Backs POST
-- /api/experimental/pythia-oracle/forecast/issue (now async: the row is
-- created UPFRONT with status='running' and updated per round, so the
-- panel stream can replay + live-tail), the per-issue history list, and
-- the continuation lineage (parent_run_id).
--
-- Mirrors the agent_self_opt_run query shapes.

-- name: CreatePythiaForecastRun :one
INSERT INTO pythia_forecast_run (
    workspace_id, issue_id, rounds, source, envelopes,
    parent_run_id, run_kind, variables, status
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9
)
RETURNING *;

-- name: GetPythiaForecastRun :one
SELECT *
FROM pythia_forecast_run
WHERE id = $1;

-- name: ListPythiaForecastRunsByIssue :many
SELECT *
FROM pythia_forecast_run
WHERE issue_id = $1
ORDER BY created_at DESC
LIMIT $2;

-- name: UpdatePythiaForecastRunProgress :one
-- Per-round progress write (0.5.111): called after EVERY landed round so a
-- mid-run crash / restart leaves the completed rounds readable (the SocialSim
-- "轮完成即时持久化" law — reconnect must not lose rounds).
UPDATE pythia_forecast_run
SET rounds = $2,
    source = $3,
    envelopes = $4,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: FinalizePythiaForecastRun :one
-- Terminal write (0.5.111): stores the synthesized conclusion report and
-- flips the row to its terminal status (completed | failed).
UPDATE pythia_forecast_run
SET report = $2,
    status = $3,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetPythiaForecastRunStatus :one
-- Cancel / sweep write (0.5.111): flips the row to 'aborted' without
-- touching report content.
UPDATE pythia_forecast_run
SET status = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: AbandonStalePythiaForecastRuns :execrows
-- Self-heal sweep: a 'running' row untouched for >15 minutes belongs to a
-- goroutine that died with its server (crash / restart mid-run). Mark it
-- aborted when a new run starts on the same issue so the runs list never
-- shows a phantom "running" entry.
UPDATE pythia_forecast_run
SET status = 'aborted',
    updated_at = now()
WHERE issue_id = $1
  AND status = 'running'
  AND updated_at < now() - interval '15 minutes';

-- name: SetPythiaForecastRunReportComment :one
-- 0.5.86: idempotency marker for the issue report writeback — the
-- handler posts the report comment, then records its id here.
-- COALESCE makes this exactly-once: a run whose marker is already set
-- KEEPS it (the $2 value is ignored), and the query still returns the
-- row instead of "no rows", so caller re-entry is a harmless no-op.
UPDATE pythia_forecast_run
SET report_comment_id = COALESCE(report_comment_id, $2)
WHERE id = $1
RETURNING *;
