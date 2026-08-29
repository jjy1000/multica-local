"use client";

// LabLastResultChip — compact "last result" affordance (0.5.81, ICP-3
// out-bound half complement). Renders above each embedded LabOutputPanel
// reader in IssueLabsSection. Shows the most recent TERMINAL
// `agent_task_queue` run summary (≤280 chars), an expand-on-click affordance,
// and a deep-link to the lab view pre-scoped to the run via
// `?issue=<id>&run=<runId>` (the same anchor LabRunLink renders).
//
// Augments, never replaces. LabOutputPanel is the canonical read-side
// surface; the chip is a glanceable summary card that lives one row
// above the panel. Both share the `lab-output-panel` TanStack Query
// cache key so a single network round-trip powers both surfaces.
//
// Scope (this commit): claude_science_lab only — the only A-class lab
// with a real LabContext implementation today. The component renders
// `null` for other labs (the panel below still works as before; the
// chip is purely additive).

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, FlaskConical } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { UI_EASE_OUT, UI_MOTION_DURATION } from "@multica/ui/lib/motion";
import { api, parseWithFallback } from "@multica/core/api";
import { EMPTY_LAB_CONTEXT, LabContextSchema } from "@multica/core/api/schemas";
import type { LabContext, LabTaskBrief } from "@multica/core/types/api";
import { LabRunLink } from "../../experimental/components/lab-run-link";
import { useT } from "../../i18n";

const SUMMARY_LIMIT = 280;

type ContextResult = { closed: true } | { closed: false; ctx: LabContext };

function sortedTasks(ctx: LabContext): LabTaskBrief[] {
  return [...ctx.tasks].sort((a, b) => (a.created_at < b.created_at ? 1 : -1));
}

function latestTerminalTask(ctx: LabContext): LabTaskBrief | null {
  const terminals = sortedTasks(ctx).filter((t) =>
    ["completed", "failed", "cancelled"].includes(t.status),
  );
  return terminals[0] ?? null;
}

function truncate(s: string, limit: number): string {
  if (s.length <= limit) return s;
  return s.slice(0, limit - 1).trimEnd() + "…";
}

export function LabLastResultChip({
  wsId,
  issueId,
  labSource,
}: {
  wsId: string;
  issueId: string;
  labSource: string;
}) {
  const { t } = useT("experimental");
  const [expanded, setExpanded] = useState(false);
  const reduceMotion = useReducedMotion() ?? false;

  // Only claude_science_lab has a real LabContext implementation today;
  // other A-class labs render their own placeholders below the chip.
  const isClaude = labSource === "claude_science_lab";
  const query = useQuery({
    queryKey: ["lab-output-panel", wsId, issueId, labSource],
    queryFn: async (): Promise<ContextResult> => {
      const r = await api.rawRequest(
        `/api/experimental/claude-science-lab/issues/${encodeURIComponent(issueId)}/context?workspace_id=${encodeURIComponent(wsId)}`,
      );
      if (r.status === 404) return { closed: true };
      if (!r.ok) throw new Error(`lab-last-result-chip ${r.status}`);
      const raw: unknown = await r.json();
      const ctx = parseWithFallback<LabContext>(raw, LabContextSchema, EMPTY_LAB_CONTEXT, {
        endpoint: "GET /api/experimental/claude-science-lab/issues/:id/context",
      });
      return { closed: false, ctx };
    },
    enabled: isClaude,
  });

  if (!isClaude) return null;
  if (query.isLoading || query.isError || !query.data || query.data.closed) return null;

  const ctx = query.data.ctx;
  const latest = latestTerminalTask(ctx);
  if (!latest) return null;

  const summary = latest.result_summary?.trim() || null;
  if (!summary) return null;

  const truncated = truncate(summary, SUMMARY_LIMIT);
  const isLong = summary.length > SUMMARY_LIMIT;

  return (
    <div
      data-testid="lab-last-result-chip"
      className="space-y-1.5 rounded-md border border-border bg-card px-3 py-2 text-caption"
    >
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        aria-expanded={expanded}
        className="flex w-full items-center justify-between gap-2 text-left"
      >
        <span className="flex items-center gap-1.5">
          <FlaskConical className="size-3.5 text-sky-600 dark:text-sky-400" aria-hidden />
          <span className="font-medium text-foreground">
            {t(($) => $.lab_last_result_chip.title)}
          </span>
          <span className="rounded bg-muted px-1 py-0.5 text-[10px] text-muted-foreground">
            {latest.status}
          </span>
        </span>
        <span className="text-muted-foreground">
          {expanded ? (
            <ChevronDown className="size-3.5" aria-hidden />
          ) : (
            <ChevronRight className="size-3.5" aria-hidden />
          )}
        </span>
      </button>
      {/* line-clamp height is CSS-driven, so the expand can't be a height
          animation — fade-through keyed on `expanded` instead (same
          AnimatePresence treatment as IssueLabsSection's body). Reduced
          motion swaps the text instantly. */}
      {reduceMotion ? (
        <p
          key={expanded ? "expanded" : "collapsed"}
          className={
            "text-xs leading-relaxed text-muted-foreground " +
            (expanded ? "" : "line-clamp-2")
          }
        >
          {expanded && isLong ? summary : truncated}
        </p>
      ) : (
        <AnimatePresence initial={false} mode="wait">
          <motion.p
            key={expanded ? "expanded" : "collapsed"}
            className={
              "text-xs leading-relaxed text-muted-foreground " +
              (expanded ? "" : "line-clamp-2")
            }
            initial={{ opacity: 0 }}
            animate={{
              opacity: 1,
              transition: { duration: UI_MOTION_DURATION.fast, ease: UI_EASE_OUT },
            }}
            exit={{
              opacity: 0,
              transition: { duration: UI_MOTION_DURATION.micro, ease: UI_EASE_OUT },
            }}
          >
            {expanded && isLong ? summary : truncated}
          </motion.p>
        </AnimatePresence>
      )}
      <div className="flex items-center justify-end gap-3">
        <LabRunLink
          flagKey={labSource}
          issueId={issueId}
          runId={latest.id}
        />
      </div>
    </div>
  );
}