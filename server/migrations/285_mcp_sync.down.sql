DROP TABLE IF EXISTS mcp_sync_state;
DROP TABLE IF EXISTS mcp_sync_server;
ALTER TABLE agent_task_queue DROP COLUMN IF EXISTS mcp_calls;
