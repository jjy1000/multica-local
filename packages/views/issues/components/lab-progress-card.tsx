"use client";

// LabProgressCard — per-lab "is it working on this issue" progress card
// (0.5.86). Generalizes the swarm-only SwarmRunStatusPill (0.5.22, kept
// untouched below the card) into every run-capable lab bound to an issue:
//
//   pythia_oracle      → GET /runs?limit=1 (shared cache key with
//                        LabOutputPanel's PythiaPanel — zero extra
//                        round-trips when both are mounted) + the 0.5.59
//                        sessionStorage trigger heuristic, read through the
//                        shared lab-run-heuristics.ts helpers so the card and
//                        the panel can never disagree.
//   timesfm            → useTimesfmForecastRuns (same hook/limit as
//                        TimesfmPanel → same cache entry). Rows are written
//                        synchronously AFTER the engine answers, so "running"
//                        is recency-only: a run younger than
//                        TIMESFM_RECENT_RUN_WINDOW_MS reads as fresh
//                        activity. Provenance renders verbatim (0.5.82
//                        honesty law — never stripped).
//   mythos_swarm       → GET /api/issues/{id}/mythos-runs (shared cache key
//                        with MythosPanel). Wire fields: run_id, status
//                        (running|supervising|completed|aborted|failed —
//                        migrations 149/157), mode, started_at, problem,
//                        iterations (= mythos_run.current_loop), completed_at,
//                        final_issue_id, coda_conclusions. There is NO
//                        max_loop_iters field, so only the current loop is
//                        shown.
//   claude_science_lab → the AgentTaskSnapshot-derived `live` flags passed
//                        down from IssueLabsSection (no second snapshot
//                        fetch).
//
//   swarm_topology / code_canvas render null — the existing pill + panel
//   already cover them (the pill is being deprecated elsewhere; leave as-is).
//
// Auxiliary labs (causal_graph, llm_wiki_bridge, semantica) have no run
// lifecycle — they render a single muted row and no click-through-run
// (causal_graph keeps its graph icon/preview elsewhere; not duplicated here).

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { CheckCircle2, CircleDashed, ExternalLink, Loader2, TriangleAlert } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { UI_EASE_OUT, UI_MOTION_DURATION } from "@multica/ui/lib/motion";
import { api, parseWithFallback } from "@multica/core/api";
import { useTimesfmForecastRuns } from "@multica/core/experimental";
import {
  MythosRunListSchema,
  PythiaForecastRunListSchema,
} from "@multica/core/api/schemas";
import type {
  MythosRunSummary,
  PythiaForecastRun,
  TimesfmForecastRun,
} from "@multica/core/types/api";
import { AppLink } from "../../navigation";
import { labRunHref } from "../../experimental/components/lab-run-link";
import {
  derivePythiaTriggerState,
  isTimesfmRunRecent,
  readPythiaTriggeredAt,
} from "../../experimental/components/lab-run-heuristics";
import { useT } from "../../i18n";

/** Labs that collaborate on the issue automatically — no run lifecycle. */
const AUXILIARY_LAB_SOURCES = new Set([
  "causal_graph",
  "llm_wiki_bridge",
  "semantica",
]);

/** Mythos run statuses that will not change again (migrations 149/157). */
const MYTHOS_TERMINAL_STATUSES = new Set(["completed", "aborted", "failed"]);

const POLL_INTERVAL_MS = 5_000;
const IDLE_INTERVAL_MS = 60_000;

export interface LabProgressCardProps {
  issueId: string;
  workspaceId: string;
  labSource: string;
  /** Experimental flag enabled — gates the click-through, never the state. */
  flagEnabled: boolean;
  /** AgentTaskSnapshot-derived live flags (IssueLabsSection already computes
   *  these); consumed by the claude_science_lab variant. */
  live?: {
    running: boolean;
    queued: boolean;
    failed: boolean;
    cancelled: boolean;
  };
}

type CardState =
  | { kind: "idle"; summary?: string }
  | { kind: "running"; summary?: string }
  | { kind: "done"; summary?: string; runId?: string | null }
  | { kind: "failed"; summary?: string; runId?: string | null }
  | { kind: "engine_down"; summary?: string; runId?: string | null };

function truncateOneLine(s: string, limit = 120): string {
  const trimmed = s.trim();
  if (trimmed.length <= limit) return trimmed;
  return trimmed.slice(0, limit - 1).trimEnd() + "…";
}

function formatRunTime(createdAt: string | null | undefined): string | null {
  if (!createdAt) return null;
  const t = Date.parse(createdAt);
  if (!Number.isFinite(t)) return null;
  try {
    return new Date(t).toLocaleString();
  } catch {
    return null;
  }
}

/**
 * Muted row for auxiliary labs — no run data, no click-through-run.
 */
function AuxiliaryRow() {
  const { t } = useT("issues");
  return (
    <div
      data-testid="lab-progress-card-auxiliary"
      className="rounded-md border border-dashed border-border/60 px-2 py-1.5 text-[11px] text-muted-foreground"
    >
      {t(($) => $.lab_section.progress_auxiliary)}
    </div>
  );
}

/**
 * 3-line loading placeholder (same idiom as LabOutputPanel's loading
 * branch) sized down to the card's compact sidebar-row footprint.
 * Keeps the card's box mounted while the first poll resolves so the
 * section layout no longer shifts when real state replaces it.
 */
export function LabProgressCardSkeleton() {
  return (
    <div
      data-testid="lab-progress-card-loading"
      className="space-y-1.5 rounded-md border border-border/60 bg-card/50 px-2 py-1.5"
      aria-hidden
    >
      <Skeleton className="h-2.5 w-1/3" />
      <Skeleton className="h-2 w-full" />
      <Skeleton className="h-2 w-2/3" />
    </div>
  );
}

/**
 * Shared shell: status dot + label (+ summary line) wrapped in the
 * issue/run-scoped lab-view link. Mirrors the compact pill styling of
 * SwarmRunStatusPill / the section's indicator chips.
 */
function ProgressCardBody({
  testId,
  labSource,
  issueId,
  runId,
  state,
  flagEnabled,
}: {
  testId: string;
  labSource: string;
  issueId: string;
  runId?: string | null;
  state: CardState;
  flagEnabled: boolean;
}) {
  const { t } = useT("issues");
  const reduceMotion = useReducedMotion() ?? false;

  const label =
    state.kind === "running"
      ? t(($) => $.lab_section.progress_running)
      : state.kind === "done"
        ? t(($) => $.lab_section.progress_done)
        : state.kind === "failed"
          ? t(($) => $.lab_section.progress_failed)
          : state.kind === "engine_down"
            ? t(($) => $.lab_section.progress_engine_down)
            : t(($) => $.lab_section.progress_idle);

  // Deep-link: run-scoped when a run id exists (all wired ?run= receivers
  // listed in lab-run-link.ts), otherwise issue-scoped. Suppressed when the
  // flag is off — the section's no-flag hint below covers that case.
  const href = flagEnabled ? labRunHref(labSource, issueId, runId) : undefined;

  const toneClass =
    state.kind === "failed"
      ? "text-red-700 dark:text-red-300"
      : state.kind === "engine_down"
        ? "text-amber-700 dark:text-amber-300"
        : state.kind === "idle"
          ? "text-muted-foreground"
          : "text-emerald-700 dark:text-emerald-300";

  // Dot + label + summary, keyed on the run status so an idle→running→
  // terminal transition crossfades (fade-through) instead of popping.
  // Summary churn within the SAME status (poll refreshes) keeps the key —
  // no re-animation. Reduced motion swaps instantly.
  const statusContent = (
    <>
      <span className={`inline-flex shrink-0 items-center gap-1 font-medium ${toneClass}`}>
        {state.kind === "running" ? (
          <Loader2 className="size-3 animate-spin" aria-hidden />
        ) : state.kind === "done" ? (
          <CheckCircle2 className="size-3" aria-hidden />
        ) : state.kind === "failed" || state.kind === "engine_down" ? (
          <TriangleAlert className="size-3" aria-hidden />
        ) : (
          <CircleDashed className="size-3" aria-hidden />
        )}
        {label}
      </span>
      {state.summary ? (
        <span className="truncate text-muted-foreground">{state.summary}</span>
      ) : null}
    </>
  );

  const statusRow = (
    <>
      {reduceMotion ? (
        <div key={state.kind} className="flex min-w-0 flex-1 items-center gap-1.5">
          {statusContent}
        </div>
      ) : (
        <AnimatePresence initial={false} mode="wait">
          <motion.div
            key={state.kind}
            className="flex min-w-0 flex-1 items-center gap-1.5"
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
            {statusContent}
          </motion.div>
        </AnimatePresence>
      )}
      {href ? (
        <ExternalLink
          className="ml-auto size-3 shrink-0 text-muted-foreground"
          aria-hidden
        />
      ) : null}
    </>
  );

  return (
    <div
      data-testid={testId}
      data-lab-source={labSource}
      data-state={state.kind}
      className="rounded-md border border-border/60 bg-card/50 px-2 py-1.5 text-[11px]"
    >
      {href ? (
        <AppLink
          href={href}
          className="flex items-center gap-1.5 rounded transition-colors hover:bg-accent/50"
          aria-label={t(($) => $.lab_section.progress_view)}
        >
          {statusRow}
        </AppLink>
      ) : (
        <div className="flex items-center gap-1.5">{statusRow}</div>
      )}
      {state.kind === "running" && (
        <p className="mt-0.5 text-[10px] leading-snug text-muted-foreground">
          {t(($) => $.lab_section.progress_running_hint)}
        </p>
      )}
      {state.kind === "idle" && (
        <p className="mt-0.5 text-[10px] leading-snug text-muted-foreground">
          {t(($) => $.lab_section.progress_idle_hint)}
        </p>
      )}
      {state.kind === "engine_down" && (
        <p className="mt-0.5 text-[10px] leading-snug text-muted-foreground">
          {t(($) => $.lab_section.progress_engine_down_hint)}
        </p>
      )}
    </div>
  );
}

// ── Pythia Oracle ─────────────────────────────────────────────────────────

function PythiaProgressCard({
  wsId,
  issueId,
  flagEnabled,
}: {
  wsId: string;
  issueId: string;
  flagEnabled: boolean;
}) {
  const { t } = useT("issues");

  // Trigger heuristic — SAME sessionStorage signal as LabOutputPanel's
  // PythiaPanel (shared helpers; see lab-run-heuristics.ts header).
  const [triggeredAt] = useState<number | null>(() =>
    readPythiaTriggeredAt(wsId, issueId),
  );

  // Shares the panel's exact cache key so the network round-trip is
  // amortised (same pattern as LabLastResultChip). The runs endpoint is a
  // pure DB read that keeps working when the engine is down (ICP-2).
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

  if (runsQuery.isLoading) return <LabProgressCardSkeleton />;
  if (runsQuery.isError) {
    return (
      <ProgressCardBody
        testId="lab-progress-card"
        labSource="pythia_oracle"
        issueId={issueId}
        state={{ kind: "failed" }}
        flagEnabled={flagEnabled}
      />
    );
  }

  const runs = runsQuery.data ?? [];
  const latest = runs[0] ?? null;
  const runTime = formatRunTime(latest?.created_at);

  if (latest) {
    return (
      <ProgressCardBody
        testId="lab-progress-card"
        labSource="pythia_oracle"
        issueId={issueId}
        runId={latest.id}
        flagEnabled={flagEnabled}
        state={{
          kind: "done",
          summary: `${t(($) => $.lab_section.progress_pythia_rounds, { rounds: String(latest.rounds) })} · ${latest.source || "—"}${runTime ? ` · ${runTime}` : ""}`,
        }}
      />
    );
  }

  const triggerState = derivePythiaTriggerState(triggeredAt, false);
  if (triggerState === "in_progress") {
    return (
      <ProgressCardBody
        testId="lab-progress-card"
        labSource="pythia_oracle"
        issueId={issueId}
        flagEnabled={flagEnabled}
        state={{ kind: "running" }}
      />
    );
  }
  if (triggerState === "stuck") {
    return (
      <ProgressCardBody
        testId="lab-progress-card"
        labSource="pythia_oracle"
        issueId={issueId}
        flagEnabled={flagEnabled}
        state={{ kind: "engine_down" }}
      />
    );
  }
  return (
    <ProgressCardBody
      testId="lab-progress-card"
      labSource="pythia_oracle"
      issueId={issueId}
      flagEnabled={flagEnabled}
      state={{ kind: "idle" }}
    />
  );
}

// ── TimesFM ───────────────────────────────────────────────────────────────

function TimesfmProgressCard({
  issueId,
  flagEnabled,
}: {
  issueId: string;
  flagEnabled: boolean;
}) {
  const { t } = useT("issues");

  // Same hook + limit as TimesfmPanel → same cache entry, no extra poll.
  const runsQuery = useTimesfmForecastRuns(issueId);

  if (runsQuery.isLoading) return <LabProgressCardSkeleton />;
  if (runsQuery.isError) {
    return (
      <ProgressCardBody
        testId="lab-progress-card"
        labSource="timesfm"
        issueId={issueId}
        state={{ kind: "engine_down" }}
        flagEnabled={flagEnabled}
      />
    );
  }

  const runs = runsQuery.data ?? [];
  const latest: TimesfmForecastRun | null = runs[0] ?? null;
  if (!latest) {
    return (
      <ProgressCardBody
        testId="lab-progress-card"
        labSource="timesfm"
        issueId={issueId}
        state={{ kind: "idle" }}
        flagEnabled={flagEnabled}
      />
    );
  }

  const runTime = formatRunTime(latest.created_at);
  const summary = `H${latest.horizons} · ${latest.provenance || "—"}${runTime ? ` · ${runTime}` : ""}`;
  // Rows persist only AFTER the engine answers, so a persisted row is a
  // completed run. Recency (<2 min) reads as fresh activity — rendered with
  // the running treatment so "just used it" is glanceable.
  const fresh = isTimesfmRunRecent(latest.created_at);

  return (
    <ProgressCardBody
      testId="lab-progress-card"
      labSource="timesfm"
      issueId={issueId}
      runId={latest.id}
      flagEnabled={flagEnabled}
      state={
        fresh
          ? {
              kind: "running",
              summary: `${summary} · ${t(($) => $.lab_section.progress_done)}`,
            }
          : { kind: "done", summary }
      }
    />
  );
}

// ── Mythos Swarm ──────────────────────────────────────────────────────────

function MythosProgressCard({
  wsId,
  issueId,
  flagEnabled,
}: {
  wsId: string;
  issueId: string;
  flagEnabled: boolean;
}) {
  const { t } = useT("issues");

  // Shares MythosPanel's cache key (same pattern as the pythia card).
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
      const latestRun = runs?.[0];
      if (!latestRun) return IDLE_INTERVAL_MS;
      return MYTHOS_TERMINAL_STATUSES.has(latestRun.status)
        ? IDLE_INTERVAL_MS
        : POLL_INTERVAL_MS;
    },
  });

  if (runsQuery.isLoading) return <LabProgressCardSkeleton />;
  if (runsQuery.isError) {
    return (
      <ProgressCardBody
        testId="lab-progress-card"
        labSource="mythos_swarm"
        issueId={issueId}
        state={{ kind: "failed" }}
        flagEnabled={flagEnabled}
      />
    );
  }

  const runs = runsQuery.data ?? [];
  const latest = runs[0] ?? null;
  if (!latest) {
    return (
      <ProgressCardBody
        testId="lab-progress-card"
        labSource="mythos_swarm"
        issueId={issueId}
        state={{ kind: "idle" }}
        flagEnabled={flagEnabled}
      />
    );
  }

  // `iterations` is the run's current loop (mythos_run.current_loop via the
  // handler); there is no max_loop_iters field on the wire.
  const loopSummary =
    latest.iterations > 0
      ? t(($) => $.lab_section.progress_mythos_loop, {
          loop: String(latest.iterations),
        })
      : undefined;
  const problemSummary = latest.problem
    ? truncateOneLine(latest.problem)
    : undefined;
  const runTime = formatRunTime(latest.completed_at ?? latest.started_at);
  const summaryTail = runTime ? ` · ${runTime}` : "";

  if (MYTHOS_TERMINAL_STATUSES.has(latest.status)) {
    return (
      <ProgressCardBody
        testId="lab-progress-card"
        labSource="mythos_swarm"
        issueId={issueId}
        runId={latest.run_id}
        flagEnabled={flagEnabled}
        state={{
          kind:
            latest.status === "completed"
              ? "done"
              : ("failed" as const),
          summary: [problemSummary, loopSummary]
            .filter(Boolean)
            .join(" · ") + summaryTail,
        }}
      />
    );
  }

  // running | supervising — live collaboration.
  return (
    <ProgressCardBody
      testId="lab-progress-card"
      labSource="mythos_swarm"
      issueId={issueId}
      runId={latest.run_id}
      flagEnabled={flagEnabled}
      state={{
        kind: "running",
        summary: [latest.mode, loopSummary].filter(Boolean).join(" · "),
      }}
    />
  );
}

// ── Claude Science Lab (snapshot-driven) ──────────────────────────────────

function ClaudeProgressCard({
  issueId,
  flagEnabled,
  live,
}: {
  issueId: string;
  flagEnabled: boolean;
  live?: LabProgressCardProps["live"];
}) {
  const state: CardState = live?.running
    ? { kind: "running" }
    : live?.queued
      ? { kind: "running" }
      : live?.failed
        ? { kind: "failed" }
        : { kind: "idle" };
  return (
    <ProgressCardBody
      testId="lab-progress-card"
      labSource="claude_science_lab"
      issueId={issueId}
      state={state}
      flagEnabled={flagEnabled}
    />
  );
}

/**
 * LabProgressCard — see file header. Returns null for labs already covered
 * by their own status surface (swarm_topology pill, code_canvas panel) and
 * for unknown / user-plugin sources.
 */
export function LabProgressCard({
  issueId,
  workspaceId,
  labSource,
  flagEnabled,
  live,
}: LabProgressCardProps) {
  if (AUXILIARY_LAB_SOURCES.has(labSource)) {
    return <AuxiliaryRow />;
  }
  if (labSource === "pythia_oracle") {
    return (
      <PythiaProgressCard
        wsId={workspaceId}
        issueId={issueId}
        flagEnabled={flagEnabled}
      />
    );
  }
  if (labSource === "timesfm") {
    return (
      <TimesfmProgressCard issueId={issueId} flagEnabled={flagEnabled} />
    );
  }
  if (labSource === "mythos_swarm") {
    return (
      <MythosProgressCard
        wsId={workspaceId}
        issueId={issueId}
        flagEnabled={flagEnabled}
      />
    );
  }
  if (labSource === "claude_science_lab") {
    return (
      <ClaudeProgressCard
        issueId={issueId}
        flagEnabled={flagEnabled}
        live={live}
      />
    );
  }
  // swarm_topology (pill below), code_canvas (panel below), user_*, unknown.
  return null;
}
