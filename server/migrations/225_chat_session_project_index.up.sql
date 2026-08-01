-- Renumbered from upstream 215_chat_session_project_index.up.sql → fork 225_chat_session_project_index.up.sql
-- (upstream 0.4.0 integration plan, wave 2; continues docs/upstream-integration/2026-07-22-migration-renumber-map.md)
-- Iron rule: IF NOT EXISTS added where applicable (CREATE TABLE / INDEX / ADD COLUMN).

-- Project deletion clears chat-session references by project_id. Partial on
-- project_id IS NOT NULL so the index stays tiny while most sessions carry no
-- project. Keep this as the migration's only statement: PostgreSQL rejects
-- CREATE INDEX CONCURRENTLY inside a transaction or multi-command string.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_chat_session_project
    ON chat_session (project_id)
    WHERE project_id IS NOT NULL;
