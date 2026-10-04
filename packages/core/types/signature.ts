/**
 * Signature authorization (mig 294): watermark signature assets with
 * per-asset Ed25519 keypairs, and signed risk authorizations covering
 * high-risk issues. Server-owned contract — the op vocabulary mirrors
 * `server/internal/signing/signing.go` and renaming either side is a
 * breaking change to stored scopes.
 */

export type SignatureOp =
  | "offensive_drill"
  | "create_agent"
  | "create_squad"
  | "create_skill"
  | "install_plugin"
  | "lab_delegate";

export const SIGNATURE_OPS: readonly SignatureOp[] = [
  "offensive_drill",
  "create_agent",
  "create_squad",
  "create_skill",
  "install_plugin",
  "lab_delegate",
] as const;

export interface SignatureAsset {
  id: string;
  workspace_id: string;
  name: string;
  mime: string;
  image_sha256: string;
  algorithm: string;
  public_key_fingerprint: string;
  activated_at: string;
  retired_at: string | null;
  created_at: string;
}

export type RiskSignatureStatus = "active" | "revoked" | "expired";

export interface RiskSignature {
  id: string;
  workspace_id: string;
  issue_id: string | null;
  asset_id: string;
  signed_by: string;
  ops: string[];
  scope: Record<string, unknown>;
  content_sha256: string;
  fingerprint: string;
  signature: string;
  signed_at: string;
  expires_at: string | null;
  revoked_at: string | null;
  status: RiskSignatureStatus;
}

export interface SignatureVerifyResult {
  signature: RiskSignature | null;
  valid: boolean;
  checks: Record<string, boolean>;
  reasons: string[];
  verified_at: string;
}

/** Display form of a fingerprint: first 8 hex chars, grouped in pairs. */
export function signatureFingerprintShort(fingerprint: string): string {
  const clean = fingerprint.replace(/^sha256:/, "");
  if (clean.length < 8) return clean;
  return `${clean.slice(0, 4)} ${clean.slice(4, 8)}`;
}
