-- 0.5.2 synthesis round: widen the application state machine + snapshot
-- support. Migration 230 shipped 'applied|suggested|rejected'; the design
-- review added two more states and two columns:
--
--   ignored  — soft archive: a suggestion nobody acted on for ~3 runs
--              expires here. NOT in the rejection buffer; re-proposable
--              with fresh validation. (Never expire-to-rejected.)
--   reverted — an applied edit was rolled back to its snapshot (the
--              '回退到上一版本' action). The rolled-back edit is
--              content-hash keyed into the rejected buffer so it cannot
--              be re-applied identically.
--
--   instructions_snapshot — full instruction set before this edit was
--              applied (the rollback point).
--   applied_by           — 'user' | 'auto' (who applied it).
--
-- Forward-only: widening a CHECK + ADD COLUMN only. 230 stays as shipped.

ALTER TABLE agent_opt_edit DROP CONSTRAINT IF EXISTS agent_opt_edit_application_check;
ALTER TABLE agent_opt_edit
    ADD CONSTRAINT agent_opt_edit_application_check
    CHECK (application IN ('applied', 'suggested', 'rejected', 'ignored', 'reverted'));

ALTER TABLE agent_opt_edit
    ADD COLUMN IF NOT EXISTS instructions_snapshot TEXT;

ALTER TABLE agent_opt_edit
    ADD COLUMN IF NOT EXISTS applied_by TEXT DEFAULT 'auto'
        CHECK (applied_by IN ('user', 'auto'));

-- Expiry sweep reads (suggestions older than N runs).
CREATE INDEX IF NOT EXISTS idx_agent_opt_edit_suggested_created
    ON agent_opt_edit (application, created_at)
    WHERE application = 'suggested';
