import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";

import { api } from "../api";
import type { RiskSignature, SignatureAsset } from "../types";

/**
 * Signature authorization queries (mig 294). Server state via TanStack
 * Query per the state rules; the signing mutation lives with the ceremony
 * dialog (it needs the dialog's animation phases, not shared state).
 */

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
