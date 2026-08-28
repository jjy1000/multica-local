-- 278_causal_edge.up.sql
-- 0.5.83 WL3 (issue causal graph): causal_edge — one directed edge of
-- the issue-level causal graph. Edge types are a CONTRACT shared
-- verbatim with the client zod schemas and the docs: ('causes',
-- 'supports', 'contradicts', 'depends_on', 'enables', 'blocks').
-- Do not rename.
--
--   weight     — structural weight, default 1.0.
--   confidence — NULL until a scorer stamps it (Tier C calibration /
--     Tier D model confidence). Manual edges may leave it NULL.
--   proposed_by — NULL for user / system edges; 'curator' | 'evolver'
--     for proposed edges (Tier D / nightly evolver, next phase).
--   status     — 'active' (visible in subgraph / path and counted as
--     real) or 'suggested' (Tier D proposals awaiting the curation
--     gate: POST .../edges/{id}/confirm | /reject). The partial unique
--     index below guarantees at most ONE active edge per
--     (from, to, type) triple; suggested edges are unconstrained by
--     it so multiple hypotheses can coexist until confirmed.
--
-- Forward-only per CLAUDE.md: additive table, no existing objects
-- touched.

CREATE TABLE IF NOT EXISTS causal_edge (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    from_node_id UUID NOT NULL REFERENCES causal_node(id) ON DELETE CASCADE,
    to_node_id UUID NOT NULL REFERENCES causal_node(id) ON DELETE CASCADE,
    type TEXT NOT NULL
        CHECK (type IN ('causes', 'supports', 'contradicts', 'depends_on', 'enables', 'blocks')),
    weight NUMERIC(4,3) NOT NULL DEFAULT 1.0,
    confidence NUMERIC(4,3) NULL,
    metadata JSONB NOT NULL DEFAULT '{}',
    provenance JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by TEXT NULL,
    proposed_by TEXT NULL
        CHECK (proposed_by IN ('curator', 'evolver')),
    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'suggested'))
);

-- At most one ACTIVE edge per (from, to, type). Suggested rows are
-- excluded from the index so curator / evolver proposals never collide
-- with each other or with the live edge they might eventually replace.
CREATE UNIQUE INDEX IF NOT EXISTS uq_causal_edge_active_from_to_type
    ON causal_edge (from_node_id, to_node_id, type)
    WHERE status = 'active';
