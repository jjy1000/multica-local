-- Skill CRUD

-- name: ListSkillsByWorkspace :many
SELECT * FROM skill
WHERE workspace_id = $1
ORDER BY name ASC;

-- name: ListSkillSummariesByWorkspace :many
-- Same as ListSkillsByWorkspace but omits the SKILL.md `content` column. Used
-- by list endpoints (CLI table, web list page) where the body is never read;
-- shipping it everywhere blew up payload size on workspaces with many skills
-- and caused 15s CLI timeouts from high-latency regions (GH multica-ai/multica#2174).
SELECT id, workspace_id, name, description, config, created_by, created_at, updated_at
FROM skill
WHERE workspace_id = $1
ORDER BY name ASC;

-- name: ListVisibleSkillsByWorkspace :many
-- Reads filtered by experimental lock: returns only rows that are NOT
-- currently claimed hidden by any experimental lab. Used by the GUI
-- skill picker + agent runtime "load skills" path. The
-- ListSkillsByWorkspace above stays as-is for system surfaces that need
-- the unfiltered view (the install handler in PR 6, and the
-- experimental diagnostic endpoint in PR 3).
SELECT * FROM skill
WHERE workspace_id = $1
  AND NOT EXISTS (
    SELECT 1 FROM experimental_resource_lock l
    WHERE l.resource_type = 'skill'
      AND l.resource_id = skill.id
      AND l.hidden = true
  )
ORDER BY name ASC;

-- name: ListVisibleSkillSummariesByWorkspace :many
-- Same visibility predicate as ListVisibleSkillsByWorkspace but omits
-- the heavy body column. The GUI list page in packages/views/skills/
-- hits this every render.
SELECT id, workspace_id, name, description, config, created_by, created_at, updated_at
FROM skill
WHERE workspace_id = $1
  AND NOT EXISTS (
    SELECT 1 FROM experimental_resource_lock l
    WHERE l.resource_type = 'skill'
      AND l.resource_id = skill.id
      AND l.hidden = true
  )
ORDER BY name ASC;

-- name: ListVisibleAgentSkillsByWorkspace :many
-- Hidden-skill-filtered variant of ListAgentSkillsByWorkspace. The
-- agent runtime's "what skills does this agent have access to" call
-- uses this so a hidden lab skill does not silently leak into the
-- agent's tool surface. Same JOIN shape as the original
-- (agent_skill × skill), just with the same NOT EXISTS predicate.
SELECT ask.agent_id, s.id, s.name, s.description
FROM agent_skill ask
JOIN skill s ON s.id = ask.skill_id
WHERE s.workspace_id = $1
  AND NOT EXISTS (
    SELECT 1 FROM experimental_resource_lock l
    WHERE l.resource_type = 'skill'
      AND l.resource_id = s.id
      AND l.hidden = true
  )
ORDER BY s.name ASC;

-- name: GetSkill :one
SELECT * FROM skill
WHERE id = $1;

-- name: GetSkillInWorkspace :one
SELECT * FROM skill
WHERE id = $1 AND workspace_id = $2;

-- name: GetSkillByWorkspaceAndName :one
-- Used by agent-template materialization to implement find-or-create: when a
-- template references a skill by name that already exists in the workspace,
-- reuse the existing skill_id rather than INSERT (which would fail the
-- UNIQUE(workspace_id, name) constraint from migration 008).
SELECT * FROM skill
WHERE workspace_id = $1 AND name = $2;

-- name: CreateSkill :one
INSERT INTO skill (workspace_id, name, description, content, config, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateSkill :one
UPDATE skill SET
    name = COALESCE(sqlc.narg('name'), name),
    description = COALESCE(sqlc.narg('description'), description),
    content = COALESCE(sqlc.narg('content'), content),
    config = COALESCE(sqlc.narg('config'), config),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteSkill :exec
-- Defense-in-depth: workspace_id is a SQL-layer tenant guard. See DeleteIssue.
DELETE FROM skill WHERE id = $1 AND workspace_id = $2;

-- Skill File CRUD

-- name: ListSkillFiles :many
SELECT * FROM skill_file
WHERE skill_id = $1
ORDER BY path ASC;

-- name: ListSkillFileMetadata :many
-- Metadata-only variant of ListSkillFiles: path, byte size and content hash
-- without the body. Same reason as ListSkillSummariesByWorkspace — a skill
-- whose supporting files total ~600KB cannot be listed at all when every row
-- carries its full content, and the one command that would show which file is
-- oversized was the command that timed out (GH multica-ai/multica#7498).
-- size/hash are computed in Postgres so the file bodies never leave it.
--
-- convert_to(content, 'UTF8'), never content::bytea: the cast runs the bytea
-- INPUT parser over the text, so it reads backslash escapes instead of taking
-- the bytes. A file containing `\x41` would hash as the single byte `A`, and
-- one containing a bare backslash — a regex `\d+`, a Windows path, a LaTeX
-- snippet — fails outright with "invalid input syntax for type bytea",
-- turning an ordinary skill into a 500 on this endpoint.
SELECT id, skill_id, path,
       octet_length(content)::bigint AS size,
       encode(sha256(convert_to(content, 'UTF8')), 'hex') AS content_hash,
       created_at, updated_at
FROM skill_file
WHERE skill_id = $1
ORDER BY path ASC;

-- name: GetSkillFile :one
SELECT * FROM skill_file
WHERE id = $1;

-- name: UpsertSkillFile :one
INSERT INTO skill_file (skill_id, path, content)
VALUES ($1, $2, $3)
ON CONFLICT (skill_id, path) DO UPDATE SET
    content = EXCLUDED.content,
    updated_at = now()
RETURNING *;

-- name: DeleteSkillFile :exec
DELETE FROM skill_file WHERE id = $1;

-- name: DeleteSkillFilesBySkill :exec
DELETE FROM skill_file WHERE skill_id = $1;

-- Agent-Skill junction

-- name: ListAgentSkills :many
SELECT s.* FROM skill s
JOIN agent_skill ask ON ask.skill_id = s.id
WHERE ask.agent_id = $1
ORDER BY s.name ASC;

-- name: ListAgentSkillSummaries :many
-- Summary variant for the agent skills list endpoint — omits `content` for
-- the same reason as ListSkillSummariesByWorkspace.
SELECT s.id, s.workspace_id, s.name, s.description, s.config, s.created_by, s.created_at, s.updated_at
FROM skill s
JOIN agent_skill ask ON ask.skill_id = s.id
WHERE ask.agent_id = $1
ORDER BY s.name ASC;

-- name: ListAgentSkillNamesByAgentIDs :many
SELECT ask.agent_id, s.name
FROM agent_skill ask
JOIN skill s ON s.id = ask.skill_id
WHERE ask.agent_id = ANY(sqlc.arg('agent_ids')::uuid[])
ORDER BY ask.agent_id, s.name ASC;

-- name: AddAgentSkill :exec
INSERT INTO agent_skill (agent_id, skill_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: RemoveAgentSkill :exec
DELETE FROM agent_skill
WHERE agent_id = $1 AND skill_id = $2;

-- name: RemoveAllAgentSkills :exec
DELETE FROM agent_skill WHERE agent_id = $1;

-- name: ListAgentSkillsByWorkspace :many
SELECT ask.agent_id, s.id, s.name, s.description
FROM agent_skill ask
JOIN skill s ON s.id = ask.skill_id
WHERE s.workspace_id = $1
ORDER BY s.name ASC;

-- name: UpdateSkillContent :one
-- 0.5.3: content-only write-back used by the self-opt runner. Narrower than
-- UpdateSkill so an optimizer edit can never clobber name/description/config
-- the user changed meanwhile.
UPDATE skill SET
    content = $2,
    updated_at = now()
WHERE id = $1 AND workspace_id = $3
RETURNING *;

-- name: ListClaudeLabSkillSummariesByWorkspace :many
-- 0.5.106: workspace-scoped summary list for the claude_science_lab
-- Knowledge surface. Unlike ListVisibleSkillSummariesByWorkspace above,
-- rows locked hidden by the claude_science labs themselves stay IN —
-- the installer ends with a blanket Hide(source) so every lab-owned
-- skill row is hidden=true by design, and this endpoint is already
-- gated behind RequireExperimentalFlag. Only locks owned by OTHER
-- labs (a skill claimed by two labs is possible in principle) still
-- suppress the row. Carries description for list rendering.
SELECT id, name, description
FROM skill
WHERE workspace_id = $1
  AND NOT EXISTS (
    SELECT 1 FROM experimental_resource_lock l
    WHERE l.resource_type = 'skill'
      AND l.resource_id = skill.id
      AND l.hidden = true
      AND l.experimental_source NOT IN ('claude_science', 'claude_science_lab')
  )
ORDER BY name ASC;
