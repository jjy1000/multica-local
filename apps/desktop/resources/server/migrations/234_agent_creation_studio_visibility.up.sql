-- 0.5.3: hide the 智能体工程师团队 (Agent Engineering Squad) from the
-- regular agent / squad lists. The user manually built this squad
-- (2026-06-26) as the workspace's agent-engineering team; 0.5.3 folded
-- its mandate into the agent_creation_studio lab (issue-bound:
-- agent_creation_expert leader + the studio's create/modify/maintain
-- duties). Per the user requirement, the engineering resources must NOT
-- appear in the normal agent/team pickers — they are lab infrastructure,
-- visible only when the lab flag is on.
--
-- Hiding works through the standard visibility contract:
-- experimental_resource_visibility rows + filterLabsHiddenByDefault
-- (flag OFF → hidden from lists; flag ON → shown). ListAgents /
-- ListSquads iterate experimental.AllFlagKeys(), so a row under
-- 'agent_creation_studio' is honored by both.
--
-- The squad + its 6 members (verified against the workspace 2026-08-02):
--   squad  智能体工程师团队        8a3c64b8-81bb-4dec-b5da-829f7c03506d
--   agent  智能体工程负责人        ca01a7fe-4d36-48fd-97ed-9634bfcf7bbb (squad leader)
--   agent  智能体专家              a97b8b4a-68b8-44cb-aa82-2bc003b7f499
--   agent  外挂知识库专家          1f50f990-acd6-4711-a255-a48b64dd4f56
--   agent  自动化专家              93061455-3883-42f4-9e4b-660cdd952dc3
--   agent  技能专家                5b16a3d2-1447-4dc0-9b8e-de589dc30a83
--   agent  智能体优化专家          6a647967-f56e-4661-ad39-774420b870d4
--
-- The IDs are this workspace's runtime values; the install handler
-- (install_agent_creation_studio.go) re-upserts by NAME for any other
-- workspace, so this migration is a local-seed, not a global contract.
--
-- Idempotent: ON CONFLICT DO NOTHING (re-running migrate is a no-op).

INSERT INTO experimental_resource_visibility (flag_key, resource_type, resource_id) VALUES
    ('agent_creation_studio', 'squad', '8a3c64b8-81bb-4dec-b5da-829f7c03506d'),
    ('agent_creation_studio', 'agent', 'ca01a7fe-4d36-48fd-97ed-9634bfcf7bbb'),
    ('agent_creation_studio', 'agent', 'a97b8b4a-68b8-44cb-aa82-2bc003b7f499'),
    ('agent_creation_studio', 'agent', '1f50f990-acd6-4711-a255-a48b64dd4f56'),
    ('agent_creation_studio', 'agent', '93061455-3883-42f4-9e4b-660cdd952dc3'),
    ('agent_creation_studio', 'agent', '5b16a3d2-1447-4dc0-9b8e-de589dc30a83'),
    ('agent_creation_studio', 'agent', '6a647967-f56e-4661-ad39-774420b870d4')
ON CONFLICT (flag_key, resource_type, resource_id) DO NOTHING;
