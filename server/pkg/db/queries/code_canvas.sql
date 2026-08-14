-- 0.5.18 M4: code_canvas issue-bound artifact persistence. Mirrors the
-- pythia_forecast_run query shapes. Each row is one rendered canvas for
-- one issue; the list read is newest-first with a caller-supplied cap.

-- name: CreateCodeCanvasArtifact :one
INSERT INTO code_canvas_artifact (workspace_id, issue_id, code, language, html)
VALUES ($1, $2, $3, $4, $5) RETURNING *;

-- name: ListCodeCanvasArtifactsByIssue :many
SELECT * FROM code_canvas_artifact
WHERE issue_id = $1 ORDER BY created_at DESC LIMIT $2;
