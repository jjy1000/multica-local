-- 165: retire the entire constitution_agent Labs plugin.
--
-- 0.3.57 ships without the charter-guardian lab. Forward-only drop:
--   1. Delete the visibility seed rows for the constitution_agent flag.
--   2. Archive the 1 agent row (it was not yet archived — autopilots
--      were archived at 0.3.20). The archived_at column already exists
--      from migration 154_agent_archive.
--   3. The 3 autopilots were archived at 0.3.20 already (status='archived'
--      in the live DB) so the migration is idempotent for those rows.
--      We re-affirm status='archived' defensively in case a fresh
--      workspace was provisioned since 0.3.20.
--   4. The skill content lives only in the experiment boot loader
--      (resources/experiments/constitution_agent/skills/...) and the
--      builtin_skills multica-constitution-agent/ tree; the agent
--      removal path also strips the loader entries in code, so no
--      skill table row to delete here.
--
-- Note: agent.name is stored as a human-readable display name, not a
-- slug — the constitution agent's name is the Chinese '宪法智能体' on
-- this fork (see migration 153 seed row + release notes). autopilot
-- has no `name` column, only `title`, so we match on `title LIKE` for
-- the three CTR/CSIL/TAOL autopilots.
DELETE FROM experimental_resource_visibility WHERE flag_key = 'constitution_agent';

UPDATE agent
   SET archived_at = COALESCE(archived_at, now())
 WHERE name = '宪法智能体';

UPDATE autopilot
   SET status = 'archived'
 WHERE title IN (
    '宪章智能体 · 宪章三周评审（CTR）',
    '宪章智能体 · 宪章自优化循环（CSIL）',
    '宪法智能体 · 任务-智能体优化循环（TAOL）'
 );
