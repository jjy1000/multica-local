-- Migration 249 (0.5.31): truncate abandoned auth tables.
--
-- The localized fork's auth contract (CLAUDE.md "Localized fork") is
-- username-only: `SendCode`, `VerifyCode`, and `GoogleLogin` all return
-- 410 Gone; `cloud PAT` was deleted. The two tables that backed those
-- flows have NO insert path in the fork:
--
--   - verification_code    (migration 009, email one-time codes)
--   - personal_access_tokens (migration 011, cloud API PAT)
--
-- Any rows present are pure legacy residue from pre-fork installs.
-- TRUNCATE (not DELETE) is used because:
--   (a) no FKs reference these tables from product tables (verified
--       via pg_constraint catalog during the 0.5.30 audit), so
--       TRUNCATE cannot cascade-delete user data;
--   (b) the operation is irreversible anyway — once a row is gone,
--       there is no recovery path without a backup. A migration is
--       the right place for an irreversible cleanup because it lands
--       in `schema_migrations` and a `down` could no-op honestly
--       (see `249_cleanup_expired_auth_tables.down.sql`).
--
-- The three auth-token tables that DO have a live insert path
-- (task_token, workspace_invitation, daemon_token) are NOT touched
-- here. They are swept by AuthTokenGC (server/internal/experimental/
-- auth_token_gc.go, 0.5.31), which runs every 6h with a 15s
-- per-table sub-context timeout.

TRUNCATE TABLE verification_code;
TRUNCATE TABLE personal_access_tokens;