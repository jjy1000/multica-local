-- experimental_claude_runtime_session + experimental_runtime_artifact
-- forward-only; drop reverses both tables and the indexes that back
-- them. Tests that reference these tables must be skipped after the
-- down migration runs.
DROP INDEX IF EXISTS idx_experimental_runtime_artifact_session;
DROP TABLE IF EXISTS experimental_runtime_artifact;
DROP INDEX IF EXISTS idx_experimental_runtime_session_expires;
DROP INDEX IF EXISTS idx_experimental_runtime_session_ws_created;
DROP TABLE IF EXISTS experimental_claude_runtime_session;
