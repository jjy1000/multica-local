-- 156: Add lab_source column to experimental_claude_runtime_session +
-- experimental_runtime_artifact (0.3.29 Claude Lab tab data flow).
--
-- Background: 0.3.29 wires the Claude Lab Plan / Artifact / Code tabs to
-- issues tagged with `issue.lab_source`. To render those tabs the runtime
-- session and its artifacts must remember which lab spawned them, so the
-- UI can filter the artifact view by `lab_source='claude_science_lab'`
-- (forward-only, additive — older NULL rows continue to load).
--
-- This migration is purely additive:
--
--   1. experimental_claude_runtime_session.lab_source TEXT nullable.
--      Index covers (lab_source, created_at DESC) when non-NULL.
--   2. experimental_runtime_artifact.lab_source TEXT nullable.
--      Paired lifecycle with the session row.
--   3. experimental_runtime_artifact.issue_id UUID nullable.
--      Inherited from the parent session so the Artifact tab can
--      filter by issue without joining through experimental_claude_
--      runtime_session. Index covers (issue_id, created_at DESC) when
--      non-NULL. Forward-only: existing NULL rows continue to load.
--
-- The existing lab_id UUID column (migration 154) remains untouched.
-- lab_source is the canonical flag key (e.g. 'claude_science_lab');
-- lab_id is the per-flag UUID (used for visibility / lock lookups).
-- They coexist: lab_source is what the renderer filters by, lab_id is
-- what the Go visibility helper matches against the catalog.

ALTER TABLE experimental_claude_runtime_session
    ADD COLUMN IF NOT EXISTS lab_source TEXT;

ALTER TABLE experimental_runtime_artifact
    ADD COLUMN IF NOT EXISTS lab_source TEXT;

ALTER TABLE experimental_runtime_artifact
    ADD COLUMN IF NOT EXISTS issue_id UUID;

CREATE INDEX IF NOT EXISTS idx_experimental_runtime_session_lab_source
    ON experimental_claude_runtime_session(lab_source, created_at DESC)
    WHERE lab_source IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_experimental_runtime_artifact_lab_source
    ON experimental_runtime_artifact(lab_source, created_at)
    WHERE lab_source IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_experimental_runtime_artifact_issue_id
    ON experimental_runtime_artifact(issue_id, created_at DESC)
    WHERE issue_id IS NOT NULL;