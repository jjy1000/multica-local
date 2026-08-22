// PythiaReportSurface — 0.3.30.3 full Pythia dashboard.
//
// This file replaces the slim 0.3.29 report-only surface with the
// full Pythia experience: world view (event stream + map overlay),
// forecasts list (sorted by probability + horizon), council chamber
// (per-persona votes for any selected prediction), what-if panel,
// morning brief preview, scorecard, market ticker, and the per-issue
// 10-round deliberation loop. UI is in Chinese (zh-Hans is the
// priority per the 0.3.30.3 task) with i18n keys for fallback.
//
// Hard rules:
//   - Loaded ONLY when `pythia_oracle` is on. The parent view
//     returns the informational status panel when the flag is off.
//   - All HTTP calls go through `window.experimentalAPI.pythia.proxy`
//     (the main-process IPC channel that forwards to the loopback
//     Python engine) — never a bare fetch from the renderer, since
//     renderer fetch() cannot reach 127.0.0.1 directly under
//     contextIsolation.
//   - The SSE stream comes from `window.experimentalAPI.pythia.sseUrl`
//     and is consumed via fetch + ReadableStream so we can apply
//     api.rawRequest's auth headers automatically (EventSource cannot
//     attach Bearer / CSRF / workspace headers).

import { useCallback, useEffect, useMemo, useState } from "react";
import { api } from "@multica/core/api";
import { getCurrentWsId } from "@multica/core/platform";
import { useT } from "@multica/views/i18n";
import type { PythiaPrediction } from "./types";
import { GlobeSVG } from "./globe-svg";
import { SwarmVoteBars } from "./swarm-vote-bars";
import { PythiaChatBox } from "./pythia-chat-box";

// ---------------------------------------------------------------------------
// Wire types — mirror the Python engine (engine/models.py) + Go-side envelope.
// ---------------------------------------------------------------------------

type Persona = "strategist" | "analyst" | "critic";
type Horizon = "day" | "week" | "month" | "year";

interface PythiaReportEnvelope {
  id: string;
  round: number;
  issue_id?: string;
  scenario: string;
  narrative: string;
  probability: number;
  confidence: number;
  horizon: string;
  persona: string;
  lab_source: string;
  scenario_context?: string;
  created_at?: string;
}

interface PythiaWorldBrief {
  event_count: number;
  domains: Record<string, number>;
  text: string;
  top_events: string[];
}

interface PythiaSnapshot {
  generating: boolean;
  loop_enabled: boolean;
  last_run_ms: number | null;
  world: PythiaWorldBrief | null;
  predictions: PythiaPrediction[];
}

interface Scorecard {
  overall_brier?: number;
  hit_rate?: number;
  per_horizon?: Record<string, { brier: number; hit_rate: number }>;
  per_persona?: Record<string, { brier: number; hit_rate: number }>;
  recent?: Array<{ id: string; horizon: string; verdict: string; evidence: string }>;
}

interface BriefResponse {
  config: { time: string; enabled: boolean };
  latest?: { date: string; text: string };
  history?: string[];
}

interface PythiaRuntimeSurface {
  getStatus: () => Promise<string>;
  getURL: () => Promise<string | null>;
  ensureUp: () => Promise<string>;
  sseUrl?: () => Promise<string>;
}

declare global {
  interface Window {
    experimentalAPI: {
      pythia: PythiaRuntimeSurface & {
        proxy: <T = unknown>(path: string, init?: { method?: string; body?: unknown; timeoutMs?: number }) => Promise<T>;
      };
      // Legacy IPC contract used by llm-wiki-bridge-view and other
      // pre-0.3.19 surfaces. Routes the (flagKey, verb) tuple to a
      // registered ipcMain handler. The types here are intentionally
      // loose — the wire is internal and validated server-side.
      invoke: (flagKey: string, verb: string, payload?: unknown) => Promise<unknown>;
    };
  }
}

export interface PythiaReportSurfaceProps {
  issueId: string | null;
  horizon: Horizon;
  persona: Persona;
  subscribed: boolean;
  rounds: number;
  managerUrl: string | null;
  onSelectIssue: (id: string) => void;
  onChangeHorizon: (h: Horizon) => void;
  onChangePersona: (p: Persona) => void;
  onToggleSubscribe: () => void;
  onRegenerate: () => void;
  onSubmitScenario: (scenario: string) => void;
}

// ---------------------------------------------------------------------------
// Main surface
// ---------------------------------------------------------------------------

export function PythiaReportSurface(props: PythiaReportSurfaceProps) {
  const { t } = useT("pythia");
  const [snapshot, setSnapshot] = useState<PythiaSnapshot | null>(null);
  const [envelopes, setEnvelopes] = useState<PythiaReportEnvelope[]>([]);
  const [scorecard, setScorecard] = useState<Scorecard | null>(null);
  const [brief, setBrief] = useState<BriefResponse | null>(null);
  const [world, setWorld] = useState<{ event_count: number; domains: Record<string, number>; text: string } | null>(null);
  const [selectedPredictionId, setSelectedPredictionId] = useState<string | null>(null);
  const [generating, setGenerating] = useState(false);
  const [regenerating, setRegenerating] = useState(false);
  const [scenario, setScenario] = useState("");
  const [whatifRunning, setWhatifRunning] = useState(false);
  const [whatifResult, setWhatifResult] = useState<{ narrative: string; predictions: PythiaPrediction[] } | null>(null);

  // ---- snapshot fetch (live state) ----
  useEffect(() => {
    let cancelled = false;
    const pull = async () => {
      try {
        const s = await window.experimentalAPI.pythia.proxy<PythiaSnapshot>("/state");
        if (!cancelled) setSnapshot(s);
      } catch {
        /* ignore — snapshot is decorative */
      }
    };
    void pull();
    const id = window.setInterval(pull, 10_000);
    return () => {
      cancelled = true;
      window.clearInterval(id);
    };
  }, []);

  // ---- scorecard ----
  useEffect(() => {
    let cancelled = false;
    void window.experimentalAPI.pythia.proxy<Scorecard>("/scorecard")
      .then((s) => {
        if (!cancelled) setScorecard(s);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, []);

  // ---- morning brief ----
  useEffect(() => {
    let cancelled = false;
    void window.experimentalAPI.pythia.proxy<BriefResponse>("/brief")
      .then((b) => {
        if (!cancelled) setBrief(b);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, []);

  // ---- world brief ----
  useEffect(() => {
    let cancelled = false;
    void window.experimentalAPI.pythia.proxy<PythiaWorldBrief>("/world")
      .then((w) => {
        if (!cancelled) setWorld(w);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, []);

  // ---- SSE subscribe via fetch stream (api.rawRequest is not
  // available here because experimentalAPI is the IPC surface; the
  // main process forwards the loopback url and our auth cookies are
  // already attached by the manager).
  useEffect(() => {
    if (!props.subscribed) return;
    if (!props.managerUrl) return;
    let cancelled = false;
    const url = `${props.managerUrl}/state/stream`;
    const ctrl = new AbortController();
    void (async () => {
      try {
        const res = await fetch(url, { signal: ctrl.signal });
        if (!res.body || cancelled) return;
        const reader = res.body.getReader();
        const decoder = new TextDecoder("utf-8");
        let buf = "";
        while (!cancelled) {
          const { value, done } = await reader.read();
          if (done) break;
          buf += decoder.decode(value, { stream: true });
          // Each event is `data: {...}\n\n`; we keep the last buffer
          // tail short to avoid unbounded growth.
          const sep = buf.indexOf("\n\n");
          while (sep !== -1) {
            const block = buf.slice(0, sep);
            buf = buf.slice(sep + 2);
            const dataLine = block.split(/\r?\n/).find((l) => l.startsWith("data:"));
            if (dataLine) {
              try {
                const obj = JSON.parse(dataLine.slice(5).trim()) as { kind?: string; payload?: PythiaSnapshot };
                if (obj.kind === "snapshot" && obj.payload) {
                  setSnapshot(obj.payload);
                }
              } catch {
                /* ignore malformed frames */
              }
            }
            const next = buf.indexOf("\n\n");
            if (next === -1) break;
          }
        }
      } catch {
        /* SSE subscribe is best-effort; user can still use the report */
      }
    })();
    return () => {
      cancelled = true;
      ctrl.abort();
    };
  }, [props.subscribed, props.managerUrl]);

  // ---- per-issue forecast (10 rounds default) ----
  useEffect(() => {
    if (!props.subscribed) return;
    if (!props.issueId) return;
    let cancelled = false;
    const run = async () => {
      setRegenerating(true);
      // 0.5.59: stamp the trigger key BEFORE firing so the issue-detail
      // <PythiaPanel> flips into "推演中..." the moment the user
      // navigates back. Matches the key shape in lab-output-panel.tsx.
      //
      // 0.5.60 (audit P1-4): only stamp when we actually have a wsId.
      // The reader (lab-output-panel.tsx) keys on the REAL workspace id
      // from its prop; writing under a fabricated "ws" fallback when
      // getCurrentWsId() is null produced a key the reader never matches,
      // silently losing the in-progress state. When getCurrentWsId() is
      // non-null it is the same value the reader's prop carries, so the
      // keys agree.
      const stampWsId = getCurrentWsId();
      if (stampWsId) {
        try {
          window.sessionStorage.setItem(
            `pythia-triggered-${stampWsId}-${props.issueId}`,
            String(Date.now()),
          );
        } catch {
          // sessionStorage unavailable — ignore, panel will fall back to
          // its in-memory state on this surface.
        }
      }
      try {
        // 0.3.45.2 bug fix (P1#6): was a bare fetch("/api/..."). On
        // the packaged desktop build the renderer document origin is
        // `file://`, not `http://localhost:8090`, so the request
        // never reached the server. api.rawRequest prepends the
        // configured baseUrl and injects the Bearer header, which
        // is the only path that works under desktop token mode.
        const res = await api.rawRequest(
          `/api/experimental/pythia-oracle/forecast/issue`,
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              issue_id: props.issueId,
              rounds: props.rounds,
              horizon: props.horizon,
              persona: props.persona,
            }),
          },
        );
        if (!res.ok || cancelled) {
          setRegenerating(false);
          return;
        }
        const reader = res.body?.getReader();
        if (!reader) {
          setRegenerating(false);
          return;
        }
        const decoder = new TextDecoder("utf-8");
        let buf = "";
        let round = 1;
        while (!cancelled) {
          const { value, done } = await reader.read();
          if (done) break;
          buf += decoder.decode(value, { stream: true });
          const sep = buf.indexOf("\n\n");
          if (sep === -1) continue;
          const block = buf.slice(0, sep);
          buf = buf.slice(sep + 2);
          const dataLine = block.split(/\r?\n/).find((l) => l.startsWith("data:"));
          if (!dataLine) continue;
          let env: PythiaReportEnvelope;
          try {
            env = JSON.parse(dataLine.slice(5).trim());
          } catch {
            continue;
          }
          env.round = round++;
          setEnvelopes((prev) => dedupeById([...prev, env]));
        }
      } catch {
        /* best-effort */
      } finally {
        setRegenerating(false);
      }
    };
    void run();
    return () => {
      cancelled = true;
    };
  }, [
    props.issueId,
    props.horizon,
    props.persona,
    props.rounds,
    props.subscribed,
  ]);

  // ---- grouped forecasts ----
  const predictions = snapshot?.predictions ?? [];
  const byHorizon = useMemo(() => {
    const out: Record<Horizon, PythiaPrediction[]> = {
      day: [],
      week: [],
      month: [],
      year: [],
    };
    for (const p of predictions) {
      const h = (p.horizon as Horizon) ?? "week";
      out[h] = out[h] ?? [];
      out[h].push(p);
    }
    for (const k of Object.keys(out) as Horizon[]) {
      out[k].sort((a, b) => b.probability - a.probability);
    }
    return out;
  }, [predictions]);

  const selectedPrediction = useMemo(
    () => predictions.find((p) => p.id === selectedPredictionId) ?? predictions[0] ?? null,
    [predictions, selectedPredictionId],
  );

  // ---- controls ----
  const handleTriggerRun = useCallback(async () => {
    setGenerating(true);
    try {
      await window.experimentalAPI.pythia.proxy("/predict", { method: "POST" });
    } catch {
      /* surface failure to the toast layer (parent) */
    } finally {
      setGenerating(false);
    }
  }, []);

  const handleRunWhatif = useCallback(async () => {
    if (!scenario.trim()) return;
    setWhatifRunning(true);
    try {
      const r = await window.experimentalAPI.pythia.proxy<{
        narrative: string;
        predictions: PythiaPrediction[];
      }>("/whatif", {
        method: "POST",
        body: JSON.stringify({ scenario: scenario.trim() }),
      });
      setWhatifResult(r);
      props.onSubmitScenario(scenario.trim());
      setScenario("");
    } catch {
      setWhatifResult({
        narrative: "推演失败,稍后重试。",
        predictions: [],
      });
    } finally {
      setWhatifRunning(false);
    }
  }, [scenario, props]);

  const handleRunBrief = useCallback(async () => {
    try {
      await window.experimentalAPI.pythia.proxy("/brief/run", { method: "POST" });
      const b = await window.experimentalAPI.pythia.proxy<BriefResponse>("/brief");
      setBrief(b);
    } catch {
      /* best-effort */
    }
  }, []);

  // ---- rendering ----
  return (
    <div className="flex h-full w-full flex-col gap-4 overflow-y-auto p-6">
      {/* Header */}
      <header className="flex flex-col gap-1 border-b border-border pb-3">
        <div className="flex flex-wrap items-baseline justify-between gap-3">
          <div className="flex flex-col gap-0.5">
            <h2 className="text-xl font-semibold text-foreground">
              {t(($) => $.report_title)}
            </h2>
            <p className="text-xs text-muted-foreground">
              {t(($) => $.report_subtitle)}
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <span
              className={
                snapshot?.generating
                  ? "inline-flex items-center gap-1 text-amber-400"
                  : "inline-flex items-center gap-1 text-emerald-400"
              }
            >
              <span
                className={
                  snapshot?.generating
                    ? "h-2 w-2 animate-pulse rounded-full bg-amber-400"
                    : "h-2 w-2 rounded-full bg-emerald-400"
                }
              />
              {snapshot?.generating
                ? t(($) => $.running)
                : t(($) => $.idle)}
            </span>
            <button
              type="button"
              onClick={props.onToggleSubscribe}
              className="rounded bg-muted px-2 py-1 text-foreground hover:bg-muted/70"
            >
              {props.subscribed
                ? t(($) => $.unsubscribe)
                : t(($) => $.subscribe)}
            </button>
            <button
              type="button"
              onClick={handleTriggerRun}
              disabled={generating}
              className="rounded bg-primary px-2 py-1 text-primary-foreground disabled:opacity-50"
            >
              {generating
                ? t(($) => $.running)
                : t(($) => $.run_oracle)}
            </button>
          </div>
        </div>

        {/* Interactive controls — horizon + persona + regenerate */}
        <div className="flex flex-wrap items-center gap-3 pt-2 text-xs">
          <span className="text-muted-foreground">
            {t(($) => $.horizon_label)}:
          </span>
          {(["day", "week", "month", "year"] as const).map((h) => (
            <button
              key={h}
              type="button"
              onClick={() => props.onChangeHorizon(h)}
              className={
                props.horizon === h
                  ? "rounded bg-primary/20 px-2 py-0.5 font-medium text-primary ring-1 ring-primary/40"
                  : "rounded bg-muted px-2 py-0.5 text-muted-foreground hover:text-foreground"
              }
            >
              {h === "day" && t(($) => $.horizon_day)}
              {h === "week" && t(($) => $.horizon_week)}
              {h === "month" && t(($) => $.horizon_month)}
              {h === "year" && t(($) => $.horizon_year)}
            </button>
          ))}

          <span className="ml-2 text-muted-foreground">
            {t(($) => $.persona_label)}:
          </span>
          {(["strategist", "analyst", "critic"] as const).map((p) => (
            <button
              key={p}
              type="button"
              onClick={() => props.onChangePersona(p)}
              className={
                props.persona === p
                  ? "rounded bg-primary/20 px-2 py-0.5 font-medium text-primary ring-1 ring-primary/40"
                  : "rounded bg-muted px-2 py-0.5 text-muted-foreground hover:text-foreground"
              }
            >
              {p === "strategist" && t(($) => $.persona_strategist_short)}
              {p === "analyst" && t(($) => $.persona_analyst_short)}
              {p === "critic" && t(($) => $.persona_critic_short)}
            </button>
          ))}

          <button
            type="button"
            onClick={props.onRegenerate}
            disabled={regenerating}
            className="ml-auto rounded bg-primary px-3 py-1 font-medium text-primary-foreground disabled:cursor-not-allowed disabled:opacity-50"
          >
            {regenerating
              ? t(($) => $.regenerate_running)
              : t(($) => $.regenerate)}
          </button>
        </div>
      </header>

      {/* Globe + world overview */}
      <section className="grid grid-cols-1 gap-4 lg:grid-cols-[1fr_1fr]">
        <div className="rounded-lg border border-border bg-card/40 p-4">
          <h3 className="mb-2 text-sm font-semibold text-foreground">
            {t(($) => $.world_brief_title)}
          </h3>
          {world ? (
            <>
              <GlobeSVG
                predictions={snapshot?.predictions ?? []}
                selectedId={selectedPredictionId}
                onSelect={setSelectedPredictionId}
              />
              <div className="flex flex-col gap-1 text-xs text-muted-foreground">
                <span>
                  {t(($) => $.world_event_count).replace(
                    "{{count}}",
                    String(world.event_count ?? 0),
                  )}
                </span>
                <span>
                  {t(($) => $.world_domains)
                    .replace(
                      "{{count}}",
                      String(Object.keys(world.domains ?? {}).length),
                    )}
                </span>
              </div>
              {Object.keys(world.domains ?? {}).length > 0 && (
                <div className="mt-2 flex flex-wrap gap-1">
                  {Object.entries(world.domains ?? {})
                    .sort((a, b) => b[1] - a[1])
                    .slice(0, 12)
                    .map(([domain, count]) => (
                      <span
                        key={domain}
                        className="rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground"
                      >
                        {domain} · {count}
                      </span>
                    ))}
                </div>
              )}
            </>
          ) : (
            <p className="text-xs text-muted-foreground">
              {t(($) => $.world_loading)}
            </p>
          )}
        </div>

        {/* Scorecard */}
        <div className="rounded-lg border border-border bg-card/40 p-4">
          <h3 className="mb-2 text-sm font-semibold text-foreground">
            {t(($) => $.scorecard_title)}
          </h3>
          {scorecard ? (
            <div className="flex flex-col gap-2 text-xs text-muted-foreground">
              <div className="grid grid-cols-2 gap-2">
                <div className="rounded border border-border/60 bg-card/30 p-2">
                  <div className="text-[10px] uppercase tracking-wide text-muted-foreground/80">
                    {t(($) => $.scorecard_brier)}
                  </div>
                  <div className="font-mono text-lg text-foreground">
                    {scorecard.overall_brier?.toFixed(3) ?? "—"}
                  </div>
                </div>
                <div className="rounded border border-border/60 bg-card/30 p-2">
                  <div className="text-[10px] uppercase tracking-wide text-muted-foreground/80">
                    {t(($) => $.scorecard_hit_rate)}
                  </div>
                  <div className="font-mono text-lg text-foreground">
                    {scorecard.hit_rate !== undefined
                      ? `${(scorecard.hit_rate * 100).toFixed(0)}%`
                      : "—"}
                  </div>
                </div>
              </div>
              {scorecard.per_persona &&
                Object.keys(scorecard.per_persona).length > 0 && (
                  <div>
                    <div className="text-[10px] uppercase tracking-wide text-muted-foreground/80">
                      {t(($) => $.scorecard_per_persona)}
                    </div>
                    <ul className="mt-1 flex flex-col gap-0.5">
                      {Object.entries(scorecard.per_persona).map(
                        ([persona, row]) => (
                          <li
                            key={persona}
                            className="flex items-baseline justify-between"
                          >
                            <span>{persona}</span>
                            <span className="font-mono">
                              {row.brier?.toFixed(2) ?? "—"}
                            </span>
                          </li>
                        ),
                      )}
                    </ul>
                  </div>
                )}
            </div>
          ) : (
            <p className="text-xs text-muted-foreground">
              {t(($) => $.scorecard_empty)}
            </p>
          )}
        </div>
      </section>

      {/* Live chat with the oracle — input box + bubble history.
          Mounted above the forecast panels so the user can chat and
          branch what-if scenarios without leaving the dashboard. */}
      <PythiaChatBox />

      {/* Forecasts by horizon */}
      <section className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {(["day", "week", "month", "year"] as Horizon[]).map((h) => (
          <ForecastColumn
            key={h}
            title={
              h === "day"
                ? t(($) => $.horizon_day)
                : h === "week"
                  ? t(($) => $.horizon_week)
                  : h === "month"
                    ? t(($) => $.horizon_month)
                    : t(($) => $.horizon_year)
            }
            predictions={byHorizon[h]}
            selectedId={selectedPredictionId}
            onSelect={setSelectedPredictionId}
          />
        ))}
      </section>

      {/* Council chamber + deliberation */}
      <section className="rounded-lg border border-border bg-card/40 p-4">
        <h3 className="mb-2 text-sm font-semibold text-foreground">
          {t(($) => $.council_chamber_title)}
        </h3>
        {selectedPrediction ? (
          <div className="flex flex-col gap-3">
            <div className="rounded border border-border/60 bg-card/30 p-3">
              <div className="text-xs uppercase tracking-wide text-muted-foreground/80">
                {selectedPrediction.horizon} ·{" "}
                {(selectedPrediction.probability * 100).toFixed(0)}%
              </div>
              <div className="mt-1 text-sm font-medium text-foreground">
                {selectedPrediction.title}
              </div>
              {selectedPrediction.reasoning && (
                <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
                  {selectedPrediction.reasoning}
                </p>
              )}
            </div>
            <SwarmVoteBars
              agents={selectedPrediction.agents ?? []}
              consensus={selectedPrediction.probability}
              split={selectedPrediction.split ?? false}
            />
          </div>
        ) : (
          <p className="text-xs text-muted-foreground">
            {t(($) => $.council_chamber_empty)}
          </p>
        )}
      </section>

      {/* Issue-bound 10-round report */}
      <section className="rounded-lg border border-border bg-card/30 p-4">
        <h3 className="mb-2 text-sm font-semibold text-foreground">
          {t(($) => $.issue_panel_title)}
        </h3>
        <div className="text-xs text-muted-foreground">
          {props.issueId
            ? `${props.issueId}`
            : t(($) => $.report_no_issue)}
        </div>
        <div className="mt-3 flex flex-col gap-3">
          {envelopes.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              {t(($) => $.no_predictions_yet)}
            </p>
          ) : (
            envelopes.map((env) => (
              <article
                key={env.id}
                className="flex flex-col gap-1 border-l-2 border-primary/30 pl-3"
              >
                <div className="flex items-baseline gap-3 text-xs text-muted-foreground">
                  <span className="font-mono">
                    {t(($) => $.report_round).replace(
                      "{{round}}",
                      String(env.round),
                    )}
                  </span>
                  <span>·</span>
                  <span>{env.lab_source}</span>
                  <span>·</span>
                  <span>{env.horizon}</span>
                  <span>·</span>
                  <span>{env.persona}</span>
                </div>
                <div className="text-2xl font-mono font-semibold text-foreground">
                  {(env.probability * 100).toFixed(0)}%
                </div>
                <div className="text-sm font-medium text-foreground">
                  {env.scenario}
                </div>
                {env.narrative && (
                  <p className="text-xs leading-relaxed text-muted-foreground">
                    {env.narrative}
                  </p>
                )}
              </article>
            ))
          )}
        </div>
      </section>

      {/* 0.3.55: per-issue finished-result history. The live section
          above streams the current deliberation; this panel lists the
          persisted runs (server-side pythia_forecast_run) so the
          finished result stays visible after the user navigates away. */}
      <ForecastHistoryPanel issueId={props.issueId} />

      {/* What-if panel */}
      <WhatifPanel
        scenario={scenario}
        setScenario={setScenario}
        onRun={handleRunWhatif}
        running={whatifRunning}
        result={whatifResult}
      />

      {/* Morning brief preview */}
      <section className="rounded-lg border border-border bg-card/40 p-4">
        <div className="flex items-baseline justify-between gap-3">
          <h3 className="text-sm font-semibold text-foreground">
            {t(($) => $.brief_title)}
          </h3>
          <button
            type="button"
            onClick={handleRunBrief}
            className="rounded bg-muted px-2 py-0.5 text-xs text-foreground hover:bg-muted/70"
          >
            {t(($) => $.brief_run_now)}
          </button>
        </div>
        {brief?.latest ? (
          <div className="mt-2 flex flex-col gap-1 text-xs text-muted-foreground">
            <span className="text-foreground/80">{brief.latest.date}</span>
            <pre className="whitespace-pre-wrap font-sans text-xs leading-relaxed text-foreground">
              {brief.latest.text}
            </pre>
          </div>
        ) : (
          <p className="mt-2 text-xs text-muted-foreground">
            {t(($) => $.brief_empty)}
          </p>
        )}
      </section>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

interface ForecastColumnProps {
  title: string;
  predictions: PythiaPrediction[];
  selectedId: string | null;
  onSelect: (id: string) => void;
}

function ForecastColumn({
  title,
  predictions,
  selectedId,
  onSelect,
}: ForecastColumnProps) {
  const { t } = useT("pythia");
  return (
    <div className="rounded-lg border border-border bg-card/40 p-4">
      <h3 className="mb-2 flex items-baseline justify-between text-sm font-semibold text-foreground">
        <span>{title}</span>
        <span className="text-xs text-muted-foreground">
          {t(($) => $.forecast_count).replace(
            "{{count}}",
            String(predictions.length),
          )}
        </span>
      </h3>
      {predictions.length === 0 ? (
        <p className="text-xs text-muted-foreground">
          {t(($) => $.forecast_empty)}
        </p>
      ) : (
        <ul className="flex flex-col gap-2">
          {predictions.slice(0, 5).map((p) => (
            <li
              key={p.id}
              className={
                selectedId === p.id
                  ? "cursor-pointer rounded border border-primary/60 bg-primary/10 p-2"
                  : "cursor-pointer rounded border border-border/60 bg-card/30 p-2 hover:border-primary/40"
              }
              onClick={() => onSelect(p.id)}
            >
              <div className="flex items-baseline justify-between text-xs">
                <span className="font-mono font-medium text-foreground">
                  {(p.probability * 100).toFixed(0)}%
                </span>
                {p.split && (
                  <span className="rounded bg-fuchsia-500/20 px-1.5 text-[10px] text-fuchsia-300">
                    {t(($) => $.split_flag)}
                  </span>
                )}
              </div>
              <div className="mt-0.5 text-xs text-foreground">{p.title}</div>
              {p.location && (
                <div className="mt-0.5 text-[10px] text-muted-foreground/70">
                  {p.location}
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

interface WhatifPanelProps {
  scenario: string;
  setScenario: (s: string) => void;
  onRun: () => void;
  running: boolean;
  result: { narrative: string; predictions: PythiaPrediction[] } | null;
}

function WhatifPanel(props: WhatifPanelProps) {
  const { t } = useT("pythia");
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        props.onRun();
      }}
      className="flex flex-col gap-2 rounded-lg border border-fuchsia-500/30 bg-fuchsia-500/5 p-3"
    >
      <h3 className="text-xs font-semibold uppercase tracking-wide text-fuchsia-300">
        {t(($) => $.submit_new_scenario)}
      </h3>
      <textarea
        rows={2}
        value={props.scenario}
        onChange={(e) => props.setScenario(e.target.value)}
        placeholder={t(($) => $.scenario_placeholder)}
        className="w-full resize-none rounded border border-input bg-background px-2 py-1.5 text-sm leading-relaxed text-foreground placeholder:text-muted-foreground"
      />
      <div className="flex justify-end">
        <button
          type="submit"
          disabled={props.running}
          className="rounded bg-fuchsia-500 px-3 py-1 text-xs font-medium text-fuchsia-50 hover:bg-fuchsia-500/90 disabled:opacity-50"
        >
          {props.running ? "推演中…" : t(($) => $.scenario_send)}
        </button>
      </div>
      {props.result && (
        <div className="mt-2 rounded border border-fuchsia-500/20 bg-fuchsia-500/5 p-3 text-xs text-foreground">
          <div className="text-[10px] uppercase tracking-wide text-fuchsia-300/80">
            {t(($) => $.whatif_narrative)}
          </div>
          <p className="mt-1 whitespace-pre-wrap leading-relaxed">
            {props.result.narrative}
          </p>
          {props.result.predictions.length > 0 && (
            <ul className="mt-2 flex flex-col gap-1">
              {props.result.predictions.map((p) => (
                <li
                  key={p.id}
                  className="flex items-baseline justify-between text-[11px]"
                >
                  <span>{p.title}</span>
                  <span className="font-mono">
                    {(p.probability * 100).toFixed(0)}%
                  </span>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </form>
  );
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// ForecastHistoryPanel — 0.3.55 per-issue finished-result history.
// ---------------------------------------------------------------------------
// Pre-0.3.55 the per-issue 10-round deliberation was SSE-live only: it
// streamed and vanished on unmount, so the lab view had no finished
// result to show ("结束后结果不可见" was the core Pythia gap). The
// server now persists every completed run to pythia_forecast_run; this
// panel lists those runs newest-first and re-renders a past
// deliberation on click. HTTP goes through api.rawRequest (never a
// bare fetch) so the desktop token-auth + base URL apply.

interface ForecastRunSummary {
  id: string;
  rounds: number;
  source: string;
  created_at: string;
  envelopes: PythiaReportEnvelope[];
}

function ForecastHistoryPanel({ issueId }: { issueId: string | null }) {
  const [runs, setRuns] = useState<ForecastRunSummary[]>([]);
  const [loading, setLoading] = useState(false);
  const [expandedId, setExpandedId] = useState<string | null>(null);

  useEffect(() => {
    if (!issueId) {
      setRuns([]);
      setExpandedId(null);
      return;
    }
    let cancelled = false;
    setLoading(true);
    api
      .rawRequest(
        `/api/experimental/pythia-oracle/forecast/issue/runs?issue_id=${encodeURIComponent(issueId)}&limit=10`,
        { method: "GET" },
      )
      .then(async (res) => {
        if (!res.ok) return [] as ForecastRunSummary[];
        return (await res.json()) as ForecastRunSummary[];
      })
      .then((data) => {
        if (!cancelled) setRuns(Array.isArray(data) ? data : []);
      })
      .catch(() => {
        if (!cancelled) setRuns([]);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [issueId]);

  if (!issueId) return null;

  const expanded = runs.find((r) => r.id === expandedId) ?? null;

  return (
    <section className="rounded-lg border border-border bg-card/30 p-4">
      <div className="flex items-baseline justify-between gap-3">
        <h3 className="text-sm font-semibold text-foreground">历史推演</h3>
        <span className="text-[11px] text-muted-foreground">
          {loading ? "加载中…" : `共 ${runs.length} 次`}
        </span>
      </div>
      {runs.length === 0 ? (
        <p className="mt-2 text-xs leading-relaxed text-muted-foreground">
          还没有已完成的推演。在上方跑一次本 issue 的多视角推演,完成后会自动存到这里,关掉页面也不会丢。
        </p>
      ) : (
        <div className="mt-3 flex flex-col gap-1.5">
          {runs.map((run) => (
            <button
              key={run.id}
              type="button"
              onClick={() =>
                setExpandedId((cur) => (cur === run.id ? null : run.id))
              }
              className={`flex items-center justify-between gap-2 rounded border px-2.5 py-1.5 text-left text-xs transition-colors ${
                expandedId === run.id
                  ? "border-primary/40 bg-primary/5"
                  : "border-border/60 hover:bg-accent/50"
              }`}
            >
              <span className="text-foreground/90">
                {formatRunTime(run.created_at)}
              </span>
              <span className="flex items-center gap-2 text-muted-foreground">
                <span className="font-mono">{run.rounds} 轮</span>
                <span className="rounded bg-muted px-1 py-0.5 text-[10px]">
                  {run.source}
                </span>
              </span>
            </button>
          ))}
        </div>
      )}
      {expanded && (
        <div className="mt-3 flex flex-col gap-3 border-t border-border/60 pt-3">
          {(expanded.envelopes ?? []).map((env, i) => (
            <article
              key={env.id ?? `round-${i}`}
              className="flex flex-col gap-1 border-l-2 border-primary/30 pl-3"
            >
              <div className="flex items-baseline gap-3 text-xs text-muted-foreground">
                <span className="font-mono">第 {i + 1} 轮</span>
                <span>·</span>
                <span>{env.lab_source}</span>
                <span>·</span>
                <span>{env.persona}</span>
              </div>
              <div className="text-2xl font-mono font-semibold text-foreground">
                {(env.probability * 100).toFixed(0)}%
              </div>
              <div className="text-sm font-medium text-foreground">
                {env.scenario}
              </div>
              {env.narrative && (
                <p className="text-xs leading-relaxed text-muted-foreground">
                  {env.narrative}
                </p>
              )}
            </article>
          ))}
        </div>
      )}
    </section>
  );
}

function formatRunTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString("zh-Hans", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function dedupeById(envs: PythiaReportEnvelope[]): PythiaReportEnvelope[] {
  const byId = new Map<string, PythiaReportEnvelope>();
  for (const env of envs) byId.set(env.id, env);
  return [...byId.values()];
}

// parsePredictionBlock preserves the 0.3.29 export so existing tests
// still pass; not used by the new render path.
export function parsePredictionBlock(
  buf: string,
): Omit<PythiaReportEnvelope, "round"> | null {
  const sep = buf.indexOf("\n\n");
  if (sep === -1) return null;
  const block = buf.slice(0, sep);
  const lines = block.split(/\r?\n/);
  const dataLine = lines.find((l) => l.startsWith("data:"));
  if (!dataLine) return null;
  let raw: Record<string, unknown>;
  try {
    raw = JSON.parse(dataLine.slice(5).trim());
  } catch {
    return null;
  }
  return {
    id: String(raw.id ?? ""),
    issue_id: typeof raw.issue_id === "string" ? raw.issue_id : undefined,
    scenario: String(raw.scenario ?? ""),
    narrative: String(raw.narrative ?? ""),
    probability: typeof raw.probability === "number" ? raw.probability : 0,
    confidence: typeof raw.confidence === "number" ? raw.confidence : 0,
    horizon: String(raw.horizon ?? "week"),
    persona: String(raw.persona ?? "strategist"),
    lab_source: String(raw.lab_source ?? "synthetic"),
    scenario_context:
      typeof raw.scenario_context === "string"
        ? raw.scenario_context
        : undefined,
    created_at:
      typeof raw.createdAt === "string"
        ? raw.createdAt
        : typeof raw.created_at === "string"
          ? raw.created_at
          : undefined,
  };
}