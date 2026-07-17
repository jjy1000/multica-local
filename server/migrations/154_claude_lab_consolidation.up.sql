-- 154: 0.3.22 Lab Consolidation (claude_science_lab)
--
-- Background: 0.3.20 ships two Labs flags:
--   - `claude_science`       — the in-app research workspace
--   - `claude_science_runtime` — the Python sandbox runtime
-- 0.3.22 folds both into a single `claude_science_lab` flag with one
-- sidebar entry, one view (`/experimental/claude-lab`), one runtime
-- gate. The Skill adapter (multica-claude-science) and the runtime
-- HTTP handler route prefix are unchanged for wire-compat.
--
-- This migration is purely additive and forward-only:
--
--   1. experimental_resource_lock.experimental_source CHECK grows to
--      accept 'claude_science_lab'. The old 'claude_science' value is
--      retained so any 0.3.20 rows already written (e.g. by 0.3.20
--      beta users with the flag enabled) keep validating. The
--      catalog.go no longer registers a flag for 'claude_science'
--      so no new rows land under the old value; existing rows are
--      inherited by the new lab's visibility filter (see
--      labs_visibility_filter.go::VisibilityFor — 'claude_science_lab'
--      supersedes 'claude_science' for display purposes).
--
--   2. experimental_claude_runtime_session gets a `lab_id` column
--      (nullable UUID). New sessions write lab_id; legacy rows keep
--      NULL and continue to be filtered by workspace_id only. The
--      Go handler reads lab_id from the request body, falling back
--      to the workspace's lab_id recorded in experimental_pref
--      (set on first install by install_claude_lab.go in a future
--      0.3.23 PR — this migration only provisions the schema).
--
--   3. experimental_pref seeded with the user's existing labId when
--      migrating from 0.3.20: if the user has any experimental_pref
--      row tagged 'claude_science' (legacy), we keep it but no new
--      reads will use it. lab_id rows for the new lab are generated
--      lazily by the runtime handler when the first session arrives
--      and no pref row exists.
--
-- Why a separate forward-only migration instead of amending 148 /
-- 151: those migrations are shipped in 0.3.20 and 0.3.19; editing
-- their already-released SQL would violate the "never rewrite a
-- released migration" contract (see CLAUDE.md: forward-only). New
-- behaviour lives here.

-- 1. Experimental lock source CHECK widened.
ALTER TABLE experimental_resource_lock
    DROP CONSTRAINT IF EXISTS experimental_resource_lock_experimental_source_check;
ALTER TABLE experimental_resource_lock
    ADD CONSTRAINT experimental_resource_lock_experimental_source_check
    CHECK (experimental_source IN ('claude_science','claude_science_lab','mythos_swarm'));

-- 2. Runtime session gains lab_id. Nullable so 0.3.20 rows keep
--    loading. The handler reads it for the visibility filter.
ALTER TABLE experimental_claude_runtime_session
    ADD COLUMN IF NOT EXISTS lab_id UUID;

-- Same for the artifact table — paired lifecycle.
ALTER TABLE experimental_runtime_artifact
    ADD COLUMN IF NOT EXISTS lab_id UUID;

CREATE INDEX IF NOT EXISTS idx_experimental_runtime_session_lab
    ON experimental_claude_runtime_session(lab_id, created_at DESC)
    WHERE lab_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_experimental_runtime_artifact_lab
    ON experimental_runtime_artifact(lab_id, created_at)
    WHERE lab_id IS NOT NULL;

-- 3. No bulk row rewrite — 0.3.20 lab_id is implicitly NULL, and the
--    handler treats NULL lab_id as "legacy / hidden-by-workspace
--    filter only". New sessions written by 0.3.22+ always include
--    lab_id from experimental_pref (handler-generated if absent).

-- Documentation note: rollback strategy is documented but the down
-- migration is intentionally narrow (it cannot drop the lab_id
-- columns because 0.3.22+ code references them). The CHECK rollback
-- removes 'claude_science_lab' / 'mythos_swarm' acceptance so a
-- downgraded server refuses to read its own catalog.