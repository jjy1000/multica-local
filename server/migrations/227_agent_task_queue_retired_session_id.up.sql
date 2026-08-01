-- Renumbered from upstream 234_agent_task_queue_retired_session_id.up.sql → fork 227_agent_task_queue_retired_session_id.up.sql
-- (upstream 0.4.0 integration plan, wave 2; continues docs/upstream-integration/2026-07-22-migration-renumber-map.md)
-- Iron rule: IF NOT EXISTS added where applicable (CREATE TABLE / INDEX / ADD COLUMN).

-- Preserves the previous CLI session id when a task's session is retired and
-- replaced, so diagnostics can trace the lineage instead of losing the old id
-- on overwrite. Inert until the daemon-side retire path is ported
-- (schema-first); nullable with no default so existing rows are untouched.
ALTER TABLE agent_task_queue ADD COLUMN IF NOT EXISTS retired_session_id TEXT;
