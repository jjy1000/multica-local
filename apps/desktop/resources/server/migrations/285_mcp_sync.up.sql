-- MCP sync (0.5.92): a read-only mirror of the user's Claude Code MCP
-- servers (~/.claude.json::mcpServers), maintained by the in-server mcpsync
-- worker.
--
-- Contract: rows in mcp_sync_server are written ONLY by the sync worker
-- applying a full snapshot of the source file. No API edits or deletes a
-- row — "synced servers cannot be deleted from Multica" holds by there
-- being no code path that deletes them. When the source drops a server the
-- row flips to status='removed' (kept for observability, excluded from the
-- claim-time merge) rather than disappearing. The agent's own manual
-- mcp_config remains the per-agent override layer; see the claim merge in
-- internal/handler/daemon.go (manual wins on name collisions).
CREATE TABLE mcp_sync_server (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    definition JSONB NOT NULL,
    source_hash TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'synced' CHECK (status IN ('synced', 'removed')),
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (name)
);

-- Singleton worker state (pattern: causal_graph_maintenance_state, mig 281).
-- last_source_hash is the canonical-JSON hash of the source's mcpServers
-- subtree — NOT the whole file, which Claude Code rewrites on every startup
-- (numStartups, feature caches) and would defeat mtime/whole-file change
-- detection.
CREATE TABLE mcp_sync_state (
    id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
    last_synced_at TIMESTAMPTZ NOT NULL DEFAULT 'epoch',
    last_source_hash TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT ''
);
INSERT INTO mcp_sync_state (id) VALUES (TRUE) ON CONFLICT DO NOTHING;

-- Task-level MCP tool-call volume (0.5.92 "MCP 调用量" usage metric). The
-- daemon counts stream tool_use events whose tool name carries the
-- `mcp__<server>__` prefix and reports the total through the existing
-- POST /api/daemon/tasks/{id}/usage channel, so failed/blocked runs are
-- captured the same way tokens are. Task-level on purpose: unlike tokens
-- there is no per-model split, so the dashboard aggregates it straight from
-- agent_task_queue (same treatment as run time / task counts, no hourly
-- rollup column).
ALTER TABLE agent_task_queue
    ADD COLUMN mcp_calls INTEGER NOT NULL DEFAULT 0;

COMMENT ON COLUMN agent_task_queue.mcp_calls IS
    'MCP tool calls observed during the run (stream tool_use events with the mcp__ prefix). Reported via the daemon usage channel; 0 for tasks that predate the metric or used no MCP tools.';
