-- Renumbered from upstream 206_agent_disabled_runtime_skills.up.sql → fork 223_agent_disabled_runtime_skills.up.sql
-- (upstream 0.4.0 integration plan, wave 2; continues docs/upstream-integration/2026-07-22-migration-renumber-map.md)
-- Iron rule: IF NOT EXISTS added where applicable (CREATE TABLE / INDEX / ADD COLUMN).

-- Per-agent opt-out list for runtime-provided skills. Inert until the Go
-- service layer is ported (schema-first, same as issue_properties in fork
-- 206-211); the '[]' default keeps every existing agent unchanged.
ALTER TABLE agent
ADD COLUMN IF NOT EXISTS disabled_runtime_skills JSONB NOT NULL DEFAULT '[]'::jsonb;
