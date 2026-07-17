-- name: ListMembers :many
SELECT * FROM member
WHERE workspace_id = $1
ORDER BY created_at ASC;

-- name: ListVisibleMembersByWorkspace :many
-- Filtered by the experimental lock. GUI settings / agent pickers
-- route through this. The unfiltered ListMembers stays for system
-- paths that need the full set (install handler PR 6, CLI tools).
SELECT * FROM member
WHERE workspace_id = $1
  AND NOT EXISTS (
    SELECT 1 FROM experimental_resource_lock l
    WHERE l.resource_type = 'member'
      AND l.resource_id = member.id
      AND l.hidden = true
  )
ORDER BY created_at ASC;

-- name: GetMember :one
SELECT * FROM member
WHERE id = $1;

-- name: GetMemberByUserAndWorkspace :one
SELECT * FROM member
WHERE user_id = $1 AND workspace_id = $2;

-- name: CreateMember :one
INSERT INTO member (workspace_id, user_id, role)
VALUES ($1, $2, $3)
RETURNING *;

-- name: UpdateMemberRole :one
UPDATE member SET role = $2
WHERE id = $1
RETURNING *;

-- name: DeleteMember :exec
DELETE FROM member WHERE id = $1;

-- name: ListMembersWithUser :many
SELECT m.id, m.workspace_id, m.user_id, m.role, m.created_at,
       u.name as user_name, u.email as user_email, u.avatar_url as user_avatar_url
FROM member m
JOIN "user" u ON u.id = m.user_id
WHERE m.workspace_id = $1
ORDER BY m.created_at ASC;

-- name: ListVisibleMembersWithUserByWorkspace :many
-- Same projection as ListMembersWithUser but respects the
-- experimental-resource-lock filter. The workspace settings "members
-- tab" hits this; the unfiltered sibling stays for audit + CLI tools.
SELECT m.id, m.workspace_id, m.user_id, m.role, m.created_at,
       u.name as user_name, u.email as user_email, u.avatar_url as user_avatar_url
FROM member m
JOIN "user" u ON u.id = m.user_id
WHERE m.workspace_id = $1
  AND NOT EXISTS (
    SELECT 1 FROM experimental_resource_lock l
    WHERE l.resource_type = 'member'
      AND l.resource_id = m.id
      AND l.hidden = true
  )
ORDER BY m.created_at ASC;
