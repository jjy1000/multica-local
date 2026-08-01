-- 0.5.2: agent trust scoring + review history for the agent_self_optimization
-- lab's merged loop (Boris Cherny ablation principle: remove → add back line by
-- line → test; history feeds learning).
--
-- Two tables:
--   - agent_trust_profile : one row per (workspace, agent). score starts at 5.0,
--     is capped at 10.0, and drops by 0.5 on a user correction. Self-review
--     passes restore +0.2; a failed review costs another -0.5.
--   - agent_trust_event   : append-only ledger of every correction / review
--     outcome so the self-opt runner can learn from *why* agents fail and the
--     history view can render a trust timeline.
--
-- Forward-only: CREATE TABLE IF NOT EXISTS only. No ALTER on existing tables.

CREATE TABLE IF NOT EXISTS agent_trust_profile (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    -- Current trust score. Range [0, 10]; starts at 5.0. Enforcement lives in
    -- the Go service (numeric is kept loose so future threshold tuning needs
    -- no migration).
    score NUMERIC(4,1) NOT NULL DEFAULT 5.0,
    review_threshold NUMERIC(4,1) NOT NULL DEFAULT 7.0,
    -- The self-review gate fires when score < review_threshold AND the task
    -- has a result payload worth reviewing.
    review_requested_count INT NOT NULL DEFAULT 0,
    review_pass_count INT NOT NULL DEFAULT 0,
    review_fail_count INT NOT NULL DEFAULT 0,
    correction_count INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, agent_id)
);

CREATE INDEX IF NOT EXISTS idx_agent_trust_profile_ws_score
    ON agent_trust_profile (workspace_id, score DESC);

CREATE TABLE IF NOT EXISTS agent_trust_event (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    -- correction        : user flagged the agent's output (score -0.5)
    -- review_requested  : automatic self-review triggered on task completion
    -- review_pass       : self-review accepted the output (score +0.2)
    -- review_fail       : self-review rejected the output (score -0.5)
    -- review_skipped    : self-review could not run (no LLM provider); no delta
    event_type TEXT NOT NULL
        CHECK (event_type IN ('correction', 'review_requested', 'review_pass',
                              'review_fail', 'review_skipped')),
    score_delta NUMERIC(4,2) NOT NULL DEFAULT 0,
    score_before NUMERIC(4,1),
    score_after NUMERIC(4,1),
    task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    issue_id UUID REFERENCES issue(id) ON DELETE SET NULL,
    note TEXT,
    created_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- History view: per-workspace timeline, newest first.
CREATE INDEX IF NOT EXISTS idx_agent_trust_event_ws_created
    ON agent_trust_event (workspace_id, created_at DESC);

-- Per-agent ledger (used by the runner's learning scan and the detail panel).
CREATE INDEX IF NOT EXISTS idx_agent_trust_event_agent_created
    ON agent_trust_event (agent_id, created_at DESC);
