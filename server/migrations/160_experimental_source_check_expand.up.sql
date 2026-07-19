-- 0.3.45.4: expand experimental_resource_lock.experimental_source
-- CHECK to cover every catalog flag. The original mig 146 only
-- accepted claude_science, claude_science_lab, mythos_swarm. 0.3.22
-- dropped claude_science; 0.3.45.1 added agent_self_optimization; the
-- remaining labs (pythia_oracle, llm_wiki_bridge) had no install
-- handler until 0.3.45.4.
--
-- Forward-only: drop + re-add the CHECK with the new value set.
-- Existing rows are unaffected; the constraint only validates
-- inserts.

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
        'llm_wiki_bridge'::text
    ]));
