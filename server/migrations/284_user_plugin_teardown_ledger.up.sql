-- Teardown ledger (0.5.89): every Multica-owned resource a user plugin
-- provisions inline (agents/skills created by the server on the plugin's
-- behalf) or declares by name (squads/autopilots/skills the manifest
-- references) is recorded here so DeleteUserPlugin can reclaim plugin-owned
-- resources instead of leaving orphans behind.
--
-- origin:
--   'provisioned' — the server CREATED this resource for the plugin
--     (manifest capabilities.agents_inline / skills_inline, or the
--     plugin's persistent env dir). Safe to reclaim on delete.
--   'declared'    — the manifest merely NAMES a pre-existing resource
--     (capabilities.agents/squads/autopilots/skills). Never reclaimed —
--     the resource belongs to the user; delete only drops its visibility
--     rows and marks the ledger row 'skipped'.
--
-- resource_id is NULL only for the 'env_dir' row type, which uses the
-- owning user_plugin row's UUID as a stable sentinel so the UNIQUE
-- constraint dedupes across re-provisioning (NULLs never conflict in PG).
CREATE TABLE IF NOT EXISTS user_plugin_resource (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    plugin_slug TEXT NOT NULL,
    resource_type TEXT NOT NULL CHECK (resource_type IN ('agent', 'squad', 'autopilot', 'skill', 'env_dir')),
    resource_id UUID,
    origin TEXT NOT NULL DEFAULT 'declared' CHECK (origin IN ('provisioned', 'declared')),
    reclaim_status TEXT NOT NULL DEFAULT 'linked' CHECK (reclaim_status IN ('linked', 'reclaimed', 'skipped', 'failed')),
    created_by_task UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (plugin_slug, resource_type, resource_id)
);

CREATE INDEX idx_user_plugin_resource_slug ON user_plugin_resource (plugin_slug);
CREATE INDEX idx_user_plugin_resource_unreclaimed ON user_plugin_resource (plugin_slug)
    WHERE reclaim_status IN ('linked', 'failed');

-- Conversational provenance (0.5.89): when a user plugin is created from
-- inside an agent task ("帮我做一个 XX 插件" in any issue), the CLI stamps
-- the creating task/issue so the Labs settings page can show where the
-- plugin came from. Nullable — UI/API creates carry no provenance.
ALTER TABLE user_plugin ADD COLUMN created_by_issue UUID;
ALTER TABLE user_plugin ADD COLUMN created_by_task UUID;
