-- 279_causal_graph_indices_and_lock_widen.down.sql
-- 0.5.83 WL3 down-migration (dev / fresh-test-DB rollback only).
-- Forward-only production rule: never run down.sql on a live DB.
--
-- Symmetric reverse of the up script: drop the indices, restore the
-- pre-279 lock CHECK (without 'causal_graph').

DROP INDEX IF EXISTS uq_causal_node_provenance_dedup_key;
DROP INDEX IF EXISTS idx_causal_edge_suggested;
DROP INDEX IF EXISTS idx_causal_edge_from_to;
DROP INDEX IF EXISTS idx_causal_edge_workspace;
DROP INDEX IF EXISTS idx_causal_node_issue;
DROP INDEX IF EXISTS idx_causal_node_workspace;

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
        'timesfm'::text
    ]));
