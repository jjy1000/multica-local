-- experimental_resource_visibility table: per-resource hiding driven by
-- Labs flags. 0.3.17 agent_self_optimization flag uses this to remove
-- the 智能体优化专家 agent + its 2 autopilots + the skillopt-multica
-- Skill from the visible agent team / automation / skill lists when
-- the flag is off.
--
-- The only access pattern the server uses is "give me the set of
-- resource_ids hidden by flag X of type Y". This is what ListHiddenIDs
-- returns. The caller then injects `WHERE id NOT IN (...)` into its
-- own list query.
--
-- We deliberately do NOT JOIN against agent / autopilot / skill from
-- here — visibility is metadata over resources that may be soft-archived
-- or otherwise not joinable, and pulling those tables in here would
-- re-introduce the soft-delete coupling the visibility table exists
-- to avoid.

-- name: ListHiddenResourceIDs :many
SELECT resource_id FROM experimental_resource_visibility
WHERE flag_key = $1 AND resource_type = $2 AND hidden = TRUE;

-- name: InsertExperimentalResourceVisibility :exec
-- 0.3.31: install-time visibility seed. Used by install_mythos.go to
-- hide the 5 mythos_* agents + the Mythos Swarm squad from the regular
-- agent/squad picker. ON CONFLICT DO NOTHING makes re-running install
-- a no-op. The CHECK on resource_type covers agent/autopilot/skill/
-- squad (widened in mig 157); no further validation here.
INSERT INTO experimental_resource_visibility (flag_key, resource_type, resource_id, hidden)
VALUES ($1, $2, $3, TRUE)
ON CONFLICT (flag_key, resource_type, resource_id) DO NOTHING;

-- name: ListLabManagedResourceIDs :many
-- 0.3.56: "lab-managed" marker source-of-truth. A row's EXISTENCE in the
-- visibility table means "this resource belongs to a Labs flag and is
-- infrastructure, not a standalone actor" — independent of the row's
-- `hidden` value and of whether the flag is currently on. ListAgents /
-- ListSquads use this to stamp `lab_managed` on the DTO so the frontend
-- can grey+disable the row in selection pickers and hide it from the
-- browse lists, while keeping the row in the list payload so the shared
-- useActorName name/avatar map still resolves it (a lab agent that is
-- auto-assigned as an issue leader, or that authors a comment, must keep
-- rendering by id). We do NOT filter by flag_key here on purpose: an
-- agent seeded by any flag is lab-managed regardless of which one.
SELECT resource_id FROM experimental_resource_visibility
WHERE resource_type = $1;
