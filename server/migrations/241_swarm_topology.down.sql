-- 0.5.21 swarm topology down: drop the 4 tables + restore lock CHECK.
-- Forward-only convention per CLAUDE.md, but the .down.sql is kept for
-- symmetry with the 240-agent_task_queue pair and as the only rollback
-- path documented in mig 149.

DROP INDEX IF EXISTS idx_swarm_interrupt_by_run;
DROP TABLE IF EXISTS swarm_interrupt;

DROP INDEX IF EXISTS idx_swarm_role_message_by_run;
DROP TABLE IF EXISTS swarm_role_message;

DROP INDEX IF EXISTS idx_swarm_role_ready;
DROP INDEX IF EXISTS idx_swarm_role_by_agent;
DROP INDEX IF EXISTS idx_swarm_role_by_run;
DROP TABLE IF EXISTS swarm_role;

DROP INDEX IF EXISTS idx_swarm_run_by_root_issue;
DROP INDEX IF EXISTS idx_swarm_run_active;
DROP INDEX IF EXISTS idx_swarm_run_by_workspace;
DROP TABLE IF EXISTS swarm_run;

-- Restore the pre-241 lock CHECK (no swarm_topology).
ALTER TABLE experimental_resource_lock
    DROP CONSTRAINT experimental_resource_lock_experimental_source_check,
    ADD CONSTRAINT experimental_resource_lock_experimental_source_check
        CHECK (experimental_source IN (
            'claude_science','mythos_swarm',
            'agent_self_optimization','agent_creation_studio','constitution_agent'
        ));