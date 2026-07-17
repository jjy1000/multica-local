-- experimental_pref table: per-user experimental feature flag preferences.
-- Forward-only: created with no FK to "user" because the user table is named
-- "user" (reserved word) and the application layer is already the authority
-- for user identity (see auth handlers). Mirrors the pattern from migration
-- 120 (runtime_profile) where relational integrity is enforced at the app
-- layer rather than the DB.
--
-- UNIQUE (user_id, flag_key) is the primary key constraint that gives us the
-- "one preference per (user, flag) pair" semantic the PATCH endpoint needs.
-- ON CONFLICT DO UPDATE in the UpsertExperimentalPref sqlc query handles
-- idempotent writes without a separate INSERT-then-UPDATE round-trip.
--
-- created_at + updated_at: updated_at is set by the application layer (Go
-- writes now() on every UPSERT) rather than a DB trigger. The other feature
-- tables (runtime_profile, chat_session, etc.) follow the same pattern — no
-- trigger boilerplate, easier to read.
CREATE TABLE IF NOT EXISTS experimental_pref (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    flag_key TEXT NOT NULL,
    enabled BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, flag_key)
);

-- Lookups by user are the only access pattern (the ListExperimentalPrefsByUser
-- query + the per-flag GetExperimentalPref query both filter by user_id).
-- The unique index above already covers per-(user,flag) lookups; this
-- secondary index lets the per-user list scan stay cheap as the catalog grows.
CREATE INDEX IF NOT EXISTS idx_experimental_pref_user_id
    ON experimental_pref (user_id);