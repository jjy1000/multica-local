import { useState } from "react";
import { useSearchParams } from "react-router-dom";
import { AlertTriangle, Loader2, RefreshCw } from "lucide-react";
import {
  useExperimentalFlag,
  useTimesfmForecastRuns,
} from "@multica/core/experimental";
import type {
  TimesfmForecastRun,
  TimesfmSeriesPoint,
} from "@multica/core/types/api";
import { useT } from "@multica/views/i18n";
import {
  IssueBreadcrumb,
  useDeepLinkRun,
} from "@multica/views/experimental/components";

// TimesfmView (0.5.82 WL2) — records-only lab surface for the TimesFM
// forecasting engine.
//
// Product law implemented here (0.5.82 ICPs):
//   - ICP-1: purely issue-driven. There is NO manual "run forecast"
//     trigger in this view — runs fire from issues via the
//     `timesfm_oracle` agent assignee (multica-timesfm skill). This
//     surface only lists what already landed in timesfm_forecast_run.
//   - ICP-2: binds the issue via `?issue=<id>` and mounts the shared
//     IssueBreadcrumb back-link (push() arms the workspace release
//     guard, matching every other lab surface).
//   - ICP-3: every run row is deep-linkable — the view reads `?run=`
//     via useDeepLinkRun, scrolls to and highlights the matching row,
//     and auto-expands it so a LabRunLink jump lands on the chart.
//   - ICP-4: history is append-only DESC records — GET /runs works
//     even when the engine subprocess is down, so this view never
//     needs a live engine.
//
// The chart is a plain <svg> quantile band (upper/lower polygon +
// median line) — no chart dependency, swarm-topology precedent.
//
// Route: /experimental/timesfm-lab. NEVER /experimental/timesfm —
// that path is the bare REST proxy the agent subprocess calls (same
// split as semantica / semantica-explorer).

const MAX_RENDERED_SERIES = 4;

export function TimesfmView() {
  const timesfmEnabled = useExperimentalFlag("timesfm", false);
  const { t } = useT("timesfm");
  const [searchParams] = useSearchParams();
  const issueId = searchParams.get("issue");

  if (!timesfmEnabled) {
    // Flag-off: bare placeholder. The engine subprocess is owned by the
    // desktop manager; without the flag there is nothing to render here.
    return (
      <div className="flex h-full w-full items-center justify-center px-6 text-sm text-muted-foreground">
        <span className="max-w-md text-center">{t(($) => $.flag_off)}</span>
      </div>
    );
  }

  return (
    <div className="flex h-full w-full flex-col overflow-y-auto">
      {!issueId && (
        <div className="border-b border-border bg-muted/40 px-6 py-2 text-xs text-muted-foreground">
          <span className="font-medium text-foreground/90">
            {t(($) => $.unbound_title)} ·{" "}
          </span>
          {t(($) => $.unbound_hint)}
        </div>
      )}
      <div className="px-6 pt-3">
        <IssueBreadcrumb />
      </div>
      {issueId ? (
        <TimesfmRecords issueId={issueId} />
      ) : (
        <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">
          {t(($) => $.unbound_title)}
        </div>
      )}
    </div>
  );
}

function TimesfmRecords({ issueId }: { issueId: string }) {
  const { t } = useT("timesfm");
  const runsQuery = useTimesfmForecastRuns(issueId);
  // ICP-3: ?run=<id> scrolls to + highlights the matching row.
  const { rowRef, isDeepLinked } = useDeepLinkRun<HTMLButtonElement>();
  const [expandedId, setExpandedId] = useState<string | null>(null);

  const runs = runsQuery.data ?? [];
  const latest = runs[0] ?? null;
  // Engine/model-down signal straight off the newest persisted row: the
  // engine answered via its seasonal-naive fallback (weights not seeded,
  // RAM floor, or torch-stack failure). The records keep flowing — only
  // the provenance changes.
  const engineFallback =
    latest?.result?.model_present === false ||
    latest?.provenance === "seasonal_naive";

  // The deep-linked run auto-expands (a LabRunLink jump means "show me
  // this run"); otherwise only an explicit click expands a row.
  const expandedRun =
    runs.find((r) => r.id === expandedId) ??
    runs.find((r) => isDeepLinked(r.id)) ??
    null;

  if (runsQuery.isLoading) {
    return (
      <div className="flex items-center gap-2 px-6 py-4 text-sm text-muted-foreground">
        <Loader2 className="size-3.5 animate-spin" aria-hidden />
        {t(($) => $.loading)}
      </div>
    );
  }

  if (runsQuery.isError) {
    return (
      <div className="mx-6 my-3 flex items-center justify-between gap-2 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2">
        <p className="text-xs text-destructive">{t(($) => $.load_failed)}</p>
        <button
          type="button"
          onClick={() => runsQuery.refetch()}
          className="shrink-0 rounded-md border border-input bg-background px-2 py-0.5 text-xs font-medium text-foreground hover:bg-muted"
        >
          {t(($) => $.retry)}
        </button>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-3 px-6 py-4">
      <div className="flex items-baseline justify-between gap-3">
        <h2 className="text-sm font-semibold text-foreground">
          {t(($) => $.records_title)}
        </h2>
        <span className="text-[11px] text-muted-foreground">
          {t(($) => $.run_count, { runs: String(runs.length) })}
        </span>
      </div>

      {engineFallback && (
        <div className="flex items-start gap-2 rounded-md border border-amber-500/40 bg-amber-500/5 px-3 py-2 text-[11px] leading-snug text-amber-700 dark:text-amber-300">
          <AlertTriangle className="mt-0.5 size-3 shrink-0" aria-hidden />
          <span>{t(($) => $.weights_hint)}</span>
        </div>
      )}

      {runs.length === 0 ? (
        <div className="space-y-1.5 rounded-md border border-dashed border-border/60 px-3 py-3">
          <p className="text-xs text-muted-foreground">{t(($) => $.empty)}</p>
          <p className="text-[11px] leading-snug text-muted-foreground/80">
            {t(($) => $.empty_hint)}
          </p>
        </div>
      ) : (
        <div className="flex flex-col gap-1.5">
          {runs.map((run) => (
            <RunRow
              key={run.id}
              run={run}
              refCallback={rowRef(run.id)}
              deepLinked={isDeepLinked(run.id)}
              expanded={expandedRun?.id === run.id}
              onToggle={() =>
                setExpandedId((cur) => (cur === run.id ? null : run.id))
              }
            />
          ))}
        </div>
      )}

      {expandedRun && <RunDetail run={expandedRun} />}
    </div>
  );
}

function RunRow({
  run,
  refCallback,
  deepLinked,
  expanded,
  onToggle,
}: {
  run: TimesfmForecastRun;
  refCallback: (el: HTMLButtonElement | null) => void;
  deepLinked: boolean;
  expanded: boolean;
  onToggle: () => void;
}) {
  const { t } = useT("timesfm");
  const horizon = run.result?.horizon ?? run.horizons;
  return (
    <button
      type="button"
      ref={refCallback}
      data-testid={`timesfm-run-row-${run.id}`}
      data-run-id={run.id}
      aria-current={deepLinked ? "true" : undefined}
      onClick={onToggle}
      className={`flex items-center justify-between gap-2 rounded border px-2.5 py-1.5 text-left text-xs transition-colors ${
        expanded || deepLinked
          ? "border-primary/40 bg-primary/5"
          : "border-border/60 hover:bg-accent/50"
      }`}
    >
      <span className="text-foreground/90">{formatRunTime(run.created_at)}</span>
      <span className="flex items-center gap-2 text-muted-foreground">
        <span className="font-mono">
          {t(($) => $.horizon_label)} {horizon}
        </span>
        <span className="rounded bg-muted px-1 py-0.5 text-[10px]">
          {run.provenance}
        </span>
      </span>
    </button>
  );
}

function RunDetail({ run }: { run: TimesfmForecastRun }) {
  const { t } = useT("timesfm");
  const series = run.result?.series ?? [];

  if (series.length === 0) {
    return (
      <div className="border-t border-border/60 pt-3 text-xs text-muted-foreground">
        {t(($) => $.no_series)}
      </div>
    );
  }

  const shown = series.slice(0, MAX_RENDERED_SERIES);

  return (
    <div className="flex flex-col gap-3 border-t border-border/60 pt-3">
      <p className="text-[11px] text-muted-foreground">
        {t(($) => $.chart_legend)}
      </p>
      {series.length > MAX_RENDERED_SERIES && (
        <p className="text-[11px] text-muted-foreground">
          {t(($) => $.series_count, {
            count: String(series.length),
            shown: String(shown.length),
          })}
        </p>
      )}
      {shown.map((s, i) => (
        <div key={i} className="flex flex-col gap-1">
          <div className="flex items-center gap-2 text-[11px] text-muted-foreground">
            <span className="font-mono">
              {t(($) => $.series_label, { index: String(i + 1) })}
            </span>
            {s.provenance && (
              <span className="rounded bg-muted px-1 py-0.5 text-[10px]">
                {s.provenance}
              </span>
            )}
            {s.dates && s.dates.length > 0 && (
              <span className="truncate font-mono text-[10px]">
                {s.dates[0]} → {s.dates[s.dates.length - 1]}
              </span>
            )}
          </div>
          <QuantileBandChart series={s} />
        </div>
      ))}
    </div>
  );
}

// Plain-<svg> quantile band chart: the 90% band as an upper/lower
// polygon, the 80% band layered on top, and the median as a line. No
// chart dependency (swarm-topology precedent). All NaN/shape drift is
// guarded — a degenerate band degrades to the median-only line.
function QuantileBandChart({
  series,
  width = 560,
  height = 140,
}: {
  series: TimesfmSeriesPoint;
  width?: number;
  height?: number;
}) {
  const median = finiteSeries(series.quantiles?.median) ?? finiteSeries(series.point) ?? [];
  if (median.length === 0) return null;

  const lower90 = bandOrFallback(series.quantiles?.lower_90, median);
  const upper90 = bandOrFallback(series.quantiles?.upper_90, median);
  const lower80 = bandOrFallback(series.quantiles?.lower_80, median);
  const upper80 = bandOrFallback(series.quantiles?.upper_80, median);

  const all = [...median, ...lower90, ...upper90];
  let min = Math.min(...all);
  let max = Math.max(...all);
  if (!Number.isFinite(min) || !Number.isFinite(max) || min === max) {
    min = min || 0;
    max = (max || 0) + 1;
  }
  const pad = (max - min) * 0.08;
  min -= pad;
  max += pad;

  const padX = 6;
  const n = median.length;
  const x = (i: number) =>
    n === 1 ? width / 2 : padX + (i / (n - 1)) * (width - 2 * padX);
  const y = (v: number) =>
    height - 6 - ((v - min) / (max - min)) * (height - 12);

  const bandPath = (lower: number[], upper: number[]) => {
    const pts = [
      ...upper.map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`),
      ...lower
        .map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`)
        .reverse(),
    ];
    return `M ${pts.join(" L ")} Z`;
  };

  const medianLine = median
    .map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`)
    .join(" ");

  return (
    <svg
      viewBox={`0 0 ${width} ${height}`}
      role="img"
      className="w-full rounded-md border border-border/60 bg-background"
    >
      <path d={bandPath(lower90, upper90)} className="fill-purple-500/15" />
      <path d={bandPath(lower80, upper80)} className="fill-purple-500/25" />
      <polyline
        points={medianLine}
        fill="none"
        strokeWidth="2"
        className="stroke-purple-600 dark:stroke-purple-400"
      />
    </svg>
  );
}

function finiteSeries(values?: number[]): number[] | undefined {
  if (!Array.isArray(values) || values.length === 0) return undefined;
  const out = values.filter((v) => Number.isFinite(v));
  return out.length === values.length ? out : out.length > 0 ? out : undefined;
}

// A band edge only renders when it is a finite series parallel to the
// median; otherwise fall back to the median itself (degenerate band →
// the polygon collapses onto the median line, the chart still draws).
function bandOrFallback(band: number[] | undefined, median: number[]): number[] {
  const b = finiteSeries(band);
  return b && b.length === median.length ? b : median;
}

function formatRunTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString(undefined, {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}
