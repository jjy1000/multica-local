-- Renumbered from upstream 214_chat_session_project.up.sql → fork 224_chat_session_project.up.sql
-- (upstream 0.4.0 integration plan, wave 2; continues docs/upstream-integration/2026-07-22-migration-renumber-map.md)
-- Iron rule: IF NOT EXISTS added where applicable (CREATE TABLE / INDEX / ADD COLUMN).

-- Chat sessions may opt into one project's durable context. Kept as a soft
-- reference (no FK): adding a foreign key would validate the established,
-- write-active chat_session table and take a cross-table lock during deploy.
-- Create/delete handlers serialize on the project row, project deletion
-- clears existing references, and daemon claim revalidates workspace
-- ownership before injecting any context. Inert until the Go handlers are
-- ported (schema-first).
ALTER TABLE chat_session
  ADD COLUMN IF NOT EXISTS project_id UUID;
