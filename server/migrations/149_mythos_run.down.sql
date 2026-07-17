-- Rollback: drop the mythos tables + restore the experimental_source
-- CHECK to its claude_science-only state. Migration is forward-only in
-- the deployed fork — this file exists for symmetry and for fresh
-- databases that run both directions during testing.
DROP INDEX IF EXISTS idx_mythos_members_by_agent;
DROP INDEX IF EXISTS idx_mythos_members_by_run;
DROP INDEX IF EXISTS idx_mythos_run_active;
DROP INDEX IF EXISTS idx_mythos_run_by_workspace;
DROP TABLE IF EXISTS mythos_members;
DROP TABLE IF EXISTS mythos_run;

ALTER TABLE experimental_resource_lock
    DROP CONSTRAINT IF EXISTS experimental_resource_lock_experimental_source_check;
-- Best-effort restore the original constraint; if a newer migration
-- already widened it further, this becomes a no-op.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE constraint_name = 'experimental_resource_lock_experimental_source_check'
          AND table_name = 'experimental_resource_lock'
    ) THEN
        ALTER TABLE experimental_resource_lock
            ADD CONSTRAINT experimental_resource_lock_experimental_source_check
                CHECK (experimental_source IN ('claude_science'));
    END IF;
END $$;
