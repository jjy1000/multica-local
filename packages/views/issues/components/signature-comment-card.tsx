"use client";

// signature-comment-card — timeline marker for `type='signature'` comments
// (mig 294). Renders as a compact authorization certificate instead of a
// normal comment: stamp icon, verified badge, signer / fingerprint / time /
// scope rows, and the signer's watermark as a faint background. The
// watermark image is fetched through api.rawRequest (authed; a bare <img>
// src would 401 in desktop — same law as lab artifacts) and revoked with
// the object URL. The background position is fixed, never random, so the
// card's innerHTML is deterministic for tests (constellation precedent).

import { useEffect, useMemo, useState } from "react";
import { Stamp } from "lucide-react";

import { api } from "@multica/core/api";
import { signatureFingerprintShort } from "@multica/core/types";
import { useIssueSignature } from "@multica/core/signature/hooks";
import { useWorkspaceId } from "@multica/core/hooks";
import type { TimelineEntry } from "@multica/core/types";

import { Badge } from "@multica/ui/components/ui/badge";

import { useT } from "../../i18n";
import { signatureOpLabel } from "./signature-shared";

/** Authed watermark fetch → object URL, revoked on change/unmount. */
function useWatermarkUrl(assetId: string | null): string | null {
  const [url, setUrl] = useState<string | null>(null);
  useEffect(() => {
    if (!assetId) {
      setUrl(null);
      return;
    }
    let revoked = false;
    let objectUrl: string | null = null;
    void (async () => {
      try {
        const res = await api.rawRequest(
          `/api/signature-assets/${encodeURIComponent(assetId)}/image`,
        );
        if (!res.ok) return;
        const blob = await res.blob();
        if (revoked) return;
        objectUrl = URL.createObjectURL(blob);
        setUrl(objectUrl);
      } catch {
        // Watermark is cosmetic; a failed fetch renders the card without it.
      }
    })();
    return () => {
      revoked = true;
      if (objectUrl) URL.revokeObjectURL(objectUrl);
      setUrl(null);
    };
  }, [assetId]);
  return url;
}

export function SignatureCommentCard({
  issueId,
  entry,
}: {
  issueId: string;
  entry: TimelineEntry;
}) {
  const { t } = useT("signature");
  // CommentCard renders only inside IssueDetail, which is always under the
  // workspace provider — the internal read is guaranteed context-safe here.
  const wsId = useWorkspaceId();
  const { data } = useIssueSignature(wsId, issueId);

  // Match the marker comment to its signature row: the newest signature
  // signed at or before the comment's creation time.
  const signature = useMemo(() => {
    const rows = data?.signatures ?? [];
    const at = new Date(entry.created_at).getTime();
    let best: (typeof rows)[number] | null = null;
    for (const row of rows) {
      const rowAt = new Date(row.signed_at).getTime();
      if (rowAt <= at && (!best || rowAt > new Date(best.signed_at).getTime())) {
        best = row;
      }
    }
    return best;
  }, [data?.signatures, entry.created_at]);

  const watermarkUrl = useWatermarkUrl(signature?.asset_id ?? null);

  const scope = signature
    ? signature.ops.map((op) => signatureOpLabel(t, op as Parameters<typeof signatureOpLabel>[1])).join(" · ")
    : null;
  const short = signature ? signatureFingerprintShort(signature.fingerprint) : null;

  return (
    <div
      className="relative overflow-hidden rounded-lg border border-emerald-500/30 bg-background p-3 pl-4"
      data-testid="signature-comment-card"
    >
      {watermarkUrl && (
        <img
          src={watermarkUrl}
          alt=""
          aria-hidden
          className="pointer-events-none absolute -right-3 -top-4 h-28 w-52 rotate-[-8deg] object-contain opacity-[0.07]"
        />
      )}
      <div className="relative flex items-center gap-2">
        <Stamp className="size-4 text-emerald-600 dark:text-emerald-400" aria-hidden />
        <span className="text-caption font-semibold">{t(($) => $.marker_title)}</span>
        <Badge variant="outline" className="text-[10px] text-emerald-600 dark:text-emerald-400">
          {t(($) => $.marker_verified)}
        </Badge>
      </div>
      <div className="relative mt-2 grid gap-x-5 gap-y-1 text-micro text-muted-foreground sm:grid-cols-2">
        <span>
          {t(($) => $.marker_signer)}:
          <span className="ml-1 font-mono text-foreground">
            {signature?.signed_by ? signature.signed_by.slice(0, 8) : "—"}
          </span>
        </span>
        <span>
          {t(($) => $.marker_fingerprint)}:
          <span className="ml-1 font-mono text-foreground">{short ? `sha256:${short}` : "—"}</span>
        </span>
        <span>
          {t(($) => $.marker_time)}:
          <span className="ml-1 font-mono text-foreground">
            {signature ? new Date(signature.signed_at).toLocaleString() : new Date(entry.created_at).toLocaleString()}
          </span>
        </span>
        {scope && (
          <span className="truncate">
            {t(($) => $.marker_scope)}:<span className="ml-1 text-foreground">{scope}</span>
          </span>
        )}
      </div>
    </div>
  );
}
