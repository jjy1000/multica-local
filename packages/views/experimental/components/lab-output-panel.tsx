"use client";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, CheckCircle2, FlaskConical, Loader2, RefreshCw, XCircle } from "lucide-react";
import { api, parseWithFallback } from "@multica/core/api";
import { useTimesfmForecastRuns } from "@multica/core/experimental";
import {
  CodeCanvasArtifactListSchema,
  CodeCanvasArtifactSchema,
  EMPTY_LAB_CONTEXT,
  EMPTY_MYTHOS_SUPERVISE_STATE,
  LabContextSchema,
  MythosRunListSchema,
  MythosSuperviseStateSchema,
  PythiaForecastRunListSchema,
} from "@multica/core/api/schemas";
import type {
  CodeCanvasArtifact,
  LabAttachment,
  LabCodeBlock,
  LabContext,
  LabPrediction,
  LabTaskBrief,
  MythosCodaConclusion,
  MythosRunSummary,
  MythosSuperviseState,
  PythiaForecastEnvelope,
  PythiaForecastRun,
} from "@multica/core/types/api";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { ArtifactRenderer, type Artifact } from "./artifact-renderer";
import { AppLink } from "../../navigation";
import { labSourceRouteSuffix } from "../../issues/components/issue-labs-section";
import { LabRunLink, labRunHref } from "./lab-run-link";
import {
  PYTHIA_IN_PROGRESS_WINDOW_MS,
  derivePythiaTriggerState,
  markPythiaTriggered,
  readPythiaTriggeredAt,
} from "./lab-run-heuristics";
import { useT } from "../../i18n";

// 0.5.18 M1-M4: unified output panel for the four A-class issue-bound labs.
// claude_science_lab (M1), mythos_swarm (M2), and pythia_oracle (M3) are
// read-side only — they poll existing endpoints (no backend changes).
// code_canvas (M4) is the exception: it adds backend persistence (migration
// 239 code_canvas_artifact) with a render+persist POST and a history GET, and
// the panel renders the persisted artifacts in sandboxed iframes (see
// .omc/plans/lab-output-panel-design.md §2.3).

const POLL_INTERVAL_MS = 5_000;
const IDLE_INTERVAL_MS = 60_000;

// Task statuses that will not change again — no point polling these at the
// live cadence (5s canonical; Active Contract #1, see agentTaskSnapshotOptions).
const TERMINAL_TASK_STATUSES = new Set(["failed", "cancelled", "completed"]);

const A_CLASS_LABS = new Set([
  "claude_science_lab",
  "pythia_oracle",
  "mythos_swarm",
  "code_canvas",
  // 0.5.82: TimesFM forecasting lab — compact reader below; the full
  // records surface is /experimental/timesfm-lab.
  "timesfm",
]);

interface LabOutputPanelProps {
  wsId: string;
  issueId: string;
  labSource: string;
  labMode?: "sole" | "enhancer";
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
// srcDoc, so it renders as html. interactive-chart carries {schema, data}
// which the generic chart renderer does not parse, so it degrades to a code
// dump rather than a misleading "invalid chart" message.
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
    case "interactive-chart":
      // TODO: interactive-chart carries {schema, data}, not the generic
      // {chart_type, labels, datasets} the ArtifactRenderer chart expects —
      // parse it into a real chart/table here in a follow-up (M2+).
      return {
        ...base,
        type: "code",
        data: typeof a.data === "string" ? a.data : JSON.stringify(a.data ?? {}, null, 2),
      };
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
  const attachments = (task.result_attachments ?? [])
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
  const latestRunHref = latestForRunLink
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

// ── Mythos Swarm (M2) ─────────────────────────────────────────────────────

// Mythos run statuses that will not change again (see migration 157 CHECK:
// running / completed / aborted / failed / supervising).
const TERMINAL_MYTHOS_STATUSES = new Set(["completed", "aborted", "failed"]);

// Supervise phases that need the live cadence; done / aborted / degraded fall
// back to the idle beat. Phase values mirror the server source-of-truth
// (server/internal/service/mythos/supervise.go SupervisionPhase).
const LIVE_MYTHOS_PHASES = new Set(["preparing", "planning", "supervising"]);

function MythosConclusions({
  conclusions,
  labViewHref,
}: {
  conclusions: MythosCodaConclusion[];
  labViewHref?: string;
}) {
  const { t } = useT("experimental");
  return (
    <div className="space-y-1.5">
      <dl className="grid grid-cols-[auto_1fr] gap-x-2 gap-y-1 text-xs">
        {conclusions.map((c, i) => (
          <span key={`${c.key}-${i}`} className="contents">
            <dt className="font-mono text-foreground">{c.key}:</dt>
            <dd className="text-foreground">{c.value}</dd>
          </span>
        ))}
      </dl>
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
  );
}

function MythosPhaseIcon({ phase }: { phase: string }) {
  const cls = "size-3 shrink-0";
  switch (phase) {
    case "preparing":
    case "planning":
      return <Loader2 className={`${cls} animate-spin text-purple-500`} aria-hidden />;
    case "supervising":
      return <Loader2 className={`${cls} animate-spin text-emerald-500`} aria-hidden />;
    case "done":
      return <CheckCircle2 className={`${cls} text-emerald-500`} aria-hidden />;
    case "aborted":
      return <XCircle className={`${cls} text-red-500`} aria-hidden />;
    case "degraded":
      return <AlertTriangle className={`${cls} text-amber-500`} aria-hidden />;
    default:
      return <FlaskConical className={`${cls} text-purple-500`} aria-hidden />;
  }
}

function MythosSuperviseSection({ runID }: { runID: string }) {
  const { t } = useT("experimental");
  const qc = useQueryClient();

  const stateQuery = useQuery({
    queryKey: ["lab-output-panel-mythos-supervise", runID],
    queryFn: async (): Promise<MythosSuperviseState | null> => {
      const r = await api.rawRequest(
        `/api/experimental/mythos-swarm/supervise/${encodeURIComponent(runID)}`,
      );
      if (r.status === 404) return null;
      if (!r.ok) throw new Error(`mythos supervise ${r.status}`);
      const raw: unknown = await r.json();
      return parseWithFallback<MythosSuperviseState>(
        raw,
        MythosSuperviseStateSchema,
        EMPTY_MYTHOS_SUPERVISE_STATE,
        { endpoint: "GET /api/experimental/mythos-swarm/supervise/:runID" },
      );
    },
    refetchInterval: (query) => {
      const snap = query.state.data;
      if (!snap) return IDLE_INTERVAL_MS;
      return LIVE_MYTHOS_PHASES.has(snap.phase) ? POLL_INTERVAL_MS : IDLE_INTERVAL_MS;
    },
  });

  const tick = useMutation({
    mutationFn: async (): Promise<MythosSuperviseState> => {
      const r = await api.rawRequest(
        `/api/experimental/mythos-swarm/supervise/${encodeURIComponent(runID)}/tick`,
        { method: "POST" },
      );
      if (!r.ok) throw new Error(`mythos supervise tick ${r.status}`);
      const raw: unknown = await r.json();
      return parseWithFallback<MythosSuperviseState>(
        raw,
        MythosSuperviseStateSchema,
        EMPTY_MYTHOS_SUPERVISE_STATE,
        { endpoint: "POST /api/experimental/mythos-swarm/supervise/:runID/tick" },
      );
    },
    onSuccess: (snapshot) => {
      qc.setQueryData(["lab-output-panel-mythos-supervise", runID], snapshot);
    },
  });

  const phase = stateQuery.data?.phase ?? "preparing";
  const isLivePhase = LIVE_MYTHOS_PHASES.has(phase);

  return (
    <div className="space-y-1 rounded-md border border-purple-500/30 bg-purple-500/5 px-2 py-1.5 text-[11px]">
      <div className="flex items-center gap-2">
        <MythosPhaseIcon phase={phase} />
        <span className="truncate font-medium text-foreground/90">{phase}</span>
      </div>

      {stateQuery.data && (
        <div className="space-y-0.5 text-muted-foreground">
          {stateQuery.data.sub_tasks_total > 0 && (
            <div>
              {stateQuery.data.sub_tasks_done}/{stateQuery.data.sub_tasks_total}
            </div>
          )}
          {stateQuery.data.total_ticks > 0 && (
            <div className="text-[10px]">
              {stateQuery.data.total_ticks} ticks · {stateQuery.data.last_tick_duration_ms}ms
            </div>
          )}
          {stateQuery.data.latest_reflection && (
            <div className="mt-1 line-clamp-2 italic text-foreground/70">
              {stateQuery.data.latest_reflection}
            </div>
          )}
        </div>
      )}

      {tick.isError && (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 px-2 py-1 text-[10px] text-destructive">
          {t(($) => $.lab_output_panel.mythos_tick_failed)}
        </div>
      )}

      <button
        type="button"
        onClick={() => tick.mutate()}
        disabled={tick.isPending || !isLivePhase}
        className="inline-flex items-center gap-1 rounded-md border border-border/60 bg-background/80 px-2 py-0.5 text-[10px] font-medium text-foreground/80 transition-colors hover:bg-accent disabled:opacity-50"
      >
        <RefreshCw className={`size-2.5 ${tick.isPending ? "animate-spin" : ""}`} aria-hidden />
        {t(($) => $.lab_output_panel.mythos_check_now)}
      </button>
    </div>
  );
}

function MythosPanel({
  wsId,
  issueId,
  labMode,
  labViewHref,
}: {
  wsId: string;
  issueId: string;
  labMode?: "sole" | "enhancer";
  labViewHref?: string;
}) {
  const { t } = useT("experimental");

  const runsQuery = useQuery({
    queryKey: ["lab-output-panel-mythos-runs", wsId, issueId],
    queryFn: async (): Promise<MythosRunSummary[]> => {
      const r = await api.rawRequest(
        `/api/issues/${encodeURIComponent(issueId)}/mythos-runs?workspace_id=${encodeURIComponent(wsId)}`,
      );
      if (r.status === 404) return [];
      if (!r.ok) throw new Error(`mythos-runs ${r.status}`);
      const raw: unknown = await r.json();
      return parseWithFallback<MythosRunSummary[]>(
        raw,
        MythosRunListSchema,
        [],
        { endpoint: "GET /api/issues/:id/mythos-runs" },
      );
    },
    refetchInterval: (query) => {
      const runs = query.state.data;
      const latest = runs?.[0];
      if (!latest) return IDLE_INTERVAL_MS;
      return TERMINAL_MYTHOS_STATUSES.has(latest.status) ? IDLE_INTERVAL_MS : POLL_INTERVAL_MS;
    },
  });

  if (runsQuery.isLoading) {
    return (
      <div className="space-y-2" data-testid="lab-output-panel-loading">
        <Skeleton className="h-3 w-1/2" />
        <Skeleton className="h-3 w-full" />
        <Skeleton className="h-3 w-2/3" />
      </div>
    );
  }

  if (runsQuery.isError) {
    return (
      <div className="flex items-center justify-between gap-2 rounded-md border border-destructive/40 bg-destructive/10 px-2 py-1.5">
        <p className="text-[11px] text-destructive">{t(($) => $.lab_output_panel.error)}</p>
        <button
          type="button"
          onClick={() => runsQuery.refetch()}
          className="shrink-0 rounded-md border border-input bg-background px-2 py-0.5 text-[11px] font-medium text-foreground hover:bg-muted"
        >
          {t(($) => $.lab_output_panel.retry)}
        </button>
      </div>
    );
  }

  const runs = runsQuery.data ?? [];
  const latest = runs[0] ?? null;

  if (!latest) {
    // 0.5.60 (audit hole #6): a lab-bound issue whose lab never executed
    // must say so — the bare "empty" string read as "no data / broken".
    // Mythos runs start ONLY from the sidebar lab form (issue binding
    // enqueues nothing), so point the user there.
    return (
      <p className="text-xs text-muted-foreground">
        {t(($) => $.lab_output_panel.mythos_never_started)}
      </p>
    );
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-2">
        <p className="text-[11px] text-muted-foreground">
          {t(($) => $.lab_output_panel.run_count, { runs: String(runs.length) })}
        </p>
        <span className="flex shrink-0 items-center gap-1.5 font-mono text-[10px] text-muted-foreground">
          <span className="rounded bg-muted px-1 py-0.5">{latest.mode}</span>
          <span className={latest.status === "completed" ? "text-emerald-600 dark:text-emerald-400" : ""}>
            {latest.status}
          </span>
          {labViewHref && (
            <AppLink
              href={labViewHref}
              className="text-[10px] text-muted-foreground hover:text-foreground"
              aria-label={t(($) => $.lab_output_panel.view_in_lab)}
            >
              {t(($) => $.lab_output_panel.view_in_lab)} →
            </AppLink>
          )}
        </span>
      </div>

      <div className="space-y-1">
        <p className="text-[11px] font-medium text-muted-foreground">
          {t(($) => $.lab_output_panel.mythos_problem_label)}
        </p>
        {latest.problem ? (
          <p className="whitespace-pre-wrap text-xs leading-relaxed text-foreground">
            {latest.problem}
          </p>
        ) : (
          <p className="text-xs text-muted-foreground">
            {t(($) => $.lab_output_panel.mythos_no_problem)}
          </p>
        )}
      </div>

      <div className="space-y-1.5">
        <p className="text-[11px] font-medium text-muted-foreground">
          {t(($) => $.lab_output_panel.mythos_conclusions_label)}
        </p>
        {latest.coda_conclusions.length > 0 ? (
          <MythosConclusions
            conclusions={latest.coda_conclusions}
            labViewHref={labViewHref}
          />
        ) : (
          <p className="text-xs text-muted-foreground">
            {t(($) => $.lab_output_panel.mythos_no_conclusions)}
          </p>
        )}
      </div>

      {labMode === "enhancer" && <MythosSuperviseSection runID={latest.run_id} />}
    </div>
  );
}

// ── Pythia Oracle (M3) ────────────────────────────────────────────────────

// Each persisted run's `envelopes` array holds the deliberation frames in
// order; the envelope element carries no `round` field (see the wire shape in
// forecast_issue.go / claude_lab_forecast.go), so the display round is the
// array index + 1.
function PythiaFrames({
  envelopes,
  labViewHref,
}: {
  envelopes: PythiaForecastEnvelope[];
  labViewHref?: string;
}) {
  const { t } = useT("experimental");

  if (envelopes.length === 0) {
    return <p className="text-xs text-muted-foreground">{t(($) => $.lab_output_panel.empty)}</p>;
  }

  return (
    <div className="space-y-1.5">
      <p className="text-[11px] font-medium text-muted-foreground">
        {t(($) => $.lab_output_panel.pythia_frames_label)}
      </p>
      {envelopes.map((e, i) => (
        <div key={e.id || i} className="space-y-0.5">
          <div className="flex items-baseline justify-between gap-2 text-[11px]">
            <span className="truncate text-foreground/90">
              R{i + 1} · {e.scenario}
            </span>
            <span className="shrink-0 font-medium text-foreground">
              {Math.round(clamp01(e.probability) * 100)}%
            </span>
          </div>
          <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
            <div
              className="h-full rounded-full bg-purple-500"
              style={{ width: `${Math.round(clamp01(e.probability) * 100)}%` }}
            />
          </div>
          {e.narrative && (
            <p className="line-clamp-2 whitespace-pre-wrap text-[10px] leading-relaxed text-foreground/70">
              {e.narrative}
            </p>
          )}
          <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[10px] text-muted-foreground">
            <span className="font-mono">{e.horizon}</span>
            <span className="font-mono">{e.persona}</span>
            <span>
              {t(($) => $.lab_output_panel.pythia_confidence_label)}{" "}
              {Math.round(clamp01(e.confidence) * 100)}%
            </span>
            {e.synthetic_oracle_failover === true && (
              <span className="rounded bg-amber-500/10 px-1 py-0.5 text-amber-600 dark:text-amber-400">
                {t(($) => $.lab_output_panel.pythia_synthetic_hint)}
              </span>
            )}
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

function PythiaPanel({
  wsId,
  issueId,
  labViewHref,
}: {
  wsId: string;
  issueId: string;
  labViewHref?: string;
}) {
  const { t } = useT("experimental");

  // 0.5.59: track when the user last triggered a forecast so the panel
  // can show "推演进行中..." for the SSE-loop window instead of an empty
  // placeholder that reads as "no data / broken". Without this, an
  // oracle still warming up looks identical to "oracle never ran"
  // and the user has no signal that work is happening.
  //
  // Persisted in sessionStorage so a navigate-away-and-back cycle
  // doesn't reset the timer. The sessionStorage write is fire-and-
  // forget — when the mutation resolves we re-read the timestamp.
  // 0.5.86: the read/write/state helpers moved to lab-run-heuristics.ts
  // so the issue-side LabProgressCard derives the SAME signal.
  const [triggeredAt, setTriggeredAt] = useState<number | null>(() =>
    readPythiaTriggeredAt(wsId, issueId),
  );

  const triggerForecast = useMutation({
    mutationFn: async (rounds: number) => {
      const r = await api.rawRequest(
        "/api/experimental/pythia-oracle/forecast/issue",
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ issue_id: issueId, rounds }),
        },
      );
      if (!r.ok && r.status !== 200) {
        throw new Error(`forecast trigger ${r.status}`);
      }
      return rounds;
    },
    onMutate: (rounds) => {
      const now = Date.now();
      markPythiaTriggered(wsId, issueId, now);
      setTriggeredAt(now);
      return { rounds };
    },
    onSettled: () => {
      runsQuery.refetch();
    },
  });

  const runsQuery = useQuery({
    queryKey: ["lab-output-panel-pythia-runs", wsId, issueId],
    queryFn: async (): Promise<PythiaForecastRun[]> => {
      const r = await api.rawRequest(
        `/api/experimental/pythia-oracle/forecast/issue/runs?issue_id=${encodeURIComponent(issueId)}&limit=1`,
      );
      if (r.status === 404) return [];
      if (!r.ok) throw new Error(`pythia forecast runs ${r.status}`);
      const raw: unknown = await r.json();
      return parseWithFallback<PythiaForecastRun[]>(
        raw,
        PythiaForecastRunListSchema,
        [],
        { endpoint: "GET /api/experimental/pythia-oracle/forecast/issue/runs" },
      );
    },
    refetchInterval: (query) => {
      const runs = query.state.data;
      return runs && runs.length > 0 ? IDLE_INTERVAL_MS : POLL_INTERVAL_MS;
    },
  });

  // 0.5.59: derive "in progress" state from the trigger timestamp.
  // The Python oracle takes ~45s for 10 rounds; we give it a 90s
  // grace before flipping to "无响应". Once rows arrive, the
  // timestamp is ignored (we have data).
  const now = Date.now();
  const hasRuns = (runsQuery.data?.length ?? 0) > 0;
  const triggerState = derivePythiaTriggerState(triggeredAt, hasRuns, now);
  const isInProgress = triggerState === "in_progress";
  const isStuck = triggerState === "stuck";

  if (runsQuery.isLoading) {
    return (
      <div className="space-y-2" data-testid="lab-output-panel-loading">
        <Skeleton className="h-3 w-1/2" />
        <Skeleton className="h-3 w-full" />
        <Skeleton className="h-3 w-2/3" />
      </div>
    );
  }

  if (runsQuery.isError) {
    return (
      <div className="flex items-center justify-between gap-2 rounded-md border border-destructive/40 bg-destructive/10 px-2 py-1.5">
        <p className="text-[11px] text-destructive">{t(($) => $.lab_output_panel.error)}</p>
        <button
          type="button"
          onClick={() => runsQuery.refetch()}
          className="shrink-0 rounded-md border border-input bg-background px-2 py-0.5 text-[11px] font-medium text-foreground hover:bg-muted"
        >
          {t(($) => $.lab_output_panel.retry)}
        </button>
      </div>
    );
  }

  // 0.5.59: in-progress UI — spinner + the round budget is shown so
  // the user sees work happening. The retry button is intentionally
  // NOT shown here (oracle is still running; clicking it would
  // stack a second 10-round run on top of the first).
  if (isInProgress) {
    const elapsed = Math.floor((now - (triggeredAt ?? now)) / 1000);
    const budgetSec = Math.floor(PYTHIA_IN_PROGRESS_WINDOW_MS / 1000);
    return (
      <div
        className="space-y-1.5 rounded-md border border-purple-500/40 bg-purple-500/5 px-2 py-1.5"
        data-testid="lab-output-panel-pythia-in-progress"
      >
        <div className="flex items-center gap-1.5 text-[11px] font-medium text-purple-700 dark:text-purple-300">
          <Loader2 className="size-3 animate-spin" aria-hidden />
          <span>Pythia 推演中 · {elapsed}s / {budgetSec}s</span>
        </div>
        <p className="text-[10px] leading-snug text-muted-foreground">
          {triggerForecast.isPending
            ? "正在请求 10 轮 SSE 流…"
            : "10 轮 oracle 调用,每轮 ~3-5s + 5s 间隔"}
        </p>
      </div>
    );
  }

  // 0.5.59: stuck UI — triggered but no rows after 90s. Surface a
  // concrete retry path so the user never sits on a silent empty
  // panel again (the pre-0.5.59 failure mode).
  if (isStuck) {
    return (
      <div
        className="space-y-1.5 rounded-md border border-amber-500/40 bg-amber-500/5 px-2 py-1.5"
        data-testid="lab-output-panel-pythia-stuck"
      >
        <p className="text-[11px] font-medium text-amber-700 dark:text-amber-300">
          推演未在 90 秒内返回结果
        </p>
        <p className="text-[10px] leading-snug text-muted-foreground">
          可能原因:Python 引擎未启动 / oracle 进程崩溃 / 网络中断。点重试可手动重新触发 3 轮。
        </p>
        <button
          type="button"
          disabled={triggerForecast.isPending}
          onClick={() => triggerForecast.mutate(3)}
          className="inline-flex shrink-0 items-center gap-1 rounded-md border border-amber-500/40 bg-background px-2 py-1 text-[11px] font-medium text-foreground hover:bg-amber-500/10 disabled:opacity-50"
        >
          <RefreshCw className="size-3" aria-hidden />
          {triggerForecast.isPending ? "请求中…" : "重新推演 (3 轮)"}
        </button>
        {triggerForecast.isError && (
          <p className="text-[10px] text-destructive">
            {triggerForecast.error instanceof Error ? triggerForecast.error.message : "重试失败"}
          </p>
        )}
      </div>
    );
  }

  const runs = runsQuery.data ?? [];
  const latest = runs[0] ?? null;

  if (!latest) {
    return (
      <div className="space-y-1.5" data-testid="lab-output-panel-pythia-empty">
        <p className="text-xs text-muted-foreground">
          {t(($) => $.lab_output_panel.empty)}
        </p>
        <button
          type="button"
          disabled={triggerForecast.isPending}
          onClick={() => triggerForecast.mutate(3)}
          className="inline-flex shrink-0 items-center gap-1 rounded-md border border-purple-500/40 bg-purple-500/5 px-2 py-1 text-[11px] font-medium text-purple-700 hover:bg-purple-500/10 disabled:opacity-50 dark:text-purple-300"
        >
          <RefreshCw className="size-3" aria-hidden />
          {triggerForecast.isPending ? "请求中…" : "启动 Pythia 推演 (3 轮)"}
        </button>
      </div>
    );
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-2">
        <p className="text-[11px] text-muted-foreground">
          {t(($) => $.lab_output_panel.run_count, { runs: String(latest.rounds) })}
        </p>
        <span className="flex shrink-0 items-center gap-1.5 font-mono text-[10px] text-muted-foreground">
          <span className="rounded bg-muted px-1 py-0.5">{latest.source}</span>
        </span>
      </div>

      <PythiaFrames envelopes={latest.envelopes} labViewHref={labViewHref} />
    </div>
  );
}

// ── TimesFM (0.5.82 WL2) ─────────────────────────────────────────────────

// Compact records reader for the timesfm forecasting lab. ICP-1:
// records-only — runs fire from issues via the timesfm_oracle agent, so
// unlike PythiaPanel there is NO manual trigger here; the panel lists what
// already landed in timesfm_forecast_run and deep-links into the full
// /experimental/timesfm-lab records view. The read goes through the shared
// core hook (5s poll, 404→[] flag-off degradation, zod fallback).
function TimesfmPanel({ issueId }: { issueId: string }) {
  const { t } = useT("experimental");
  const runsQuery = useTimesfmForecastRuns(issueId);

  if (runsQuery.isLoading) {
    return (
      <div className="space-y-2" data-testid="lab-output-panel-loading">
        <Skeleton className="h-3 w-1/2" />
        <Skeleton className="h-3 w-full" />
      </div>
    );
  }

  if (runsQuery.isError) {
    return (
      <div className="flex items-center justify-between gap-2 rounded-md border border-destructive/40 bg-destructive/10 px-2 py-1.5">
        <p className="text-[11px] text-destructive">{t(($) => $.lab_output_panel.error)}</p>
        <button
          type="button"
          onClick={() => runsQuery.refetch()}
          className="shrink-0 rounded-md border border-input bg-background px-2 py-0.5 text-[11px] font-medium text-foreground hover:bg-muted"
        >
          {t(($) => $.lab_output_panel.retry)}
        </button>
      </div>
    );
  }

  const runs = runsQuery.data ?? [];
  if (runs.length === 0) {
    return (
      <div className="space-y-1.5" data-testid="lab-output-panel-timesfm-empty">
        <p className="text-xs text-muted-foreground">
          {t(($) => $.lab_output_panel.empty)}
        </p>
        <p className="text-[10px] leading-snug text-muted-foreground/80">
          {t(($) => $.lab_output_panel.timesfm_empty_hint)}
        </p>
      </div>
    );
  }

  const latest = runs[0];
  if (!latest) return null;
  // Engine answered without weights (or fell back outright) — surface the
  // provenance drop so "a number" never reads as a real model forecast.
  const engineFallback =
    latest.result?.model_present === false || latest.provenance === "seasonal_naive";

  return (
    <div className="space-y-1.5" data-testid="lab-output-panel-timesfm">
      <div className="flex items-center justify-between gap-2">
        <p className="text-[11px] font-medium text-foreground/90">
          {t(($) => $.lab_output_panel.timesfm_title)}
        </p>
        <span className="shrink-0 rounded bg-muted px-1 py-0.5 font-mono text-[10px] text-muted-foreground">
          {latest.provenance}
        </span>
      </div>
      <p className="text-[11px] text-muted-foreground">
        {t(($) => $.lab_output_panel.run_count, { runs: String(runs.length) })}
      </p>
      {engineFallback && (
        <p className="rounded-md border border-amber-500/40 bg-amber-500/5 px-2 py-1 text-[10px] leading-snug text-amber-700 dark:text-amber-300">
          {t(($) => $.lab_output_panel.timesfm_fallback_hint)}
        </p>
      )}
      {/* ICP-3: jump into the records view pre-scoped to the newest run —
          the timesfm-lab run list is a wired ?run= receiver. */}
      <LabRunLink flagKey="timesfm" issueId={issueId} runId={latest.id} />
    </div>
  );
}

// ── Code Canvas (M4) ──────────────────────────────────────────────────────

// Duplicated from apps/desktop/src/renderer/src/pages/code-canvas-view.tsx —
// views cannot import from apps/desktop, and the panel needs the same
// language list + starter snippet to offer an issue-scoped render input.
const LANGUAGES = [
  "python",
  "javascript",
  "typescript",
  "go",
  "rust",
  "java",
  "cpp",
  "c",
  "ruby",
  "bash",
  "sql",
  "html",
  "css",
  "json",
  "text",
];

const DEFAULT_SNIPPET = `# fibonacci\ndef fib(n: int) -> int:\n    """Return the n-th Fibonacci number."""\n    if n < 2:\n        return n\n    return fib(n - 1) + fib(n - 2)\n\nprint(fib(10))  # 55\n`;

function CodeCanvasPanel({
  wsId,
  issueId,
  labViewHref,
}: {
  wsId: string;
  issueId: string;
  labViewHref?: string;
}) {
  const { t } = useT("experimental");
  const qc = useQueryClient();
  const [code, setCode] = useState(DEFAULT_SNIPPET);
  const [language, setLanguage] = useState("python");

  const historyQuery = useQuery({
    queryKey: ["lab-output-panel-code-canvas-artifacts", wsId, issueId],
    queryFn: async (): Promise<CodeCanvasArtifact[]> => {
      const r = await api.rawRequest(
        `/api/experimental/code-canvas/issues/${encodeURIComponent(issueId)}/artifacts?limit=20`,
      );
      if (r.status === 404) return [];
      if (!r.ok) throw new Error(`code-canvas artifacts ${r.status}`);
      const raw: unknown = await r.json();
      return parseWithFallback<CodeCanvasArtifact[]>(
        raw,
        CodeCanvasArtifactListSchema,
        [],
        { endpoint: "GET /api/experimental/code-canvas/issues/:id/artifacts" },
      );
    },
    // Artifacts only change when the user actively renders, so the idle beat
    // is enough (no live cadence, no WS push).
    refetchInterval: IDLE_INTERVAL_MS,
  });

  const render = useMutation({
    mutationFn: async ({
      code,
      language,
    }: {
      code: string;
      language: string;
    }): Promise<CodeCanvasArtifact> => {
      // Step 1: render through the same-origin proxy (reuses the
      // code-canvas-view.tsx render path) to obtain the canvas HTML.
      const renderResp = await api.rawRequest("/experimental/code-canvas/render", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ code, language }),
      });
      if (!renderResp.ok) {
        const message = await renderResp.text().catch(() => `HTTP ${renderResp.status}`);
        throw new Error(message || `HTTP ${renderResp.status}`);
      }
      const html = await renderResp.text();

      // Step 2: persist the canvas against this issue.
      const saveResp = await api.rawRequest(
        `/api/experimental/code-canvas/issues/${encodeURIComponent(issueId)}/artifacts`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ code, language, html }),
        },
      );
      if (!saveResp.ok) {
        const message = await saveResp.text().catch(() => `HTTP ${saveResp.status}`);
        throw new Error(message || `HTTP ${saveResp.status}`);
      }
      const raw: unknown = await saveResp.json();
      return parseWithFallback<CodeCanvasArtifact>(
        raw,
        CodeCanvasArtifactSchema,
        { id: "", code, language, html, created_at: "" },
        { endpoint: "POST /api/experimental/code-canvas/issues/:id/artifacts" },
      );
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["lab-output-panel-code-canvas-artifacts", wsId, issueId] });
    },
  });

  const artifacts = historyQuery.data ?? [];

  return (
    <div className="space-y-3">
      <div className="space-y-1.5 rounded-md border border-border bg-card/50 p-2">
        <div className="flex items-center justify-between gap-2">
          <label
            htmlFor="code-canvas-panel-code"
            className="text-[11px] font-medium text-muted-foreground"
          >
            {t(($) => $.lab_output_panel.code_canvas_input_label)}
          </label>
          <div className="flex items-center gap-1.5">
            <label
              htmlFor="code-canvas-panel-language"
              className="text-[10px] text-muted-foreground"
            >
              {t(($) => $.lab_output_panel.code_canvas_language_label)}
            </label>
            <select
              id="code-canvas-panel-language"
              value={language}
              onChange={(e) => setLanguage(e.target.value)}
              className="h-7 rounded-md border border-border bg-background px-1.5 text-[10px] text-foreground"
            >
              {LANGUAGES.map((l) => (
                <option key={l} value={l}>
                  {l}
                </option>
              ))}
            </select>
          </div>
        </div>
        <textarea
          id="code-canvas-panel-code"
          value={code}
          onChange={(e) => setCode(e.target.value)}
          spellCheck={false}
          className="h-40 w-full resize-y rounded-md border border-border bg-background p-2 font-mono text-[11px] leading-relaxed text-foreground outline-none focus:border-primary"
        />
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => render.mutate({ code, language })}
            disabled={render.isPending || code.trim().length === 0}
            className="inline-flex h-8 items-center gap-1.5 rounded-md bg-primary px-3 text-xs font-medium text-primary-foreground disabled:cursor-not-allowed disabled:opacity-50"
          >
            {render.isPending
              ? t(($) => $.lab_output_panel.code_canvas_rendering)
              : t(($) => $.lab_output_panel.code_canvas_render)}
          </button>
          {render.isError && (
            <span className="text-[11px] text-destructive">
              {t(($) => $.lab_output_panel.code_canvas_render_failed, {
                error: render.error instanceof Error ? render.error.message : String(render.error),
              })}
            </span>
          )}
        </div>
      </div>

      <div className="space-y-1.5">
        <p className="text-[11px] font-medium text-muted-foreground">
          {t(($) => $.lab_output_panel.code_canvas_history_label)}
        </p>

        {historyQuery.isLoading ? (
          <div className="space-y-2" data-testid="lab-output-panel-loading">
            <Skeleton className="h-3 w-1/2" />
            <Skeleton className="h-24 w-full" />
          </div>
        ) : historyQuery.isError ? (
          <div className="flex items-center justify-between gap-2 rounded-md border border-destructive/40 bg-destructive/10 px-2 py-1.5">
            <p className="text-[11px] text-destructive">{t(($) => $.lab_output_panel.error)}</p>
            <button
              type="button"
              onClick={() => historyQuery.refetch()}
              className="shrink-0 rounded-md border border-input bg-background px-2 py-0.5 text-[11px] font-medium text-foreground hover:bg-muted"
            >
              {t(($) => $.lab_output_panel.retry)}
            </button>
          </div>
        ) : artifacts.length === 0 ? (
          <p className="text-xs text-muted-foreground">
            {t(($) => $.lab_output_panel.code_canvas_no_artifacts)}
          </p>
        ) : (
          <div className="space-y-2">
            {artifacts.map((a) => (
              <div key={a.id} className="overflow-hidden rounded-lg border border-border">
                <div className="flex items-center gap-2 border-b border-border bg-muted/50 px-2 py-1">
                  <span className="rounded bg-muted px-1 py-0.5 font-mono text-[10px] text-muted-foreground">
                    {a.language}
                  </span>
                  <span className="truncate font-mono text-[10px] text-muted-foreground">
                    {a.created_at}
                  </span>
                </div>
                <iframe
                  srcDoc={a.html}
                  sandbox=""
                  title={`${a.language} · ${a.created_at}`}
                  className="h-48 w-full bg-background"
                />
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
      </div>
    </div>
  );
}

export function LabOutputPanel({ wsId, issueId, labSource, labMode }: LabOutputPanelProps) {
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
    return <PythiaPanel wsId={wsId} issueId={issueId} labViewHref={labViewHref} />;
  }

  if (labSource === "timesfm") {
    return <TimesfmPanel issueId={issueId} />;
  }

  if (labSource === "mythos_swarm") {
    return (
      <MythosPanel
        wsId={wsId}
        issueId={issueId}
        labMode={labMode}
        labViewHref={labViewHref}
      />
    );
  }

  if (labSource === "code_canvas") {
    return <CodeCanvasPanel wsId={wsId} issueId={issueId} labViewHref={labViewHref} />;
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
