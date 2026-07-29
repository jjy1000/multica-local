-- 0.3.63: soft-deleted plugins must not block slug/flag_key reuse.
-- 166 created column-level UNIQUE constraints on slug and flag_key,
-- but deletion is a soft delete (status = 'deleted'), so re-creating
-- a plugin with a previously used slug hit 409 forever. Replace the
-- full-table constraints with partial unique indexes scoped to live
-- rows — this matches the lookup queries, which all filter
-- status != 'deleted'.
ALTER TABLE user_plugin DROP CONSTRAINT IF EXISTS user_plugin_slug_key;
ALTER TABLE user_plugin DROP CONSTRAINT IF EXISTS user_plugin_flag_key_key;

CREATE UNIQUE INDEX IF NOT EXISTS idx_user_plugin_slug_live
    ON user_plugin (slug) WHERE status != 'deleted';
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_plugin_flag_key_live
    ON user_plugin (flag_key) WHERE status != 'deleted';
