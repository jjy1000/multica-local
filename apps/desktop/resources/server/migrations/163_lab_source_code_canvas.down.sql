-- Rollback for 163_lab_source_code_canvas.
-- Remove `code_canvas` from the CHECK constraint. Pre-production only;
-- forward-only migration policy applies in production.

ALTER TABLE experimental_resource_lock
    DROP CONSTRAINT experimental_resource_lock_experimental_source_check;

ALTER TABLE experimental_resource_lock
    ADD CONSTRAINT experimental_resource_lock_experimental_source_check
    CHECK (experimental_source = ANY (ARRAY[
        'claude_science'::text,
        'claude_science_lab'::text,
        'mythos_swarm'::text,
        'agent_self_optimization'::text,
        'pythia_oracle'::text,
        'llm_wiki_bridge'::text,
        'constitution_agent'::text
    ]));
