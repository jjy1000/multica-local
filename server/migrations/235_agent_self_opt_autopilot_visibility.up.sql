-- 0.5.3: hide the agent-engineering AUTOPILOTS that migration 150 /
-- install-time seeding missed. Root cause: install_agent_self_opt.go
-- provisions the two SkillOpt autopilots with NO-space titles
-- ("SkillOpt-Multica 每日自进化循环" / "智能体工程师团队·每3工作日批量优化"),
-- while migration 150 hard-coded visibility IDs for the 2026-06
-- space-separated legacy rows ("SkillOpt-Multica · 每日 00:00 自进化循环"
-- / "智能体工程师团队 · 每3工作日批量优化"). A re-install (2026-07-19)
-- created NEW rows under the no-space titles; their visibility rows never
-- landed. A third autopilot ("[智能体工程] multica_work 定期巡查（7 天）",
-- user-created 2026-06-30, assignee 智能体工程负责人) was never seeded at
-- all.
--
-- All three dispatch to lab-hidden agents (智能体优化专家 /
-- 智能体工程负责人) and belong to the agent_engineering lab — hide them
-- behind the agent_self_optimization flag exactly like the seeded pair.
--
-- Idempotent: ON CONFLICT DO NOTHING.

INSERT INTO experimental_resource_visibility (flag_key, resource_type, resource_id) VALUES
    ('agent_self_optimization', 'autopilot', '124f2a14-c210-44ab-912a-6490e449a252')
ON CONFLICT (flag_key, resource_type, resource_id) DO NOTHING;

-- The two no-space install-created rows (resolved by title at runtime by
-- the install handler for other workspaces; here they are the 2026-07-19
-- rows — resolve via a subquery so this migration survives a re-install
-- that recreates them with fresh ids).
INSERT INTO experimental_resource_visibility (flag_key, resource_type, resource_id)
SELECT 'agent_self_optimization', 'autopilot', ap.id
FROM autopilot ap
WHERE ap.workspace_id = '283d3de3-e3a8-4ede-b27b-6f039a87b881'
  AND ap.title IN ('SkillOpt-Multica 每日自进化循环', '智能体工程师团队·每3工作日批量优化')
  AND NOT EXISTS (
      SELECT 1 FROM experimental_resource_visibility v
      WHERE v.flag_key = 'agent_self_optimization'
        AND v.resource_type = 'autopilot'
        AND v.resource_id = ap.id
  );
