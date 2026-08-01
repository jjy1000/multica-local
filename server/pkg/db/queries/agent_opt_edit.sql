-- agent_opt_edit: SkillOpt-style edit ledger (0.5.2, generalized 0.5.3).
-- Every add/delete/replace edit the optimizer proposes for an optimizable
-- subject (agent / skill / squad / autopilot) is recorded — accepted edits
-- (written back to the subject's instruction text) and rejected ones
-- (negative experience, never re-proposed). See migrations 229-232.
--
-- 0.5.3 polymorphic subjects: target_type ∈ ('agent','skill','squad',
-- 'autopilot') + target_id point at the subject row. agent_id is kept for
-- legacy rows (pre-0.5.3 rows have target_type='agent', target_id=agent_id)
-- and is NULL for non-agent rows written by 0.5.3+.

-- name: CreateAgentOptEdit :one
INSERT INTO agent_opt_edit (
    agent_id, target_type, target_id, subject_scope,
    run_id, workspace_id, edit_type,
    before_text, after_text, rationale, accepted, iteration,
    application, validation_score, validation_reason,
    instructions_snapshot, applied_by, corrected_task_id
) VALUES (
    sqlc.narg('agent_id')::uuid, $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11, $12, $13, $14,
    $15, sqlc.narg('applied_by'), sqlc.narg('corrected_task_id')
)
RETURNING *;

-- name: ListAgentOptEditsByAgent :many
-- Per-subject edit history, newest first. The runner uses this to build the
-- rejection buffer (skip any proposal whose before+after match a rejected
-- edit) and to report the subject's optimization track record.
SELECT * FROM agent_opt_edit
WHERE target_type = $1 AND target_id = $2 AND workspace_id = $3
ORDER BY created_at DESC
LIMIT $4;

-- name: ListNegativeExperienceEdits :many
-- Per-subject negative-experience buffer for the optimizer: ONLY 'rejected'
-- and 'reverted' rows count as "do not re-propose" (0.5.2 adversarial review
-- d6/d7). Suggested / ignored / applied rows are NOT negative experience —
-- ignored stays re-proposable, applied is live in the instructions, and
-- suggested is still awaiting human judgment. Feeding non-negative rows to
-- the proposal prompt would mislabel them as rejected (d7) and letting a
-- reverted (before,after) pair be re-proposed would undo the user's explicit
-- rollback (d6).
SELECT * FROM agent_opt_edit
WHERE target_type = $1 AND target_id = $2 AND workspace_id = $3
  AND application IN ('rejected', 'reverted')
ORDER BY created_at DESC
LIMIT $4;

-- name: ListAppliedAgentOptEdits :many
-- Post-hoc commit-gate revalidation (0.5.2 adversarial review d1): the next
-- run re-scores the subject's most recent auto-applied edit(s) against their
-- pre-edit snapshots. Newest first so each run re-validates the newest
-- applied edit (auto-apply is rate-capped at 1/run, so there is at most one
-- new applied edit per run to re-score).
SELECT * FROM agent_opt_edit
WHERE target_type = $1 AND target_id = $2 AND workspace_id = $3
  AND application = 'applied'
ORDER BY created_at DESC
LIMIT $4;

-- name: ListAgentOptEditsByRun :many
-- Per-run edits for the report markdown (what was proposed, accepted or
-- rejected, with rationale).
SELECT * FROM agent_opt_edit
WHERE run_id = $1
ORDER BY iteration ASC, created_at ASC;

-- name: DeleteAgentOptEditsByAgent :exec
-- Archive closure (0.5.2, generalized 0.5.3): when a subject is archived
-- (soft delete), its optimization records must disappear too. The
-- hard-delete path is covered by FK ON DELETE CASCADE; this covers the
-- archive path. agent-only for now (the archive handlers for skill / squad /
-- autopilot do not hard-delete rows — see migrations 008 / 084 / 042).
DELETE FROM agent_opt_edit
WHERE target_type = 'agent' AND target_id = $1 AND workspace_id = $2;

-- name: DeleteAgentOptEditsBySubject :exec
-- Generalized archive closure: removes every edit row for a (target_type,
-- target_id) subject. Used by skill / squad / autopilot archive paths and by
-- any future subject type.
DELETE FROM agent_opt_edit
WHERE target_type = $1 AND target_id = $2 AND workspace_id = $3;

-- name: DeleteAgentTrustProfile :exec
-- Archive closure for the trust profile row.
DELETE FROM agent_trust_profile
WHERE agent_id = $1 AND workspace_id = $2;

-- name: DeleteAgentTrustEvents :exec
-- Archive closure for the trust event ledger.
DELETE FROM agent_trust_event
WHERE agent_id = $1 AND workspace_id = $2;

-- name: GetAgentOptEdit :one
-- Single edit row (apply / reject endpoint reads it first to resolve the
-- subject + before/after text for re-application).
SELECT * FROM agent_opt_edit
WHERE id = $1 AND workspace_id = $2;

-- name: UpdateAgentOptEditApplication :one
-- Move an edit between applied / suggested / rejected / ignored /
-- reverted. The `accepted` boolean is kept in sync for backward compat.
-- instructions_snapshot + applied_by are set when the transition is into
-- 'applied' (the pre-edit set becomes the revert point; applied_by records
-- user vs auto).
UPDATE agent_opt_edit
SET application = $2,
    accepted = ($2 = 'applied'),
    instructions_snapshot = COALESCE($4, instructions_snapshot),
    applied_by = COALESCE(sqlc.narg(applied_by), applied_by),
    updated_at = now()
WHERE id = $1 AND workspace_id = $3
RETURNING *;

-- name: ListAgentOptEditsByWorkspaceAndApplication :many
-- The pending-confirmation list: suggested edits for a workspace, newest
-- first, ranked by validation_score so the strongest suggestion is first.
-- 0.5.2 adversarial review d5: correction-backed suggestions (those with a
-- persisted corrected_task_id) get the +5 ordering credit INSIDE the
-- suggested queue — the credit never affects the auto-apply gate (which
-- reads the raw score), it only surfaces the strongest-correlated fix first.
SELECT * FROM agent_opt_edit
WHERE workspace_id = $1 AND application = $2
ORDER BY
    (CASE WHEN corrected_task_id IS NOT NULL THEN validation_score + $5 ELSE validation_score END) DESC NULLS LAST,
    created_at DESC
LIMIT $3 OFFSET $4;

-- name: ListExpiredSuggestedAgentOptEdits :many
-- 0.5.2 synthesis: suggestions nobody acted on for the expiry window are
-- soft-archived to 'ignored' (NOT 'rejected' — ignored stays out of the
-- rejection buffer and is re-proposable with fresh validation). The
-- Service runs this sweep once per scheduler tick.
SELECT id, target_type, target_id, workspace_id
FROM agent_opt_edit
WHERE application = 'suggested'
  AND created_at < $1
LIMIT $2;

-- name: UpdateAgentOptEditApplicationByIDs :exec
-- Bulk state transition (used by the expiry sweep).
UPDATE agent_opt_edit
SET application = $2,
    updated_at = now()
WHERE id = ANY($1::uuid[]);
