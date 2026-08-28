-- 0.5.83 WL3: issue causal graph persistence. Backs the gated REST
-- surface in server/internal/handler/causal_graph.go and the Tier A/B
-- recorders in server/internal/service/causal_graph/recorder.go.
--
-- Node/edge type CHECK sets and the active|suggested edge lifecycle
-- are CONTRACT shared verbatim with migrations 277/278, the client
-- zod schemas (packages/core/api/causal_graph.ts) and the docs.
-- Node dedup rides the partial unique index
-- uq_causal_node_provenance_dedup_key (mig 279): recorders look up by
-- (workspace_id, provenance->>'dedup_key') before INSERT so retries
-- collapse onto the same row. All params are named with explicit
-- casts — sqlc's pgx/v5 inference cannot size bare narg() params in
-- INSERT VALUES / JSONB expressions.

-- name: CreateCausalNode :one
INSERT INTO causal_node (
    workspace_id, issue_id, type, label, description,
    metadata, provenance, created_by, lab_source, lab_run_id
) VALUES (
    sqlc.arg('workspace_id')::uuid,
    sqlc.narg('issue_id')::uuid,
    sqlc.arg('node_type')::text,
    sqlc.arg('label')::text,
    sqlc.narg('description')::text,
    COALESCE(sqlc.narg('metadata')::jsonb, '{}'::jsonb),
    COALESCE(sqlc.narg('provenance')::jsonb, '{}'::jsonb),
    sqlc.narg('created_by')::text,
    sqlc.narg('lab_source')::text,
    sqlc.narg('lab_run_id')::uuid
)
RETURNING *;

-- name: GetCausalNode :one
SELECT * FROM causal_node WHERE id = sqlc.arg('node_id')::uuid;

-- name: ListCausalNodes :many
SELECT *
FROM causal_node
WHERE workspace_id = sqlc.arg('workspace_id')::uuid
  AND (sqlc.narg('issue_id')::uuid IS NULL OR issue_id = sqlc.narg('issue_id')::uuid)
  AND (sqlc.narg('node_type')::text IS NULL OR type = sqlc.narg('node_type')::text)
ORDER BY created_at DESC
LIMIT COALESCE(sqlc.narg('lim')::int, 50)
OFFSET COALESCE(sqlc.narg('offset')::int, 0);

-- name: ListCausalNodesByIssue :many
SELECT *
FROM causal_node
WHERE issue_id = sqlc.arg('issue_id')::uuid
  AND status = 'active'
ORDER BY created_at ASC;

-- name: ListCausalNodesByIDs :many
SELECT * FROM causal_node WHERE id = ANY(sqlc.arg('node_ids')::uuid[]);

-- name: FindCausalNodeByDedupKey :one
SELECT *
FROM causal_node
WHERE workspace_id = sqlc.arg('workspace_id')::uuid
  AND provenance->>'dedup_key' = sqlc.arg('dedup_key')::text
LIMIT 1;

-- name: TouchCausalNode :exec
UPDATE causal_node SET last_observed_at = now()
WHERE id = sqlc.arg('node_id')::uuid;

-- 0.5.84 P0 #3 fix: bulk-touch last_observed_at on every active
-- node whose primary issue matches AND on every active 1-hop
-- graph neighbor (nodes linked via an edge to any primary node).
-- Wired from UpdateIssue / CreateComment / enqueueTask /
-- CompleteTask hot paths so volatile node types (action /
-- outcome / decision / evidence) do not flip to status='stale'
-- after 30 days. Constraint/assumption nodes are exempt from the
-- stale ladder (the maintenance ticker keeps them exempt), but
-- touching them here is harmless and keeps recent issue activity
-- surfaced on long-lived anchor nodes.
-- name: RefreshCausalNodesForIssue :execrows
WITH touch AS (
    SELECT id FROM causal_node
    WHERE status = 'active' AND issue_id = sqlc.arg('issue_id')::uuid
    UNION
    SELECT DISTINCT e.from_node_id AS id FROM causal_edge e
    JOIN causal_node n ON e.to_node_id = n.id
    WHERE n.issue_id = sqlc.arg('issue_id')::uuid
      AND n.status = 'active'
      AND e.from_node_id <> n.id
    UNION
    SELECT DISTINCT e.to_node_id AS id FROM causal_edge e
    JOIN causal_node n ON e.from_node_id = n.id
    WHERE n.issue_id = sqlc.arg('issue_id')::uuid
      AND n.status = 'active'
      AND e.to_node_id <> n.id
)
UPDATE causal_node SET last_observed_at = now()
WHERE status = 'active' AND id IN (SELECT id FROM touch);

-- Recorder gate: the Tier A/B hooks ride the hot enqueue/complete
-- paths, so the enabled check is one indexed EXISTS rather than the
-- ListEnabledFlagKeys enumeration. Same "any user" semantics — in
-- this single-user fork the set collapses to the one user.
-- name: FlagEnabledForAnyUser :one
SELECT EXISTS(
    SELECT 1 FROM experimental_pref
    WHERE flag_key = sqlc.arg('flag_key')::text AND enabled = true
) AS enabled;

-- Trigger-node resolution for Tier A enables edges: the latest
-- outcome node recorded for the issue, if any. Recorders chain
-- action → outcome → next action so the graph grows along the
-- issue's real execution history.
-- name: FindLatestOutcomeNodeForIssue :one
SELECT *
FROM causal_node
WHERE issue_id = sqlc.arg('issue_id')::uuid
  AND type = 'outcome'
  AND status = 'active'
ORDER BY created_at DESC
LIMIT 1;

-- name: CreateCausalEdge :one
INSERT INTO causal_edge (
    workspace_id, from_node_id, to_node_id, type,
    weight, confidence, metadata, provenance, created_by, proposed_by, status
) VALUES (
    sqlc.arg('workspace_id')::uuid,
    sqlc.arg('from_node_id')::uuid,
    sqlc.arg('to_node_id')::uuid,
    sqlc.arg('edge_type')::text,
    COALESCE(sqlc.narg('weight')::numeric, 1.0),
    sqlc.narg('confidence')::numeric,
    COALESCE(sqlc.narg('metadata')::jsonb, '{}'::jsonb),
    COALESCE(sqlc.narg('provenance')::jsonb, '{}'::jsonb),
    sqlc.narg('created_by')::text,
    sqlc.narg('proposed_by')::text,
    COALESCE(sqlc.narg('edge_status')::text, 'active')
)
RETURNING *;

-- name: GetCausalEdge :one
SELECT * FROM causal_edge WHERE id = sqlc.arg('edge_id')::uuid;

-- name: DeleteCausalEdge :exec
DELETE FROM causal_edge WHERE id = sqlc.arg('edge_id')::uuid;

-- name: ListCausalEdges :many
SELECT *
FROM causal_edge
WHERE workspace_id = sqlc.arg('workspace_id')::uuid
  AND (sqlc.narg('edge_status')::text IS NULL OR status = sqlc.narg('edge_status')::text)
  AND (sqlc.narg('edge_type')::text IS NULL OR type = sqlc.narg('edge_type')::text)
  AND (sqlc.narg('min_confidence')::numeric IS NULL OR confidence >= sqlc.narg('min_confidence')::numeric)
  AND (
    sqlc.narg('issue_id')::uuid IS NULL
    OR from_node_id IN (SELECT id FROM causal_node WHERE issue_id = sqlc.narg('issue_id')::uuid)
    OR to_node_id IN (SELECT id FROM causal_node WHERE issue_id = sqlc.narg('issue_id')::uuid)
  )
ORDER BY created_at DESC
LIMIT COALESCE(sqlc.narg('lim')::int, 50)
OFFSET COALESCE(sqlc.narg('offset')::int, 0);

-- BFS hop expansion: every active edge touching any node of the
-- current frontier (both directions — the subgraph is traversed
-- undirected so a depth-2 slice from an issue's outcome nodes still
-- reaches the actions that enabled them).
-- name: ListActiveEdgesTouching :many
SELECT *
FROM causal_edge
WHERE status = 'active'
  AND (from_node_id = ANY(sqlc.arg('node_ids')::uuid[])
       OR to_node_id = ANY(sqlc.arg('node_ids')::uuid[]));

-- Directed BFS for the shortest-path endpoint: only outward edges.
-- name: ListActiveEdgesFrom :many
SELECT *
FROM causal_edge
WHERE status = 'active'
  AND from_node_id = ANY(sqlc.arg('node_ids')::uuid[]);

-- Curation gate (Tier D): confirm promotes a suggested edge to
-- active; the partial unique index may reject if an active edge with
-- the same (from, to, type) triple materialised meanwhile — the
-- handler maps that SQLSTATE to 409. Reject TOMBSTONES the proposal
-- (mig 280): the row stays as an audit trail and a never-nag dedup
-- anchor (proposers probe FindCausalEdgeBetween, any status, before
-- re-proposing). Only suggested rows qualify (0 rows = 409 in the
-- handler).
-- name: ConfirmCausalEdge :one
UPDATE causal_edge
SET status = 'active', created_by = 'user'
WHERE id = sqlc.arg('edge_id')::uuid AND status = 'suggested'
RETURNING *;

-- name: RejectCausalEdge :one
UPDATE causal_edge
SET status = 'rejected'
WHERE id = sqlc.arg('edge_id')::uuid AND status = 'suggested'
RETURNING *;

-- Any-status probe between a node pair: the evolver / curator scan
-- check this before proposing so a decided (confirmed OR rejected)
-- pair is never re-proposed — ICP-5 never-nag.
-- name: FindCausalEdgeBetween :one
SELECT *
FROM causal_edge
WHERE from_node_id = sqlc.arg('from_node_id')::uuid
  AND to_node_id = sqlc.arg('to_node_id')::uuid
  AND type = sqlc.arg('edge_type')::text
LIMIT 1;

-- ── issue_dependency revive (mig 276) ────────────────────────────────

-- name: ListIssueDependencies :many
SELECT * FROM issue_dependency
WHERE issue_id = sqlc.arg('issue_id')::uuid
ORDER BY created_at ASC;

-- name: ListIssueDependents :many
SELECT * FROM issue_dependency
WHERE depends_on_issue_id = sqlc.arg('issue_id')::uuid
ORDER BY created_at ASC;

-- name: GetIssueDependency :one
SELECT * FROM issue_dependency
WHERE id = sqlc.arg('dependency_id')::uuid;

-- name: GetIssueDependencyByPair :one
SELECT *
FROM issue_dependency
WHERE issue_id = sqlc.arg('issue_id')::uuid
  AND depends_on_issue_id = sqlc.arg('depends_on_issue_id')::uuid;

-- name: CreateIssueDependency :one
INSERT INTO issue_dependency (
    issue_id, depends_on_issue_id, type, evidence_comment_id, created_by
) VALUES (
    sqlc.arg('issue_id')::uuid,
    sqlc.arg('depends_on_issue_id')::uuid,
    sqlc.arg('dep_type')::text,
    sqlc.narg('evidence_comment_id')::uuid,
    COALESCE(sqlc.narg('created_by')::text, 'user')
)
RETURNING *;

-- name: DeleteIssueDependency :exec
DELETE FROM issue_dependency WHERE id = sqlc.arg('dependency_id')::uuid;
