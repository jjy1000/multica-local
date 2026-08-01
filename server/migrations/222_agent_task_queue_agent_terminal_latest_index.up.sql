-- Renumbered from upstream 233_agent_task_queue_agent_terminal_latest_index.up.sql → fork 222_agent_task_queue_agent_terminal_latest_index.up.sql
-- (upstream 0.4.0 integration plan, wave 2; continues docs/upstream-integration/2026-07-22-migration-renumber-map.md)
-- Iron rule: IF NOT EXISTS added where applicable (CREATE TABLE / INDEX / ADD COLUMN).

-- Per-agent "latest terminal task" lookup. Backs the agent activity panes
-- that show the most recent completed/failed run per agent; the composite
-- ordering matches the ORDER BY so the plan is a pure index scan. Keep this
-- as the migration's only statement: PostgreSQL rejects CREATE INDEX
-- CONCURRENTLY inside a transaction or multi-command string.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_agent_task_queue_agent_terminal_latest
    ON agent_task_queue (agent_id, completed_at DESC NULLS LAST, created_at DESC, id DESC)
    WHERE status IN ('completed', 'failed');
