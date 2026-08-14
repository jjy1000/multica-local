-- 0.5.18 M4: code_canvas issue-bound artifact persistence. Backs
-- POST /api/experimental/code-canvas/issues/{issueId}/artifacts, which
-- renders a code snippet through the code_canvas subprocess and persists
-- the self-contained HTML canvas so it survives unmount, plus the
-- history list the LabOutputPanel reads. Additive + idempotent.
CREATE TABLE IF NOT EXISTS code_canvas_artifact (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    code TEXT NOT NULL,
    language TEXT NOT NULL DEFAULT 'text',
    html TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_code_canvas_artifact_issue
    ON code_canvas_artifact (issue_id, created_at DESC);
