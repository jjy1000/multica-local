-- 290_pythia_forecast_continuity.up.sql
-- 0.5.111 pythia continuation batch: multi-run lineage + live-run status +
-- the synthesized conclusion report.
--
-- Before this migration a pythia_forecast_run was a standalone row: no
-- link to the run it continued from, no status (the row only appeared
-- AFTER the SSE loop finished, so "running" could only be guessed from a
-- sessionStorage timestamp), and the only text report was the mechanical
-- 140-rune comment summary.
--
-- This migration turns the table into the substrate for the SocialSim
-- alignment contract (continuation rounds with injected variables + a
-- real-time panel stream + an LLM-synthesized conclusion report):
--
--   parent_run_id — lineage. A continuation run points at the run whose
--     envelopes seeded its context (原问题 + 历轮摘要 + 新变量). NULL for
--     first runs. SET NULL on parent delete so deleting an old run never
--     cascades into its continuations (the issue FK already cascades).
--   run_kind      — 'initial' | 'continuation'. Continuations default to
--     6 rounds (user contract: 5-8 to save tokens) vs 3 for initial.
--   variables     — the user-injected variable text that shaped this
--     run, echoed in the report comment and the panel.
--   status        — 'running' | 'completed' | 'aborted' | 'failed'.
--     Rows are now created UPFRONT (status='running') and updated per
--     round, so the panel stream can replay + live-tail a run and a
--     crash mid-run is visible instead of invisible. Existing rows
--     backfill to 'completed' (they were written once, at the end).
--   report        — the LLM-synthesized conclusion report (markdown),
--     persisted so the panel report tab and the issue comment stay in
--     sync without re-calling the engine.
--   updated_at    — progress stamp; also drives the stale-'running'
--     sweep (a 'running' row untouched for >15 min is abandoned).
--
-- Forward-only additive: new nullable / defaulted columns + one index,
-- no existing data touched, no drops.

ALTER TABLE pythia_forecast_run
    ADD COLUMN IF NOT EXISTS parent_run_id UUID NULL
        REFERENCES pythia_forecast_run(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS run_kind TEXT NOT NULL DEFAULT 'initial'
        CHECK (run_kind IN ('initial', 'continuation')),
    ADD COLUMN IF NOT EXISTS variables TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'completed'
        CHECK (status IN ('running', 'completed', 'aborted', 'failed')),
    ADD COLUMN IF NOT EXISTS report TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

CREATE INDEX IF NOT EXISTS idx_pythia_forecast_run_parent
    ON pythia_forecast_run (parent_run_id);
