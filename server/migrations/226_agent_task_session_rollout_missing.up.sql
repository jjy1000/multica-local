-- Renumbered from upstream 224_agent_task_session_rollout_missing.up.sql → fork 226_agent_task_session_rollout_missing.up.sql
-- (upstream 0.4.0 integration plan, wave 2; continues docs/upstream-integration/2026-07-22-migration-renumber-map.md)
-- Iron rule: IF NOT EXISTS added where applicable (CREATE TABLE / INDEX / ADD COLUMN).

-- Marks tasks whose recorded CLI session rollout file has gone missing so
-- resume attempts can fall back to a fresh session instead of failing
-- repeatedly. Inert until the daemon-side detection is ported
-- (schema-first); FALSE default leaves existing rows untouched.
ALTER TABLE agent_task_queue
  ADD COLUMN IF NOT EXISTS session_rollout_missing BOOLEAN NOT NULL DEFAULT FALSE;
