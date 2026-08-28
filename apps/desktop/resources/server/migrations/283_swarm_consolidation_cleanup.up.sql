-- 283_swarm_consolidation_cleanup.up.sql
-- 0.5.86 swarm consolidation: reap the stuck swarm_topology machinery.
--
-- Runtime audit (2026-08-28, packaged 0.5.85 DB) found every
-- swarm_topology run parked in status='preparing' /
-- current_phase='research' forever: the orchestrator's
-- bootstrapFromSpec no-ops because topology_spec has no server-side
-- writer, and the 72h reap timer resets on every app restart
-- (ResumeOrchestration re-adopts non-terminal runs at boot), so the
-- zombies were immortal. The same audit found leader-created role
-- agents orphaned outside the GC's terminal-only sweep — e.g. the
-- bootstrap probe `swarm_919d978b_test_role` stuck in status='working'.
--
-- 0.5.86 consolidation: mythos_swarm is the single 蜂群 lab;
-- swarm_topology is frozen for new bindings (catalog
-- HideFromIssueLabPicker + manifest sidebar removal). This migration
-- closes out the stuck legacy state:
--
--   1. Non-terminal swarm_run rows (preparing/running/paused) are
--      failed with an explicit interrupt_reason. Their root issues
--      keep their history; /experimental/swarm-topology stays readable
--      (forward-only law — nothing is dropped).
--   2. Run-created role agents (naming pattern swarm_<run>_<role> —
--      at least two underscore segments, which CANNOT match the
--      boot-provisioned `swarm_coordinator` leader) that have no
--      swarm_role row are archived and marked offline. Boot-provisioned
--      per-workspace leaders are deliberately untouched.
--
-- Idempotent: both statements are WHERE-guarded, so re-running (fresh
-- restore, replays) is a no-op.

UPDATE swarm_run
SET status = 'failed',
    interrupt_reason = 'orphaned pre-consolidation run (0.5.86 swarm consolidation — topology_spec had no writer)',
    is_paused = FALSE
WHERE status NOT IN ('completed', 'failed', 'cancelled');

UPDATE agent
SET archived_at = COALESCE(archived_at, now()),
    status = 'offline'
WHERE name LIKE 'swarm\_%\_%'
  AND NOT EXISTS (SELECT 1 FROM swarm_role r WHERE r.agent_id = agent.id)
  AND (archived_at IS NOT NULL AND status <> 'offline'
       OR archived_at IS NULL);
