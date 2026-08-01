-- Renumbered from upstream 231_agent_task_queue_terminal_completed_at_index.up.sql → fork 221_agent_task_queue_terminal_completed_at_index.up.sql
-- (upstream 0.4.0 integration plan, wave 2; continues docs/upstream-integration/2026-07-22-migration-renumber-map.md)
-- Iron rule: IF NOT EXISTS added where applicable (CREATE TABLE / INDEX / ADD COLUMN).

-- Partial index over terminal tasks ordered by completion time. Serves
-- retention sweeps and "recently finished" listings without scanning the
-- live queue rows. Keep this as the migration's only statement: PostgreSQL
-- rejects CREATE INDEX CONCURRENTLY inside a transaction or multi-command
-- string.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_agent_task_queue_terminal_completed_at
    ON agent_task_queue (completed_at)
    WHERE status IN ('completed', 'failed');
