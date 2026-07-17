-- 154 down: reverses the lab consolidation migration. Cannot drop
-- lab_id columns (0.3.22+ code reads them); removes the widened
-- CHECK so a downgraded server refuses its own catalog.
ALTER TABLE experimental_resource_lock
    DROP CONSTRAINT IF EXISTS experimental_resource_lock_experimental_source_check;
ALTER TABLE experimental_resource_lock
    ADD CONSTRAINT experimental_resource_lock_experimental_source_check
    CHECK (experimental_source IN ('claude_science'));

DROP INDEX IF EXISTS idx_experimental_runtime_session_lab;
DROP INDEX IF EXISTS idx_experimental_runtime_artifact_lab;
-- lab_id columns intentionally retained; see migration 154.up.sql
-- comment for the forward-only contract.