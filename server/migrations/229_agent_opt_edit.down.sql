-- 0.5.2: rollback for the deferred columns + edit ledger (dev only —
-- production migrations are forward-only).

DROP TABLE IF EXISTS agent_opt_edit;

ALTER TABLE agent_self_opt_run DROP CONSTRAINT IF EXISTS agent_self_opt_run_status_check;
ALTER TABLE agent_self_opt_run
    ADD CONSTRAINT agent_self_opt_run_status_check
    CHECK (status IN ('pending', 'running', 'done', 'failed', 'cancelled'));

ALTER TABLE agent_self_opt_run
    DROP COLUMN IF EXISTS deferred_reason,
    DROP COLUMN IF EXISTS deferred_until,
    DROP COLUMN IF EXISTS data_count;
