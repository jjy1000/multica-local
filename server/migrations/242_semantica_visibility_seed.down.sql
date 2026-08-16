-- 0.5.22 Semantica × Multica Phase 2 — down-migration.
--
-- Restore the pre-242 lock CHECK (no 'semantica'). Mirrors the
-- pre-241 value set so a rollback to mig 241 (swarm_topology) leaves
-- the DB consistent. Per-row visibility cleanup is the install path's
-- job, not the migration's — same contract as mig 241.
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
        'swarm_topology'::text
    ]));