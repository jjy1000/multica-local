-- MCP sync mirror (migration 285). Rows are written only by the mcpsync
-- worker; the read side is the settings "MCP 管理" tab (redacted) and the
-- claim-time merge in internal/handler/daemon.go (status='synced' rows only).

-- name: UpsertMcpSyncServer :one
-- `(xmax = 0)` is true only on the INSERT leg (runtime.sql's
-- UpsertAgentRuntime trick) so the worker can count adds vs updates.
INSERT INTO mcp_sync_server (name, definition, source_hash, status, last_seen_at, updated_at)
VALUES (sqlc.arg('name'), sqlc.arg('definition'), sqlc.arg('source_hash'), 'synced', now(), now())
ON CONFLICT (name) DO UPDATE SET
    definition = EXCLUDED.definition,
    source_hash = EXCLUDED.source_hash,
    status = 'synced',
    last_seen_at = now(),
    updated_at = now()
RETURNING *, (xmax = 0) AS inserted;

-- name: MarkMcpSyncServersRemoved :execrows
-- Source-driven removal: servers absent from the current snapshot flip to
-- 'removed' instead of being deleted. Empty @names marks everything, which
-- is the correct semantics for "source file parsed fine but has no servers".
UPDATE mcp_sync_server
SET status = 'removed', updated_at = now()
WHERE status <> 'removed'
  AND name != ALL(sqlc.arg('names')::text[]);

-- name: GetMcpSyncServers :many
SELECT * FROM mcp_sync_server ORDER BY name;

-- name: GetSyncedMcpServerDefinitions :many
-- Claim-merge read: only live rows, only the two columns the merge needs.
SELECT name, definition FROM mcp_sync_server WHERE status = 'synced' ORDER BY name;

-- name: UpsertMcpSyncState :exec
INSERT INTO mcp_sync_state (id, last_synced_at, last_source_hash, last_error)
VALUES (TRUE, now(), sqlc.arg('source_hash'), sqlc.arg('last_error'))
ON CONFLICT (id) DO UPDATE SET
    last_synced_at = now(),
    last_source_hash = EXCLUDED.last_source_hash,
    last_error = EXCLUDED.last_error;

-- name: TouchMcpSyncState :exec
-- Fast-path bump for "source read fine, mcpServers subtree unchanged" —
-- refreshes the freshness indicator without rewriting any mirror rows.
UPDATE mcp_sync_state SET last_synced_at = now() WHERE id = TRUE;

-- name: RecordMcpSyncStateError :exec
-- Error-path write for "source missing/unreadable/malformed": the mirror and
-- its hash stay untouched (the last good snapshot remains the merge source),
-- only the error string is surfaced so the settings tab can explain staleness.
UPDATE mcp_sync_state SET last_error = sqlc.arg('last_error') WHERE id = TRUE;

-- name: GetMcpSyncState :one
SELECT * FROM mcp_sync_state WHERE id = TRUE;
