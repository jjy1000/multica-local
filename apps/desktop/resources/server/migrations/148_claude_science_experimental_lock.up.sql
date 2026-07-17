-- experimental_resource_lock: per-resource experimental lock applied by the
-- Labs framework. A row in this table is the user's opt-in fingerprint:
-- it marks a domain row (skill / agent / squad / member / workspace /
-- mcp_server) as belonging to a specific lab (currently only
-- claude_science) and lets the read path hide it via the `hidden`
-- boolean.
--
-- Why a separate table (not a column on each domain table):
--   - zero-delta migrations for the domain tables (CLAUDE.md requires
--     additive-only schema changes);
--   - one place to enforce the row-level security policy at the SQL
--     layer, instead of duplicating the predicate in 5+ handler
--     helpers;
--   - the lock can be added to new resource types (PR 9 ships mcp_server)
--     by inserting, not by altering the domain table.
--
-- The lock rows themselves are system-owned. No user-facing handler in
-- this fork writes to this table — only the install / rollback endpoints
-- under /api/experimental-resources/{key}, which are gated by the same
-- feature flag they hide.

CREATE TABLE experimental_resource_lock (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- closed enum so a typo cannot accidentally attach a resource to a
    -- never-declared lab. Mirrors the Source constant in
    -- server/internal/experimental/locks.go.
    experimental_source TEXT NOT NULL
        CHECK (experimental_source IN ('claude_science')),
    -- closed enum of resource kinds the lock can attach to. Adding
    -- mcp_server in PR 9 is an additive enum value; existing rows keep
    -- working because the enum grows on the right.
    resource_type TEXT NOT NULL
        CHECK (resource_type IN
            ('workspace','skill','agent','squad','member','mcp_server')),
    resource_id UUID NOT NULL,
    -- false (default) ⇒ the underlying row is visible to user reads.
    -- true ⇒ reads through Visible* helpers skip it; the underlying
    -- domain row is NOT deleted, so toggling the flag on again
    -- restores 1:1 with no recreate cost.
    hidden BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    hidden_at TIMESTAMPTZ,
    UNIQUE(experimental_source, resource_type, resource_id)
);

-- Lookup-by-source hot path: "is this lab on or off, and which rows
-- are affected?" The partial index keeps the index compact when every
-- row is hidden=true (off state).
CREATE INDEX idx_lock_visible_by_source
    ON experimental_resource_lock(experimental_source, resource_type, resource_id)
    WHERE hidden = false;

-- Reverse lookup: "every row currently hidden by this source" — used
-- by the rollback path to flip hidden=true and by the activity panel
-- to count "items this lab controls". The hidden=false filter in the
-- index above is enough for installation; this one is for diagnostics.
CREATE INDEX idx_lock_hidden_by_source
    ON experimental_resource_lock(experimental_source, resource_type)
    WHERE hidden = true;
