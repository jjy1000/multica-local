-- Signature authorization (Phase 1 observation layer): watermark signature
-- assets with per-asset Ed25519 keypairs, signed risk authorizations for
-- high-risk issues, the 'signature' comment type for the timeline marker,
-- and the 'waiting_signature' parked task status (its state machine lands
-- with Phase 3; the CHECK value ships now so it needs no second migration).
--
-- Key custody: public keys live in signature_asset; private keys NEVER
-- enter the database — the server writes them to
-- ~/.multica/signing/<asset-id>.key (0600) at upload time. Watermark bytes
-- stay in-bytea (2 MiB cap enforced in the handler): images are small and
-- keeping them in the row makes upload + key generation + insert one
-- transaction, with no partial-failure window to reconcile.
CREATE TABLE signature_asset (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name TEXT NOT NULL DEFAULT '',
    mime TEXT NOT NULL,
    image BYTEA NOT NULL,
    image_sha256 TEXT NOT NULL,
    algorithm TEXT NOT NULL DEFAULT 'ed25519',
    public_key BYTEA NOT NULL,
    activated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    retired_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (algorithm IN ('ed25519'))
);
CREATE INDEX idx_signature_asset_workspace ON signature_asset (workspace_id, created_at DESC);

-- One row per completed signing ceremony. scope is the canonical JSON the
-- signer authorized (issue binding + content snapshot hash + op set +
-- expiry); fingerprint = sha256(canonical scope); signature = Ed25519 over
-- the fingerprint bytes. Revocation is a tombstone (revoked_at), never a
-- delete — same law as causal edges (mig 280).
CREATE TABLE risk_signature (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    issue_id UUID REFERENCES issue(id) ON DELETE CASCADE,
    asset_id UUID NOT NULL REFERENCES signature_asset(id),
    signed_by UUID NOT NULL REFERENCES "user"(id),
    ops TEXT[] NOT NULL DEFAULT '{}',
    scope JSONB NOT NULL,
    content_sha256 TEXT NOT NULL DEFAULT '',
    fingerprint TEXT NOT NULL,
    signature TEXT NOT NULL,
    signed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (jsonb_typeof(scope) = 'object')
);
CREATE INDEX idx_risk_signature_issue ON risk_signature (issue_id, signed_at DESC) WHERE issue_id IS NOT NULL;
CREATE INDEX idx_risk_signature_workspace ON risk_signature (workspace_id, signed_at DESC);

-- comment gains the 'signature' kind: the signing ceremony posts one
-- top-level marker comment the FE renders as the watermark card.
ALTER TABLE comment DROP CONSTRAINT IF EXISTS comment_type_check;
ALTER TABLE comment ADD CONSTRAINT comment_type_check
    CHECK (type IN ('comment', 'status_change', 'progress_update', 'system', 'signature'));

-- Parked status for high-risk tasks awaiting a signature. No state-machine
-- transitions write it yet (Phase 3); widening the CHECK now is additive.
-- The rebuilt set preserves every prior member, including 'deferred'
-- (added by mig 128's escalation routing) — dropping one would break the
-- deferred-fire path the moment this migration lands.
ALTER TABLE agent_task_queue DROP CONSTRAINT IF EXISTS agent_task_queue_status_check;
ALTER TABLE agent_task_queue ADD CONSTRAINT agent_task_queue_status_check
    CHECK (status IN ('queued', 'dispatched', 'running', 'waiting_local_directory', 'waiting_signature', 'deferred', 'completed', 'failed', 'cancelled'));
