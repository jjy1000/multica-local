-- 0.5.6 down: no-op. The corresponding lock / visibility rows
-- were idempotent inserts against a catalog flag that no longer
-- exists. Re-inserting them on down would point at a flag the
-- server can no longer resolve (the catalog literal is gone); the
-- only safe recovery is a fresh install that re-creates the flag
-- (0.5.5.x or earlier) and then re-runs the visibility seeding.
--
-- If a real rollback is required, downgrade to 0.5.5.x, run
-- `multica experimental install agent_creation_studio` and
-- `multica experimental install agent_self_optimization` to
-- re-claim the lock + re-seed the visibility rows, then
-- downgrade the binary.

SELECT 1;
