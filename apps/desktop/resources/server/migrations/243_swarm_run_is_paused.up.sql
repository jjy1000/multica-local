-- 0.5.22 swarm pause/resume: add swarm_run.is_paused so the
-- orchestrator's tick loop can skip phase advance + task enqueue while
-- the user has paused the run. Pause is NOT a terminal state — the run
-- stays active and the orchestrator keeps its 30s tick, but tick returns
-- early when is_paused is true (server/internal/service/swarm/orchestrator.go).
--
-- Also widens swarm_interrupt.kind to include 'resume' (the toggle back
-- to false). The handler writes a swarm_interrupt audit row for every
-- interrupt kind, so the CHECK must admit 'resume' or the
-- CreateSwarmInterrupt insert rejects with 23514.
--
-- Forward-only per CLAUDE.md. The CHECK widening is a drop + re-add
-- with a superset of the original values (same shape as mig 242's
-- experimental_resource_lock widening), so every existing row still
-- satisfies the new constraint.

ALTER TABLE swarm_run
    ADD COLUMN IF NOT EXISTS is_paused BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE swarm_interrupt
    DROP CONSTRAINT swarm_interrupt_kind_check;

ALTER TABLE swarm_interrupt
    ADD CONSTRAINT swarm_interrupt_kind_check
    CHECK (kind IN ('pause','resume','cancel','redirect','inject_message'));
