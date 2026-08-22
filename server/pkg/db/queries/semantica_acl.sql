-- name: CountWorkspaceMembers :one
-- Used by the 0.5.56 P4 mode detector (individual vs team) — single
-- member workspace => individual mode (per-actor decisions hidden
-- from siblings); multi-member => team mode (shared visibility).
-- Bounded by workspace_id; the caller is membership-gated.
SELECT COUNT(*)::bigint AS count
FROM member
WHERE workspace_id = $1
  AND deleted_at IS NULL;

-- name: UpsertSemanticaDecisionACL :exec
-- Insert (or update on PK conflict) the ACL row for one decision.
-- Idempotent: the goroutine in postDecisionSync can retry on transient
-- write errors without producing duplicate ACL rows.
INSERT INTO semantica_local_decision_acl (
  decision_id, workspace_id, actor_type, actor_id, visibility
) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (decision_id) DO UPDATE SET
  workspace_id = EXCLUDED.workspace_id,
  actor_type   = EXCLUDED.actor_type,
  actor_id     = EXCLUDED.actor_id,
  visibility   = EXCLUDED.visibility;

-- name: ListSemanticaDecisionsForViewer :many
-- Returns the ACL rows visible to one viewer in one workspace.
-- "Visible" semantics (P4 contract):
--   - actor_type='team' rows                        -> visible to all members
--   - actor_type='user' AND actor_id = viewer       -> visible to that user
--   - actor_type='user' AND actor_id != viewer       -> visible iff visibility='shared_team'
--   - actor_type='agent'                            -> visible to agents of the workspace
--                                                          (filtered upstream at ListAgents)
-- Returns ordered by created_at DESC; capped at 200 to bound the query.
SELECT decision_id, workspace_id, actor_type, actor_id, created_at, visibility
FROM semantica_local_decision_acl
WHERE workspace_id = $1
  AND (
    actor_type = 'team'
    OR (actor_type = 'user' AND actor_id = $2)
    OR (actor_type = 'user' AND visibility = 'shared_team')
    OR (actor_type = 'agent')
  )
ORDER BY created_at DESC
LIMIT 200;
