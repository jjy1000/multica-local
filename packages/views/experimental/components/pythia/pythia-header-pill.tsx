"use client";

// pythia-header-pill — the small status indicator on the issue title row
// (0.5.113). Same data shape as PythiaIssueEmbed — they share the
// usePythiaIssueLab hook so both react to the same SSE round frames
// without duplicate subscriptions.
//
// Status palette mirrors agent running pills: spinning dot when the run
// is in flight, a check when done, an alert when aborted/failed. The pill
// is always present when the issue is pythia-bound (so the user knows
// "this issue is being read by the oracle" at a glance) and collapses when
// there is no run row AND no live stream (no signal to show).

import { AlertTriangle, CheckCircle2, CircleDashed, Loader2 } from "lucide-react";
import { usePythiaIssueLab } from "../../hooks/use-pythia-issue-lab";
import { useT } from "../../../i18n";

export function PythiaHeaderPill({
  wsId,
  issueId,
}: {
  wsId: string;
  issueId: string;
}) {
  const { t } = useT("experimental");
  const lab = usePythiaIssueLab(wsId, issueId);
  const { stream, runs, hasLiveRun } = lab;
  if (!runs.length && !hasLiveRun) return null;
  const status = hasLiveRun ? "running" : runs[0]?.status ?? "completed";

  const tone =
    status === "running"
      ? "bg-purple-500/10 text-purple-700 dark:text-purple-300"
      : status === "completed"
        ? "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
        : "bg-amber-500/10 text-amber-700 dark:text-amber-300";

  const icon =
    status === "running" ? (
      <Loader2 className="size-2.5 animate-spin" aria-hidden />
    ) : status === "completed" ? (
      <CheckCircle2 className="size-2.5" aria-hidden />
    ) : status === "aborted" || status === "failed" ? (
      <AlertTriangle className="size-2.5" aria-hidden />
    ) : (
      <CircleDashed className="size-2.5" aria-hidden />
    );

  const label =
    status === "running"
      ? t(($) => $.pythia_lab.pill_running, {
          round:
            stream.totalRounds > 0
              ? stream.totalRounds
              : stream.envelopes.length,
        })
      : status === "completed"
        ? t(($) => $.pythia_lab.pill_done, { rounds: runs[0]?.rounds ?? 0 })
        : status === "aborted"
          ? t(($) => $.pythia_lab.pill_aborted)
          : status === "failed"
            ? t(($) => $.pythia_lab.pill_failed)
            : t(($) => $.pythia_lab.pill_idle);

  return (
    <span
      className={`inline-flex shrink-0 items-center gap-1 rounded-full border border-current/20 px-1.5 py-0.5 text-[10px] font-medium ${tone}`}
      data-testid="pythia-header-pill"
      data-status={status}
      aria-live="polite"
    >
      {icon}
      {label}
    </span>
  );
}