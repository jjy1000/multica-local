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

-- name: UpdateExperimentalClaudeRuntimeSessionSummary :exec
-- 0.5.114: migration 151 shipped the summary column but no writer ever
-- filled it (audit gap). Best-effort digest write after the artifact
-- comment composes, so workspace session lists stay one-line readable.
UPDATE experimental_claude_runtime_session
SET summary = $2
WHERE id = $1;

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
-- 0.5.114: write the mig-156 issue_id column — it shipped for exactly
-- this "filter artifacts by issue without joining" purpose but the
-- INSERT never wired it (dead column audit). Sessions carry the
-- originating issue; the handler passes it through so the issue-first
-- embed lists a run's artifacts with one workspace-scoped query.
INSERT INTO experimental_runtime_artifact
  (session_id, workspace_id, issue_id, name, kind, bytes, sha256, path)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ListExperimentalRuntimeArtifactsByIssue :many
-- 0.5.114 issue-first embed: newest artifacts across a lab-bound
-- issue's sandbox sessions. Workspace-scoped (defense-in-depth; the
-- router gates membership) with an explicit LIMIT budget.
SELECT * FROM experimental_runtime_artifact
WHERE workspace_id = $1 AND issue_id = $2
ORDER BY created_at DESC
LIMIT $3;

-- name: ListExperimentalRuntimeArtifactsBySession :many
SELECT * FROM experimental_runtime_artifact
WHERE session_id = $1
ORDER BY created_at ASC;

-- name: GetExperimentalRuntimeArtifact :one
SELECT * FROM experimental_runtime_artifact
WHERE id = $1;
