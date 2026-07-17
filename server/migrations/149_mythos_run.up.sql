-- mythos_run + mythos_members: 0.3.16-patch.1 Mythos Swarm storage.
--
-- Mythos Swarm ports OpenMythos RDT (Recurrent-Depth Transformer) to a
-- Multica squad topology. Each `mythos_run` row records one invocation
-- of `multica mythos run`:
--   - prelude: one issue per run, the "root decomposition"
--   - loop:    sub-issues created per loop iteration
--   - coda:    a single summary comment written back to the root issue
--
-- We deliberately store convergence as a JSONB array so the renderer
-- can plot cosine-over-iteration without round-tripping every iteration
-- to the client. The 4 KB upper bound on convergence_history is more
-- than enough for max_loop_iters=48 (one float per iter ≈ 40 bytes).
--
-- No FK to agent: mythos_members records WHICH agent ran which role on
-- which iteration, but the agent row may be hidden by the lab and we
-- want to keep the run row readable even after a rollback.

CREATE TABLE mythos_run (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    -- ownership transfers via member FK; the caller is the user who
    -- invoked the run, not necessarily the workspace owner.
    creator_user_id UUID NOT NULL REFERENCES "user"(id),
    problem TEXT NOT NULL,
    -- running / completed / aborted / failed
    status TEXT NOT NULL DEFAULT 'running'
        CHECK (status IN ('running','completed','aborted','failed')),
    current_loop INT NOT NULL DEFAULT 0,
    -- cosine similarity per iteration; len <= max_loop_iters.
    -- [] while the run is still in prelude or pre-iteration 1.
    convergence_history JSONB NOT NULL DEFAULT '[]'::jsonb,
    max_loop_iters INT NOT NULL DEFAULT 16,
    convergence_threshold DOUBLE PRECISION NOT NULL DEFAULT 0.95,
    -- root issue created in the prelude. NOT NULL after the runner
    -- finishes preluding; nullable so the insert can happen first and
    -- the issue id is written in a follow-up step.
    root_issue_id UUID REFERENCES issue(id),
    final_issue_id UUID REFERENCES issue(id),
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ
);

CREATE INDEX idx_mythos_run_by_workspace
    ON mythos_run(workspace_id, started_at DESC);
CREATE INDEX idx_mythos_run_active
    ON mythos_run(workspace_id)
    WHERE status = 'running';

-- mythos_members: per-iteration agent assignments. One row per (run,
-- role, iteration) so the renderer can show "iteration 3 was handled
-- by biology agent + researcher agent". role is the RDT three-stage
-- label and matches the agent.role column for query ergonomics.
CREATE TABLE mythos_members (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES mythos_run(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id),
    role TEXT NOT NULL
        CHECK (role IN ('prelude','loop','coda')),
    iteration INT NOT NULL DEFAULT 0,
    -- the sub-issue this member produced (loop / coda only).
    result_issue_id UUID REFERENCES issue(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(run_id, role, iteration)
);

CREATE INDEX idx_mythos_members_by_run
    ON mythos_members(run_id, iteration);
CREATE INDEX idx_mythos_members_by_agent
    ON mythos_members(agent_id, created_at DESC);

-- experimental_resource_lock CHECK constraint widened to include the
-- mythos_swarm source (mirrors the catalog entry). PR 8 (0.3.16-patch.1)
-- adds the first MCP-server row, but we widen the enum here so the
-- mythos install handler can claim its lab-owned rows.
ALTER TABLE experimental_resource_lock
    DROP CONSTRAINT experimental_resource_lock_experimental_source_check,
    ADD CONSTRAINT experimental_resource_lock_experimental_source_check
        CHECK (experimental_source IN ('claude_science','mythos_swarm'));
