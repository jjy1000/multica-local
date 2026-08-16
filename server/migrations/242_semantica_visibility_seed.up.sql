-- 0.5.22 Semantica × Multica Phase 2 — widen
-- experimental_resource_lock.experimental_source CHECK to include
-- 'semantica'. Without this widening, install_semantica.go's
-- `experimental.Claim(ctx, h.Queries, "semantica", LockAgent, agentID)`
-- INSERTs reject with 23514 "violates check constraint
-- experimental_resource_lock_experimental_source_check".
--
-- Same shape as mig 160 (pythia_oracle + llm_wiki_bridge), mig 163
-- (code_canvas + constitution_agent), and mig 241 (swarm_topology):
-- drop + re-add the CHECK with the full value set. Existing rows are
-- unaffected (the constraint only validates inserts).
--
-- Forward-only per CLAUDE.md. The retired flag literals
-- (agent_self_optimization, agent_creation_studio, constitution_agent)
-- are kept in the CHECK even though their catalog entries are gone —
-- per CLAUDE.md "experimental_resource_lock CHECK constraints outlive
-- retired flags — and that's expected", the CHECK is forward-only so
-- historical rows remain valid.

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

-- Visibility seed placeholder: the install handler
-- (server/internal/handler/install_semantica.go::upsertSemanticaVisibility)
-- writes the per-leader experimental_resource_visibility row at install
-- time with ON CONFLICT DO NOTHING. No backfill is needed for Phase 2
-- because every fresh install seeds the row, and re-runs are idempotent.
SELECT 1;