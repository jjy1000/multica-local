-- 157 reverse: drop columns + restore resource_type CHECK. Safe: every
-- new column is nullable or has a default, so dropping them does not
-- affect rows created under 0.3.31 (the data they held would be lost,
-- which is acceptable for a forward-only labs migration roll-back).

DROP INDEX IF EXISTS idx_issue_lab_mode_enhancer;

ALTER TABLE issue DROP COLUMN IF EXISTS lab_mode;

ALTER TABLE mythos_run
    DROP COLUMN IF EXISTS supervision_state,
    DROP COLUMN IF EXISTS target_assignee,
    DROP COLUMN IF EXISTS mode;

-- Restore the original mythos_run.status CHECK before the squad-widening
-- branch ran. Old status rows are preserved because we only drop the
-- constraint, never the data; rows still labelled 'supervising' would
-- violate the constraint on re-apply, but a rollback is expected to
-- discard those rows anyway (the supervise goroutine stops writing
-- them once the daemon is rebuilt without 0.3.31).
ALTER TABLE mythos_run
    DROP CONSTRAINT IF EXISTS mythos_run_status_check;
ALTER TABLE mythos_run
    ADD CONSTRAINT mythos_run_status_check
        CHECK (status IN ('running','completed','aborted','failed'));

ALTER TABLE mythos_members DROP COLUMN IF EXISTS reflection_iter;

ALTER TABLE experimental_resource_visibility
    DROP CONSTRAINT IF EXISTS experimental_resource_visibility_resource_type_check;
ALTER TABLE experimental_resource_visibility
    ADD CONSTRAINT experimental_resource_visibility_resource_type_check
        CHECK (resource_type IN ('agent','autopilot','skill'));

-- Visibility rows seeded by the 0.3.31 install handler (5 mythos_* agents)
-- stay on rollback — they reference agent UUIDs that remain valid; the
-- old non-squad CHECK still passes them.