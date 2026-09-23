"use client";

// claude-header-pill — the issue-title status indicator for
// claude_science_lab (0.5.114). Direct analogue of PythiaHeaderPill;
// shares useClaudeLabIssue with the main-pane embed so both react to
// the same AgentTaskSnapshot round-trips without duplicate fetches.
// Sky palette matches the Claude Lab badge on issue rows. Collapses to
// null when the issue has no lab tasks at all (no signal to show).

import { AlertTriangle, CheckCircle2, CircleDashed, Loader2 } from "lucide-react";
import { useClaudeLabIssue } from "../../hooks/use-claude-lab-issue";
import { useT } from "../../../i18n";

export function ClaudeHeaderPill({
  wsId,
  issueId,
}: {
  wsId: string;
  issueId: string;
}) {
  const { t } = useT("experimental");
  const { tasks, status } = useClaudeLabIssue(wsId, issueId);
  if (!tasks.length) return null;

  const tone =
    status === "running"
      ? "bg-sky-500/10 text-sky-700 dark:text-sky-300"
      : status === "completed"
        ? "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
        : "bg-amber-500/10 text-amber-700 dark:text-amber-300";

  const icon =
    status === "running" || status === "queued" ? (
      <Loader2 className="size-2.5 animate-spin" aria-hidden />
    ) : status === "completed" ? (
      <CheckCircle2 className="size-2.5" aria-hidden />
    ) : status === "failed" || status === "cancelled" ? (
      <AlertTriangle className="size-2.5" aria-hidden />
    ) : (
      <CircleDashed className="size-2.5" aria-hidden />
    );

  const label =
    status === "running"
      ? t(($) => $.claude_lab.pill_running)
      : status === "queued"
        ? t(($) => $.claude_lab.pill_queued)
        : status === "completed"
          ? t(($) => $.claude_lab.pill_done)
          : status === "failed"
            ? t(($) => $.claude_lab.pill_failed)
            : status === "cancelled"
              ? t(($) => $.claude_lab.pill_cancelled)
              : t(($) => $.claude_lab.pill_idle);

  return (
    <span
      className={`inline-flex shrink-0 items-center gap-1 rounded-full border border-current/20 px-1.5 py-0.5 text-[10px] font-medium ${tone}`}
      data-testid="claude-header-pill"
      data-status={status}
      aria-live="polite"
    >
      {icon}
      {label}
    </span>
  );
}
