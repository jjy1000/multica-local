"use client";

// signature-ceremony-dialog — the signing ceremony for high-risk issues
// (mig 294). Three-beat animation ported from the approved prototype
// (.omc/prototypes/signature-ceremony/index.html):
//   1. pending — watermark preview + scope + fingerprint, cinnabar seal
//      resting beside the document;
//   2. stamping — seal drops (scale 1.8→1 + rotate), ink ripple expands,
//      paper gives one small thud-shake;
//   3. signed — emerald check pops in, badge flips to verified.
// All keyframes live in one component-mounted <style> block with a single
// prefers-reduced-motion gate; logic never depends on rAF or animation
// completion — the mutation result drives the phases, animation is purely
// cosmetic (canvas law from 0.5.132-0.5.136).

import { useEffect, useMemo, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { BadgeCheck, Fingerprint, PenLine, ShieldAlert } from "lucide-react";
import { toast } from "sonner";

import { api } from "@multica/core/api";
import { SIGNATURE_OPS, signatureFingerprintShort } from "@multica/core/types";
import type { SignatureOp } from "@multica/core/types";
import {
  useInvalidateSignatures,
  useIssueSignature,
  useSignatureAssets,
} from "@multica/core/signature/hooks";

import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";

import { useT } from "../../i18n";
import { useNavigation } from "../../navigation";
import { signatureOpLabel } from "./signature-shared";

const CEREMONY_STYLE = `
@keyframes sig-seal-drop {
  0% { opacity: 0; transform: scale(1.8) rotate(-12deg); }
  55% { opacity: 1; transform: scale(0.94) rotate(-5deg); }
  75% { transform: scale(1.04) rotate(-6deg); }
  100% { opacity: 0.92; transform: scale(1) rotate(-6deg); }
}
@keyframes sig-ink-ripple {
  0% { opacity: 0.9; transform: scale(0.6); }
  100% { opacity: 0; transform: scale(2.4); }
}
@keyframes sig-thud {
  0%, 100% { transform: translateX(0); }
  25% { transform: translateX(-2px); }
  50% { transform: translateX(2px); }
  75% { transform: translateX(-1px); }
}
@keyframes sig-check-pop {
  0% { transform: scale(0.4); opacity: 0; }
  100% { transform: scale(1); opacity: 1; }
}
.sig-seal { opacity: 0; transform: scale(1.8) rotate(-12deg); }
.sig-phase-stamping .sig-seal { animation: sig-seal-drop 0.55s cubic-bezier(0.23, 1, 0.32, 1) forwards; }
.sig-ink-ripple { opacity: 0; }
.sig-phase-stamping .sig-ink-ripple { animation: sig-ink-ripple 1.1s cubic-bezier(0.23, 1, 0.32, 1) forwards; }
.sig-phase-stamping .sig-doc { animation: sig-thud 0.4s cubic-bezier(0.23, 1, 0.32, 1) 1; }
.sig-check-pop { animation: sig-check-pop 0.35s cubic-bezier(0.23, 1, 0.32, 1) 1; }
@media (prefers-reduced-motion: reduce) {
  .sig-seal { opacity: 0.92; transform: scale(1) rotate(-6deg); animation: none; }
  .sig-ink-ripple, .sig-doc, .sig-check-pop { animation: none; }
  .sig-check-pop { opacity: 1; }
}
`;

type CeremonyPhase = "idle" | "stamping" | "signed";

const EXPIRY_CHOICES = [0, 7, 30] as const;

export function SignatureCeremonyDialog({
  open,
  onOpenChange,
  wsId,
  issueId,
  issueTitle,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  wsId: string;
  issueId: string;
  issueTitle: string;
}) {
  const { t } = useT("signature");
  const reduceMotion = useReducedMotion() ?? false;
  const navigation = useNavigation();

  const assetsQuery = useSignatureAssets(wsId);
  const activeAsset = assetsQuery.data?.find((a) => !a.retired_at);
  const { data: sigState } = useIssueSignature(wsId, issueId);
  const alreadyActive = Boolean(sigState?.active);

  const [selectedOps, setSelectedOps] = useState<readonly SignatureOp[]>(SIGNATURE_OPS);
  const [expiresDays, setExpiresDays] = useState<number>(0);
  const [phase, setPhase] = useState<CeremonyPhase>("idle");
  const [signedFingerprint, setSignedFingerprint] = useState<string | null>(null);
  const invalidate = useInvalidateSignatures(wsId);

  useEffect(() => {
    if (!open) {
      setPhase("idle");
      setSignedFingerprint(null);
    }
  }, [open]);

  const signMutation = useMutation({
    mutationFn: () =>
      api.signIssue(issueId, {
        asset_id: activeAsset?.id ?? "",
        ops: [...selectedOps],
        expires_days: expiresDays,
      }),
    onSuccess: (sig) => {
      setSignedFingerprint(sig.fingerprint);
      invalidate(issueId);
      toast.success(t(($) => $.toast_signed));
      // Keep the stamp animation at least one beat when motion is allowed;
      // under reduced motion the terminal state renders immediately anyway.
      window.setTimeout(() => setPhase("signed"), reduceMotion ? 0 : 650);
    },
    onError: (err: Error) => {
      setPhase("idle");
      toast.error(`${t(($) => $.toast_sign_failed)}: ${err.message}`);
    },
  });

  const toggleOp = (op: SignatureOp) => {
    setSelectedOps((prev) =>
      prev.includes(op) ? prev.filter((o) => o !== op) : [...prev, op],
    );
  };

  const fingerprintShort = useMemo(
    () => (signedFingerprint ? signatureFingerprintShort(signedFingerprint) : null),
    [signedFingerprint],
  );

  const handleStamp = () => {
    if (!selectedOps.length || !activeAsset) return;
    setPhase("stamping");
    signMutation.mutate();
  };

  const busy = phase === "stamping" || signMutation.isPending;

  return (
    <Dialog open={open} onOpenChange={(next) => !busy && onOpenChange(next)}>
      <DialogContent className="max-w-lg" data-testid="signature-ceremony-dialog">
        <style>{CEREMONY_STYLE}</style>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-body">
            <ShieldAlert className="size-4 text-red-600 dark:text-red-400" aria-hidden />
            {t(($) => $.ceremony_title)}
            {alreadyActive && phase !== "signed" && (
              <Badge variant="outline" className="text-emerald-600 dark:text-emerald-400">
                {t(($) => $.pill_signed)}
              </Badge>
            )}
          </DialogTitle>
          <DialogDescription>{issueTitle}</DialogDescription>
        </DialogHeader>

        {activeAsset ? (
          <div className={`sig-doc relative ${phase === "stamping" ? "sig-phase-stamping" : ""} rounded-lg border bg-muted/40 p-4`}>
            <p className="text-caption text-muted-foreground leading-relaxed">
              {t(($) => $.ceremony_scope)}
            </p>

            <div className="mt-3 space-y-2">
              <p className="text-caption font-medium">{t(($) => $.ceremony_ops)}</p>
              <div className="flex flex-wrap gap-1.5" data-testid="signature-op-choices">
                {SIGNATURE_OPS.map((op) => {
                  const on = selectedOps.includes(op);
                  return (
                    <button
                      key={op}
                      type="button"
                      onClick={() => !busy && toggleOp(op)}
                      className={`rounded-full border px-2.5 py-1 text-caption transition-colors ${
                        on
                          ? "border-red-500/40 bg-red-500/10 text-red-700 dark:text-red-300"
                          : "border-border text-muted-foreground"
                      }`}
                      data-op={op}
                      data-selected={on}
                      aria-pressed={on}
                    >
                      {signatureOpLabel(t, op)}
                    </button>
                  );
                })}
              </div>
            </div>

            <div className="mt-3 flex items-center gap-2">
              <p className="text-caption font-medium">{t(($) => $.ceremony_expiry)}</p>
              {EXPIRY_CHOICES.map((days) => (
                <button
                  key={days}
                  type="button"
                  onClick={() => !busy && setExpiresDays(days)}
                  className={`rounded-md border px-2 py-0.5 text-caption ${
                    expiresDays === days
                      ? "border-primary/50 bg-primary/10 text-primary"
                      : "border-border text-muted-foreground"
                  }`}
                  data-expiry={days}
                  aria-pressed={expiresDays === days}
                >
                  {days === 0
                    ? t(($) => $.expiry_none)
                    : t(($) => $.expiry_days, { n: String(days) })}
                </button>
              ))}
            </div>

            <div className="mt-3 flex items-center gap-1.5 font-mono text-micro text-muted-foreground">
              <Fingerprint className="size-3" aria-hidden />
              {phase === "signed" && fingerprintShort
                ? `${t(($) => $.ceremony_fingerprint)} sha256:${fingerprintShort}`
                : `${t(($) => $.ceremony_fingerprint)} —`}
            </div>

            {/* Seal + ripple layer (right-bottom of the document block). */}
            <span className="pointer-events-none absolute bottom-3 right-4 flex size-16 items-center justify-center" aria-hidden>
              <span className="sig-ink-ripple absolute inset-0 rounded-full border-2 border-red-500/30" />
              <svg className="sig-seal size-16" viewBox="0 0 96 96">
                <circle cx="48" cy="48" r="42" fill="none" stroke="var(--seal, #c2402a)" strokeWidth="3.5" />
                <circle cx="48" cy="48" r="35" fill="none" stroke="var(--seal, #c2402a)" strokeWidth="1.2" opacity="0.7" />
                {/* eslint-disable-next-line i18next/no-literal-string -- decorative seal glyph, part of the stamp graphic (like a logo), not UI copy */}
                <text x="48" y="44" textAnchor="middle" fontSize="17" fill="var(--seal, #c2402a)" fontWeight="700" style={{ fontFamily: "inherit" }}>
                  授 权
                </text>
                <text x="48" y="62" textAnchor="middle" fontSize="8.5" fill="var(--seal, #c2402a)" style={{ fontFamily: "var(--font-mono, monospace)" }}>
                  {fingerprintShort?.replace(" ", "·") ?? "····"}
                </text>
              </svg>
            </span>

            <AnimatePresence>
              {phase === "signed" && (
                <motion.span
                  initial={reduceMotion ? false : { opacity: 0, y: 4 }}
                  animate={{ opacity: 1, y: 0 }}
                  exit={{ opacity: 0 }}
                  className="sig-check-pop absolute bottom-4 left-4 inline-flex items-center gap-1.5 text-caption font-semibold text-emerald-600 dark:text-emerald-400"
                  data-testid="signature-ceremony-verified"
                >
                  <BadgeCheck className="size-4" aria-hidden />
                  {t(($) => $.ceremony_signed)}
                </motion.span>
              )}
            </AnimatePresence>
          </div>
        ) : (
          <div className="rounded-lg border border-dashed p-4 text-center" data-testid="signature-ceremony-empty">
            <PenLine className="mx-auto mb-2 size-5 text-muted-foreground" aria-hidden />
            <p className="text-caption font-medium">{t(($) => $.ceremony_no_asset)}</p>
            <p className="mt-1 text-micro text-muted-foreground">{t(($) => $.ceremony_no_asset_hint)}</p>
            <Button
              variant="outline"
              size="sm"
              className="mt-3"
              onClick={() => {
                onOpenChange(false);
                navigation.push("/settings?tab=signature");
              }}
            >
              {t(($) => $.ceremony_go_settings)}
            </Button>
          </div>
        )}

        <p className="text-micro leading-relaxed text-muted-foreground">
          {t(($) => $.ceremony_responsibility)}
        </p>

        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
            {phase === "signed" ? t(($) => $.ceremony_cancel) : t(($) => $.ceremony_cancel)}
          </Button>
          {activeAsset && phase !== "signed" && (
            <Button
              variant="default"
              className="bg-red-700/90 text-white hover:bg-red-700"
              onClick={handleStamp}
              disabled={busy || selectedOps.length === 0}
              data-testid="signature-ceremony-stamp"
            >
              <PenLine className="size-3.5" aria-hidden />
              {busy ? t(($) => $.ceremony_signing) : t(($) => $.ceremony_sign)}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
