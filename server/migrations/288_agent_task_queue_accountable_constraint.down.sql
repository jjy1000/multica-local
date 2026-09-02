-- Undo 288: drop the constraint only if it carries the relaxed definition
-- this migration installed. A lineage whose constraint is a different
-- definition was not changed by 288 and is not ours to drop here.
DO $$
DECLARE
    want text := 'CHECK (((originator_user_id IS NULL) OR (accountable_user_id IS NULL) OR (accountable_user_id = originator_user_id)))';
    have text;
BEGIN
    SELECT pg_get_constraintdef(oid) INTO have
    FROM pg_constraint
    WHERE conrelid = 'agent_task_queue'::regclass
      AND conname = 'agent_task_queue_accountable_matches_originator';

    IF have = want THEN
        ALTER TABLE agent_task_queue DROP CONSTRAINT agent_task_queue_accountable_matches_originator;
    END IF;
END $$;
