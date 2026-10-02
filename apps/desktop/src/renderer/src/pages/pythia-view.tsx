import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ArrowRight, Loader2 } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { UI_EASE_OUT, UI_MOTION_DURATION } from "@multica/ui/lib/motion";
import { useExperimentalFlag } from "@multica/core/experimental";
import { api, parseWithFallback } from "@multica/core/api";
import {
  PythiaForecastRunListSchema,
  PythiaMonitorRunListSchema,
} from "@multica/core/api/schemas";
import type { PythiaForecastRun, PythiaMonitorRun } from "@multica/core/types/api";
import { getCurrentSlug, getCurrentWsId } from "@multica/core/platform";
import { paths } from "@multica/core/paths";
import { useNavigation } from "@multica/views/navigation";
import { useT } from "@multica/views/i18n";
import { PythiaCouncilCanvas } from "@multica/views/experimental/components";

// PythiaView (0.3.18+, monitor rewrite 0.5.112)
//
// The /experimental/pythia surface is a PASSIVE monitor now. The
// interactive forecast panel lives on the ISSUE property panel
// (packages/views/experimental/components/pythia/); this page lists the
// workspace's deduction runs across every issue with live status and jumps
// INTO the issue on click — it hosts no interactive forecast UI and no
// world-brief/globe display. Osiris intelligence stays backend-side: the
// engine grounds every forecast round on a freshly refreshed world
// snapshot (vendor engine forecast_issue, PYTHIA_WORLD_TTL).
//
//   flag = off  → honest placeholder, no engine boot, no imports beyond
//                 this file. Hard constraint from the Labs framework
//                 (0.3.6 memo): flag-off completely bypass.
//
//   flag = on   → boot the engine (ensureUp, same contract the old report
//                 surface had — the 0.5.103 event-driven bring-up law),
//                 then the service health strip + the run monitor.

type ManagerStatus = string;

interface EngineHealth {
  engine: boolean;
  osiris: boolean;
  oracle: boolean;
}

const POLL_INTERVAL_MS = 5_000;
const IDLE_INTERVAL_MS = 60_000;
const HEALTH_INTERVAL_MS = 30_000;

export function PythiaView({ issueId: _initialIssueId = null }: { issueId?: string | null } = {}) {
  const pythiaOracleEnabled = useExperimentalFlag("pythia_oracle", false);
  const { t } = useT("pythia");
  const reduceMotion = useReducedMotion() ?? false;
  const nav = useNavigation();
  const [url, setUrl] = useState<string | null>(null);
  const [status, setStatus] = useState<ManagerStatus>("idle");
  const [error, setError] = useState<string | null>(null);
  const [health, setHealth] = useState<EngineHealth | null>(null);

  useEffect(() => {
    if (!pythiaOracleEnabled) return;
    let cancelled = false;

    async function bootAndPoll() {
      try {
        await window.experimentalAPI.pythia.ensureUp();
      } catch (err) {
        if (!cancelled) {
          setError(humanizeBootError(err));
        }
        return;
      }
      while (!cancelled) {
        const [nextStatus, nextUrl] = await Promise.all([
          window.experimentalAPI.pythia.getStatus(),
          window.experimentalAPI.pythia.getURL(),
        ]);
        setStatus(nextStatus);
        if (nextUrl) {
          setUrl(nextUrl);
          break;
        }
        if (nextStatus === "error") {
          setError(
            "Pythia manager reported error — verify python3 is on PATH and that resources/pythia/engine/ is staged",
          );
          return;
        }
        await new Promise((r) => setTimeout(r, 1_000));
      }
    }

    void bootAndPoll();
    return () => {
      cancelled = true;
    };
  }, [pythiaOracleEnabled]);

  // Engine health strip (0.5.112; route fixed 0.5.131): the engine's
  // /links endpoint answers {engine, osiris, oracle, model, ...} — the
  // backend intel + oracle services the forecast rounds call. (The strip
  // originally polled /status, a route the engine never defined, so the
  // chips spun "…" forever.) Loopback proxy IPC (allowlisted +
  // rate-limited in pythia-manager.ts), NOT api.rawRequest — see
  // apps/desktop/CLAUDE.md.
  useEffect(() => {
    if (!url) return;
    let cancelled = false;
    const poll = async () => {
      const res = await window.experimentalAPI.pythia.proxy({
        path: "/links",
      });
      if (!cancelled) setHealth(res.ok ? (res.body as EngineHealth) : null);
    };
    void poll();
    const timer = setInterval(poll, HEALTH_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [url]);

  const wsId = getCurrentWsId() ?? "";
  const slug = getCurrentSlug();

  // Workspace-wide run monitor. 5s poll while any run is live, 60s idle
  // (Active Contract #1 cadence). The endpoint sweeps phantom running rows
  // before listing, so a server restart self-heals here too.
  const runsQuery = useQuery({
    queryKey: ["pythia-monitor-runs", wsId],
    queryFn: async (): Promise<PythiaMonitorRun[]> => {
      // rawRequest never sends X-Workspace-ID (only the slug header), and
      // the monitor route carries no workspace middleware — pass the id
      // explicitly (0.5.115; GetClaudeLabContext query-param contract).
      const r = await api.rawRequest(
        `/api/experimental/pythia-oracle/forecast/monitor?limit=30&workspace_id=${encodeURIComponent(wsId)}`,
      );
      // 0.5.113: a 404 is the legitimate "flag off / no runs yet" signal — keep
      // returning [] so the monitor page renders the empty hint instead of an
      // error banner. Every other non-OK response (400 missing workspace, 500
      // etc.) surfaces to the user as `runsQuery.error` instead of being
      // silently swallowed to [] by the parseWithFallback fallback arg.
      if (r.status === 404) return [];
      if (!r.ok) {
        const errText = await r.text().catch(() => "");
        throw new Error(
          `pythia monitor ${r.status}: ${errText.slice(0, 160) || r.statusText}`,
        );
      }
      const raw: unknown = await r.json();
      return parseWithFallback<PythiaMonitorRun[]>(
        raw,
        PythiaMonitorRunListSchema,
        [],
        { endpoint: "GET /api/experimental/pythia-oracle/forecast/monitor" },
      );
    },
    enabled: Boolean(pythiaOracleEnabled && wsId),
    refetchInterval: (query) => {
      const runs = query.state.data ?? [];
      return runs.some((run) => run.status === "running")
        ? POLL_INTERVAL_MS
        : IDLE_INTERVAL_MS;
    },
  });

  const openIssue = (issueId: string) => {
    if (!slug) return;
    nav.push(paths.workspace(slug).issueDetail(issueId));
  };

  // 0.5.134 council hero — the monitor list is envelope-less by design
  // (PythiaMonitorRun), so the chamber pulls the newest run's envelopes
  // through the issue-runs endpoint (same lenient schema as the embed).
  // No runs yet → the canvas seats the standby roster; a failed fetch
  // degrades to the same standby view (the hero is presentational).
  // Hooks law: this useQuery sits BEFORE the early returns below.
  const latestMonitorRun = (runsQuery.data ?? [])[0] ?? null;
  const hasLive = (runsQuery.data ?? []).some((run) => run.status === "running");
  const chamberQuery = useQuery({
    queryKey: ["pythia-monitor-chamber", wsId, latestMonitorRun?.id ?? null],
    enabled: Boolean(pythiaOracleEnabled && wsId),
    queryFn: async (): Promise<PythiaForecastRun | null> => {
      if (!latestMonitorRun) return null;
      const r = await api.rawRequest(
        `/api/experimental/pythia-oracle/forecast/issue/runs?issue_id=${encodeURIComponent(latestMonitorRun.issue_id)}&limit=1`,
      );
      if (r.status === 404) return null;
      if (!r.ok) throw new Error(`pythia chamber runs ${r.status}`);
      const raw: unknown = await r.json();
      const parsed = parseWithFallback<PythiaForecastRun[]>(
        raw,
        PythiaForecastRunListSchema,
        [],
        { endpoint: "GET /api/experimental/pythia-oracle/forecast/issue/runs" },
      );
      return parsed[0] ?? null;
    },
    refetchInterval: hasLive ? POLL_INTERVAL_MS : IDLE_INTERVAL_MS,
  });

  if (error) {
    return (
      <div className="flex h-full w-full flex-col items-center justify-center gap-3 p-6 text-center text-muted-foreground">
        <h2 className="text-lg font-semibold text-foreground">
          {t(($) => $.pythia_unavailable)}
        </h2>
        <p className="max-w-md text-sm">{error}</p>
      </div>
    );
  }

  if (!pythiaOracleEnabled) {
    return (
      <div className="flex h-full w-full items-center justify-center text-sm text-muted-foreground">
        <span>{t(($) => $.starting_pythia)} (status: {status})…</span>
      </div>
    );
  }

  const runs = runsQuery.data ?? [];
  const chamberRun = chamberQuery.data ?? null;

  return (
    <div className="flex h-full w-full flex-col overflow-y-auto">
      <div className="border-b border-border px-6 pb-3 pt-4">
        <h1 className="text-base font-semibold text-foreground">
          {t(($) => $.monitor_title)}
        </h1>
        <p className="mt-0.5 text-xs text-muted-foreground">
          {t(($) => $.monitor_subtitle)}
        </p>
        <div className="mt-2 flex flex-wrap items-center gap-1.5 text-[10px]">
          <HealthChip
            label={t(($) => $.monitor_manager)}
            ok={status === "ready" || status === "running"}
            pending={status === "starting" || status === "idle"}
            okText={t(($) => $.monitor_on)}
            offText={t(($) => $.monitor_off)}
          />
          <HealthChip
            label={t(($) => $.monitor_oracle)}
            ok={health?.oracle === true}
            pending={health == null}
            okText={t(($) => $.monitor_on)}
            offText={t(($) => $.monitor_off)}
          />
          <HealthChip
            label={t(($) => $.monitor_osiris)}
            ok={health?.osiris === true}
            pending={health == null}
            okText={t(($) => $.monitor_on)}
            offText={t(($) => $.monitor_off)}
          />
        </div>
      </div>

      {/* 0.5.134: council chamber hero — the deliberation visual lives on
          this page too (standby roster between runs, live votes while the
          newest run executes). */}
      <div className="px-6 pt-4">
        <PythiaCouncilCanvas
          envelopes={chamberRun?.envelopes ?? []}
          totalRounds={chamberRun?.rounds ?? 0}
          running={chamberRun?.status === "running"}
        />
      </div>

      <div className="flex-1 px-6 py-4">
        {runsQuery.isLoading ? (
          <div className="space-y-2">
            {[0, 1, 2].map((i) => (
              <div key={i} className="h-12 animate-pulse rounded-md bg-muted/50" />
            ))}
          </div>
        ) : runsQuery.isError ? (
          <p className="text-xs text-destructive">
            {String(runsQuery.error ?? "monitor error")}
          </p>
        ) : runs.length === 0 ? (
          <div className="rounded-md border border-dashed border-border/60 px-4 py-6 text-center">
            <p className="text-sm font-medium text-foreground/85">
              {t(($) => $.monitor_empty)}
            </p>
            <p className="mx-auto mt-1 max-w-md text-xs leading-relaxed text-muted-foreground">
              {t(($) => $.monitor_empty_hint)}
            </p>
          </div>
        ) : (
          <div className="space-y-1.5" data-testid="pythia-monitor-list">
            {runs.map((run) => (
              <MonitorRow
                key={run.id}
                run={run}
                reduceMotion={reduceMotion}
                onOpen={() => openIssue(run.issue_id)}
                openLabel={t(($) => $.monitor_open_issue)}
                kindLabel={
                  run.run_kind === "continuation"
                    ? t(($) => $.monitor_kind_continuation)
                    : t(($) => $.monitor_kind_initial)
                }
                roundsLabel={t(($) => $.monitor_rounds, { rounds: String(run.rounds ?? 0) })}
              />
            ))}
          </div>
        )}
        {hasLive && (
          <p className="mt-3 flex items-center gap-1.5 text-[10px] text-muted-foreground">
            {/* 0.5.131: the pythia-pulse-ring keyframes finally have a
                consumer — a live-feed heartbeat dot (previously dead CSS
                since 0.5.112). Reduced-motion gates live in globals.css. */}
            <span className="relative inline-flex size-2" aria-hidden>
              <span className="animate-pythia-pulse-ring absolute inset-0 rounded-full bg-purple-500/50" />
              <span className="relative inline-flex size-2 rounded-full bg-purple-500" />
            </span>
            live · {POLL_INTERVAL_MS / 1000}s
          </p>
        )}
      </div>
    </div>
  );
}

function HealthChip({
  label,
  ok,
  pending,
  okText,
  offText,
}: {
  label: string;
  ok: boolean;
  pending: boolean;
  okText: string;
  offText: string;
}) {
  return (
    <span
      className={`inline-flex items-center gap-1 rounded-full border px-1.5 py-0.5 font-medium ${
        pending
          ? "border-border text-muted-foreground"
          : ok
            ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
            : "border-amber-500/40 bg-amber-500/5 text-amber-700 dark:text-amber-300"
      }`}
    >
      {pending ? (
        <Loader2 className="size-2.5 animate-spin" aria-hidden />
      ) : (
        <span
          className={`size-1.5 rounded-full ${ok ? "bg-emerald-500" : "bg-amber-500"}`}
          aria-hidden
        />
      )}
      {label} · {pending ? "…" : ok ? okText : offText}
    </span>
  );
}

function MonitorRow({
  run,
  reduceMotion,
  onOpen,
  openLabel,
  kindLabel,
  roundsLabel,
}: {
  run: PythiaMonitorRun;
  reduceMotion: boolean;
  onOpen: () => void;
  openLabel: string;
  kindLabel: string;
  roundsLabel: string;
}) {
  const status = run.status ?? "unknown";
  const tone =
    status === "running"
      ? "text-purple-700 dark:text-purple-300"
      : status === "completed"
        ? "text-emerald-600 dark:text-emerald-400"
        : status === "aborted" || status === "failed"
          ? "text-red-600 dark:text-red-400"
          : "text-muted-foreground";

  const statusContent = (
    <span className={`inline-flex shrink-0 items-center gap-1 font-medium ${tone}`}>
      {status === "running" ? (
        <Loader2 className="size-3 animate-spin" aria-hidden />
      ) : (
        <span
          className={`size-1.5 rounded-full ${
            status === "completed"
              ? "bg-emerald-500"
              : status === "aborted" || status === "failed"
                ? "bg-red-500"
                : "bg-muted-foreground/40"
          }`}
          aria-hidden
        />
      )}
      {status}
    </span>
  );

  return (
    <button
      type="button"
      onClick={onOpen}
      aria-label={`${openLabel}: ${run.issue_title || run.issue_id}`}
      data-testid="pythia-monitor-row"
      className="flex w-full items-center gap-2 rounded-md border border-border bg-card/50 px-3 py-2 text-left transition-colors hover:bg-accent/50"
    >
      <span className="rounded bg-muted px-1 py-0.5 font-mono text-[10px] text-muted-foreground">
        {kindLabel}
      </span>
      <span className="min-w-0 flex-1 truncate text-xs font-medium text-foreground/90">
        {run.issue_title || run.issue_id}
      </span>
      {run.variables ? (
        <span className="hidden max-w-40 truncate text-[10px] text-foreground/60 md:inline">
          {run.variables}
        </span>
      ) : null}
      <span className="shrink-0 font-mono text-[10px] text-muted-foreground">
        {roundsLabel}
      </span>
      {reduceMotion ? (
        statusContent
      ) : (
        <AnimatePresence initial={false} mode="wait">
          <motion.span
            key={status}
            className="inline-flex"
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
          </motion.span>
        </AnimatePresence>
      )}
      <ArrowRight className="size-3 shrink-0 text-muted-foreground" aria-hidden />
    </button>
  );
}

// humanizeBootError mirrors the Claude Science helper. The main
// process surfaces a structured error with `code = "BINARY_NOT_BUNDLED"`
// when the bundled Python engine / uvicorn launcher is missing under
// resources/pythia/. The renderer copy points the user at the right
// vendor path so the fix path is one command away.
function humanizeBootError(err: unknown): string {
  if (err instanceof Error) {
    if ((err as Error & { code?: string }).code === "BINARY_NOT_BUNDLED") {
      return (
        "Pythia 的 Python 引擎还没有被打包进桌面 app。请把 Pythia 源码放到 " +
        "apps/desktop/vendor/pythia-src/engine/,然后跑 " +
        "`pnpm --filter @multica/desktop bundle-cli && pnpm --filter @multica/desktop package` " +
        "重新打包。当前 Labs flag 已启用,但 service not bundled。"
      );
    }
    return err.message;
  }
  return "failed to start Pythia manager";
}
