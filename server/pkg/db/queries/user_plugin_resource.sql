-- user_plugin_resource: teardown ledger (0.5.89, mig 284). One row per
-- resource a user plugin provisions or declares, so DeleteUserPlugin can
-- reclaim plugin-owned resources and report what it did.

-- name: InsertUserPluginResource :exec
INSERT INTO user_plugin_resource (workspace_id, plugin_slug, resource_type, resource_id, origin, created_by_task)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (plugin_slug, resource_type, resource_id) DO NOTHING;

-- name: ListUserPluginResources :many
SELECT * FROM user_plugin_resource WHERE plugin_slug = $1 ORDER BY created_at ASC, id ASC;

-- name: UpdateUserPluginResourceReclaimStatus :execrows
UPDATE user_plugin_resource SET reclaim_status = $2 WHERE id = $1;

-- name: CountActiveIssuesByLabSource :one
-- Terminal statuses mirror isTerminalIssueStatus (service/mythos/supervise.go):
-- done / closed / cancelled. A plugin with non-terminal bound issues cannot be
-- deleted (DeleteUserPlugin 409 guard).
SELECT COUNT(*) FROM issue WHERE lab_source = $1 AND status NOT IN ('done', 'closed', 'cancelled');

-- Reclaim primitives — each is idempotent (no-op on already-reclaimed rows)
-- so a failed reclaim can be retried via POST /api/user-plugins/{slug}/reclaim.

-- name: ArchiveUserPluginAgent :execrows
UPDATE agent SET archived_at = now(), status = 'offline'
WHERE id = $1 AND archived_at IS NULL;

-- name: ArchiveUserPluginSquad :execrows
UPDATE squad SET archived_at = now()
WHERE id = $1 AND archived_at IS NULL;

-- name: DisableUserPluginAutopilot :execrows
-- The autopilot status CHECK is ('active','paused','archived') — 'paused' is
-- the "kept but inert" state that mirrors the reclaim contract.
UPDATE autopilot SET status = 'paused'
WHERE id = $1 AND status = 'active';

-- name: GetUserPluginBySlugAnyStatus :one
-- The reclaim-retry path runs against an already-soft-deleted plugin, which
-- GetUserPluginBySlug filters out; this variant ignores status.
SELECT * FROM user_plugin WHERE slug = $1;
