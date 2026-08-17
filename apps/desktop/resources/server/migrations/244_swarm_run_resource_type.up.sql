-- 0.5.22 (P0 fix, audit 2026-08-16): widen
-- experimental_resource_lock.resource_type CHECK to include 'swarm_run'.
-- Without this widening, handler/swarm_run.go::PostSwarmRun's
-- `experimental.Claim(ctx, h.Queries, SourceSwarmTopology,
-- ResourceType("swarm_run"), run.ID)` returns ErrUnknownResourceType
-- from lock.go::Claim's switch, every bootstrap 500s.
--
-- Same shape as mig 157 (squad) and mig 148 (mcp_server): drop +
-- re-add the CHECK with the full value set. Existing rows are
-- unaffected (the constraint only validates inserts). The CHECK is
-- forward-only so historical rows remain valid — exactly the
-- pattern documented in CLAUDE.md "experimental_resource_lock CHECK
-- constraints outlive retired flags".

ALTER TABLE experimental_resource_lock
    DROP CONSTRAINT experimental_resource_lock_resource_type_check;

ALTER TABLE experimental_resource_lock
    ADD CONSTRAINT experimental_resource_lock_resource_type_check
    CHECK (resource_type = ANY (ARRAY[
        'workspace'::text,
        'skill'::text,
        'agent'::text,
        'squad'::text,
        'member'::text,
        'mcp_server'::text,
        'swarm_run'::text
    ]));

SELECT 1;