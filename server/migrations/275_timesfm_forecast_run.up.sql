-- 0.5.82 WL2: TimesFM per-issue forecast persistence + lock-source
-- enum widening.
--
-- Part 1 — timesfm_forecast_run. Sibling of pythia_forecast_run
-- (mig 164): one row per completed issue-bound forecast. The loopback
-- TimesFM 2.5 engine answers POST /forecast with a quantile-band
-- envelope per series; server/internal/handler/timesfm_forecast.go
-- forwards it and persists the response here so the read-only lab
-- view (route suffix timesfm-lab) can list runs per issue WITHOUT a
-- live engine (ICP-2 record listing) and deep links can target a
-- single run (ICP-3, ?issue=<id>&run=<runId>).
--
-- Part 2 — widen experimental_resource_lock.experimental_source CHECK
-- with 'timesfm'. Without this, install_timesfm.go's
-- experimental.Claim(ctx, h.Queries, "timesfm", LockAgent, agentID)
-- INSERTs reject with 23514. Same drop + re-add shape as migs 149 /
-- 154 / 160 / 163 / 242: the constraint only validates inserts, so
-- existing rows are unaffected. The retired flag literals are kept —
-- the CHECK is forward-only so historical rows remain valid.
--
-- Forward-only per CLAUDE.md: no table/column drops, no renames.

CREATE TABLE IF NOT EXISTS timesfm_forecast_run (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    -- The issue this forecast was bound to. CASCADE so deleting the
    -- issue takes its forecasts with it (they have no meaning
    -- detached from the issue they predicted on).
    issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    -- Number of horizon steps actually forecast (the engine caps at
    -- 256; 0 can only appear if a future writer persists an empty
    -- envelope set — the column is DEFAULT 0 to keep the INSERT path
    -- total, unlike pythia's NOT NULL-without-default rounds).
    horizons INT NOT NULL DEFAULT 0,
    -- Effective provenance, passed through from the engine response:
    -- "model" (seeded weights answered), "seasonal_naive" (Tier-0
    -- fallback), "mixed" (per-series outcomes differed).
    provenance TEXT NOT NULL
        CHECK (provenance IN ('model', 'seasonal_naive', 'mixed')),
    -- The full engine response envelope (series array with point +
    -- quantiles + per-series provenance), as JSONB.
    result JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Dominant read path: "recent forecasts for issue X, newest first"
-- (GET /api/experimental/timesfm/forecast/issue/runs).
CREATE INDEX IF NOT EXISTS idx_timesfm_forecast_run_issue
    ON timesfm_forecast_run (issue_id, created_at DESC);

ALTER TABLE experimental_resource_lock
    DROP CONSTRAINT experimental_resource_lock_experimental_source_check;

ALTER TABLE experimental_resource_lock
    ADD CONSTRAINT experimental_resource_lock_experimental_source_check
    CHECK (experimental_source = ANY (ARRAY[
        'claude_science'::text,
        'claude_science_lab'::text,
        'mythos_swarm'::text,
        'agent_self_optimization'::text,
        'agent_creation_studio'::text,
        'pythia_oracle'::text,
        'llm_wiki_bridge'::text,
        'constitution_agent'::text,
        'code_canvas'::text,
        'swarm_topology'::text,
        'semantica'::text,
        'timesfm'::text
    ]));
