-- 156: 0.3.29 Mythos Swarm round extension — extra agents, self-reflection, structured conclusions.
--
-- Background: 0.3.28 shipped the core Mythos RDT three-stage runner
-- (prelude + loop + coda, five-agent canonical roster, real sub-issue
-- forking, hard-cap 5 iters, 1 run / 5min rate limit, workspace-scoped
-- root_issue_id with cross-workspace auth bypass fix). 0.3.29 closes
-- the original Mythos vision by adding three user-facing knobs:
--
--   1. User-picked extension agents — beyond the canonical 5-agent
--      roster, the user can attach additional agents (any agent in
--      the active workspace) that participate in loop iterations.
--      This is the ONLY lab that allows a free agent picker —
--      claude_science_lab, pythia_oracle, and the other flags keep
--      the agent roster locked to the install handler's choice.
--
--   2. Self-reflection / self-optimization — when enabled, the coda
--      agent writes a per-iteration reflection comment after each
--      loop turn. The reflection text is stored on the loop member
--      row (mythos_members.reflection) and rendered in the Mythos
--      view as a feedback signal for human-in-the-loop tuning.
--
--   3. Structured conclusions — the coda stage now publishes a JSONB
--      array (mythos_run.coda_conclusions) of named keys (e.g. areas
--      of consensus, areas of disagreement, follow-up actions). The
--      UI renders these alongside the free-text summary so the user
--      gets both prose and machine-readable structure.
--
-- This migration is forward-only and additive. All new columns are
-- nullable or have sensible defaults so the change applies cleanly to
-- running mythos_run rows from 0.3.27/0.3.28 without backfill.
--
-- No reserved workspace is created (the lab already lives in the
-- caller's active workspace per 0.3.25). No FKs are altered; the new
-- extension_agent_ids column stores JSONB UUID arrays because the
-- cross-agent relationship is polymorphic (member or agent — only
-- agent is currently supported but the JSONB keeps the door open).

-- (1) Extension agents: append-only list of extra UUIDs the user
-- chose beyond the canonical loop roster. Stored as JSONB so the
-- shape can grow (e.g. future "extra coda" or "human override")
-- without more migrations. Default '[]' (no extensions).
ALTER TABLE mythos_run
    ADD COLUMN extension_agent_ids JSONB NOT NULL DEFAULT '[]'::jsonb;

-- (2) Self-optimization toggle. Default false so old runs (which
-- ran without reflection) keep their original semantics. A simple
-- BOOLEAN — no enum — because the runner reads it as a straight flag.
ALTER TABLE mythos_run
    ADD COLUMN self_optimization_enabled BOOLEAN NOT NULL DEFAULT false;

-- (3) Structured conclusions from the coda agent. JSONB array of
-- {key, value} pairs (caller defined). Default '[]' so old runs
-- stay legacy-text-only.
ALTER TABLE mythos_run
    ADD COLUMN coda_conclusions JSONB NOT NULL DEFAULT '[]'::jsonb;

-- (4) Per-iteration reflection. The coda agent writes one reflection
-- per loop member; nullable so pre-0.3.29 rows / non-reflective runs
-- stay readable. TEXT (not JSONB) — the reflection is a single
-- short free-form note, the structured conclusions already cover
-- the machine-readable side.
ALTER TABLE mythos_members
    ADD COLUMN reflection TEXT;

-- Index the extension_agent_ids JSONB (GIN) so queries that filter
-- runs by participant can use the index when the participant set
-- gets large. Cheap on small arrays (1-5 entries).
CREATE INDEX idx_mythos_run_extension_agents
    ON mythos_run USING GIN (extension_agent_ids);

-- Partial index on self_optimization_enabled = true so a future
-- "show me only self-reflective runs" query stays fast as the
-- table grows.
CREATE INDEX idx_mythos_run_self_opt
    ON mythos_run(workspace_id, started_at DESC)
    WHERE self_optimization_enabled = true;
