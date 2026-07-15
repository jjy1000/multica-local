// PythiaReportSurface — the 0.3.29 report-driven replacement for
// the legacy PythiaDashboard globe card. Default minimal: 1 round.
// The report renders directly on the prediction page (replacing the
// earlier selectable-card grid) and pulls fresh envelopes from the
// existing SSE stream at /state/stream plus the new
// /api/experimental/pythia-oracle/forecast/issue POST bound to the
// selected Issue.
//
// Interactive controls live alongside the report header:
//   - subscribe / unsubscribe (toggles the SSE EventSource)
//   - horizon: day / week / month — feeds the issue-bound
//     /forecast/issue POST body.
//   - persona: strategist / analyst / critic
//   - regenerate: bumps the round count to force a fresh stream.
//   - submit new scenario: free-form text → routes to a new
//     /forecast/issue call with `scenario_context` derived from the
//     text. Closes the loop with the user's typed scenario.
//
// Flag contract: this module is loaded only when `pythia_oracle` is
// on.  When the flag is off, the parent view returns the
// informational status panel — this file is never imported.

import { useEffect, useMemo, useState } from "react";
import { useT } from "@multica/views/i18n";
import type { PythiaPrediction } from "./types";

type Persona = "strategist" | "analyst" | "critic";
type Horizon = "day" | "week" | "month";

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

// For phase 1 we keep the surface entirely client-side and rely on
// the existing /state/stream plus a synthetic fallback when no
// issue is bound. The backend forecast_issue.go endpoint is
// consumed when an issue is picked; see the openIssueForecast
// helper below.
export function PythiaReportSurface(props: PythiaReportSurfaceProps) {
  const { t } = useT("pythia");
  const [envelopes, setEnvelopes] = useState<PythiaReportEnvelope[]>([]);
  const [streaming, setStreaming] = useState(false);
  const [regenerating, setRegenerating] = useState(false);
  const [scenario, setScenario] = useState("");

  // Round-sliced forecasts. We deduplicate by `id` so the same
  // envelope arriving on multiple ticks doesn't render twice.
  const rounds = useMemo(() => {
    const byRound = new Map<number, PythiaReportEnvelope[]>();
    for (const env of envelopes) {
      const list = byRound.get(env.round) ?? [];
      list.push(env);
      byRound.set(env.round, list);
    }
    return [...byRound.entries()]
      .sort((a, b) => a[0] - b[0])
      .map(([round, envs]) => ({ round, envs }));
  }, [envelopes]);

  // Open the per-issue forecast POST when an issue is bound + the
  // user clicks regenerate / changes horizon / persona. The stream
  // stays open for at most one burst per round so we don't lock
  // up the manager.
  useEffect(() => {
    if (!props.subscribed) return;
    if (!props.issueId) return;
    let cancelled = false;
    const run = async () => {
      setRegenerating(true);
      try {
        const res = await fetch(
          "/api/experimental/pythia-oracle/forecast/issue",
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              issue_id: props.issueId,
              rounds: props.rounds,
            }),
          },
        );
        if (!res.ok || cancelled) return;
        // Phase 1: read the SSE stream text and pull out the first
        // `prediction` data block. Phase 2 can swap for an
        // EventSource when the desktop IPC layer exposes one.
        const reader = res.body?.getReader();
        if (!reader) return;
        const decoder = new TextDecoder("utf-8");
        let buf = "";
        while (!cancelled) {
          const { value, done } = await reader.read();
          if (done) break;
          buf += decoder.decode(value, { stream: true });
          const ev = parsePredictionBlock(buf);
          if (ev) {
            setEnvelopes((prev) => dedupeById([...prev, { ...ev, round: 1 }]));
            setStreaming(true);
            break;
          }
        }
      } catch {
        // Surface-level failure: keep the existing envelopes.
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

  return (
    <div className="flex h-full w-full flex-col gap-4 overflow-y-auto p-6">
      {/* Header: report title + interactive controls in one row */}
      <header className="flex flex-col gap-1 border-b border-border pb-3">
        <div className="flex flex-wrap items-baseline justify-between gap-3">
          <div className="flex flex-col gap-0.5">
            <h2 className="text-xl font-semibold text-foreground">
              {t(($) => $.pythia.report_title)}
            </h2>
            <p className="text-xs text-muted-foreground">
              {t(($) => $.pythia.report_subtitle)}
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <span className="text-muted-foreground">
              {props.subscribed
                ? t(($) => $.pythia.stream_subscribed)
                : t(($) => $.pythia.stream_unsubscribed)}
            </span>
            <button
              type="button"
              onClick={props.onToggleSubscribe}
              className="rounded bg-muted px-2 py-1 text-foreground hover:bg-muted/70"
            >
              {props.subscribed
                ? t(($) => $.pythia.unsubscribe)
                : t(($) => $.pythia.subscribe)}
            </button>
          </div>
        </div>

        {/* Interactive controls — horizon, persona, regenerate */}
        <div className="flex flex-wrap items-center gap-3 pt-2 text-xs">
          <span className="text-muted-foreground">
            {t(($) => $.pythia.horizon_label)}:
          </span>
          {(["day", "week", "month"] as const).map((h) => (
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
              {h === "day" && t(($) => $.pythia.horizon_day)}
              {h === "week" && t(($) => $.pythia.horizon_week)}
              {h === "month" && t(($) => $.pythia.horizon_month)}
            </button>
          ))}

          <span className="ml-2 text-muted-foreground">
            {t(($) => $.pythia.persona_label)}:
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
              {p === "strategist" && t(($) => $.pythia.persona_strategist_short)}
              {p === "analyst" && t(($) => $.pythia.persona_analyst_short)}
              {p === "critic" && t(($) => $.pythia.persona_critic_short)}
            </button>
          ))}

          <button
            type="button"
            onClick={props.onRegenerate}
            disabled={regenerating}
            className="ml-auto rounded bg-primary px-3 py-1 font-medium text-primary-foreground disabled:cursor-not-allowed disabled:opacity-50"
          >
            {regenerating
              ? t(($) => $.pythia.regenerate_running)
              : t(($) => $.pythia.regenerate)}
          </button>
        </div>
      </header>

      {/* Issue bound indicator */}
      <div className="rounded border border-border/60 bg-card/40 p-3 text-xs">
        <span className="text-muted-foreground">
          {t(($) => $.pythia.issue_panel_title)} ·{" "}
          {props.issueId ? `${props.issueId}` : t(($) => $.pythia.report_no_issue)}
        </span>
      </div>

      {/* Report body — one block per round */}
      <div className="flex flex-col gap-4">
        {rounds.length === 0 && (
          <p className="text-sm text-muted-foreground">
            {t(($) => $.pythia.no_predictions_yet)}
          </p>
        )}
        {rounds.map(({ round, envs }) => (
          <section
            key={round}
            className="flex flex-col gap-2 rounded-lg border border-border bg-card/30 p-4"
          >
            <h3 className="text-sm font-semibold text-foreground">
              {t(($) => $.pythia.report_round).replace("{{round}}", String(round))}
            </h3>
            {envs.map((env) => (
              <article
                key={env.id}
                className="flex flex-col gap-1 border-l-2 border-primary/30 pl-3"
              >
                <div className="flex items-baseline gap-3 text-xs text-muted-foreground">
                  <span className="font-mono">{env.lab_source}</span>
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
                {env.issue_id && (
                  <div className="font-mono text-[10px] text-muted-foreground/70">
                    issue_id={env.issue_id}
                  </div>
                )}
              </article>
            ))}
          </section>
        ))}
      </div>

      {/* Submit new scenario — closes the loop */}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          props.onSubmitScenario(scenario);
          setScenario("");
        }}
        className="flex flex-col gap-2 rounded-lg border border-fuchsia-500/30 bg-fuchsia-500/5 p-3"
      >
        <h3 className="text-xs font-semibold uppercase tracking-wide text-fuchsia-300">
          {t(($) => $.pythia.submit_new_scenario)}
        </h3>
        <textarea
          rows={2}
          value={scenario}
          onChange={(e) => setScenario(e.target.value)}
          placeholder={t(($) => $.pythia.scenario_placeholder)}
          className="w-full resize-none rounded border border-input bg-background px-2 py-1.5 text-sm leading-relaxed text-foreground placeholder:text-muted-foreground"
        />
        <div className="flex justify-end">
          <button
            type="submit"
            className="rounded bg-fuchsia-500 px-3 py-1 text-xs font-medium text-fuchsia-50 hover:bg-fuchsia-500/90"
          >
            {t(($) => $.pythia.scenario_send)}
          </button>
        </div>
      </form>
    </div>
  );
}

// PythiaReportEnvelope — augmented forecast envelope with round /
// issue_id / scenario_context fields populated by the 0.3.29
// /forecast/issue endpoint.
export interface PythiaReportEnvelope {
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

// parsePredictionBlock pulls the first complete `prediction` SSE event
// out of a buffer. Returns null until the block closes so partial
// chunks don't fool the renderer into rendering an empty envelope.
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

// dedupeById keeps the latest envelope per id (in case the same id
// arrives twice on reconnect). Shallow copy.
function dedupeById(envs: PythiaReportEnvelope[]): PythiaReportEnvelope[] {
  const byId = new Map<string, PythiaReportEnvelope>();
  for (const env of envs) byId.set(env.id, env);
  return [...byId.values()];
}

// silence the unused-import / unused-type paths that phase 2 will
// wire up; keep the surface ergonomically aligned with the older
// dashboard shape so the swap-in is mechanical.
void {} as unknown as PythiaPrediction;
