-- Project start_date / due_date rollback.
--
-- Forward-only policy (CLAUDE.md §Data Safety): Migrations are append-only.
-- This down migration exists so `migrate down` works in dev but should NOT be
-- run against production data. If you really need to drop these columns in a
-- future release, write a new migration that does an explicit safe rewrite
-- (copy data out, drop column, copy back) rather than calling this one back.

DROP INDEX IF EXISTS idx_project_workspace_due_date;
DROP INDEX IF EXISTS idx_project_workspace_start_date;
ALTER TABLE project DROP COLUMN IF EXISTS due_date;
ALTER TABLE project DROP COLUMN IF EXISTS start_date;