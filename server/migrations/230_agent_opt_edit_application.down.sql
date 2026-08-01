-- 0.5.2: rollback for the application lifecycle (dev only — production
-- migrations are forward-only).

ALTER TABLE agent_opt_edit
    DROP COLUMN IF EXISTS application,
    DROP COLUMN IF EXISTS validation_score,
    DROP COLUMN IF EXISTS validation_reason,
    DROP COLUMN IF EXISTS instructions_snapshot,
    DROP COLUMN IF EXISTS applied_by,
    DROP COLUMN IF EXISTS corrected_task_id;

DROP INDEX IF EXISTS idx_agent_opt_edit_ws_application_created;
