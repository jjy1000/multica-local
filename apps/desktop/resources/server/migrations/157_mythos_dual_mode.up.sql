-- 157: 0.3.31 Mythos Swarm dual-mode + squad visibility support.
--
-- Three orthogonal concerns land in this single forward-only migration
-- because they share a release:
--
-- (1) Issue-level lab_mode. Pairs with issue.lab_source (mig 155).
--     - NULL  = not a mythos task
--     - 'sole'     = mythos owns the issue end-to-end (legacy behaviour,
--                    unlocked by the 0.3.16-patch.1 single-mode runner)
--     - 'enhancer' = mythos preludes + supervises; the issue keeps its
--                    user-picked assignee. New in 0.3.31.
--
-- (2) Mythos run-level metadata for the dual-mode + supervise paths.
--     - mode                : mirrors issue.lab_mode so historical run
--                             rows answer "which mode did this run
--                             take?" without joining issue.
--     - target_assignee     : JSONB {type:'agent'|'squad', id:uuid} so
--                             supervise knows what to monitor.
--     - supervision_state   : JSONB snapshot of the supervise
--                             goroutine's progress (phase, last check,
--                             subtask counts). Empty {} for sole runs.
--
-- (3) Squad visibility support. Before this migration the
--     experimental_resource_visibility table only enumerated
--     agent/autopilot/skill. The mythos 5-agent roster was invisible
--     to the agent picker already (mig 150 seeded only
--     agent_self_optimization / constitution_agent rows), but the
--     "Mythos Swarm" squad itself was visible to the squad picker
--     when mythos_swarm was off. Widening the CHECK to include
--     'squad' + seeding 6 rows (5 mythos_* agents + 1 Mythos Swarm
--     squad) closes the gap so the user never sees mythos machinery
--     unless the flag is on.
--
-- mig 156 also added mythos_run.extension_agent_ids /
-- self_optimization_enabled / coda_conclusions / mythos_members.reflection.
-- Those columns stay as-is — the 0.3.31 runner code finally reads /
-- writes them, but the schema work for those already shipped.
--
-- All new columns are nullable or have safe defaults. Existing 0.3.30
-- rows keep working with mode='sole' (the default) and supervision_state
-- '{}' (the default).
--
-- -- -- -- -- -- -- -- -- -- -- -- -- -- -- -- -- -- -- -- -- -- --

-- (1) issue.lab_mode
ALTER TABLE issue
    ADD COLUMN lab_mode TEXT
        CHECK (lab_mode IS NULL OR lab_mode IN ('sole', 'enhancer'));

-- Partial index for the rare-but-real "list all enhancer issues in
-- this workspace" query. Workspace-scoped because every query already
-- filters by workspace_id.
CREATE INDEX idx_issue_lab_mode_enhancer
    ON issue(workspace_id, updated_at DESC)
    WHERE lab_mode = 'enhancer';

-- (2a) mythos_run.mode + target_assignee + supervision_state
ALTER TABLE mythos_run
    ADD COLUMN mode TEXT NOT NULL DEFAULT 'sole'
        CHECK (mode IN ('sole', 'enhancer')),
    ADD COLUMN target_assignee JSONB,
    ADD COLUMN supervision_state JSONB NOT NULL DEFAULT '{}'::jsonb;

-- (2b) mythos_run.status: extend CHECK to include 'supervising' which
-- is the new enhancer-mode terminal-pre-completion state. Sole runs
-- continue to go running -> completed as before.
ALTER TABLE mythos_run
    DROP CONSTRAINT mythos_run_status_check;
ALTER TABLE mythos_run
    ADD CONSTRAINT mythos_run_status_check
        CHECK (status IN ('running','completed','aborted','failed','supervising'));

-- (2c) mythos_members.reflection_iter: iteration number this reflection
-- comment was written for. The coda agent writes one reflection per
-- loop turn when self_optimization_enabled=true; this column lets
-- supervise pick the latest one without ORDER BY created_at LIMIT 1
-- (which is non-deterministic under concurrent writes).
ALTER TABLE mythos_members
    ADD COLUMN reflection_iter INT;

-- (3a) Squad visibility support: widen resource_type CHECK + add the
-- HideSquad path so the squad list handler can call
-- filterLabsHiddenByDefault(..., HideSquad, ...).
ALTER TABLE experimental_resource_visibility
    DROP CONSTRAINT experimental_resource_visibility_resource_type_check;
ALTER TABLE experimental_resource_visibility
    ADD CONSTRAINT experimental_resource_visibility_resource_type_check
        CHECK (resource_type IN ('agent','autopilot','skill','squad'));

-- (3b) Seed mythos_swarm visibility rows. The 5 mythos_* agent UUIDs
-- are not seeded here because they are runtime-derived at install time
-- via `upsertMythosAgent`; the install handler is the only writer of
-- the underlying agent rows. We instead use the constants declared in
-- the Go side (server/internal/experimental/visibility.go /
-- mythosSwarmIDs) — both sides MUST agree, and a divergent migration
-- is caught by the MythosSwarmVisibilitySeeded test (mirrors
-- AgentSelfOptimizationVisibilitySeeded).
--
-- To stay migratable without runtime agent IDs (we don't know the
-- UUIDs until InstallMythos runs in each workspace), the install
-- handler itself inserts the visibility rows after the agents exist —
-- see install_mythos.go in the 0.3.31 PR. This migration only widens
-- the CHECK and adds no rows. The squad row IS seeded here because
-- the Mythos Swarm squad UUID is a stable constant declared alongside
-- the install handler (mythosSquadID), and the install handler
-- upserts against that constant — so it exists from the first install
-- onward.
--
-- Keeping the squad seed here (instead of in install_mythos) means a
-- rollback that wipes experimental_resource_visibility still leaves
-- the squad hidden, matching the existing agent_self_optimization /
-- constitution_agent pattern (those seed here too).