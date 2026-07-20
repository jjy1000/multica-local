-- Rollback for 161_agent_system_key. Drops the column added in the up
-- migration. Use only during development / pre-production; production
-- rollbacks should follow the forward-only migration policy.
ALTER TABLE agent DROP COLUMN IF EXISTS system_key;