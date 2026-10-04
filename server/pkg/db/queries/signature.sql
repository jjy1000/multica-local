-- Signature authorization (mig 294). Key custody notes live in the
-- migration; these queries deliberately never touch private-key material —
-- the key files are server-local state under ~/.multica/signing/.

-- name: CreateSignatureAsset :one
-- The id is caller-supplied on purpose: the private key file is named
-- after it and is written BEFORE the row — letting the DB mint its own id
-- would desynchronize the key filename from the row.
INSERT INTO signature_asset (id, workspace_id, name, mime, image, image_sha256, algorithm, public_key)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ListSignatureAssets :many
SELECT * FROM signature_asset
WHERE workspace_id = $1
ORDER BY created_at DESC;

-- name: GetSignatureAsset :one
SELECT * FROM signature_asset WHERE id = $1;

-- name: GetSignatureAssetImage :one
SELECT image, mime FROM signature_asset WHERE id = $1;

-- name: RetireSignatureAsset :one
UPDATE signature_asset SET retired_at = now()
WHERE id = $1 AND retired_at IS NULL
RETURNING *;

-- name: CreateRiskSignature :one
INSERT INTO risk_signature (workspace_id, issue_id, asset_id, signed_by, ops, scope, content_sha256, fingerprint, signature, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: GetRiskSignature :one
SELECT * FROM risk_signature WHERE id = $1;

-- name: GetRiskSignatureByFingerprint :one
SELECT * FROM risk_signature WHERE fingerprint = $1;

-- name: GetActiveRiskSignatureForIssue :one
-- Newest-first first-hit-wins: re-signing an issue supersedes the previous
-- authorization for claim-time attestation without invalidating history.
SELECT * FROM risk_signature
WHERE issue_id = $1
  AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > now())
ORDER BY signed_at DESC
LIMIT 1;

-- name: ListRiskSignaturesByWorkspace :many
SELECT * FROM risk_signature
WHERE workspace_id = $1
ORDER BY signed_at DESC
LIMIT $2;

-- name: ListRiskSignaturesByIssue :many
SELECT * FROM risk_signature
WHERE issue_id = $1
ORDER BY signed_at DESC;

-- name: RevokeRiskSignature :one
UPDATE risk_signature SET revoked_at = now()
WHERE id = $1 AND revoked_at IS NULL
RETURNING *;

-- name: MarkAgentTaskSignature :exec
-- Claim-time audit linkage: which run was authorized by which signature.
-- context is a free-form jsonb bag (quick-create precedent, mig 003), so
-- this is additive for every existing reader.
UPDATE agent_task_queue
SET context = COALESCE(context, '{}'::jsonb)
    || jsonb_build_object('signature_id', @signature_id::text, 'signature_fingerprint', @signature_fingerprint::text)
WHERE id = @id;
