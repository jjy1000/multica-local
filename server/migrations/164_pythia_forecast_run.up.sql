-- 0.3.55: Pythia per-issue forecast persistence.
--
-- Before this migration the 10-round per-issue Pythia deliberation
-- (POST /api/experimental/pythia-oracle/forecast/issue) was SSE-live
-- only: the renderer streamed the rounds and dropped them on unmount,
-- so a forecast was invisible the moment the user navigated away. That
-- violated the labs contract "完成后结果在实验室功能中可见" — the lab
-- view had no finished-result substrate to read.
--
-- This table stores one row per completed deliberation: the bound
-- issue, the round count, the effective source (live oracle vs the
-- synthetic failover the handler relabels), and the full envelope
-- array as JSONB. The renderer's Pythia report surface lists these
-- per-issue (newest first) and re-renders a past run on click.
--
-- Forward-only: CREATE TABLE IF NOT EXISTS + CREATE INDEX IF NOT
-- EXISTS. No DROP, no change to existing tables.

CREATE TABLE IF NOT EXISTS pythia_forecast_run (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    -- The issue this deliberation was bound to. CASCADE so deleting
    -- the issue takes its forecasts with it (they have no meaning
    -- detached from the issue they predicted on).
    issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    -- Number of rounds actually emitted (<= the requested count; a
    -- mid-stream client disconnect persists the partial set).
    rounds INT NOT NULL,
    -- Effective provenance of the envelopes. Mirrors the envelope
    -- `lab_source` the handler already computes: "oracle" for a live
    -- model answer, "synthetic" for the in-process fallback,
    -- "synthetic_oracle_failover" when the oracle errored mid-run,
    -- "mixed" when rounds came from more than one source.
    source TEXT NOT NULL DEFAULT 'synthetic'
        CHECK (source IN ('oracle', 'synthetic', 'synthetic_oracle_failover', 'mixed')),
    -- The full forecastEnvelope array, in round order.
    envelopes JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Dominant read path: "recent forecasts for issue X, newest first".
CREATE INDEX IF NOT EXISTS idx_pythia_forecast_run_issue
    ON pythia_forecast_run (issue_id, created_at DESC);
