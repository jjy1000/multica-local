-- Restore the 166 full-table unique constraints. May fail if
-- duplicate slugs now exist among deleted rows — acceptable for a
-- down migration (migrations are forward-only in practice).
DROP INDEX IF EXISTS idx_user_plugin_slug_live;
DROP INDEX IF EXISTS idx_user_plugin_flag_key_live;

ALTER TABLE user_plugin ADD CONSTRAINT user_plugin_slug_key UNIQUE (slug);
ALTER TABLE user_plugin ADD CONSTRAINT user_plugin_flag_key_key UNIQUE (flag_key);
