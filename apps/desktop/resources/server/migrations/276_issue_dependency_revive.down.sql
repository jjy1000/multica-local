-- 276_issue_dependency_revive.down.sql
-- 0.5.83 WL3 down-migration (dev / fresh-test-DB rollback only).
-- Forward-only production rule: never run down.sql on a live DB.
--
-- Symmetric reverse of the up script: drop the named created_by CHECK,
-- then the four added columns. The 001_init base columns
-- (id / issue_id / depends_on_issue_id / type) are untouched.

ALTER TABLE issue_dependency
    DROP CONSTRAINT IF EXISTS issue_dependency_created_by_check;

ALTER TABLE issue_dependency
    DROP COLUMN IF EXISTS evidence_comment_id,
    DROP COLUMN IF EXISTS created_by,
    DROP COLUMN IF EXISTS created_at,
    DROP COLUMN IF EXISTS updated_at;
