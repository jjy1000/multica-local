-- 165 down: restore the constitution_agent visibility rows + agent
-- archived state. Symmetric rollback for development.
INSERT INTO experimental_resource_visibility (flag_key, resource_type, resource_id) VALUES
    ('constitution_agent', 'agent',     '125890ef-a7a2-4f94-80e2-a4ecb03407b5'),
    ('constitution_agent', 'autopilot', 'eb4f3a30-5604-47c4-919c-29757cf9bfa0'),
    ('constitution_agent', 'autopilot', 'e6bc3a0e-aac3-4b77-8f0f-5e85889369a5'),
    ('constitution_agent', 'autopilot', 'b3da8c47-81d0-45ae-94f7-152dc416c6cf')
ON CONFLICT DO NOTHING;

UPDATE agent
   SET archived_at = NULL
 WHERE name = '宪法智能体';

UPDATE autopilot
   SET status = 'active'
 WHERE title IN (
    '宪章智能体 · 宪章三周评审（CTR）',
    '宪章智能体 · 宪章自优化循环（CSIL）',
    '宪法智能体 · 任务-智能体优化循环（TAOL）'
 );
