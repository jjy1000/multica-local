-- Reconcile lineages whose agent_task_queue predates the 240 foreign keys.
--
-- 240 added originator_user_id / accountable_user_id with
-- REFERENCES "user"(id) ON DELETE SET NULL — but via ADD COLUMN IF NOT
-- EXISTS, which is all-or-nothing per column: on databases where the
-- columns had been added out-of-band before 240 ran (the fork's own
-- lineage), the IF NOT EXISTS skip also skipped the FK clauses. Those
-- databases therefore accepted values that the declared schema forbids —
-- and the agent-authored-mention enqueue path did write them: agent ids
-- landed in originator_user_id (a user-id column) until the 2026-09-02
-- code fix stopped producing them.
--
-- This migration makes every lineage match what a fresh install gets from
-- 240:
--
--   1. Null out dangling references — values that name no "user" row.
--      Reading code already treats them as "no originator" (every join
--      against "user" misses), so NULLing is behavior-preserving, and NULL
--      is exactly what the resolution contract (MUL-4304) specifies for an
--      unresolvable chain.
--   2. Add the two foreign keys when absent. Fresh databases already have
--      them from 240, so the guard makes this a no-op there.
--
-- Idempotent by construction; forward-only (the FKs mirror 240's
-- definition; nulled values are not recoverable, by design — they were
-- contract-invalid).

UPDATE agent_task_queue atq
SET originator_user_id = NULL
WHERE atq.originator_user_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM "user" u WHERE u.id = atq.originator_user_id);

UPDATE agent_task_queue atq
SET accountable_user_id = NULL
WHERE atq.accountable_user_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM "user" u WHERE u.id = atq.accountable_user_id);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'agent_task_queue'::regclass
          AND conname = 'agent_task_queue_originator_user_id_fkey'
    ) THEN
        ALTER TABLE agent_task_queue ADD CONSTRAINT agent_task_queue_originator_user_id_fkey
            FOREIGN KEY (originator_user_id) REFERENCES "user"(id) ON DELETE SET NULL;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'agent_task_queue'::regclass
          AND conname = 'agent_task_queue_accountable_user_id_fkey'
    ) THEN
        ALTER TABLE agent_task_queue ADD CONSTRAINT agent_task_queue_accountable_user_id_fkey
            FOREIGN KEY (accountable_user_id) REFERENCES "user"(id) ON DELETE SET NULL;
    END IF;
END $$;
