// PythiaDashboard — assembles GlobeSVG + selection card + swarm bars.
//
// Phase 1 wired it to SAMPLE_PREDICTIONS. Phase 2 supersedes that with
// the live SSE snapshot from /state/stream when the Pythia manager is
// up and serving. The two data sources share the PythiaPrediction wire
// type, so the SVG + selection card render identically — only the
// source changes.
//
// Resilience rules:
//   - manager not yet up (url = null) → fall back to SAMPLE_PREDICTIONS
//     so the page renders something useful while the engine boots.
//   - manager up but SSE in `error` / `closed` state → keep showing the
//     LAST good snapshot; the SSE hook retries every 2 s.
//   - livePredictions = [] (engine up but no runs yet) → empty-state
//     card on the right, globe stays on the graticule.

import { useEffect, useState } from "react";
import { GlobeSVG } from "./globe-svg";
import { SwarmVoteBars } from "./swarm-vote-bars";
import { SAMPLE_PREDICTIONS } from "./sample-predictions";
import { WhatIfPanel, type WhatIfResult } from "./whatif-panel";
import { usePythiaSse } from "./use-pythia-sse";
import type { PythiaPrediction } from "./types";
import { useT } from "@multica/views/i18n";

export interface PythiaDashboardProps {
  /**
   * Optional override for tests / storybook. When omitted, the
   * dashboard fetches the loopback URL from the experimentalAPI and
   * opens an SSE stream. Tests can pass a fixed URL string or an
   * empty array to bypass the manager entirely.
   */
  pythiaUrl?: string | null;
  /** When true, skip the SSE connection and render SAMPLE_PREDICTIONS. */
  forceSample?: boolean;
}

export function PythiaDashboard({
  pythiaUrl,
  forceSample = false,
}: PythiaDashboardProps = {}) {
  const { t } = useT("pythia");
  const [resolvedUrl, setResolvedUrl] = useState<string | null>(pythiaUrl ?? null);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [whatif, setWhatif] = useState<WhatIfResult | null>(null);
  const [whatifHistory, setWhatifHistory] = useState<WhatIfResult[]>([]);
  const [accentByPersona, setAccentByPersona] = useState(false);

  // Resolve the loopback URL once on mount if the caller did not
  // supply one explicitly. The dashboard is lazy-loaded only when the
  // pythia_oracle flag is on, so calling the IPC here is safe.
  useEffect(() => {
    if (pythiaUrl !== undefined) return;
    if (forceSample) return;
    let cancelled = false;
    void window.experimentalAPI.pythia
      .getURL()
      .then((url) => {
        if (!cancelled) setResolvedUrl(url ?? null);
      })
      .catch(() => {
        if (!cancelled) setResolvedUrl(null);
      });
    return () => {
      cancelled = true;
    };
  }, [pythiaUrl, forceSample]);

  const live = usePythiaSse(resolvedUrl, {
    enabled: !forceSample && resolvedUrl !== null,
  });

  const livePredictions: ReadonlyArray<PythiaPrediction> = live.snapshot?.predictions ?? [];

  // Source of truth for what the globe renders. We only swap from
  // sample to live once we actually have live data, so the globe
  // doesn't blink to empty during the first reconnect.
  const useSample = forceSample || livePredictions.length === 0;
  const basePredictions = useSample ? SAMPLE_PREDICTIONS : livePredictions;

  // WhatIf predictions sit on top of the source list — they are the
  // user's own counterfactual, so they're always shown when present
  // (even when we're rendering sample data). The selection state
  // prefers a WhatIf match when the id is in that list.
  const predictions = whatif ? [...whatif.predictions, ...basePredictions] : basePredictions;
  const selected =
    predictions.find((p) => p.id === selectedId) ?? predictions[0] ?? null;

  return (
    <div className="flex h-full w-full flex-col gap-4 overflow-hidden p-6">
      <DashboardHeader
        predictionCount={predictions.length}
        usingSample={useSample}
        connection={live.connection}
        managerReady={resolvedUrl !== null}
        lastRunMs={live.snapshot?.last_run_ms ?? null}
        accentByPersona={accentByPersona}
        onToggleAccent={() => setAccentByPersona((v) => !v)}
      />

      {!useSample &&
        (live.connection === "error" || live.connection === "closed") && (
          <StreamInterruptedBanner />
        )}

      {whatif && (
        <WhatIfBanner
          scenario={whatif.scenario}
          narrative={whatif.narrative}
          count={whatif.predictions.length}
          onDismiss={() => setWhatif(null)}
        />
      )}

      {whatifHistory.length > 0 && (
        <WhatIfHistory
          entries={whatifHistory}
          onSelect={(id) => setSelectedId(id)}
          onClear={() => setWhatifHistory([])}
        />
      )}

      <div className="grid min-h-0 flex-1 grid-cols-1 gap-4 lg:grid-cols-[2fr_1fr]">
        <div className="flex min-h-0 flex-col gap-3">
          <GlobeSVG
            predictions={predictions}
            selectedId={selected?.id ?? null}
            onSelect={setSelectedId}
            accentByPersona={accentByPersona}
          />
          <WhatIfPanel onResult={(r: WhatIfResult) => {
            setWhatif(r);
            setWhatifHistory((prev) => [r, ...prev].slice(0, 5));
            if (r.predictions[0]) setSelectedId(r.predictions[0].id);
          }} disabled={forceSample} />
        </div>

        <aside className="flex min-h-0 flex-col gap-4 overflow-y-auto rounded-lg border border-border bg-card/40 p-4">
          {selected ? (
            <PredictionCard prediction={selected} />
          ) : (
            <p className="text-sm text-muted-foreground">{t(($) => $.no_predictions_yet)}</p>
          )}
        </aside>
      </div>
    </div>
  );
}

function WhatIfBanner({
  scenario,
  narrative,
  count,
  onDismiss,
}: {
  scenario: string;
  narrative: string;
  count: number;
  onDismiss: () => void;
}) {
  return (
    <div className="flex items-start justify-between gap-3 rounded-lg border border-fuchsia-500/30 bg-fuchsia-500/5 px-3 py-2 text-xs">
      <div className="flex flex-col gap-1">
        <span className="font-medium text-fuchsia-300">
          What-if · {count} knock-on prediction{count === 1 ? "" : "s"}
        </span>
        <span className="text-foreground">
          “{scenario}”
          {narrative && (
            <>
              {" — "}
              <span className="text-muted-foreground">{narrative}</span>
            </>
          )}
        </span>
      </div>
      <button
        type="button"
        onClick={onDismiss}
        className="rounded px-2 py-0.5 text-[10px] uppercase tracking-wide text-muted-foreground hover:text-foreground"
      >
        clear
      </button>
    </div>
  );
}

function WhatIfHistory({
  entries,
  onSelect,
  onClear,
}: {
  entries: ReadonlyArray<WhatIfResult>;
  onSelect: (id: string) => void;
  onClear: () => void;
}) {
  return (
    <div className="flex flex-col gap-1 rounded-lg border border-border/60 bg-card/30 p-2">
      <div className="flex items-center justify-between">
        <span className="text-[10px] uppercase tracking-wide text-muted-foreground">
          What-if history · last {entries.length}
        </span>
        <button
          type="button"
          onClick={onClear}
          className="text-[10px] text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
        >
          clear
        </button>
      </div>
      <div className="flex flex-wrap gap-1">
        {entries.map((e, idx) => (
          <button
            key={`${e.scenario}-${idx}`}
            type="button"
            onClick={() => {
              const id = e.predictions[0]?.id;
              if (id) onSelect(id);
            }}
            className="max-w-[18ch] truncate rounded bg-fuchsia-500/10 px-2 py-0.5 text-[11px] text-fuchsia-200 ring-1 ring-fuchsia-500/20 hover:bg-fuchsia-500/15"
            title={`${e.scenario} — ${e.predictions.length} predictions`}
          >
            “{e.scenario}”
          </button>
        ))}
      </div>
    </div>
  );
}

function StreamInterruptedBanner() {
  return (
    <div
      className="flex items-center justify-between gap-3 rounded-lg border border-amber-500/30 bg-amber-500/5 px-3 py-2 text-xs"
      role="status"
    >
      <div className="flex items-center gap-2">
        <span
          className="inline-block h-2 w-2 animate-pulse rounded-full bg-amber-400"
          aria-hidden
        />
        <span className="text-amber-300">
          Stream interrupted — the dashboard keeps showing the last known
          predictions while we retry.
        </span>
      </div>
    </div>
  );
}

interface DashboardHeaderProps {
  predictionCount: number;
  usingSample: boolean;
  connection: ReturnType<typeof usePythiaSse>["connection"];
  managerReady: boolean;
  lastRunMs: number | null;
  accentByPersona: boolean;
  onToggleAccent: () => void;
}

function DashboardHeader({
  predictionCount,
  usingSample,
  connection,
  managerReady,
  lastRunMs,
  accentByPersona,
  onToggleAccent,
}: DashboardHeaderProps) {
  const { t } = useT("pythia");
  let badge: { label: string; tone: "neutral" | "warn" | "ok" } = {
    label: t(($) => $.badge_sample),
    tone: "neutral",
  };
  if (!usingSample) {
    if (connection === "open") {
      badge = { label: t(($) => $.badge_live), tone: "ok" };
    } else if (connection === "connecting" || connection === "idle") {
      badge = { label: `${t(($) => $.badge_connecting)} (${connection})`, tone: "neutral" };
    } else if (connection === "error") {
      badge = { label: t(($) => $.badge_stream_error), tone: "warn" };
    } else if (connection === "closed") {
      badge = { label: t(($) => $.badge_stream_closed), tone: "warn" };
    }
  } else if (managerReady && connection !== "open") {
    badge = { label: t(($) => $.manager_up_no_runs), tone: "neutral" };
  }

  const badgeClass =
    badge.tone === "ok"
      ? "bg-emerald-500/15 text-emerald-300"
      : badge.tone === "warn"
        ? "bg-amber-500/15 text-amber-300"
        : "bg-muted text-muted-foreground";

  return (
    <header className="flex flex-col gap-1">
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <h2 className="text-lg font-semibold text-foreground">
            Pythia — forecast globe
          </h2>
          <button
            type="button"
            onClick={onToggleAccent}
            className={
              accentByPersona
                ? "rounded px-1.5 py-0.5 text-[10px] uppercase tracking-wide ring-1 ring-fuchsia-500/40 bg-fuchsia-500/15 text-fuchsia-300"
                : "rounded px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-muted-foreground hover:text-foreground"
            }
            title="Color ring fills by the dominant persona instead of horizon"
          >
            persona
          </button>
        </div>
        <span
          className={`rounded px-2 py-0.5 text-[11px] uppercase tracking-wide ${badgeClass}`}
        >
          {badge.label}
        </span>
      </div>
      <p className="text-xs text-muted-foreground">
        {predictionCount} prediction{predictionCount === 1 ? "" : "s"} on the globe.
        Click a ring to read the swarm&apos;s deliberation.
        {!usingSample && lastRunMs !== null && (
          <>
            {" · "}
            <FreshnessTimestamp sinceMs={lastRunMs} />
          </>
        )}
      </p>
    </header>
  );
}

function FreshnessTimestamp({ sinceMs }: { sinceMs: number }) {
  const now = Date.now();
  const diffSec = Math.max(0, Math.round((now - sinceMs) / 1000));
  let label: string;
  if (diffSec < 5) label = "just now";
  else if (diffSec < 60) label = `${diffSec}s ago`;
  else if (diffSec < 3600) label = `${Math.round(diffSec / 60)}m ago`;
  else label = `${Math.round(diffSec / 3600)}h ago`;
  return (
    <span className="font-mono text-[11px] text-muted-foreground/80" title={new Date(sinceMs).toLocaleString()}>
      {label}
    </span>
  );
}

function PredictionCard({ prediction }: { prediction: PythiaPrediction }) {
  const drift =
    prediction.base_probability !== null
      ? prediction.probability - prediction.base_probability
      : null;

  return (
    <article className="flex flex-col gap-3">
      <div className="flex items-center gap-2 text-[11px] uppercase tracking-wide text-muted-foreground">
        <span>{prediction.horizon}</span>
        <span aria-hidden>·</span>
        <span>{prediction.location || "Global"}</span>
      </div>
      <h3 className="text-sm font-semibold leading-snug text-foreground">
        {prediction.title}
      </h3>

      <div className="flex items-baseline gap-3">
        <span className="text-2xl font-mono text-foreground">
          {(prediction.probability * 100).toFixed(0)}%
        </span>
        {drift !== null && (
          <span
            className={
              drift >= 0
                ? "text-xs font-mono text-emerald-400"
                : "text-xs font-mono text-rose-400"
            }
          >
            {drift >= 0 ? "+" : ""}
            {(drift * 100).toFixed(0)}pp vs oracle
          </span>
        )}
      </div>

      <p className="text-xs leading-relaxed text-muted-foreground">
        {prediction.reasoning}
      </p>

      <hr className="border-border" />

      <SwarmVoteBars
        agents={prediction.agents}
        consensus={prediction.probability}
        split={prediction.split}
      />
    </article>
  );
}