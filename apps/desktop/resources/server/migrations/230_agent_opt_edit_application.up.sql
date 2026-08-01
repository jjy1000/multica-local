-- 0.5.2: agent_opt_edit gains the three-state application lifecycle +
-- numeric validation score. Replaces the boolean `accepted` as the source
-- of truth for "did this edit land":
--
--   applied  — validated above the auto-apply threshold, written back to
--              agent.instructions (auto).
--   suggested — validated but below the auto-apply threshold; parked for
--              human confirmation (the "待确认建议" tier).
--   rejected  — validation failed / human ignored; permanent negative
--              experience (never re-proposed).
--
-- The `accepted` column is kept (forward-only migration rule) and
-- backfilled to match, but new code reads `application`. Adding the
-- numeric score lets the UI rank suggestions and lets the loop tune its
-- own thresholds from real outcomes.
--
-- Forward-only: ALTER TABLE ADD COLUMN only. No DROP of the old column.

ALTER TABLE agent_opt_edit
    ADD COLUMN IF NOT EXISTS application TEXT NOT NULL DEFAULT 'suggested'
        CHECK (application IN ('applied', 'suggested', 'rejected', 'ignored', 'reverted'));

ALTER TABLE agent_opt_edit
    ADD COLUMN IF NOT EXISTS validation_score NUMERIC(5,2);

ALTER TABLE agent_opt_edit
    ADD COLUMN IF NOT EXISTS validation_reason TEXT;

-- 0.5.2 synthesis: snapshot of the full instruction set before this edit
-- was applied (the revert/rollback point) + who applied it (user | auto).
ALTER TABLE agent_opt_edit
    ADD COLUMN IF NOT EXISTS instructions_snapshot TEXT;

-- applied_by is nullable: only APPLIED edits carry a value ('user' | 'auto').
-- Suggested / rejected / ignored / reverted edits record NULL (design-review
-- d4: a non-applied edit recording an "applied by" marker is misleading, and
-- a NOT NULL CHECK would reject the NULL the recordOptEdits path must write).
ALTER TABLE agent_opt_edit
    ADD COLUMN IF NOT EXISTS applied_by TEXT DEFAULT 'auto'
        CHECK (applied_by IN ('user', 'auto'));

-- corrected_task_id (0.5.2 adversarial review c10): the task whose output the
-- user corrected — the confirmed-correction anchor for an auto-applied edit.
-- NULL for edits not auto-applied. Persisted so the ledger shows WHICH
-- correction backed an auto-apply (traceability the design verdict requires).
ALTER TABLE agent_opt_edit
    ADD COLUMN IF NOT EXISTS corrected_task_id UUID;

-- Backfill: pre-0.5.2 rows used `accepted` as the binary gate.
UPDATE agent_opt_edit
SET application = CASE WHEN accepted THEN 'applied' ELSE 'rejected' END
WHERE application = 'suggested';

-- Ranking reads (the pending-confirmation list + leaderboard).
CREATE INDEX IF NOT EXISTS idx_agent_opt_edit_ws_application_created
    ON agent_opt_edit (workspace_id, application, created_at DESC);
