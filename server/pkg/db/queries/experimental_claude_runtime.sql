-- name: InsertExperimentalClaudeRuntimeSession :one
INSERT INTO experimental_claude_runtime_session
  (workspace_id, agent_id, issue_id, language, code)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateExperimentalClaudeRuntimeSessionFinished :one
UPDATE experimental_claude_runtime_session
SET
  status = $2,
  exit_code = $3,
  stdout = $4,
  stderr = $5,
  duration_ms = $6,
  started_at = $7,
  finished_at = $8
WHERE id = $1
RETURNING *;

-- name: GetExperimentalClaudeRuntimeSession :one
SELECT * FROM experimental_claude_runtime_session
WHERE id = $1;

-- name: ListExperimentalClaudeRuntimeSessionsByWorkspace :many
SELECT * FROM experimental_claude_runtime_session
WHERE workspace_id = $1
ORDER BY created_at DESC
LIMIT 50;

-- name: ListExperimentalClaudeRuntimeSessionsByIssue :many
-- 0.3.45.8: used by the Claude Lab "产物" page to filter the session
-- list to the currently focused issue. The agent task that produced
-- the report above (and any sibling research snippets) is what the
-- user came here to inspect; showing the full workspace session list
-- is misleading because most rows belong to unrelated chat sessions.
-- Filter by both workspace_id (defense-in-depth; the router already
-- gates membership) and issue_id, newest first, with an explicit
-- LIMIT so a runaway issue does not blow the response budget.
SELECT * FROM experimental_claude_runtime_session
WHERE workspace_id = $1 AND issue_id = $2
ORDER BY created_at DESC
LIMIT $3;

-- name: DeleteExperimentalClaudeRuntimeSession :exec
DELETE FROM experimental_claude_runtime_session
WHERE id = $1;

-- name: ListExperimentalClaudeRuntimeSessionsExpired :many
SELECT * FROM experimental_claude_runtime_session
WHERE expires_at < now()
ORDER BY expires_at ASC
LIMIT 100;

-- name: InsertExperimentalRuntimeArtifact :one
INSERT INTO experimental_runtime_artifact
  (session_id, workspace_id, name, kind, bytes, sha256, path)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListExperimentalRuntimeArtifactsBySession :many
SELECT * FROM experimental_runtime_artifact
WHERE session_id = $1
ORDER BY created_at ASC;

-- name: GetExperimentalRuntimeArtifact :one
SELECT * FROM experimental_runtime_artifact
WHERE id = $1;
