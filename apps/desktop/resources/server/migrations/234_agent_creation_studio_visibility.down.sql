-- 0.5.3: rollback for migration 234 (dev only).

DELETE FROM experimental_resource_visibility
WHERE flag_key = 'agent_creation_studio'
  AND resource_id IN (
    '8a3c64b8-81bb-4dec-b5da-829f7c03506d',
    'ca01a7fe-4d36-48fd-97ed-9634bfcf7bbb',
    'a97b8b4a-68b8-44cb-aa82-2bc003b7f499',
    '1f50f990-acd6-4711-a255-a48b64dd4f56',
    '93061455-3883-42f4-9e4b-660cdd952dc3',
    '5b16a3d2-1447-4dc0-9b8e-de589dc30a83',
    '6a647967-f56e-4661-ad39-774420b870d4'
  );
