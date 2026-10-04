import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";

import { api } from "../api";
import type { RiskSignature, SignatureAsset } from "../types";

/**
 * Signature authorization queries (mig 294). Server state via TanStack
 * Query per the state rules; the signing mutation lives with the ceremony
 * dialog (it needs the dialog's animation phases, not shared state).
 *
 * The feature is default-OFF: nothing renders, uploads, signs, or injects
 * until the workspace arm switch (workspace.settings) is explicitly set
 * from Settings. The server independently enforces the same gate.
 */

export const SIGNATURE_ENABLED_SETTINGS_KEY = "signature_authorization_enabled";

/** Server-mirroring read: boolean-true only, every other shape is off. */
export function signatureEnabledFromSettings(
  settings: Record<string, unknown> | null | undefined,
): boolean {
  return settings?.[SIGNATURE_ENABLED_SETTINGS_KEY] === true;
}

export const signatureKeys = {
  assets: (wsId: string) => ["signature-assets", wsId] as const,
  workspaceHistory: (wsId: string) => ["signature-history", wsId] as const,
  issue: (wsId: string, issueId: string) => ["issue-signature", wsId, issueId] as const,
};

export function useSignatureAssets(wsId: string) {
  return useQuery({
    queryKey: signatureKeys.assets(wsId),
    queryFn: () => api.listSignatureAssets(),
    enabled: Boolean(wsId),
    staleTime: 60_000,
  });
}

export function useActiveSignatureAsset(assets: SignatureAsset[] | undefined): SignatureAsset | undefined {
  return assets?.find((a) => !a.retired_at);
}

export interface IssueSignatureState {
  signatures: RiskSignature[];
  active: RiskSignature | null;
}

export function useIssueSignature(wsId: string, issueId: string) {
  return useQuery<IssueSignatureState>({
    queryKey: signatureKeys.issue(wsId, issueId),
    queryFn: () => api.listIssueSignatures(issueId),
    enabled: Boolean(wsId && issueId),
  });
}

export function useInvalidateSignatures(wsId: string) {
  const qc = useQueryClient();
  return useCallback(
    (issueId?: string) => {
      void qc.invalidateQueries({ queryKey: signatureKeys.assets(wsId) });
      void qc.invalidateQueries({ queryKey: signatureKeys.workspaceHistory(wsId) });
      if (issueId) {
        void qc.invalidateQueries({ queryKey: signatureKeys.issue(wsId, issueId) });
      }
    },
    [qc, wsId],
  );
}

/**
 * Arm-switch read for the current workspace. Mirrors the server's
 * EnabledFromSettings semantics (boolean-true only). Callers gate ALL
 * signature UI on this — the server independently refuses disarmed
 * requests, so this hook is UX, not security.
 */
export function useSignatureEnabled(
  settings: Record<string, unknown> | null | undefined,
): boolean {
  return signatureEnabledFromSettings(settings);
}

/**
 * Flip the arm switch server-side. Merges into the live workspace settings
 * (append-only law — unknown keys survive; disabling writes an explicit
 * false for auditability, mirroring signing.WithEnabled).
 */
export async function setSignatureEnabled(
  workspaceId: string,
  currentSettings: Record<string, unknown> | null | undefined,
  enabled: boolean,
): Promise<void> {
  const merged: Record<string, unknown> = { ...(currentSettings ?? {}) };
  merged[SIGNATURE_ENABLED_SETTINGS_KEY] = enabled;
  await api.updateWorkspace(workspaceId, { settings: merged });
}
