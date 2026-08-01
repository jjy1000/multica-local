-- Renumbered from upstream 205_issue_workspace_position_index.up.sql → fork 220_issue_workspace_position_index.up.sql
-- (upstream 0.4.0 integration plan, wave 2; continues docs/upstream-integration/2026-07-22-migration-renumber-map.md)
-- Iron rule: IF NOT EXISTS added where applicable (CREATE TABLE / INDEX / ADD COLUMN).

-- Ordering index for the default workspace issue listing
-- (position, created_at DESC, id DESC). Without it the sort spills to an
-- explicit sort node on every page load once idx_issue_workspace narrows by
-- workspace only. Keep this as the migration's only statement: PostgreSQL
-- rejects CREATE INDEX CONCURRENTLY inside a transaction or multi-command
-- string.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_issue_workspace_position
    ON issue (workspace_id, position, created_at DESC, id DESC);
