-- 0.5.3: agent_opt_edit was missing the updated_at column that the
-- UpdateAgentOptEditApplication / UpdateAgentOptEditApplicationByIDs
-- queries reference (`updated_at = now()`). The human-confirm apply /
-- revert path never exercised the real schema before 0.5.3 (the handler
-- tests ran with a nil SelfOptService → 503), so the 0.5.2 ship shipped
-- the queries + schema drift undetected. Add the column forward-only.

ALTER TABLE agent_opt_edit
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();
