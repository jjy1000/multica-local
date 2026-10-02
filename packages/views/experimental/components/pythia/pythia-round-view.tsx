"use client";

// pythia-round-view — the live-deduction visualization pieces of the
// Pythia issue panel (0.5.111, SocialSim PDStep3 alignment):
//
//   PythiaRoundTimeline — per-round cards (active / completed) with a
//     pulse indicator on the round in flight and pending slots for the
//     rounds still to come.
//   PythiaCouncilPanel  — the 4-persona vote sheet for one round:
//     per-persona probability bar + argument, the Brier-weighted
//     consensus headline vs the oracle's solo estimate, and the split
//     badge when the council genuinely disagrees (spread ≥ 0.30).
//   PythiaTrajectory    — pure-SVG probability/confidence lines across
//     rounds (the envelopes have carried this series since 0.3.55; it
//     just never had a chart).
//
// All animation is class-transition only (no animation library), and all
// strings go through the i18n selector arrow form.

import { useState } from "react";
import { motion, useReducedMotion } from "motion/react";
import { Brain, Loader2, Sparkles } from "lucide-react";
import { UI_EASE_OUT, UI_MOTION_DURATION } from "@multica/ui/lib/motion";
import type { PythiaCouncil, PythiaForecastEnvelope } from "@multica/core/types/api";
import { useT } from "../../../i18n";

function pct(v: number): number {
  return Math.round(Math.max(0, Math.min(1, v)) * 100);
}

/** One round card inside the timeline. */
function RoundCard({
  index,
  envelope,
  active,
}: {
  index: number;
  envelope?: PythiaForecastEnvelope;
  active: boolean;
}) {
  const { t } = useT("experimental");
  if (!envelope) {
    return (
      <div
        className="rounded-md border border-dashed border-purple-500/30 bg-purple-500/5 px-2 py-1.5"
        data-testid="pythia-round-pending"
      >
        <div className="flex items-center gap-1.5 text-[11px] font-medium text-purple-700 dark:text-purple-300">
          <Loader2 className="size-3 animate-spin" aria-hidden />
          {t(($) => $.pythia_lab.round_pending, { round: index + 1 })}
        </div>
      </div>
    );
  }
  return (
    <div
      className={
        active
          ? "rounded-md border border-purple-500/40 bg-purple-500/5 px-2 py-1.5"
          : "rounded-md border border-border px-2 py-1.5"
      }
      data-testid="pythia-round-card"
    >
      <div className="flex items-baseline justify-between gap-2 text-[11px]">
        <span className="flex min-w-0 items-center gap-1.5">
          {active ? (
            <Loader2 className="size-3 shrink-0 animate-spin text-purple-500" aria-hidden />
          ) : (
            <span className="flex size-3 shrink-0 items-center justify-center rounded-full bg-purple-500/15 font-mono text-[8px] text-purple-600 dark:text-purple-300">
              {index + 1}
            </span>
          )}
          <span className="truncate text-foreground/90">{envelope.scenario}</span>
        </span>
        <span className="shrink-0 font-medium text-foreground">{pct(envelope.probability)}%</span>
      </div>
      <div className="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-muted">
        <div
          className="h-full rounded-full bg-purple-500 transition-all duration-500"
          style={{ width: `${pct(envelope.probability)}%` }}
        />
      </div>
      {envelope.narrative && (
        <p className="mt-1 line-clamp-2 whitespace-pre-wrap text-[10px] leading-relaxed text-foreground/70">
          {envelope.narrative}
        </p>
      )}
      <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[10px] text-muted-foreground">
        <span className="font-mono">{envelope.horizon}</span>
        <span>
          {t(($) => $.lab_output_panel.pythia_confidence_label)} {pct(envelope.confidence)}%
        </span>
        {envelope.synthetic_oracle_failover === true && (
          <span className="rounded bg-amber-500/10 px-1 py-0.5 text-amber-600 dark:text-amber-400">
            {t(($) => $.lab_output_panel.pythia_synthetic_hint)}
          </span>
        )}
      </div>
      {envelope.council && <PythiaCouncilPanel council={envelope.council} baseProbability={envelope.base_probability} />}
    </div>
  );
}

export function PythiaRoundTimeline({
  envelopes,
  totalRounds,
  running,
}: {
  envelopes: PythiaForecastEnvelope[];
  totalRounds: number;
  running: boolean;
}) {
  const { t } = useT("experimental");
  const pending = running ? Math.max(0, totalRounds - envelopes.length) : 0;
  return (
    <div className="space-y-1.5">
      <p className="text-[11px] font-medium text-muted-foreground">
        {t(($) => $.pythia_lab.timeline_label, {
          done: envelopes.length,
          total: Math.max(totalRounds, envelopes.length),
        })}
      </p>
      <div className="space-y-1.5">
        {envelopes.map((e, i) => (
          <RoundCard key={e.id || i} index={i} envelope={e} active={running && i === envelopes.length - 1} />
        ))}
        {Array.from({ length: pending }, (_, i) => (
          <RoundCard key={`pending-${i}`} index={envelopes.length + i} active={running} />
        ))}
        {!running && envelopes.length === 0 && (
          <p className="text-xs text-muted-foreground">{t(($) => $.lab_output_panel.empty)}</p>
        )}
      </div>
    </div>
  );
}

/** Stance of one persona's vote vs the oracle baseline (percentage
 *  points). Drives the typed act badge on the action card (MiroFish
 *  Step3 per-action-card pattern, 0.5.131): a persona leaning ≥5pp
 *  above baseline reads as "leans higher", ≤5pp below as "leans
 *  lower", else "near baseline". ±0.05 keeps the badge honest — the
 *  engine's own split threshold is 0.30 spread. Exported for tests. */
export function voteStance(
  vote: { probability: number },
  baseProbability: number | null | undefined,
): "support" | "challenge" | "neutral" | null {
  if (baseProbability == null || !Number.isFinite(baseProbability)) return null;
  // Integer tenth-of-a-percent compare — a raw float difference lets
  // 0.45-0.5 = -0.04999… slip past the threshold (unit-pinned).
  const d10 = Math.round((vote.probability - baseProbability) * 1000);
  if (d10 >= 50) return "support";
  if (d10 <= -50) return "challenge";
  return "neutral";
}

export function PythiaCouncilPanel({
  council,
  baseProbability,
}: {
  council: PythiaCouncil;
  baseProbability?: number | null;
}) {
  const { t } = useT("experimental");
  const reduceMotion = useReducedMotion() ?? false;
  const [open, setOpen] = useState(false);
  const consensus = council.consensus;
  if (council.votes.length === 0 || consensus == null) return null;

  return (
    <div className="mt-1.5 rounded border border-border/70 bg-background/60 px-1.5 py-1" data-testid="pythia-council">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-1.5 text-left text-[10px] font-medium text-foreground/80"
        aria-expanded={open}
      >
        <Brain className="size-3 shrink-0 text-purple-500" aria-hidden />
        <span className="min-w-0 flex-1 truncate">
          {t(($) => $.pythia_lab.council_headline, {
            consensus: pct(consensus),
            votes: council.votes.length,
          })}
        </span>
        {baseProbability != null && (
          <span className="shrink-0 font-mono text-[9px] text-muted-foreground">
            {t(($) => $.pythia_lab.council_base_delta, {
              base: pct(baseProbability),
              delta: pct(consensus) - pct(baseProbability),
            })}
          </span>
        )}
        {council.split && (
          <span className="shrink-0 rounded bg-amber-500/15 px-1 py-0.5 text-[9px] text-amber-600 dark:text-amber-400">
            {t(($) => $.pythia_lab.council_split)}
          </span>
        )}
      </button>
      {open && (
        <div className="mt-1 space-y-1">
          {council.votes.map((v, i) => {
            const stance = voteStance(v, baseProbability);
            const stanceLabel =
              stance === "support"
                ? t(($) => $.pythia_lab.council_act_support)
                : stance === "challenge"
                  ? t(($) => $.pythia_lab.council_act_challenge)
                  : stance === "neutral"
                    ? t(($) => $.pythia_lab.council_act_neutral)
                    : null;
            return (
              <motion.div
                key={v.persona}
                initial={reduceMotion ? false : { opacity: 0, x: -4 }}
                animate={{ opacity: 1, x: 0 }}
                transition={{
                  duration: UI_MOTION_DURATION.fast,
                  delay: reduceMotion ? 0 : Math.min(i * 0.05, 0.25),
                  ease: UI_EASE_OUT,
                }}
                className="space-y-0.5 rounded border border-border/50 bg-background/60 px-1.5 py-1"
                data-testid="pythia-council-vote"
                data-stance={stance ?? "none"}
              >
                <div className="flex items-baseline justify-between gap-2 text-[10px]">
                  <span className="flex min-w-0 items-center gap-1">
                    <span className="truncate font-medium text-foreground/85">{v.persona}</span>
                    {stanceLabel ? (
                      <span
                        className={
                          "shrink-0 rounded px-1 py-px text-[8px] font-medium " +
                          (stance === "support"
                            ? "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400"
                            : stance === "challenge"
                              ? "bg-amber-500/15 text-amber-600 dark:text-amber-400"
                              : "bg-muted text-muted-foreground")
                        }
                      >
                        {stanceLabel}
                      </span>
                    ) : null}
                  </span>
                  <span className="shrink-0 font-mono text-muted-foreground">{pct(v.probability)}%</span>
                </div>
                <div className="h-1 w-full overflow-hidden rounded-full bg-muted">
                  <div
                    className="h-full rounded-full bg-purple-400/70"
                    style={{ width: `${pct(v.probability)}%` }}
                  />
                </div>
                {v.note && <p className="text-[9px] leading-snug text-muted-foreground">{v.note}</p>}
              </motion.div>
            );
          })}
        </div>
      )}
    </div>
  );
}

/** Pure-SVG probability/confidence trajectory. No chart library — two
 *  polylines + dots on a normalized 0-100 band. */
export function PythiaTrajectory({
  envelopes,
  totalRounds,
}: {
  envelopes: PythiaForecastEnvelope[];
  totalRounds: number;
}) {
  const { t } = useT("experimental");
  if (envelopes.length < 2) return null;
  const W = 280;
  const H = 64;
  const pad = 6;
  const slots = Math.max(totalRounds, envelopes.length, 2);
  const x = (i: number) => pad + (i / (slots - 1)) * (W - pad * 2);
  const y = (v: number) => H - pad - Math.max(0, Math.min(1, v)) * (H - pad * 2);
  const line = (get: (e: PythiaForecastEnvelope) => number) =>
    envelopes.map((e, i) => `${i === 0 ? "M" : "L"}${x(i).toFixed(1)},${y(get(e)).toFixed(1)}`).join(" ");

  return (
    <div className="space-y-1" data-testid="pythia-trajectory">
      <p className="flex items-center gap-1 text-[11px] font-medium text-muted-foreground">
        <Sparkles className="size-3" aria-hidden />
        {t(($) => $.pythia_lab.trajectory_label)}
      </p>
      <svg viewBox={`0 0 ${W} ${H}`} className="w-full" role="img" aria-label={t(($) => $.pythia_lab.trajectory_label)}>
        <line x1={pad} y1={y(0.5)} x2={W - pad} y2={y(0.5)} stroke="currentColor" strokeDasharray="2 4" className="text-border" strokeWidth="1" />
        <path d={line((e) => e.probability)} fill="none" className="text-purple-500" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
        <path d={line((e) => e.confidence)} fill="none" className="text-muted-foreground/50" stroke="currentColor" strokeWidth="1.5" strokeDasharray="4 3" />
        {envelopes.map((e, i) => (
          <circle key={e.id || i} cx={x(i)} cy={y(e.probability)} r="2.5" className="fill-purple-500" />
        ))}
      </svg>
      <div className="flex gap-3 text-[10px] text-muted-foreground">
        <span className="flex items-center gap-1">
          <span className="inline-block h-0.5 w-3 bg-purple-500" aria-hidden />
          {t(($) => $.pythia_lab.trajectory_probability)}
        </span>
        <span className="flex items-center gap-1">
          <span className="inline-block h-0.5 w-3 bg-muted-foreground/40" aria-hidden />
          {t(($) => $.lab_output_panel.pythia_confidence_label)}
        </span>
      </div>
    </div>
  );
}
