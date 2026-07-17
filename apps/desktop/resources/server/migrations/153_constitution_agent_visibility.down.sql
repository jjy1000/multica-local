-- 153 down: drop the constitution_agent seed rows. Forward-only policy
-- still applies for upgrades; this down file exists for symmetric
-- rollback during development. Production rollback must follow the
-- same flag-off-then-migrate order as the agent_self_optimization
-- down file (see 150_experimental_resource_visibility.down.sql).
DELETE FROM experimental_resource_visibility WHERE flag_key = 'constitution_agent';