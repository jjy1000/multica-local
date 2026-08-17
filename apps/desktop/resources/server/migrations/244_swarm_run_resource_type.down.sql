-- 244 reverse: restore pre-244 resource_type CHECK. Safe because
-- the migration only adds the 'swarm_run' value (no rows depend on
-- it — the install/handler paths are the only writers and they're
-- forward-only).
ALTER TABLE experimental_resource_lock
    DROP CONSTRAINT experimental_resource_lock_resource_type_check;

ALTER TABLE experimental_resource_lock
    ADD CONSTRAINT experimental_resource_lock_resource_type_check
    CHECK (resource_type IN (
        'workspace','skill','agent','squad','member','mcp_server'
    ));

SELECT 1;