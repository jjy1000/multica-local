"use client";

import { useQuery } from "@tanstack/react-query";
import { ExternalLink, FlaskConical } from "lucide-react";
import { api, parseWithFallback } from "@multica/core/api";
import { EMPTY_LAB_CONTEXT, LabContextSchema } from "@multica/core/api/schemas";
import type { LabContext, LabTaskBrief } from "@multica/core/types/api";
import { AppLink } from "../../navigation";
import { labSourceRouteSuffix } from "../../issues/components/issue-labs-section";
import { LabRunLink } from "./lab-run-link";
import { useT } from "../../i18n";

// Timeline summary card for labs that declare
// `hides_deliverable_in_issue_timeline` (currently claude_science_lab): the
// full report lives in the lab workbench view, so the timeline gets one
// condensed card ("task done + one-line summary + jump to lab") instead of
// the raw agent deliverable. Shares LabOutputPanel's query key so a page
// rendering both surfaces issues exactly one context request.

const POLL_INTERVAL_MS = 5_000;
const IDLE_INTERVAL_MS = 60_000;
const TERMINAL_TASK_STATUSES = new Set(["failed", "cancelled", "completed"]);

type ContextResult = { closed: true } | { closed: false; ctx: LabContext };

function sortedTasks(ctx: LabContext): LabTaskBrief[] {
  return [...ctx.tasks].sort((a, b) => (a.created_at < b.created_at ? 1 : -1));
}

function latestTask(ctx: LabContext): LabTaskBrief | null {
  return sortedTasks(ctx)[0] ?? null;
}

export function LabDeliverableSummary({
  wsId,
  issueId,
  labSource,
}: {
  wsId: string;
  issueId: string;
  labSource: string;
}) {
  const { t } = useT("experimental");
  const suffix = labSourceRouteSuffix(labSource);

  const query = useQuery({
    queryKey: ["lab-output-panel", wsId, issueId, labSource],
    queryFn: async (): Promise<ContextResult> => {
      const r = await api.rawRequest(
        `/api/experimental/claude-science-lab/issues/${encodeURIComponent(issueId)}/context?workspace_id=${encodeURIComponent(wsId)}`,
      );
      if (r.status === 404) return { closed: true };
      if (!r.ok) throw new Error(`lab-deliverable-summary ${r.status}`);
      const raw: unknown = await r.json();
      const ctx = parseWithFallback<LabContext>(raw, LabContextSchema, EMPTY_LAB_CONTEXT, {
        endpoint: "GET /api/experimental/claude-science-lab/issues/:id/context",
      });
      return { closed: false, ctx };
    },
    refetchInterval: (q) => {
      const data = q.state.data;
      if (!data || data.closed) return IDLE_INTERVAL_MS;
      const latest = latestTask(data.ctx);
      return latest && TERMINAL_TASK_STATUSES.has(latest.status)
        ? IDLE_INTERVAL_MS
        : POLL_INTERVAL_MS;
    },
  });

  // 404 = flag off / no lab context — must not surface a card in the timeline.
  // A transient error stays silent too (the lab view shows its own error state).
  if (query.isLoading || query.isError || query.data?.closed) return null;

  const ctx = query.data?.ctx ?? EMPTY_LAB_CONTEXT;
  const latest = latestTask(ctx);

  const openLink = suffix ? (
    <AppLink
      href={`/experimental/${suffix}?issue=${encodeURIComponent(issueId)}`}
      className="inline-flex items-center gap-1 text-caption text-purple-600 hover:text-purple-700 dark:text-purple-400 dark:hover:text-purple-300 transition-colors"
    >
      <ExternalLink className="size-3" />
      {t(($) => $.lab_output_panel.open_in_lab)}
    </AppLink>
  ) : null;

  if (!latest) {
    return (
      <div
        data-testid="lab-deliverable-summary"
        className="flex items-center justify-between gap-2 rounded-md border border-border bg-card px-3 py-2"
      >
        <div className="flex items-center gap-2 text-caption">
          <FlaskConical className="size-4 text-sky-600 dark:text-sky-400" aria-hidden />
          <span className="font-medium text-foreground">
            {t(($) => $.lab_output_panel.summary_card_title)}
          </span>
          <span className="text-muted-foreground">{t(($) => $.lab_output_panel.empty)}</span>
        </div>
        {openLink}
      </div>
    );
  }

  const status = latest.status;
  const isQueued = status === "queued" || status === "deferred";

  let statusText: string;
  let statusTone: string;
  if (status === "completed") {
    statusText = t(($) => $.lab_output_panel.status_completed);
    statusTone = "text-success";
  } else if (status === "failed") {
    statusText = t(($) => $.lab_output_panel.status_failed);
    statusTone = "text-destructive";
  } else if (status === "cancelled") {
    statusText = t(($) => $.lab_output_panel.status_cancelled);
    statusTone = "text-warning";
  } else if (isQueued) {
    statusText = t(($) => $.lab_output_panel.status_queued);
    statusTone = "text-muted-foreground";
  } else {
    statusText = t(($) => $.lab_output_panel.status_running);
    statusTone = "text-info";
  }

  return (
    <div
      data-testid="lab-deliverable-summary"
      className="space-y-1.5 rounded-md border border-border bg-card px-3 py-2"
    >
      <div className="flex items-center gap-2">
        <FlaskConical className="size-4 text-sky-600 dark:text-sky-400" aria-hidden />
        <span className="text-caption font-medium text-foreground">
          {t(($) => $.lab_output_panel.summary_card_title)}
        </span>
        <span className={`text-[10px] font-medium ${statusTone}`}>{statusText}</span>
      </div>
      {latest.result_summary && (
        <p className="line-clamp-3 text-xs leading-relaxed text-muted-foreground">
          {latest.result_summary}
        </p>
      )}
      <div className="flex flex-wrap items-center justify-end gap-3">
        {openLink}
        {/* 0.5.81 (ICP-3): deep-link to the lab view pre-scoped to this
            specific run via ?issue=&run=. Renders alongside the existing
            "Open in lab" link, not in place of it. */}
        <LabRunLink flagKey={labSource} issueId={issueId} runId={latest.id} />
      </div>
    </div>
  );
}
