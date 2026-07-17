-- experimental_resource_visibility: per-resource hiding driven by Labs
-- flags. 0.3.17 agent_self_optimization flag uses this to remove the
-- 智能体优化专家 agent + its 2 autopilots + the skillopt-multica Skill
-- from the visible agent team / automation / skill lists when the flag
-- is off.
--
-- A row means "this (flag_key, resource_type, resource_id) is hidden by
-- default and shown only when the user opts into the flag". We do NOT
-- model "explicitly shown" — every Catalog entry has DefaultVal=false
-- per the Labs constraint, so the natural state for any new flag is
-- "hidden". The visibility table is therefore write-once per resource:
-- seed rows go here once, and toggling the flag never rewrites this
-- table (it only swaps the filter at query time).
--
-- resource_type is a closed enum so adding a new hideable surface
-- requires a code change, not a runtime string. The companion Go file
-- server/internal/experimental/visibility.go must enumerate the same
-- set; adding a value here without updating the Go validator will fail
-- the corresponding CHECK violation cleanly.
--
-- No FK on resource_id by design — visibility is metadata over an
-- arbitrary resource table; we don't want CASCADE chains through the
-- resource table to silently lose visibility rows.

CREATE TABLE experimental_resource_visibility (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    flag_key TEXT NOT NULL,
    resource_type TEXT NOT NULL
        CHECK (resource_type IN ('agent','autopilot','skill')),
    resource_id UUID NOT NULL,
    hidden BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (flag_key, resource_type, resource_id)
);

-- Lookup by (flag_key, resource_type) is the only access pattern the
-- server uses (ListAutopilot / ListAgent / ListSkill pass them in).
-- Index ordering puts resource_type first because the flag set is
-- small (one flag in 0.3.17) and the resource_type filter is the
-- selective one once we grow flag count.
CREATE INDEX idx_experimental_resource_visibility_lookup
    ON experimental_resource_visibility(flag_key, resource_type);

-- 0.3.17 agent_self_optimization seed: hide the 智能体优化专家 agent,
-- its 2 autopilots (per-3-workday bulk optimization + daily SkillOpt
-- self-evolution loop), and the skillopt-multica Skill.
--
-- The agent row stays in the squad — we hide it from the visible
-- agent roster only. squad_member FK is preserved so leader briefs
-- still resolve. The autopilot rows are kept in the DB so re-enabling
-- the flag restores scheduling without re-provisioning.
--
-- IDs are hard-coded as constants in the deployment log and in
-- server/internal/experimental/visibility.go::agentSelfOptimizationIDs;
-- a divergent migration would be caught by the LabCatalogHasFlag
-- test in the same package.
INSERT INTO experimental_resource_visibility (flag_key, resource_type, resource_id) VALUES
    ('agent_self_optimization', 'agent',     '6a647967-f56e-4661-ad39-774420b870d4'),
    ('agent_self_optimization', 'autopilot', 'f788217e-ef6a-4af0-a5a1-cbf85d8dbb8e'),
    ('agent_self_optimization', 'autopilot', 'ab5de2d9-7af9-491d-a42f-2c9f87fbcdf3'),
    ('agent_self_optimization', 'skill',     '18edfaed-c493-4a61-89b4-8b6bff84d8fc');
