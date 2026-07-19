-- Revert mig 160: shrink CHECK back to the 0.3.22 set. New rows
-- inserted under the wider CHECK would block this revert, so the
-- down migration deletes them first (matching the spirit of mig
-- 146 which silently dropped visibility rows on rollback).

DELETE FROM experimental_resource_lock
WHERE experimental_source IN (
    'agent_self_optimization',
    'pythia_oracle',
    'llm_wiki_bridge'
);

ALTER TABLE experimental_resource_lock
    DROP CONSTRAINT experimental_resource_lock_experimental_source_check;

ALTER TABLE experimental_resource_lock
    ADD CONSTRAINT experimental_resource_lock_experimental_source_check
    CHECK (experimental_source = ANY (ARRAY[
        'claude_science'::text,
        'claude_science_lab'::text,
        'mythos_swarm'::text
    ]));
