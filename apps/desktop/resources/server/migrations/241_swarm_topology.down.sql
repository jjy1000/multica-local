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

-- Lock CHECK update moved to migration 242 — see that migration's
-- comment for the rationale (mig 241's original CHECK update was
-- too narrow and failed against existing pythia_oracle /
-- llm_wiki_bridge / code_canvas rows).