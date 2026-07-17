-- experimental_claude_runtime_session + experimental_runtime_artifact
-- 0.3.19+ claude_science_runtime Labs flag backing tables.
--
-- A session represents one run of code by an agent on a research
-- issue; an artifact is a single file the session emitted (PNG/SVG/
-- HTML/JSON/CSV/MD/etc.). Both are forward-only; sessions and their
-- artifacts are GC'd after 30 days by the background goroutine
-- (server/internal/experimental/runtime_gc.go).
--
-- Hard constraints baked into the schema:
--
--  1. status is a closed enum to keep the GC / cleanup state machine
--     narrow. Adding a state requires the same update in
--     server/internal/experimental/runtime.go::SessionStatus* constants.
--
--  2. expires_at defaults to now()+30d so callers don't have to compute
--     retention. The GC loop respects expires_at — running a session
--     that has already expired returns ErrSessionExpired from the
--     handler.
--
--  3. artifacts.path is relative to ~/.multica/experimental/claude-
--     science/runtime/<session_uuid>/ and is NOT exposed to the HTTP
--     API surface directly — the handler joins session_id and verifies
--     the row belongs to the calling workspace before serving bytes.
--     This keeps the listing composable without leaking sibling
--     workspaces' files.
--
--  4. No FK on workspace_id / agent_id / issue_id by design — the
--     experimental subsystem uses the experimental lock pattern
--     (see lock.go) which already enforces access scoping at read
--     time, and FK chains through the rest of the schema would block
--     forward-only cleanup of legacy rows when a workspace is
--     deleted out from under an open session.

CREATE TABLE experimental_claude_runtime_session (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    issue_id UUID,
    language TEXT NOT NULL
        CHECK (language IN ('python')),
    code TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued','running','completed','failed','expired','timeout')),
    exit_code INTEGER,
    stdout TEXT,
    stderr TEXT,
    summary TEXT,
    duration_ms INTEGER,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL DEFAULT (now() + INTERVAL '30 days')
);

-- Hot read paths: workspace-scoped listing by created_at desc, plus
-- the GC sweep on expires_at. status is in the second index because
-- the workspace listing filters on status='completed' or NULL
-- (live rows) depending on view.
CREATE INDEX idx_experimental_runtime_session_ws_created
    ON experimental_claude_runtime_session(workspace_id, created_at DESC);
CREATE INDEX idx_experimental_runtime_session_expires
    ON experimental_claude_runtime_session(expires_at)
    WHERE status IN ('queued','running','completed','failed');

CREATE TABLE experimental_runtime_artifact (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    name TEXT NOT NULL,
    kind TEXT NOT NULL
        CHECK (kind IN ('png','svg','html','json','csv','md','txt','log')),
    bytes INTEGER NOT NULL,
    sha256 TEXT NOT NULL,
    path TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (session_id, name)
);

-- Listing by session is the only access pattern the UI uses; bytes
-- are never JOINed into the row (handler reads the file from disk
-- after the auth check). Add a (workspace_id, created_at) index
-- later if a workspace-wide artifact view ever ships.
CREATE INDEX idx_experimental_runtime_artifact_session
    ON experimental_runtime_artifact(session_id, created_at);
