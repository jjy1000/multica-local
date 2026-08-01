-- 0.5.2: rollback for migration 231 (dev only).

DROP INDEX IF EXISTS idx_agent_opt_edit_suggested_created;

ALTER TABLE agent_opt_edit
    DROP COLUMN IF EXISTS instructions_snapshot,
    DROP COLUMN IF EXISTS applied_by;

ALTER TABLE agent_opt_edit DROP CONSTRAINT IF EXISTS agent_opt_edit_application_check;
ALTER TABLE agent_opt_edit
    ADD CONSTRAINT agent_opt_edit_application_check
    CHECK (application IN ('applied', 'suggested', 'rejected'));
