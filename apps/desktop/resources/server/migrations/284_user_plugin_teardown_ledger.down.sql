ALTER TABLE user_plugin DROP COLUMN IF EXISTS created_by_issue;
ALTER TABLE user_plugin DROP COLUMN IF EXISTS created_by_task;
DROP TABLE IF EXISTS user_plugin_resource;
