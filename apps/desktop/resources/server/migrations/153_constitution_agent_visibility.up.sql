-- 153: constitution_agent flag seed rows.
--
-- The constitution_agent Labs flag hides the 宪法智能体 agent + its
-- 3 autopilots (CTR 三周评审 / CSIL 宪章自优化 / TAOL 任务-智能体优
-- 化) by default. Toggling the flag on restores them without re-
-- provisioning — the agent row, autopilot rows, and the
-- multica-constitution-agent Skill content (charter v6 + CSIL/CTR/
-- TAOL protocols in apps/desktop/resources/experiments/
-- constitution_agent/skills/multica-constitution-agent/SKILL.md)
-- stay in the DB / resources tree.
--
-- IDs are sourced from the live local workspace; the mirror constant
-- lives in server/internal/experimental/visibility.go (see
-- constitutionAgentIDs). Hard-coding here keeps the visibility layer
-- a single SELECT per list handler; the helper-side constants are
-- only consulted by the autopilot scheduler's shouldSkipDispatch hot
-- path (same as AgentSelfOptimizationAutopilotIDs).
--
-- Note: there's no skill row yet — the multica-constitution-agent
-- Skill content ships via the experiment boot loader
-- (server/internal/service/builtin_skills.go::scanExperimentSkills)
-- and is not stored in the workspace skill table. Adding a skill
-- visibility row would point at a non-existent ID and the CHECK
-- constraint would still pass (no FK), but we keep the seed row count
-- honest by omitting it.
INSERT INTO experimental_resource_visibility (flag_key, resource_type, resource_id) VALUES
    ('constitution_agent', 'agent',     '125890ef-a7a2-4f94-80e2-a4ecb03407b5'),
    ('constitution_agent', 'autopilot', 'eb4f3a30-5604-47c4-919c-29757cf9bfa0'),
    ('constitution_agent', 'autopilot', 'e6bc3a0e-aac3-4b77-8f0f-5e85889369a5'),
    ('constitution_agent', 'autopilot', 'b3da8c47-81d0-45ae-94f7-152dc416c6cf');