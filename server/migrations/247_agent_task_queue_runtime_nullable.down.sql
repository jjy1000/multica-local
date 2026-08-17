-- Reverts 247_agent_task_queue_runtime_nullable.up.sql.
--
-- Restoring NOT NULL requires that no detached-history row exists. The down
-- migration does NOT delete detached task rows to make itself succeed — the
-- runtime GC only nulls runtime_id for terminal tasks whose runtime row was
-- deleted, so an operator rolling a schema back is not being asked to accept
-- that loss. It fails loudly while such rows are present.
ALTER TABLE agent_task_queue
    DROP CONSTRAINT IF EXISTS agent_task_queue_active_requires_runtime;

ALTER TABLE agent_task_queue
    ALTER COLUMN runtime_id SET NOT NULL;
