-- Rollback: drop the new columns + indexes added in 156.
-- Forward-only in the deployed fork — this file exists for symmetry
-- and for fresh databases that run both directions during testing.

DROP INDEX IF EXISTS idx_mythos_run_self_opt;
DROP INDEX IF EXISTS idx_mythos_run_extension_agents;

ALTER TABLE mythos_members DROP COLUMN IF EXISTS reflection;

ALTER TABLE mythos_run DROP COLUMN IF EXISTS coda_conclusions;
ALTER TABLE mythos_run DROP COLUMN IF EXISTS self_optimization_enabled;
ALTER TABLE mythos_run DROP COLUMN IF EXISTS extension_agent_ids;
