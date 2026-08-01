-- 0.5.3: rollback for migration 232 (dev only — production migrations are
-- forward-only).

ALTER TABLE agent_opt_edit
    DROP COLUMN IF EXISTS subject_scope,
    DROP COLUMN IF EXISTS target_id,
    DROP COLUMN IF EXISTS target_type;

DROP INDEX IF EXISTS idx_agent_opt_edit_target;
