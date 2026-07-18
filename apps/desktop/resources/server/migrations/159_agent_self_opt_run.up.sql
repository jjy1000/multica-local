-- 0.3.45.1: agent_self_optimization run metadata tables.
--
-- Forward-only: CREATE TABLE IF NOT EXISTS + CREATE INDEX IF NOT EXISTS. No DROP.
-- No new columns on existing tables — `issue.lab_source='agent_self_optimization'`
-- already auto-hides self-opt issues from the main workspace task list
-- (handler ListIssues / ListOpenIssues honor exclude_lab=true via the
-- lab_source IS NULL filter introduced in 0.3.33), so we don't need an
-- archived_at column on issue. The "auto-archive after run" UX is just
-- "this issue is lab-bound → exclude_lab=true hides it from the main
-- panel; the self-opt-history view shows it under
-- /experimental/self-opt-history".
--
-- The two new tables hold:
--   - agent_self_opt_run     : one row per scheduled / manual self-opt
--                              run, with JSONB prompt_suggestions +
--                              report_md + kb_appendix_path
--   - agent_self_opt_run_issue : m:n of run ↔ issue that the run scanned
--                                (basis for incremental scans in 0.3.46+)

CREATE TABLE IF NOT EXISTS agent_self_opt_run (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'running', 'done', 'failed', 'cancelled')),
    trigger_kind TEXT NOT NULL DEFAULT 'scheduled'
        CHECK (trigger_kind IN ('scheduled', 'manual')),
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    source_issue_count INT NOT NULL DEFAULT 0,
    prompt_suggestions JSONB NOT NULL DEFAULT '[]'::jsonb,
    report_md TEXT,
    kb_appendix_path TEXT,
    error_message TEXT,
    -- The self-opt issue itself — created by the runner so the user can
    -- see the run in their main issue panel *with* the lab badge while
    -- it's running (then auto-archived once done via lab_source). ON
    -- DELETE SET NULL so dropping the issue row doesn't cascade.
    created_issue_id UUID REFERENCES issue(id) ON DELETE SET NULL
);

-- Most reads are "list runs for workspace X, newest first" — covered
-- by this composite index.
CREATE INDEX IF NOT EXISTS idx_self_opt_run_ws_started
    ON agent_self_opt_run (workspace_id, started_at DESC);

-- Partial index for the daemon bootstrap Resume() scan — only scans
-- pending / running rows, keeping the index tiny even at scale.
CREATE INDEX IF NOT EXISTS idx_self_opt_run_pending
    ON agent_self_opt_run (status)
    WHERE status IN ('pending', 'running');