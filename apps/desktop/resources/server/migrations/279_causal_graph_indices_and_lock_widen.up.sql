-- 279_causal_graph_indices_and_lock_widen.up.sql
-- 0.5.83 WL3 (issue causal graph): read-path indices + Tier A/B dedup
-- anchor + experimental_resource_lock source widening.
--
-- Part 1 — causal_node / causal_edge indices. The dominant read paths
-- are the gated API surface (server/internal/handler/causal_graph.go):
--   - nodes by workspace (list) / by issue (subgraph seed, issue
--     filter on lists)
--   - edges by workspace (list) / by endpoint pair (BFS hop
--     expansion) / suggested-only (curation queue, partial index)
--
-- Part 2 — Tier A/B dedup anchor: partial unique index on the
-- provenance dedup_key. Recorder retries (task re-enqueue, decision
-- re-sync) collapse onto the same row instead of duplicating nodes.
-- Expression + WHERE predicate mirror the provenance->>'dedup_key'
-- stamp written by service/causal_graph/recorder.go.
--
-- Part 3 — widen experimental_resource_lock.experimental_source CHECK
-- with 'causal_graph' (0.5.82 lesson): a later phase Claims the lock
-- with source "causal_graph"; without the widened CHECK the INSERT
-- rejects with SQLSTATE 23514 and the install path hangs/fails. Same
-- drop + re-add shape as migs 149 / 154 / 160 / 163 / 242 / 275: the
-- constraint only validates inserts/updates, so existing rows are
-- unaffected. Retired flag literals are kept — the CHECK is
-- forward-only so historical rows remain valid.

CREATE INDEX IF NOT EXISTS idx_causal_node_workspace
    ON causal_node (workspace_id);
CREATE INDEX IF NOT EXISTS idx_causal_node_issue
    ON causal_node (issue_id);
CREATE INDEX IF NOT EXISTS idx_causal_edge_workspace
    ON causal_edge (workspace_id);
CREATE INDEX IF NOT EXISTS idx_causal_edge_from_to
    ON causal_edge (from_node_id, to_node_id);
CREATE INDEX IF NOT EXISTS idx_causal_edge_suggested
    ON causal_edge (status)
    WHERE status = 'suggested';

CREATE UNIQUE INDEX IF NOT EXISTS uq_causal_node_provenance_dedup_key
    ON causal_node (workspace_id, (provenance->>'dedup_key'))
    WHERE provenance->>'dedup_key' IS NOT NULL;

ALTER TABLE experimental_resource_lock
    DROP CONSTRAINT experimental_resource_lock_experimental_source_check;

ALTER TABLE experimental_resource_lock
    ADD CONSTRAINT experimental_resource_lock_experimental_source_check
    CHECK (experimental_source = ANY (ARRAY[
        'claude_science'::text,
        'claude_science_lab'::text,
        'mythos_swarm'::text,
        'agent_self_optimization'::text,
        'agent_creation_studio'::text,
        'pythia_oracle'::text,
        'llm_wiki_bridge'::text,
        'constitution_agent'::text,
        'code_canvas'::text,
        'swarm_topology'::text,
        'semantica'::text,
        'timesfm'::text,
        'causal_graph'::text
    ]));
