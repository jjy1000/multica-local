-- Renumbered from upstream 204_issue_workspace_parent_index.up.sql → fork 219_issue_workspace_parent_index.up.sql
-- (upstream 0.4.0 integration plan, wave 2; continues docs/upstream-integration/2026-07-22-migration-renumber-map.md)
-- Iron rule: IF NOT EXISTS added where applicable (CREATE TABLE / INDEX / ADD COLUMN).

-- Composite index for workspace-scoped sub-issue lookups. The existing
-- idx_issue_parent (parent_issue_id) lacks workspace_id, so listing children
-- within one workspace pays for cross-workspace rows first. Keep this as the
-- migration's only statement: PostgreSQL rejects CREATE INDEX CONCURRENTLY
-- inside a transaction or multi-command string.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_issue_workspace_parent
    ON issue (workspace_id, parent_issue_id);
