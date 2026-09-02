-- Land the relaxed agent_task_queue_accountable_matches_originator CHECK on
-- every lineage. The constraint's statement of intent lives in 167 ("if both
-- are set they must match; either may be NULL on its own"), but no fresh
-- install could ever reach it there: 167 runs before 240 creates the columns
-- it references, and its gated ADD (2026-09-02 fresh-install fix) correctly
-- skips on that path. This migration runs after 240 on every database, so it
-- is where the constraint actually materializes for fresh installs.
--
-- Idempotent by definition comparison:
--   - missing            → add the relaxed CHECK (fresh-install path; the
--                          table is empty there, and any restored dump from
--                          a stricter lineage satisfies the relaxed rule a
--                          fortiori, so the ADD cannot fail on data).
--   - already relaxed    → no-op (the shipped database's current state).
--   - a different (e.g. the pre-0.3.61 strict) definition → swap to relaxed.
--                          A swap can only fail on rows that violate the
--                          relaxed rule while living under a stricter
--                          constraint — impossible, since anything passing a
--                          stricter CHECK passes the relaxed one — so a
--                          failure here means the schema was hand-edited
--                          into an unknown state and needs a human.
--
-- Forward-only + additive per CLAUDE.md: nothing is dropped unless it is
-- being replaced by the same-named constraint with the intended definition.
DO $$
DECLARE
    want text := 'CHECK (((originator_user_id IS NULL) OR (accountable_user_id IS NULL) OR (accountable_user_id = originator_user_id)))';
    have text;
BEGIN
    SELECT pg_get_constraintdef(oid) INTO have
    FROM pg_constraint
    WHERE conrelid = 'agent_task_queue'::regclass
      AND conname = 'agent_task_queue_accountable_matches_originator';

    IF have IS NULL THEN
        ALTER TABLE agent_task_queue ADD CONSTRAINT agent_task_queue_accountable_matches_originator
            CHECK (
                (originator_user_id IS NULL)
                OR (accountable_user_id IS NULL)
                OR (accountable_user_id = originator_user_id)
            );
    ELSIF have <> want THEN
        ALTER TABLE agent_task_queue DROP CONSTRAINT agent_task_queue_accountable_matches_originator;
        ALTER TABLE agent_task_queue ADD CONSTRAINT agent_task_queue_accountable_matches_originator
            CHECK (
                (originator_user_id IS NULL)
                OR (accountable_user_id IS NULL)
                OR (accountable_user_id = originator_user_id)
            );
    END IF;
END $$;
