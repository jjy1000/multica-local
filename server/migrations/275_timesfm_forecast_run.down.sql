-- 0.5.82 WL2 down-migration (dev / fresh-test-DB rollback only).
-- Forward-only production rule: never run down.sql on a live DB.
--
-- Restore the pre-275 lock CHECK (no 'timesfm') and drop the run
-- table. Mirrors the pre-242/275 convention: per-row lock/visibility
-- cleanup is the install path's job, not the migration's.

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
        'semantica'::text
    ]));

DROP TABLE IF EXISTS timesfm_forecast_run;
