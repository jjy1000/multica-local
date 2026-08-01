-- 0.5.2: self-opt weekly scheduling + SkillOpt-style edit ledger.
--
-- Three changes, all additive / forward-only:
--
--  1. agent_self_opt_run gains the deferred lifecycle: a run whose source
--     data is too thin is parked (status='deferred') with a retry window
--     (deferred_until) instead of burning a report. The scheduler re-fires
--     it once the window passes and enough data has accumulated.
--  2. status CHECK widened with 'deferred' (migration 229 must run after
--     228; the CHECK re-create is idempotent).
--  3. New agent_opt_edit table: the SkillOpt-style edit ledger. Every
--     accepted/rejected instruction edit proposed by the optimizer is
--     recorded here so the loop (a) never re-proposes a rejected edit and
--     (b) learns from its own history. FK CASCADE on agent_id handles the
--     hard-delete closure; the archive path purges rows in the handler.

-- 1. Deferred lifecycle columns.
ALTER TABLE agent_self_opt_run
    ADD COLUMN IF NOT EXISTS deferred_reason TEXT,
    ADD COLUMN IF NOT EXISTS deferred_until TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS data_count INT NOT NULL DEFAULT 0;

-- Widen the status CHECK. Re-create the same constraint with the new value
-- (idempotent: IF NOT EXISTS on the new name, drop of the old name first).
ALTER TABLE agent_self_opt_run DROP CONSTRAINT IF EXISTS agent_self_opt_run_status_check;
ALTER TABLE agent_self_opt_run
    ADD CONSTRAINT agent_self_opt_run_status_check
    CHECK (status IN ('pending', 'running', 'done', 'failed', 'cancelled', 'deferred'));

-- 2. SkillOpt edit ledger.
CREATE TABLE IF NOT EXISTS agent_opt_edit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    run_id UUID NOT NULL REFERENCES agent_self_opt_run(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    -- add | delete | replace
    edit_type TEXT NOT NULL CHECK (edit_type IN ('add', 'delete', 'replace')),
    -- The instruction text before the edit (empty for 'add').
    before_text TEXT NOT NULL DEFAULT '',
    -- The instruction text after the edit (empty for 'delete').
    after_text TEXT NOT NULL DEFAULT '',
    rationale TEXT,
    -- Whether the validation gate accepted the edit and it was written
    -- back to agent.instructions. Rejected edits stay in the ledger as
    -- negative experience so the optimizer does not re-propose them.
    accepted BOOLEAN NOT NULL DEFAULT FALSE,
    -- Optimizer iteration within the run that proposed this edit.
    iteration INT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Ledger reads: per-agent history (runner + rejection buffer), per-run
-- (report), per-workspace (archive purge).
CREATE INDEX IF NOT EXISTS idx_agent_opt_edit_agent_created
    ON agent_opt_edit (agent_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_opt_edit_run
    ON agent_opt_edit (run_id);
CREATE INDEX IF NOT EXISTS idx_agent_opt_edit_ws
    ON agent_opt_edit (workspace_id);
