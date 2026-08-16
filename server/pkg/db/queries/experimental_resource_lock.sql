-- name: InsertExperimentalResourceLock :exec
-- Claim a lock on (source, resource_type, resource_id). Idempotent via
-- the UNIQUE constraint — re-claiming an already-claimed row is a no-op.
-- The hidden flag is initialized to false (visible). The caller can later
-- flip it to true via the rollback path.
INSERT INTO experimental_resource_lock
    (experimental_source, resource_type, resource_id, hidden)
VALUES ($1, $2, $3, false)
ON CONFLICT (experimental_source, resource_type, resource_id) DO NOTHING;

-- name: HideExperimentalResourceLocksBySource :execrows
-- Flip every row attached to source to hidden=true. Returns the count
-- of rows touched so the rollback handler can report "hidden N rows".
UPDATE experimental_resource_lock
SET hidden = true,
    hidden_at = now()
WHERE experimental_source = $1
  AND hidden = false;

-- name: RestoreExperimentalResourceLocksBySource :execrows
-- Flip every hidden row attached to source back to visible. Used by the
-- install path on re-toggle-on after a previous rollback.
UPDATE experimental_resource_lock
SET hidden = false,
    hidden_at = NULL
WHERE experimental_source = $1
  AND hidden = true;

-- name: ListExperimentalResourceLocks :many
-- Diagnostic read: every lock row attached to source, visible or not.
-- Used by the activity endpoint in PR 3 and by tests asserting the lock
-- graph is well-formed.
SELECT id,
       experimental_source,
       resource_type,
       resource_id,
       hidden,
       created_at,
       hidden_at
FROM experimental_resource_lock
WHERE experimental_source = $1
ORDER BY created_at;

-- name: CountExperimentalResourceLocksByType :many
-- Per-resource_type counts for the install manifest. Returns rows
-- (resource_type, total, hidden) so the installer can report both
-- "how many I imported" and "how many are currently visible".
SELECT resource_type,
       COUNT(*) AS total,
       COUNT(*) FILTER (WHERE hidden = false) AS visible
FROM experimental_resource_lock
WHERE experimental_source = $1
GROUP BY resource_type;

-- name: GetExperimentalResourceLock :one
-- Single-row lookup for write-handler rejection. Returns sql.ErrNoRows
-- when no lock exists (the caller treats this as "not locked"); the
-- caller then checks .hidden to decide whether to 423 Locked (visible
-- claim by a lab → reject) or just no-op (no claim at all).
SELECT id,
       experimental_source,
       resource_type,
       resource_id,
       hidden,
       created_at,
       hidden_at
FROM experimental_resource_lock
WHERE experimental_source = $1
  AND resource_type = $2
  AND resource_id = $3;

-- name: IsExperimentalResourceHidden :one
-- Hot-path read used by Visible* helpers. Returns true iff a lock row
-- exists for (source, resource_type, resource_id) AND is currently hidden.
-- The build-up the NOT EXISTS clauses duplicate this is fine for
-- correctness, but a dedicated one-row predicate keeps the helper
-- package's API small.
SELECT EXISTS (
    SELECT 1
    FROM experimental_resource_lock
    WHERE experimental_source = $1
      AND resource_type = $2
      AND resource_id = $3
      AND hidden = true
) AS hidden;

-- name: RestoreExperimentalResourceLockByID :execrows
-- Flip a single (source, resource_type, resource_id) row back to visible.
-- Unlike RestoreExperimentalResourceLocksBySource (which un-hides every
-- row for the source), this targets one lock so callers can keep a
-- lifecycle marker visible while every other lab resource stays hidden.
-- 0.3.44: install_claude_science uses this to restore its workspace
-- lifecycle marker after the blanket Hide(src) that suppresses lab
-- resources from the main pickers.
UPDATE experimental_resource_lock
SET hidden = false,
    hidden_at = NULL
WHERE experimental_source = $1
  AND resource_type = $2
  AND resource_id = $3
  AND hidden = true;

-- name: DeleteExperimentalResourceLockByID :exec
-- 0.5.22 (P0 fix, audit 2026-08-16): used by swarm_gc.archiveOne
-- to release the per-swarm_run lock row after archive. Without this,
-- every swarm_run row leaves an orphan lock row in
-- experimental_resource_lock — the table has no TTL column, so the
-- leak is unbounded (verified — releaseSwarmLock was a stub returning
-- nil at swarm_gc.go:253-265).
DELETE FROM experimental_resource_lock
WHERE experimental_source = $1
  AND resource_type = $2
  AND resource_id = $3;
