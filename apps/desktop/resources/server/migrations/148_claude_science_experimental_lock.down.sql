-- Reverse of 148_claude_science_experimental_lock.up.sql. Drops the lock
-- table. Down-migrations on experimental framework tables are safe because
-- the framework has not yet been wired into the runtime (PR 1 only adds
-- the table + helper; downstream usage lands in PR 3+).
DROP TABLE IF EXISTS experimental_resource_lock;
