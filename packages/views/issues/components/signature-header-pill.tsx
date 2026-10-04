"use client";

// signature-header-pill — issue-title indicator for signed authorization
// (mig 294), mounted next to Pythia/Claude lab pills. Unlike the lab pills
// it never hides: the ghost affordance IS the entry point for the signing
// ceremony (Phase 1 observation layer — nothing forces a signature yet, so
// discoverability rides on the pill itself).

import { BadgeCheck, PenLine } from "lucide-react";

import { signatureFingerprintShort } from "@multica/core/types";
import { useIssueSignature } from "@multica/core/signature/hooks";

import { useT } from "../../i18n";

export function SignatureHeaderPill({
  wsId,
  issueId,
  onOpen,
}: {
  wsId: string;
  issueId: string;
  onOpen: () => void;
}) {
  const { t } = useT("signature");
  const { data } = useIssueSignature(wsId, issueId);
  const active = data?.active ?? null;

  const signed = Boolean(active);
  const tone = signed
    ? "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
    : "border-dashed bg-transparent text-muted-foreground hover:text-foreground";

  return (
    <button
      type="button"
      onClick={onOpen}
      className={`inline-flex shrink-0 cursor-pointer items-center gap-1 rounded-full border border-current/20 px-1.5 py-0.5 text-[10px] font-medium transition-colors ${tone}`}
      data-testid="signature-header-pill"
      data-status={signed ? "signed" : "unsigned"}
      aria-live="polite"
      title={signed ? t(($) => $.pill_view) : t(($) => $.pill_unsigned)}
    >
      {signed ? (
        <>
          <BadgeCheck className="size-2.5" aria-hidden />
          {t(($) => $.pill_signed)}
          <span className="font-mono opacity-70">
            {signatureFingerprintShort(active?.fingerprint ?? "")}
          </span>
        </>
      ) : (
        <>
          <PenLine className="size-2.5" aria-hidden />
          {t(($) => $.pill_unsigned)}
        </>
      )}
    </button>
  );
}
