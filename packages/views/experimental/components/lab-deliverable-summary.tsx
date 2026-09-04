"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChartLine, ChevronUp, FlaskConical } from "lucide-react";
import { api, parseWithFallback } from "@multica/core/api";
import { EMPTY_LAB_CONTEXT, LabContextSchema } from "@multica/core/api/schemas";
import type { LabContext, LabTaskBrief } from "@multica/core/types/api";
import {
  LabTaskResultView,
  labTaskHasStructuredDeliverables,
} from "./lab-task-result-view";
import { LabRunLink } from "./lab-run-link";
import { useT } from "../../i18n";

// Timeline summary card for labs that declare
// `hides_deliverable_in_issue_timeline` (currently claude_science_lab):
// the full report lives in the lab workbench view, so the timeline gets
// one condensed card ("task done + one-line summary + two affordances")
// instead of the raw agent deliverable. The two affordances have distinct
// jobs (0.5.97 UX split, supersedes the old open-in-lab + run-link pair
// that both jumped to the same page):
//
//   - 查看结果渲染  → expands THIS card in place and renders the run's
//     structured deliverables (charts / predictions / code) via the
//     shared LabTaskResultView. No navigation.
//   - 在实验室查看完整记录 → the LabRunLink deep link
//     (/experimental/<lab>?issue=&run=) into the lab panel's run history.
//
// Shares LabOutputPanel's query key so a page rendering both surfaces
// issues exactly one context request.

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
  const [resultOpen, setResultOpen] = useState(false);

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
        <LabRunLink flagKey={labSource} issueId={issueId} />
      </div>
    );
  }

  const status = latest.status;
  const isQueued = status === "queued" || status === "deferred";
  const isTerminal = TERMINAL_TASK_STATUSES.has(status);
  const hasDeliverables = labTaskHasStructuredDeliverables(latest);

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
        {ctx.lab_seq > 0 && (
          <span className="text-[10px] text-muted-foreground">
            {t(($) => $.lab_output_panel.run_count, { runs: String(ctx.lab_seq) })}
          </span>
        )}
      </div>
      {latest.result_summary && (
        <p className="line-clamp-3 text-xs leading-relaxed text-muted-foreground">
          {latest.result_summary}
        </p>
      )}
      {/* In-place result rendering (no navigation) — only for terminal runs
          with structured deliverables; expanding an in-flight run would just
          render a spinner-shaped void. */}
      {isTerminal && hasDeliverables && resultOpen && (
        <div className="max-h-96 overflow-y-auto rounded-md border border-border/60 bg-background/60 p-2.5">
          <LabTaskResultView task={latest} />
        </div>
      )}
      <div className="flex flex-wrap items-center justify-end gap-3">
        {isTerminal && hasDeliverables && (
          <button
            type="button"
            onClick={() => setResultOpen((v) => !v)}
            aria-expanded={resultOpen}
            data-testid="lab-deliverable-result-toggle"
            className="inline-flex items-center gap-1 text-caption text-purple-600 hover:text-purple-700 dark:text-purple-400 dark:hover:text-purple-300 transition-colors"
          >
            {resultOpen ? (
              <ChevronUp className="size-3" aria-hidden />
            ) : (
              <ChartLine className="size-3" aria-hidden />
            )}
            {resultOpen
              ? t(($) => $.lab_output_panel.hide_result_render)
              : t(($) => $.lab_output_panel.view_result_render)}
          </button>
        )}
        {/* Deep link to the lab panel's run history, pre-scoped to this
            specific run via ?issue=&run= (0.5.81 ICP-3 contract). */}
        <LabRunLink flagKey={labSource} issueId={issueId} runId={latest.id} />
      </div>
    </div>
  );
}
