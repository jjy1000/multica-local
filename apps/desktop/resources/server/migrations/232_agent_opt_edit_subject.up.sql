-- 0.5.3: generalize the self-optimization ledger from agent-only to four
-- optimizable subjects (agent / skill / squad / autopilot), per the user's
-- "进化可以删除" + "对技能、团队、自动化工程进行优化" requirements.
--
-- Two additions:
--   - target_type + target_id  : polymorphic subject reference. 'agent' rows
--     keep their existing agent_id; the other kinds point at skill / squad /
--     autopilot rows. agent_id becomes nullable (still backfilled from the
--     legacy value on migration).
--   - subject_scope            : the optimization scope this edit ran under.
--     'enroll'   → the subject opted into auto-apply (agent marker or global
--                  opt-in), the classic 0.5.2 gate.
--     'trust'    → trust-driven optimization (low trust = needs fixing).
--     'retain'   → trust ≥ 8 retention mode: subject is parked, no new edits
--                  proposed. Rows under this scope record the park decision.
--
-- Forward-only: ADD COLUMN IF NOT EXISTS + backfill + CHECK. No drops.

ALTER TABLE agent_opt_edit
    ADD COLUMN IF NOT EXISTS target_type TEXT NOT NULL DEFAULT 'agent'
        CHECK (target_type IN ('agent', 'skill', 'squad', 'autopilot'));

ALTER TABLE agent_opt_edit
    ADD COLUMN IF NOT EXISTS target_id UUID;

-- Backfill: every pre-0.5.3 row is an 'agent' subject pointing at agent_id.
UPDATE agent_opt_edit
SET target_type = 'agent',
    target_id = agent_id
WHERE target_id IS NULL;

ALTER TABLE agent_opt_edit
    ADD COLUMN IF NOT EXISTS subject_scope TEXT NOT NULL DEFAULT 'enroll'
        CHECK (subject_scope IN ('enroll', 'trust', 'retain'));

-- agent_id becomes nullable: a skill/squad/autopilot edit row has no agent.
-- Legacy agent rows keep their backfilled value.
ALTER TABLE agent_opt_edit
    ALTER COLUMN agent_id DROP NOT NULL;

-- Per-subject indexes (the ledger is read per target during the run scan).
CREATE INDEX IF NOT EXISTS idx_agent_opt_edit_target
    ON agent_opt_edit (target_type, target_id, created_at DESC);
