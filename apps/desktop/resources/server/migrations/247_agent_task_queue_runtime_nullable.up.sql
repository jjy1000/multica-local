-- Migration 247 (partial port of upstream MUL-5559 / migration 251):
-- make agent_task_queue.runtime_id nullable so the runtime GC can detach
-- terminal task history instead of letting the ON DELETE CASCADE FK destroy
-- task_message / task_usage / task_token.
--
-- Only the agent_task_queue half is ported. The agent side of upstream 251
-- (agent.runtime_id DROP NOT NULL) and autopilot.pause_reason are deliberately
-- NOT ported: the fork's runtime-teardown path does not unbind agents, and
-- autopilot pause reasons are out of scope for this GC change.
--
-- The CHECK is the invariant that keeps NULL confined to history: an ACTIVE
-- task must always have a runtime, so claim / dispatch / delivery-CAS paths can
-- never observe runtime_id IS NULL. It is expressed with completed_at rather
-- than a status list because every terminal transition stamps completed_at.
-- NOT VALID keeps this a metadata-only add on a hot table (no ACCESS EXCLUSIVE
-- full-table scan); PostgreSQL still enforces it on every INSERT and UPDATE
-- from this point on. Existing rows all satisfy it (runtime_id was NOT NULL
-- until now), so there is nothing to back-fill.
ALTER TABLE agent_task_queue
    ALTER COLUMN runtime_id DROP NOT NULL;

ALTER TABLE agent_task_queue
    ADD CONSTRAINT agent_task_queue_active_requires_runtime
    CHECK (runtime_id IS NOT NULL OR completed_at IS NOT NULL)
    NOT VALID;
