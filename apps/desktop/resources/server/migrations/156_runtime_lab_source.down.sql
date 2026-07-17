-- 156 down: drop lab_source columns + indexes + issue_id on artifact.
--
-- Rollback strategy: drop the columns and their partial indexes. The
-- forward-only contract is preserved — running this down removes only
-- the 0.3.29 columns. lab_id (migration 154) is preserved.
ALTER TABLE experimental_claude_runtime_session
    DROP COLUMN IF EXISTS lab_source;

ALTER TABLE experimental_runtime_artifact
    DROP COLUMN IF EXISTS lab_source;

ALTER TABLE experimental_runtime_artifact
    DROP COLUMN IF EXISTS issue_id;