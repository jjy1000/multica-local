-- 277_causal_node.up.sql
-- 0.5.83 WL3 (issue causal graph): causal_node — one vertex of the
-- issue-level causal graph. Node types are a CONTRACT shared verbatim
-- with the client zod schemas (packages/core/api/causal_graph.ts) and
-- the docs: ('decision','action','outcome','assumption','evidence',
-- 'constraint'). Do not rename.
--
--   provenance — the Tier A/B/C dedup anchor: {"source": ..., ...
--     native ids ..., "dedup_key": ...}. A partial unique index on
--     (workspace_id, provenance->>'dedup_key') (mig 279) makes
--     recorder retries collapse to the same row.
--   issue_id NULL — abstract nodes are allowed (workspace-scoped
--     assumptions, constraints, evidence not tied to one issue).
--   lab_source / lab_run_id — nullable stamp for nodes contributed by
--     a Labs run (parallel to issue.lab_source); NULL for native /
--     mirrored nodes.
--   status — 'active' by default; recorder-originated nodes are
--     always active (the suggested gate applies to EDGES only).
--
-- Forward-only per CLAUDE.md: additive table, no existing objects
-- touched.

CREATE TABLE IF NOT EXISTS causal_node (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    -- NULL = abstract node (assumption / constraint / evidence that
    -- spans issues). CASCADE: a node bound to an issue has no meaning
    -- detached from it.
    issue_id UUID NULL REFERENCES issue(id) ON DELETE CASCADE,
    type TEXT NOT NULL
        CHECK (type IN ('decision', 'action', 'outcome', 'assumption', 'evidence', 'constraint')),
    label TEXT NOT NULL,
    description TEXT NULL,
    metadata JSONB NOT NULL DEFAULT '{}',
    -- Source-system stamp + native ids for dedup (see above).
    provenance JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by TEXT NULL,
    lab_source TEXT NULL,
    lab_run_id UUID NULL,
    status TEXT NOT NULL DEFAULT 'active',
    last_observed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
