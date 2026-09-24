"use client";

import { useQuery } from "@tanstack/react-query";
import { api, parseWithFallback } from "@multica/core/api";
import {
  EMPTY_LAB_CONTEXT,
  LabContextSchema,
} from "@multica/core/api/schemas";
import type {
  LabAttachment,
  LabCodeBlock,
  LabContext,
  LabPrediction,
  LabTaskBrief,
} from "@multica/core/types/api";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { ArtifactRenderer, type Artifact } from "./artifact-renderer";
import { InteractiveChartEnvelope } from "./interactive-chart-envelope";
import { AppLink } from "../../navigation";
import { labSourceRouteSuffix } from "../../issues/components/issue-labs-section";
import { LabRunLink, labRunHref } from "./lab-run-link";
import { useT } from "../../i18n";
import { PythiaPanel } from "./pythia/pythia-panel";

// 0.5.18 M1-M4: unified output panel for the four A-class issue-bound labs.
// claude_science_lab (M1), mythos_swarm (M2), and pythia_oracle (M3) are
// read-side pollers; code_canvas (M4) adds backend persistence (migration
// 239 code_canvas_artifact). 0.5.111: the pythia reader moved to
// ./pythia/pythia-panel.tsx (live SSE stream + continuation + report tabs;
// it now owns server-side run state instead of polling a static row).

const POLL_INTERVAL_MS = 5_000;
const IDLE_INTERVAL_MS = 60_000;

// Task statuses that will not change again — no point polling these at the
// live cadence (5s canonical; Active Contract #1, see agentTaskSnapshotOptions).
const TERMINAL_TASK_STATUSES = new Set(["failed", "cancelled", "completed"]);

const A_CLASS_LABS = new Set([
  "claude_science_lab",
  "pythia_oracle",
]);

interface LabOutputPanelProps {
  wsId: string;
  issueId: string;
  labSource: string;
}

// A 404 is the "flag turned off" signal (the route is flag-gated and vanishes
// when disabled), distinct from a transient network/server failure.
type ClaudeContextResult = { closed: true } | { closed: false; ctx: LabContext };

function sortedTasks(ctx: LabContext): LabTaskBrief[] {
  // The handler does not guarantee task order; sort by ISO timestamp
  // (same-format UTC strings compare lexicographically) so `[0]` is newest.
  return [...ctx.tasks].sort((a, b) => (a.created_at < b.created_at ? 1 : -1));
}

function latestTask(ctx: LabContext): LabTaskBrief | null {
  return sortedTasks(ctx)[0] ?? null;
}

function hasOutput(task: LabTaskBrief | null): boolean {
  if (!task) return false;
  return Boolean(
    task.result_summary ||
      (task.result_attachments?.length ?? 0) > 0 ||
      (task.result_predictions?.length ?? 0) > 0 ||
      (task.result_code_blocks?.length ?? 0) > 0,
  );
}

function latestTaskWithOutput(ctx: LabContext): LabTaskBrief | null {
  return sortedTasks(ctx).find((task) => hasOutput(task)) ?? null;
}

// Map a lab attachment `kind` (lab taxonomy) onto the generic Artifact `type`
// the shared ArtifactRenderer understands. URL-less SVG is valid as an iframe
// srcDoc, so it renders as html. `interactive-chart` is NOT mapped here —
// TaskOutput renders those envelopes directly via the shared
// InteractiveChartEnvelope (real recharts); anything reaching this function
// with that kind (envelope missing its data payload) falls through to the
// generic file/download fallback.
function attachmentToArtifact(a: LabAttachment): Artifact | null {
  const base = { id: a.name || a.kind, title: a.name || a.kind, created_at: "" };

  switch (a.kind) {
    case "png": {
      // Inline png data is base64 and must not be stuffed into an iframe
      // srcDoc; image attachments only render from a server URL.
      if (a.url) return { ...base, type: "image", url: a.url };
      return null;
    }
    case "svg": {
      if (a.url) return { ...base, type: "image", url: a.url };
      // Inline SVG is valid HTML, so a URL-less svg renders as an iframe srcDoc.
      if (typeof a.data === "string") return { ...base, type: "html", data: a.data };
      return null;
    }
    case "html":
      return { ...base, type: "html", data: a.data, url: a.url };
    case "md":
    case "txt":
    case "log":
      return {
        ...base,
        type: "text",
        data: typeof a.data === "string" ? a.data : String(a.data ?? ""),
      };
    case "csv":
    case "json":
      return {
        ...base,
        type: "code",
        data: typeof a.data === "string" ? a.data : JSON.stringify(a.data ?? {}, null, 2),
      };
    default:
      if (a.url) {
        return { ...base, type: "file", url: a.url, mime_type: a.mime, size: a.bytes };
      }
      return null;
  }
}

function clamp01(v: number): number {
  return Math.max(0, Math.min(1, v));
}

function PredictionsList({
  predictions,
  labViewHref,
}: {
  predictions: LabPrediction[];
  labViewHref?: string;
}) {
  const { t } = useT("experimental");
  return (
    <div className="space-y-1.5">
      {predictions.map((p, i) => (
        <div key={i} className="space-y-0.5">
          <div className="flex items-baseline justify-between gap-2 text-[11px]">
            <span className="truncate text-foreground/90">
              {p.round > 0 ? `R${p.round} · ` : ""}
              {p.scenario}
            </span>
            <span className="shrink-0 font-medium text-foreground">
              {Math.round(clamp01(p.probability) * 100)}%
            </span>
          </div>
          <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
            <div
              className="h-full rounded-full bg-purple-500"
              style={{ width: `${Math.round(clamp01(p.probability) * 100)}%` }}
            />
          </div>
          {labViewHref && (
            <div className="flex justify-end">
              <AppLink
                href={labViewHref}
                className="text-[10px] text-muted-foreground hover:text-foreground"
                aria-label={t(($) => $.lab_output_panel.view_in_lab)}
              >
                {t(($) => $.lab_output_panel.view_in_lab)} →
              </AppLink>
            </div>
          )}
        </div>
      ))}
    </div>
  );
}

function CodeBlockList({
  blocks,
  labViewHref,
}: {
  blocks: LabCodeBlock[];
  labViewHref?: string;
}) {
  const { t } = useT("experimental");
  return (
    <div className="space-y-2">
      {blocks.map((b, i) => (
        <div key={i} className="overflow-hidden rounded-lg border border-border">
          {(b.filename || b.language) && (
            <div className="flex items-center gap-2 border-b border-border bg-muted/50 px-2 py-1 text-[10px] text-muted-foreground">
              {b.language && <span className="font-mono">{b.language}</span>}
              {b.filename && <span className="truncate">{b.filename}</span>}
            </div>
          )}
          <pre className="overflow-x-auto p-2 text-xs">
            <code className="font-mono text-foreground">{b.code}</code>
          </pre>
          {labViewHref && (
            <div className="flex justify-end border-t border-border bg-muted/30 px-2 py-1">
              <AppLink
                href={labViewHref}
                className="text-[10px] text-muted-foreground hover:text-foreground"
                aria-label={t(($) => $.lab_output_panel.view_in_lab)}
              >
                {t(($) => $.lab_output_panel.view_in_lab)} →
              </AppLink>
            </div>
          )}
        </div>
      ))}
    </div>
  );
}

function TaskOutput({ task, labViewHref }: { task: LabTaskBrief; labViewHref?: string }) {
  const { t } = useT("experimental");
  const allAttachments = task.result_attachments ?? [];
  // interactive-chart envelopes render as real charts (shared renderer);
  // everything else goes through the generic Artifact mapping.
  const chartAttachments = allAttachments.filter(
    (a) => a.kind === "interactive-chart" && a.data,
  );
  const attachments = allAttachments
    .filter((a) => !(a.kind === "interactive-chart" && a.data))
    .map(attachmentToArtifact)
    .filter((a): a is Artifact => a != null);
  const predictions = task.result_predictions ?? [];
  const codeBlocks = task.result_code_blocks ?? [];

  return (
    <div className="space-y-3">
      {task.result_summary && (
        <div className="space-y-1">
          <p className="text-[11px] font-medium text-muted-foreground">
            {t(($) => $.lab_output_panel.summary_label)}
          </p>
          <p className="whitespace-pre-wrap text-xs leading-relaxed text-foreground">
            {task.result_summary}
          </p>
        </div>
      )}

      {chartAttachments.length > 0 && (
        <div className="space-y-1.5">
          <p className="text-[11px] font-medium text-muted-foreground">
            {t(($) => $.lab_output_panel.attachments_label)}
          </p>
          {chartAttachments.map((a, i) => (
            <figure
              key={`${a.name ?? "chart"}-${i}`}
              className="overflow-hidden rounded-lg border border-border"
            >
              <InteractiveChartEnvelope data={a.data} />
              {a.name ? (
                <figcaption className="border-t border-border px-2 py-1 text-[10px] text-muted-foreground">
                  {a.name}
                </figcaption>
              ) : null}
              {labViewHref && (
                <div className="flex justify-end border-t border-border bg-muted/30 px-2 py-1">
                  <AppLink
                    href={labViewHref}
                    className="text-[10px] text-muted-foreground hover:text-foreground"
                    aria-label={t(($) => $.lab_output_panel.view_in_lab)}
                  >
                    {t(($) => $.lab_output_panel.view_in_lab)} →
                  </AppLink>
                </div>
              )}
            </figure>
          ))}
        </div>
      )}

      {attachments.length > 0 && (
        <div className="space-y-1.5">
          <p className="text-[11px] font-medium text-muted-foreground">
            {t(($) => $.lab_output_panel.attachments_label)}
          </p>
          {attachments.map((art, i) => (
            <div
              key={`${art.id}-${i}`}
              className="overflow-hidden rounded-lg border border-border"
            >
              <ArtifactRenderer artifact={art} pluginSlug="" />
              {labViewHref && (
                <div className="flex justify-end border-t border-border bg-muted/30 px-2 py-1">
                  <AppLink
                    href={labViewHref}
                    className="text-[10px] text-muted-foreground hover:text-foreground"
                    aria-label={t(($) => $.lab_output_panel.view_in_lab)}
                  >
                    {t(($) => $.lab_output_panel.view_in_lab)} →
                  </AppLink>
                </div>
              )}
            </div>
          ))}
        </div>
      )}

      {predictions.length > 0 && (
        <div className="space-y-1.5">
          <p className="text-[11px] font-medium text-muted-foreground">
            {t(($) => $.lab_output_panel.predictions_label)}
          </p>
          <PredictionsList predictions={predictions} labViewHref={labViewHref} />
        </div>
      )}

      {codeBlocks.length > 0 && (
        <div className="space-y-1.5">
          <p className="text-[11px] font-medium text-muted-foreground">
            {t(($) => $.lab_output_panel.code_blocks_label)}
          </p>
          <CodeBlockList blocks={codeBlocks} labViewHref={labViewHref} />
        </div>
      )}
    </div>
  );
}

function ClaudePanel({ ctx, labSource, labViewHref }: { ctx: LabContext; labSource: string; labViewHref?: string }) {
  const { t } = useT("experimental");

  const runCount = (
    <p className="text-[11px] text-muted-foreground">
      {t(($) => $.lab_output_panel.run_count, { runs: String(ctx.lab_seq) })}
    </p>
  );

  // 0.5.81 (ICP-3): the latest task's id IS the run id (AgentTask.id) for
  // AgentTask-backed labs. Render a "View full record in lab" link that
  // jumps to the lab view pre-scoped via ?issue=&run= — independent of
  // the existing "View in lab" affordance so existing UX is preserved.
  const latestForRunLink = latestTask(ctx);
  const latestRunHref =
    latestForRunLink && ctx.issue?.id
      ? labRunHref(labSource, ctx.issue.id, latestForRunLink.id)
      : undefined;
  const runLink = latestRunHref ? (
    <LabRunLink
      flagKey={labSource}
      issueId={ctx.issue.id}
      runId={latestForRunLink!.id}
    />
  ) : null;

  // Empty only when there are truly no runs — a failed/cancelled/in-progress
  // run is not "no runs" and must not masquerade as one.
  if (ctx.tasks.length === 0) {
    return (
      <div className="space-y-1.5">
        {runCount}
        <p className="text-xs text-muted-foreground">{t(($) => $.lab_output_panel.empty)}</p>
      </div>
    );
  }

  const latest = latestTask(ctx);
  if (!latest) return null; // unreachable: tasks.length > 0 was checked above

  if (latest.status === "failed" || latest.status === "cancelled") {
    const reason = latest.error || latest.failure_reason;
    return (
      <div className="space-y-1.5">
        {runCount}
        <p className="whitespace-pre-wrap text-xs text-destructive">
          {reason || t(($) => $.lab_output_panel.run_failed)}
        </p>
      </div>
    );
  }

  // Latest run is still in flight with no output; surface the most recent
  // completed output so run-1's result isn't hidden behind the running run.
  const outputTask = hasOutput(latest) ? latest : latestTaskWithOutput(ctx);

  if (!outputTask) {
    return (
      <div className="space-y-1.5">
        {runCount}
        <p className="text-xs text-muted-foreground">
          {t(($) => $.lab_output_panel.in_progress)}
        </p>
      </div>
    );
  }

  const showRunningHint = outputTask.id !== latest.id;

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-2">
        {runCount}
        {runLink}
      </div>
      {showRunningHint && (
        <p className="text-xs text-muted-foreground">
          {t(($) => $.lab_output_panel.in_progress)}
        </p>
      )}
      <TaskOutput task={outputTask} labViewHref={labViewHref} />
    </div>
  );
}

export function LabOutputPanel({ wsId, issueId, labSource }: LabOutputPanelProps) {
  const { t } = useT("experimental");
  const isClaude = labSource === "claude_science_lab";
  const suffix = labSourceRouteSuffix(labSource);
  const labViewHref = suffix
    ? `/experimental/${suffix}?issue=${encodeURIComponent(issueId)}`
    : undefined;

  const query = useQuery({
    queryKey: ["lab-output-panel", wsId, issueId, labSource],
    queryFn: async (): Promise<ClaudeContextResult> => {
      const r = await api.rawRequest(
        `/api/experimental/claude-science-lab/issues/${encodeURIComponent(issueId)}/context?workspace_id=${encodeURIComponent(wsId)}`,
      );
      if (r.status === 404) return { closed: true };
      if (!r.ok) throw new Error(`lab-output-panel ${r.status}`);
      const raw: unknown = await r.json();
      const ctx = parseWithFallback<LabContext>(raw, LabContextSchema, EMPTY_LAB_CONTEXT, {
        endpoint: "GET /api/experimental/claude-science-lab/issues/:id/context",
      });
      return { closed: false, ctx };
    },
    enabled: isClaude,
    refetchInterval: (query) => {
      const data = query.state.data;
      if (!data || data.closed) return IDLE_INTERVAL_MS;
      const latest = latestTask(data.ctx);
      return latest && TERMINAL_TASK_STATUSES.has(latest.status)
        ? IDLE_INTERVAL_MS
        : POLL_INTERVAL_MS;
    },
  });

  if (!A_CLASS_LABS.has(labSource)) return null;

  if (labSource === "pythia_oracle") {
    // 0.5.112: no labViewHref — the interactive panel lives on the issue;
    // the /experimental/pythia page is a passive monitor, so the
    // issue→lab jump link is gone.
    return <PythiaPanel wsId={wsId} issueId={issueId} />;
  }



  if (!isClaude) {
    return (
      <p className="rounded-md border border-dashed border-border/60 px-2 py-1.5 text-[11px] text-muted-foreground">
        {t(($) => $.lab_output_panel.not_implemented)}
      </p>
    );
  }

  if (query.isLoading) {
    return (
      <div className="space-y-2" data-testid="lab-output-panel-loading">
        <Skeleton className="h-3 w-1/2" />
        <Skeleton className="h-3 w-full" />
        <Skeleton className="h-3 w-2/3" />
      </div>
    );
  }

  if (query.isError) {
    return (
      <div className="flex items-center justify-between gap-2 rounded-md border border-destructive/40 bg-destructive/10 px-2 py-1.5">
        <p className="text-[11px] text-destructive">{t(($) => $.lab_output_panel.error)}</p>
        <button
          type="button"
          onClick={() => query.refetch()}
          className="shrink-0 rounded-md border border-input bg-background px-2 py-0.5 text-[11px] font-medium text-foreground hover:bg-muted"
        >
          {t(($) => $.lab_output_panel.retry)}
        </button>
      </div>
    );
  }

  if (query.data?.closed) {
    return (
      <p className="rounded-md border border-dashed border-border/60 px-2 py-1.5 text-[11px] text-muted-foreground">
        {t(($) => $.lab_output_panel.closed)}
      </p>
    );
  }

  return <ClaudePanel ctx={query.data?.ctx ?? EMPTY_LAB_CONTEXT} labSource={labSource} labViewHref={labViewHref} />;
}
