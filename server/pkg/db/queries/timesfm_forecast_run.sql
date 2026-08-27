-- 0.5.82 WL2: TimesFM per-issue forecast persistence. Backs
-- POST /api/experimental/timesfm/forecast/issue persisting each
-- completed engine response, and the read-only lab view's per-issue
-- history list (route suffix timesfm-lab). Mirrors the
-- pythia_forecast_run query shapes (pythia_forecast_run.sql).

-- name: CreateTimesfmForecastRun :one
INSERT INTO timesfm_forecast_run (
    workspace_id, issue_id, horizons, provenance, result
) VALUES (
    $1, $2, $3, $4, $5
)
RETURNING *;

-- name: GetTimesfmForecastRun :one
SELECT *
FROM timesfm_forecast_run
WHERE id = $1;

-- name: ListTimesfmForecastRunsByIssue :many
SELECT *
FROM timesfm_forecast_run
WHERE issue_id = $1
ORDER BY created_at DESC
LIMIT $2;
