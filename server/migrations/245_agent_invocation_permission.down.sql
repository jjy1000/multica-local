-- 245 reverse: drop agent_invocation_target table + agent.permission_mode
-- column. Safe: pre-migration data is captured by the visibility column
-- (which the API layer keeps in sync as a derived legacy field).

DROP TABLE IF EXISTS agent_invocation_target;

ALTER TABLE agent
    DROP COLUMN IF EXISTS permission_mode;

SELECT 1;