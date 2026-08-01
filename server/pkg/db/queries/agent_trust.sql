-- agent_trust tables: per-(workspace, agent) trust scores + append-only event
-- ledger. See migration 228. The score lives in NUMERIC(4,1) with the Go
-- service owning the arithmetic (start 5.0, correction -0.5, review pass +0.2,
-- review fail -0.5, clamp [0, 10]); SQL stays a dumb store so future tuning
-- needs no migration.
--
-- Counter atomicity: each score-changing action is ONE upsert that bumps the
-- relevant counter with `counter = counter + 1` on conflict, so two concurrent
-- events for the same agent cannot clobber each other's counters. The event
-- row (agent_trust_event) is appended separately by the service with the
-- before/after scores for the timeline renderer.

-- name: GetAgentTrustProfile :one
-- Read one profile; returns sql.ErrNoRows when the agent has never been
-- scored (caller treats that as the initial 5.0 default).
SELECT * FROM agent_trust_profile
WHERE workspace_id = $1 AND agent_id = $2;

-- name: ApplyAgentTrustCorrection :one
-- User correction: score = $3, correction_count + 1. First sight of the
-- agent creates the row with correction_count = 1 (the initial INSERT
-- branch does NOT hit ON CONFLICT, so the +1 must live in VALUES too).
INSERT INTO agent_trust_profile (workspace_id, agent_id, score, correction_count)
VALUES ($1, $2, $3, 1)
ON CONFLICT (workspace_id, agent_id) DO UPDATE
SET score = EXCLUDED.score,
    correction_count = agent_trust_profile.correction_count + 1,
    updated_at = now()
RETURNING *;

-- name: ApplyAgentTrustReviewRequested :one
-- Gate fired: review_requested_count + 1. The score does not change on a
-- request; the INSERT branch seeds the counter at 1 (same first-row rule
-- as ApplyAgentTrustCorrection).
INSERT INTO agent_trust_profile (workspace_id, agent_id, score, review_requested_count)
VALUES ($1, $2, $3, 1)
ON CONFLICT (workspace_id, agent_id) DO UPDATE
SET review_requested_count = agent_trust_profile.review_requested_count + 1,
    updated_at = now()
RETURNING *;

-- name: ApplyAgentTrustReviewPass :one
-- Self-review accepted the output: score = $3, review_pass_count + 1.
-- INSERT branch seeds the counter at 1 (first-row rule, see correction).
INSERT INTO agent_trust_profile (workspace_id, agent_id, score, review_pass_count)
VALUES ($1, $2, $3, 1)
ON CONFLICT (workspace_id, agent_id) DO UPDATE
SET score = EXCLUDED.score,
    review_pass_count = agent_trust_profile.review_pass_count + 1,
    updated_at = now()
RETURNING *;

-- name: ApplyAgentTrustReviewFail :one
-- Self-review rejected the output: score = $3, review_fail_count + 1.
-- INSERT branch seeds the counter at 1 (first-row rule, see correction).
INSERT INTO agent_trust_profile (workspace_id, agent_id, score, review_fail_count)
VALUES ($1, $2, $3, 1)
ON CONFLICT (workspace_id, agent_id) DO UPDATE
SET score = EXCLUDED.score,
    review_fail_count = agent_trust_profile.review_fail_count + 1,
    updated_at = now()
RETURNING *;

-- name: ListAgentTrustProfilesByWorkspace :many
-- Leaderboard for the trust panel. The index is (workspace_id, score DESC);
-- LIMIT keeps the payload small (a workspace rarely has > 50 agents).
SELECT * FROM agent_trust_profile
WHERE workspace_id = $1
ORDER BY score DESC
LIMIT $2;

-- name: CreateAgentTrustEvent :one
-- Append to the ledger. The service passes score_before/after so the event
-- row is self-contained for the timeline renderer (no join needed).
INSERT INTO agent_trust_event (
    workspace_id, agent_id, event_type, score_delta,
    score_before, score_after, task_id, issue_id, note, created_by
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
)
RETURNING *;

-- name: ListAgentTrustEventsByWorkspace :many
-- Timeline for the merged self-opt view, newest first.
SELECT * FROM agent_trust_event
WHERE workspace_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListAgentTrustEventsByAgent :many
-- Per-agent ledger used by the runner's learning scan (which issues were
-- corrected, how often, and whether reviews passed) and by the detail panel.
SELECT * FROM agent_trust_event
WHERE agent_id = $1 AND workspace_id = $2
ORDER BY created_at DESC
LIMIT $3;

-- name: CountAgentTrustEventsByType :one
-- Aggregate used by the runner report (correction_count etc. can also be
-- read off the profile row; this query is for the windowed summary).
SELECT COUNT(*) FROM agent_trust_event
WHERE workspace_id = $1
  AND event_type = $2
  AND created_at >= $3;

-- name: ListTrustLearningEvents :many
-- 0.5.2: correction / review-fail events in a window, joined with the
-- agent name. This is what the self-opt runner learns from: which agents
-- were corrected, when, and on which issue.
SELECT e.agent_id, a.name AS agent_name, e.event_type, e.note,
       e.issue_id, e.created_at
FROM agent_trust_event e
JOIN agent a ON a.id = e.agent_id
WHERE e.workspace_id = $1
  AND e.created_at >= $2
  AND e.event_type IN ('correction', 'review_fail')
ORDER BY e.created_at DESC
LIMIT $3;

-- name: ListLowTrustAgents :many
-- 0.5.3: low-trust subjects that MANDATE optimization — agents whose
-- current trust score is below the given threshold AND who have at least
-- one correction / review_fail event in the window. Used by the runner's
-- deferral gate: a workspace with such an agent must never defer (the
-- system is mandated to fix what the user corrected).
SELECT DISTINCT p.agent_id
FROM agent_trust_profile p
WHERE p.workspace_id = $1
  AND p.score < $2
  AND EXISTS (
      SELECT 1 FROM agent_trust_event e
      WHERE e.agent_id = p.agent_id
        AND e.workspace_id = p.workspace_id
        AND e.event_type IN ('correction', 'review_fail')
        AND e.created_at >= $3
  );
