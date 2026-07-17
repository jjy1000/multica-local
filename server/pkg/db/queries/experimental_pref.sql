-- experimental_pref table: per-user experimental feature flag preferences.
-- See migration 145 for the schema. The pattern follows runtime_profile.sql
-- (no DB-side foreign keys; relational integrity is enforced in the app
-- layer because the user table is named "user" — a reserved word in SQL
-- that complicates migration authoring).
--
-- All four queries are tied to the (user_id, flag_key) UNIQUE constraint
-- defined in 145. UpsertExperimentalPref relies on ON CONFLICT to keep
-- the write path a single round-trip — the PATCH endpoint is on the
-- hot path of the labs UI and would otherwise need read-then-write
-- retry logic to handle concurrent toggles.

-- name: GetExperimentalPref :one
SELECT * FROM experimental_pref
WHERE user_id = $1 AND flag_key = $2;

-- name: UpsertExperimentalPref :one
INSERT INTO experimental_pref (user_id, flag_key, enabled)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, flag_key) DO UPDATE
SET enabled = EXCLUDED.enabled,
    updated_at = now()
RETURNING *;

-- name: ListExperimentalPrefsByUser :many
SELECT * FROM experimental_pref
WHERE user_id = $1
ORDER BY flag_key ASC;

-- name: DeleteExperimentalPref :exec
DELETE FROM experimental_pref
WHERE user_id = $1 AND flag_key = $2;