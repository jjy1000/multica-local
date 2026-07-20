-- Rollback for 162_lab_resource_visibility_backfill. Flips every lab
-- lock row that this migration touched back to hidden=false. Use only
-- during development / pre-production; production rollbacks should
-- follow the forward-only migration policy.
UPDATE experimental_resource_lock
SET hidden = false,
    hidden_at = NULL
WHERE resource_type IN ('agent', 'skill', 'squad', 'member', 'mcp_server')
  AND experimental_source IN (
    'claude_science',
    'claude_science_lab',
    'claude_science_runtime',
    'mythos_swarm',
    'agent_self_optimization',
    'constitution_agent'
  );