-- 0.5.2: rollback for the agent trust tables (dev / rollback only — migrations
-- are forward-only in production, this file exists for symmetry with the
-- migration convention).

DROP TABLE IF EXISTS agent_trust_event;
DROP TABLE IF EXISTS agent_trust_profile;
