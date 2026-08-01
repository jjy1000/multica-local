-- Renumbered from upstream 203_issue_workspace_assignee_index.up.sql → fork 218_issue_workspace_assignee_index.up.sql
-- (upstream 0.4.0 integration plan, wave 2; continues docs/upstream-integration/2026-07-22-migration-renumber-map.md)
-- Iron rule: IF NOT EXISTS added where applicable (CREATE TABLE / INDEX / ADD COLUMN).

-- Composite index for workspace-scoped assignee filters. The existing
-- idx_issue_assignee (assignee_type, assignee_id) lacks workspace_id, so
-- "issues assigned to X in workspace W" scans every workspace the assignee
-- ever touched. Keep this as the migration's only statement: PostgreSQL
-- rejects CREATE INDEX CONCURRENTLY inside a transaction or multi-command
-- string.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_issue_workspace_assignee
    ON issue (workspace_id, assignee_type, assignee_id);
