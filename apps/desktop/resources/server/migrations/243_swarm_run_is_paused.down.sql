-- 0.5.22 swarm pause/resume rollback: drop is_paused + narrow the
-- interrupt CHECK back to the 4 original kinds.

ALTER TABLE swarm_interrupt
    DROP CONSTRAINT swarm_interrupt_kind_check;

ALTER TABLE swarm_interrupt
    ADD CONSTRAINT swarm_interrupt_kind_check
    CHECK (kind IN ('pause','cancel','redirect','inject_message'));

ALTER TABLE swarm_run
    DROP COLUMN IF EXISTS is_paused;
