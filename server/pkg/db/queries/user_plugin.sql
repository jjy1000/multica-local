-- user_plugin table: user-created plugins that extend the built-in
-- experimental catalog (0.3.60 Labs sandbox). Each row maps to a
-- dynamic Flag merged into the Registry at boot; flag_key is always
-- "user_<slug>". Relational integrity for created_by is enforced in
-- the app layer (same convention as experimental_pref.sql — the user
-- table is a reserved word in SQL).

-- name: CreateUserPlugin :one
-- 0.5.89: created_by_issue/created_by_task carry conversational provenance
-- (nullable — UI/API creates leave them NULL; the agent-context CLI stamps
-- them from MULTICA_ISSUE_ID / MULTICA_TASK_ID).
INSERT INTO user_plugin (slug, flag_key, title_en, title_zh, description_en, description_zh, manifest_json, trigger_mode, runtime_kind, status, created_by, created_by_issue, created_by_task)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING *;

-- name: GetUserPluginBySlug :one
SELECT * FROM user_plugin WHERE slug = $1 AND status != 'deleted';

-- name: GetUserPluginByFlagKey :one
SELECT * FROM user_plugin WHERE flag_key = $1 AND status != 'deleted';

-- name: ListUserPlugins :many
SELECT * FROM user_plugin WHERE status != 'deleted' ORDER BY created_at DESC;

-- name: ListActiveUserPlugins :many
SELECT * FROM user_plugin WHERE status = 'active' ORDER BY created_at DESC;

-- name: UpdateUserPlugin :one
UPDATE user_plugin SET
    title_en = $2,
    title_zh = $3,
    description_en = $4,
    description_zh = $5,
    manifest_json = $6,
    trigger_mode = $7,
    runtime_kind = $8,
    status = $9,
    updated_at = now()
WHERE slug = $1 AND status != 'deleted'
RETURNING *;

-- name: SoftDeleteUserPlugin :exec
UPDATE user_plugin SET status = 'deleted', updated_at = now() WHERE slug = $1;
