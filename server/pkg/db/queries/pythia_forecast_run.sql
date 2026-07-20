-- 0.3.55: Pythia per-issue forecast persistence. Backs
-- POST /api/experimental/pythia-oracle/forecast/issue persisting each
-- completed deliberation, and the report surface's per-issue history
-- list. Mirrors the agent_self_opt_run query shapes.

-- name: CreatePythiaForecastRun :one
INSERT INTO pythia_forecast_run (
    workspace_id, issue_id, rounds, source, envelopes
) VALUES (
    $1, $2, $3, $4, $5
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
