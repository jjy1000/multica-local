-- 0.5.21 (MUL-4304 fork-local): add originator_user_id + accountable_user_id
-- columns to agent_task_queue. Migration 167 references both in the
-- agent_task_queue_accountable_matches_originator CHECK constraint but
-- never ADDed the columns — the constraint is therefore latent until the
-- columns exist. Closing the gap so reconcileCommentsOnCompletion's
-- (MUL-4304) ResolveOriginatorFromTriggerComment chain can actually
-- resolve the top-of-chain human for an agent-authored comment.
--
-- Idempotent + additive (CLAUDE.md: forward-only additive migrations).
-- On a fresh DB this is the first time the columns appear. On the
-- user's existing fork DB migration 167 was applied without these
-- columns (constraint drop succeeded; constraint add referenced
-- nonexistent columns and would have raised, so the constraint is
-- absent today); IF NOT EXISTS makes the ADD COLUMN idempotent in case
-- some other code path added them out-of-band.
ALTER TABLE agent_task_queue ADD COLUMN IF NOT EXISTS originator_user_id UUID REFERENCES "user"(id) ON DELETE SET NULL;
ALTER TABLE agent_task_queue ADD COLUMN IF NOT EXISTS accountable_user_id UUID REFERENCES "user"(id) ON DELETE SET NULL;
-- Backfill: existing tasks without an originator inherit the issue
-- creator when the creator is a member. NULL for agent-created issues
-- (MUL-4195's recompute-on-merge path then fixes them on the next
-- comment). Forward-only — the backfill is a one-shot best-effort.
UPDATE agent_task_queue atq
SET originator_user_id = issue.creator_id
FROM issue
WHERE atq.issue_id = issue.id
  AND issue.creator_type = 'member'
  AND atq.originator_user_id IS NULL;
UPDATE agent_task_queue atq
SET accountable_user_id = issue.creator_id
FROM issue
WHERE atq.issue_id = issue.id
  AND issue.creator_type = 'member'
  AND atq.accountable_user_id IS NULL;