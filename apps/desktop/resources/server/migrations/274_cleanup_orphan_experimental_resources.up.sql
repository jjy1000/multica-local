-- 274_cleanup_orphan_experimental_resources.up.sql
--
-- 0.5.60 (audit P0-3): one-shot cleanup of orphaned
-- experimental_resource_lock / experimental_resource_visibility rows.
-- Resource deletion cascades (runtime teardown, workspace CASCADE)
-- hard-delete agents/squads/skills/members without releasing their lock
-- or visibility rows, and no GC covered these tables (only swarm_run
-- locks are released by swarm_gc). At audit time 3401/3418 lock rows and
-- 378/434 visibility rows referenced vanished resources, growing daily.
-- The periodic counterpart lives in internal/experimental/lock_gc.go,
-- wired into the SwarmGC tick.

DELETE FROM experimental_resource_lock l
WHERE (l.resource_type = 'agent' AND NOT EXISTS (SELECT 1 FROM agent a WHERE a.id = l.resource_id))
   OR (l.resource_type = 'squad' AND NOT EXISTS (SELECT 1 FROM squad s WHERE s.id = l.resource_id))
   OR (l.resource_type = 'skill' AND NOT EXISTS (SELECT 1 FROM skill s WHERE s.id = l.resource_id))
   OR (l.resource_type = 'member' AND NOT EXISTS (SELECT 1 FROM member m WHERE m.id = l.resource_id))
   OR (l.resource_type = 'workspace' AND NOT EXISTS (SELECT 1 FROM workspace w WHERE w.id = l.resource_id));

DELETE FROM experimental_resource_visibility v
WHERE (v.resource_type = 'agent' AND NOT EXISTS (SELECT 1 FROM agent a WHERE a.id = v.resource_id))
   OR (v.resource_type = 'squad' AND NOT EXISTS (SELECT 1 FROM squad s WHERE s.id = v.resource_id));
