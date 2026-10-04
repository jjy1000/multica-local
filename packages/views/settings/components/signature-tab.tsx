"use client";

// signature-tab — Settings → Signatures (mig 294): the default-OFF arm
// switch with its purpose + risk declaration, then (once armed) the
// watermark asset manager and signing history. The checkbox → enable flow
// is the acknowledgment record; the server independently refuses every
// signature surface on a disarmed workspace, so this UI is UX, not
// security.

import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound, ShieldAlert, ShieldCheck, Stamp } from "lucide-react";
import { toast } from "sonner";

import { api } from "@multica/core/api";
import { signatureFingerprintShort } from "@multica/core/types";
import {
  setSignatureEnabled,
  signatureEnabledFromSettings,
  signatureKeys,
  useInvalidateSignatures,
} from "@multica/core/signature/hooks";
import { useCurrentWorkspace } from "@multica/core/paths";
import { workspaceListOptions } from "@multica/core/workspace/queries";
import { useWorkspaceId } from "@multica/core/hooks";

import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";

import { useT } from "../../i18n";

export function SignatureTab() {
  const { t } = useT("signature");
  const wsId = useWorkspaceId();
  const workspace = useCurrentWorkspace();
  const queryClient = useQueryClient();
  const enabled = signatureEnabledFromSettings(workspace?.settings);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const invalidate = useInvalidateSignatures(wsId);
  const [confirmRetireId, setConfirmRetireId] = useState<string | null>(null);
  const [confirmRevokeId, setConfirmRevokeId] = useState<string | null>(null);
  const [ackChecked, setAckChecked] = useState(false);
  const [confirmDisable, setConfirmDisable] = useState(false);

  const assetsQuery = useQuery({
    queryKey: signatureKeys.assets(wsId),
    queryFn: () => api.listSignatureAssets(),
    enabled: Boolean(wsId) && enabled,
  });
  const historyQuery = useQuery({
    queryKey: signatureKeys.workspaceHistory(wsId),
    queryFn: () => api.listWorkspaceSignatures(),
    enabled: Boolean(wsId) && enabled,
  });

  const armMutation = useMutation({
    mutationFn: (next: boolean) => setSignatureEnabled(workspace!.id, workspace?.settings, next),
    onSuccess: (_data, next) => {
      if (!next) {
        setConfirmDisable(false);
        setAckChecked(false);
      }
      // Arm/disarm lives on the workspace object — refresh the workspace
      // list cache so useCurrentWorkspace().settings (and everything gated
      // on it, including the issue-header pill) reflects the new state.
      void queryClient.invalidateQueries({ queryKey: workspaceListOptions().queryKey });
      invalidate();
    },
    onError: (err: Error) => toast.error(err.message),
  });

  const uploadMutation = useMutation({
    mutationFn: (file: File) => api.uploadSignatureAsset(file),
    onSuccess: () => {
      toast.success(t(($) => $.toast_upload_ok));
      invalidate();
    },
    onError: (err: Error) => toast.error(`${t(($) => $.toast_upload_failed)}: ${err.message}`),
  });

  const retireMutation = useMutation({
    mutationFn: (id: string) => api.retireSignatureAsset(id),
    onSuccess: () => {
      toast.success(t(($) => $.toast_retired));
      setConfirmRetireId(null);
      invalidate();
    },
  });

  const revokeMutation = useMutation({
    mutationFn: (id: string) => api.revokeSignature(id),
    onSuccess: () => {
      toast.success(t(($) => $.toast_revoked));
      setConfirmRevokeId(null);
      invalidate();
    },
  });

  // ── Disarmed: purpose + risk declaration + acknowledgment gate ──────────
  if (!enabled) {
    return (
      <div className="space-y-5" data-testid="settings-signature-tab" data-armed="false">
        <div className="rounded-lg border border-amber-500/40 bg-amber-500/5 p-4">
          <h2 className="flex items-center gap-2 text-body font-semibold">
            <ShieldAlert className="size-4 text-amber-600 dark:text-amber-400" aria-hidden />
            {t(($) => $.enable_title)}
          </h2>
          <p className="mt-2 text-caption leading-relaxed text-muted-foreground">
            {t(($) => $.enable_purpose)}
          </p>
          <ul className="mt-2 list-disc space-y-1 pl-5 text-caption leading-relaxed text-muted-foreground">
            <li>{t(($) => $.enable_point_offensive)}</li>
            <li>{t(($) => $.enable_point_agents)}</li>
            <li>{t(($) => $.enable_point_tasks)}</li>
          </ul>
          <p className="mt-3 text-caption leading-relaxed text-amber-700 dark:text-amber-300">
            {t(($) => $.enable_risk)}
          </p>
          <label className="mt-4 flex cursor-pointer items-start gap-2 text-caption">
            <input
              type="checkbox"
              className="mt-0.5"
              checked={ackChecked}
              onChange={(e) => setAckChecked(e.target.checked)}
              data-testid="signature-ack-checkbox"
            />
            <span>{t(($) => $.enable_ack)}</span>
          </label>
          <Button
            className="mt-3"
            size="sm"
            disabled={!ackChecked || armMutation.isPending}
            onClick={() => armMutation.mutate(true)}
            data-testid="signature-enable-button"
          >
            <ShieldCheck className="size-3.5" aria-hidden />
            {t(($) => $.enable_button)}
          </Button>
        </div>
        <p className="text-micro text-muted-foreground">{t(($) => $.enable_off_note)}</p>
      </div>
    );
  }

  const assets = assetsQuery.data ?? [];
  const history = historyQuery.data ?? [];

  // ── Armed: management surface ───────────────────────────────────────────
  return (
    <div className="space-y-8" data-testid="settings-signature-tab" data-armed="true">
      <div className="flex flex-wrap items-center gap-3 rounded-lg border border-emerald-500/30 bg-emerald-500/5 px-4 py-3">
        <ShieldCheck className="size-4 text-emerald-600 dark:text-emerald-400" aria-hidden />
        <span className="text-caption font-medium">{t(($) => $.enable_armed_note)}</span>
        <Button
          variant="ghost"
          size="sm"
          className="ml-auto"
          disabled={armMutation.isPending}
          onClick={() => (confirmDisable ? armMutation.mutate(false) : setConfirmDisable(true))}
          data-testid="signature-disable-button"
        >
          {confirmDisable ? t(($) => $.disable_confirm) : t(($) => $.disable_button)}
        </Button>
      </div>

      <section>
        <h2 className="flex items-center gap-2 text-body font-semibold">
          <Stamp className="size-4" aria-hidden />
          {t(($) => $.asset_section)}
        </h2>
        <p className="mt-1 max-w-xl text-caption leading-relaxed text-muted-foreground">
          {t(($) => $.tab_description)}
        </p>

        {assets.length === 0 ? (
          <p className="mt-4 rounded-lg border border-dashed p-4 text-caption text-muted-foreground">
            {t(($) => $.asset_empty)}
          </p>
        ) : (
          <ul className="mt-4 space-y-2" data-testid="signature-asset-list">
            {assets.map((asset) => (
              <li
                key={asset.id}
                className="flex flex-wrap items-center gap-3 rounded-lg border p-3"
                data-testid="signature-asset-row"
                data-retired={Boolean(asset.retired_at)}
              >
                <ShieldCheck
                  className={`size-4 ${asset.retired_at ? "text-muted-foreground" : "text-emerald-600 dark:text-emerald-400"}`}
                  aria-hidden
                />
                <div className="min-w-0 flex-1">
                  <p className="truncate text-caption font-medium">
                    {asset.name || asset.id.slice(0, 8)}
                  </p>
                  <p className="flex items-center gap-1 font-mono text-micro text-muted-foreground">
                    <KeyRound className="size-3" aria-hidden />
                    {t(($) => $.asset_key_fp)}: {asset.public_key_fingerprint}
                  </p>
                </div>
                <Badge
                  variant="outline"
                  className={
                    asset.retired_at
                      ? "text-muted-foreground"
                      : "text-emerald-600 dark:text-emerald-400"
                  }
                >
                  {asset.retired_at ? t(($) => $.asset_retired) : t(($) => $.asset_active)}
                </Badge>
                {!asset.retired_at && (
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() =>
                      confirmRetireId === asset.id
                        ? retireMutation.mutate(asset.id)
                        : setConfirmRetireId(asset.id)
                    }
                  >
                    {confirmRetireId === asset.id ? t(($) => $.asset_retire_confirm) : t(($) => $.asset_retire)}
                  </Button>
                )}
              </li>
            ))}
          </ul>
        )}

        <input
          ref={fileInputRef}
          type="file"
          accept="image/*"
          className="hidden"
          onChange={(e) => {
            const file = e.target.files?.[0];
            if (file) uploadMutation.mutate(file);
            e.target.value = "";
          }}
        />
        <Button
          className="mt-3"
          size="sm"
          onClick={() => fileInputRef.current?.click()}
          disabled={uploadMutation.isPending}
          data-testid="signature-upload-button"
        >
          <Stamp className="size-3.5" aria-hidden />
          {uploadMutation.isPending ? t(($) => $.asset_uploading) : t(($) => $.asset_upload)}
        </Button>
      </section>

      <section>
        <h2 className="text-body font-semibold">{t(($) => $.history_section)}</h2>
        {history.length === 0 ? (
          <p className="mt-2 text-caption text-muted-foreground">{t(($) => $.history_empty)}</p>
        ) : (
          <ul className="mt-3 space-y-2" data-testid="signature-history-list">
            {history.map((sig) => (
              <li
                key={sig.id}
                className="flex flex-wrap items-center gap-3 rounded-lg border p-3"
                data-status={sig.status}
              >
                <Badge
                  variant="outline"
                  className={
                    sig.status === "active"
                      ? "text-emerald-600 dark:text-emerald-400"
                      : sig.status === "revoked"
                        ? "text-red-600 dark:text-red-400"
                        : "text-amber-600 dark:text-amber-400"
                  }
                >
                  {sig.status === "active"
                    ? t(($) => $.status_active)
                    : sig.status === "revoked"
                      ? t(($) => $.status_revoked)
                      : t(($) => $.status_expired)}
                </Badge>
                <span className="font-mono text-micro text-muted-foreground">
                  {t(($) => $.history_fp)}: {signatureFingerprintShort(sig.fingerprint)}
                </span>
                <span className="font-mono text-micro text-muted-foreground">
                  {new Date(sig.signed_at).toLocaleString()}
                </span>
                <span className="min-w-0 flex-1 truncate text-micro text-muted-foreground">
                  {t(($) => $.history_scope)}: {sig.ops.join(", ")}
                  {sig.issue_id ? ` · ${sig.issue_id.slice(0, 8)}` : ""}
                </span>
                {sig.status === "active" && (
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() =>
                      confirmRevokeId === sig.id
                        ? revokeMutation.mutate(sig.id)
                        : setConfirmRevokeId(sig.id)
                    }
                  >
                    {confirmRevokeId === sig.id ? t(($) => $.history_revoke_confirm) : t(($) => $.history_revoke)}
                  </Button>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
