-- 274_cleanup_orphan_experimental_resources.down.sql
-- Irreversible data cleanup: the deleted rows referenced resources that
-- no longer exist, so there is nothing to restore.
SELECT 1;
