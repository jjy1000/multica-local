-- Migration 249 (0.5.31) DOWN: TRUNCATE is irreversible for row data.
-- The schema is unchanged by the up migration (no DDL was issued —
-- TRUNCATE only removes rows). Running `migrate down` on this
-- migration is a documented no-op; the tables remain empty.
--
-- If a user explicitly needs the legacy rows back, they must
-- restore from a pre-update snapshot taken via
-- `~/.multica/scripts/pre-update-snapshot.sh` (CLAUDE.md §Ship
-- chain step 1). The migration history does not retain row data.

SELECT 'irreversible: see 249_cleanup_expired_auth_tables.up.sql comment';