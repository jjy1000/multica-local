-- 0.5.3: rollback for migration 233 (dev only).

ALTER TABLE agent_opt_edit
    DROP COLUMN IF EXISTS updated_at;
