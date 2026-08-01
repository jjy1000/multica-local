-- name: CreateSquad :one
INSERT INTO squad (workspace_id, name, description, leader_id, creator_id, avatar_url)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetSquad :one
SELECT * FROM squad WHERE id = $1;

-- name: GetSquadInWorkspace :one
SELECT * FROM squad WHERE id = $1 AND workspace_id = $2;

-- name: ListSquads :many
SELECT * FROM squad WHERE workspace_id = $1 AND archived_at IS NULL ORDER BY created_at ASC;

-- name: ListVisibleSquadsByWorkspace :many
-- Hidden-lock-filtered variant of ListSquads. The sidebar squad list
-- in the GUI renders through this; a hidden Claude-Science squad
-- disappears as soon as the user toggles the flag off, with no
-- client-side filter needed. The unfiltered ListSquads stays for
-- system surfaces (CLI dump, install handler in PR 6) and the
-- diagnostic endpoint in PR 3.
SELECT * FROM squad
WHERE workspace_id = $1
  AND archived_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM experimental_resource_lock l
    WHERE l.resource_type = 'squad'
      AND l.resource_id = squad.id
      AND l.hidden = true
  )
ORDER BY created_at ASC;

-- name: ListSquadMemberPreviewRows :many
-- Static squad membership summary for list/hover previews. This deliberately
-- excludes derived runtime/task status; the squad detail members-status
-- endpoint owns live state.
SELECT
    sm.squad_id,
    sm.member_type,
    sm.member_id,
    sm.role
FROM squad_member sm
JOIN squad s ON s.id = sm.squad_id
WHERE s.workspace_id = $1 AND s.archived_at IS NULL
ORDER BY
    sm.squad_id ASC,
    (sm.member_type = 'agent' AND sm.member_id = s.leader_id) DESC,
    sm.created_at ASC;

-- name: ListSquadMemberPreviewRowsBySquad :many
SELECT
    sm.squad_id,
    sm.member_type,
    sm.member_id,
    sm.role
FROM squad_member sm
JOIN squad s ON s.id = sm.squad_id
WHERE sm.squad_id = $1
ORDER BY
    (sm.member_type = 'agent' AND sm.member_id = s.leader_id) DESC,
    sm.created_at ASC;

-- name: ListAllSquads :many
SELECT * FROM squad WHERE workspace_id = $1 ORDER BY created_at ASC;

-- name: UpdateSquad :one
UPDATE squad SET
    name = COALESCE(sqlc.narg('name'), name),
    description = COALESCE(sqlc.narg('description'), description),
    leader_id = COALESCE(sqlc.narg('leader_id'), leader_id),
    avatar_url = COALESCE(sqlc.narg('avatar_url'), avatar_url),
    instructions = COALESCE(sqlc.narg('instructions'), instructions),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ArchiveSquad :one
UPDATE squad SET archived_at = now(), archived_by = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: GetSquadByWorkspaceAndName :one
-- Find-or-create helper for PR 6's claude_science installer: when an
-- OpenScience squad already exists in the workspace under the same
-- name (re-install / re-toggle), reuse the row rather than INSERT
-- (which would fail UNIQUE(workspace_id, name) added by migration 084).
SELECT * FROM squad
WHERE workspace_id = $1 AND name = $2;

-- name: AddSquadMember :one
INSERT INTO squad_member (squad_id, member_type, member_id, role)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: RemoveSquadMember :execrows
DELETE FROM squad_member
WHERE squad_id = $1 AND member_type = $2 AND member_id = $3;

-- name: ListSquadMembers :many
SELECT * FROM squad_member WHERE squad_id = $1 ORDER BY created_at ASC;

-- name: ListVisibleSquadMembers :many
-- Hides squad_member rows whose member is currently hidden by an
-- experimental lock. When the parent squad is already hidden this
-- filter is redundant, but defense-in-depth keeps a half-visible
-- squad from leaking its membership graph (members could otherwise
-- be referenced from another squad). The unfiltered ListSquadMembers
-- stays for system surfaces.
SELECT * FROM squad_member
WHERE squad_id = $1
  AND NOT EXISTS (
    SELECT 1 FROM experimental_resource_lock l
    WHERE l.resource_type = 'member'
      AND l.resource_id = squad_member.member_id
      AND l.hidden = true
  )
ORDER BY created_at ASC;

-- name: UpdateSquadMemberRole :one
UPDATE squad_member SET role = $4
WHERE squad_id = $1 AND member_type = $2 AND member_id = $3
RETURNING *;

-- name: IsSquadMember :one
SELECT EXISTS(
    SELECT 1 FROM squad_member
    WHERE squad_id = $1 AND member_type = $2 AND member_id = $3
) AS is_member;

-- name: CountSquadMembers :one
SELECT count(*) FROM squad_member WHERE squad_id = $1;

-- name: GetSquadByAssignee :one
-- Look up the squad when an issue is assigned to a squad.
SELECT s.* FROM squad s WHERE s.id = $1 AND s.workspace_id = $2;

-- name: ListSquadsByMember :many
-- Find all squads a given entity belongs to in a workspace.
SELECT s.* FROM squad s
JOIN squad_member sm ON sm.squad_id = s.id
WHERE s.workspace_id = $1 AND sm.member_type = $2 AND sm.member_id = $3
ORDER BY s.created_at ASC;

-- name: TransferSquadAssignees :exec
-- Transfer all issues assigned to a squad to the squad's leader agent.
UPDATE issue SET assignee_type = 'agent', assignee_id = $2, updated_at = now()
WHERE assignee_type = 'squad' AND assignee_id = $1;

-- name: TransferSquadAutopilotsToLeader :exec
-- Mirrors TransferSquadAssignees for autopilot rows: when a squad is archived,
-- any autopilot still pointing at the squad would otherwise dangle and the
-- admission gate would skip every subsequent dispatch with "assignee squad
-- cannot be resolved". Rewrite the assignee in place to the leader agent so
-- the autopilot keeps firing under the same leader-only execution semantics
-- it had a moment before the archive (Path A from MUL-2429).
UPDATE autopilot
SET assignee_type = 'agent',
    assignee_id = $2,
    updated_at = now()
WHERE assignee_type = 'squad' AND assignee_id = $1;

-- name: ListSquadMemberStatusRows :many
-- Per-row join used to build the squad-members status view. One row per
-- (squad_member × active_task); members with no active task return a
-- single row with NULL task_* columns. Human members and agent members
-- with no agent row also return one row with NULL agent_/runtime_ columns.
-- The handler aggregates rows by member_id.
SELECT
    sm.id              AS squad_member_id,
    sm.member_type     AS member_type,
    sm.member_id       AS member_id,
    a.archived_at      AS agent_archived_at,
    ar.status          AS runtime_status,
    ar.last_seen_at    AS runtime_last_seen_at,
    atq.id             AS task_id,
    atq.status         AS task_status,
    atq.issue_id       AS task_issue_id,
    atq.dispatched_at  AS task_dispatched_at,
    i.number           AS issue_number,
    i.title            AS issue_title,
    i.status           AS issue_status
FROM squad_member sm
LEFT JOIN agent a
       ON sm.member_type = 'agent' AND a.id = sm.member_id
LEFT JOIN agent_runtime ar
       ON ar.id = a.runtime_id
LEFT JOIN agent_task_queue atq
       ON sm.member_type = 'agent'
      AND atq.agent_id = sm.member_id
      AND atq.status IN ('dispatched', 'running', 'waiting_local_directory')
LEFT JOIN issue i
       ON i.id = atq.issue_id
WHERE sm.squad_id = $1
ORDER BY sm.created_at ASC, atq.dispatched_at DESC NULLS LAST;

-- name: UpdateSquadInstructions :one
-- 0.5.3: instructions-only write-back used by the self-opt runner. Narrower
-- than UpdateSquad so an optimizer edit can never clobber name/description/
-- leader the user changed meanwhile.
UPDATE squad SET
    instructions = $2,
    updated_at = now()
WHERE id = $1 AND workspace_id = $3
RETURNING *;
