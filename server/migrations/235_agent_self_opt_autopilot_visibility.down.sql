-- 0.5.3: rollback for migration 235 (dev only).

DELETE FROM experimental_resource_visibility
WHERE flag_key = 'agent_self_optimization'
  AND resource_type = 'autopilot'
  AND resource_id IN ('124f2a14-c210-44ab-912a-6490e449a252');
