-- Forward-only policy: this .down.sql exists for development parity
-- (so `make migrate down` works locally). Production deployments never
-- roll forward migrations back; see CLAUDE.md "Data Safety & Version
-- Upgrades" — migrations are append-only.
DROP INDEX IF EXISTS idx_experimental_pref_user_id;
DROP TABLE IF EXISTS experimental_pref;