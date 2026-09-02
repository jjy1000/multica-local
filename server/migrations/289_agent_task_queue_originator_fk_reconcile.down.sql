-- Undo 289's schema half: drop the two foreign keys where this migration
-- added them. Lineages that already had them from 240 keep them (289 was a
-- no-op there). Nulled dangling references are not restored — they were
-- contract-invalid values with no meaning to recover.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'agent_task_queue'::regclass
          AND conname = 'agent_task_queue_originator_user_id_fkey'
    ) THEN
        ALTER TABLE agent_task_queue DROP CONSTRAINT agent_task_queue_originator_user_id_fkey;
    END IF;
    IF EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'agent_task_queue'::regclass
          AND conname = 'agent_task_queue_accountable_user_id_fkey'
    ) THEN
        ALTER TABLE agent_task_queue DROP CONSTRAINT agent_task_queue_accountable_user_id_fkey;
    END IF;
END $$;
