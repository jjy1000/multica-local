-- 147: backfill delivered_comment_ids for in-flight tasks (MUL-4195 / MUL-4304)
--
-- The column itself was added by migration 141 (locally) / 157 (upstream).
-- Migration 141's local backfill did NOT include this UPDATE — we do it here
-- so completion reconciliation has a truthful starting point: every active
-- task that did claim with a trigger comment is recorded as having received
-- that trigger. Anything posted DURING the run and not in this receipt will
-- earn a follow-up via reconcileCommentsOnCompletion.
--
-- We deliberately do not backfill coalesced_comment_ids for the same reason
-- upstream did: an older daemon may have ignored the structured fields, and
-- a duplicate replay is safer than silently losing a deliberate instruction.
--
-- Renumbered from upstream 157 to 147 to fit the local fork's migration slot.
UPDATE agent_task_queue
SET delivered_comment_ids = ARRAY[trigger_comment_id]
WHERE trigger_comment_id IS NOT NULL
  AND status IN ('dispatched', 'running', 'waiting_local_directory')
  AND (delivered_comment_ids IS NULL OR delivered_comment_ids = '{}');