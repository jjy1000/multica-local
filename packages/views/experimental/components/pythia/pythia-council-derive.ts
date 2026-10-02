// pythia-council-derive — pure derivation for the council chamber canvas
// (0.5.132, ported from .omc/prototypes/pythia-council-visual.html after
// user approval). Everything here consumes the REAL forecast envelopes
// the SSE stream / run rows already carry — personas, votes, consensus,
// spread and split all come from PythiaCouncil (engine swarm.py writes
// them), so the chamber is a projection of live deliberation, never a
// simulation.
//
// Props-driven by design: usePythiaIssueLab owns the single SSE
// subscription — a second hook instance would open a second stream, so
// the canvas takes `envelopes` from its parent instead of subscribing.

import type { PythiaCouncil, PythiaForecastEnvelope } from "@multica/core/types/api";

export interface CouncilSeat {
  /** Persona name as written by the engine (Strategist/Economist/…). */
  persona: string;
  /** Latest vote probability (0..1); null before the persona ever voted. */
  probability: number | null;
  note: string;
  /** Stable seat hue by first-seen order (4-engine roster → 4 hues). */
  hue: string;
}

export const COUNCIL_SEAT_HUES = [
  "text-purple-600 dark:text-purple-400",
  "text-amber-600 dark:text-amber-400",
  "text-teal-600 dark:text-teal-400",
  "text-slate-600 dark:text-slate-400",
];

/** Product split threshold (engine swarm.py _SPLIT_TRACK = 0.30). */
export const COUNCIL_SPLIT_THRESHOLD = 0.3;

/** Latest envelope that carries a council, scanning newest-first. */
export function latestCouncil(
  envelopes: PythiaForecastEnvelope[],
): { envelope: PythiaForecastEnvelope; council: PythiaCouncil } | null {
  for (let i = envelopes.length - 1; i >= 0; i--) {
    const env = envelopes[i]!;
    if (env.council && env.council.votes.length > 0) {
      return { envelope: env, council: env.council };
    }
  }
  return null;
}

/**
 * Seats for the chamber: personas of the LATEST council, keeping each
 * persona's most recent vote across rounds (a persona absent from the
 * latest round keeps its earlier estimate — the engine roster is fixed,
 * a missing vote is a round in flight, not a resigned member).
 */
export function deriveCouncilSeats(envelopes: PythiaForecastEnvelope[]): CouncilSeat[] {
  const latest = latestCouncil(envelopes);
  if (!latest) return [];
  const order: string[] = [];
  const byPersona = new Map<string, { probability: number | null; note: string }>();
  // newest-first: a persona's FIRST-seen vote is its most recent one and
  // wins outright; probability 0 is the zod default (missing value), so a
  // 0 from the newest round keeps the seat unvoted instead of pinning 0%
  for (let i = envelopes.length - 1; i >= 0; i--) {
    const council = envelopes[i]!.council;
    if (!council) continue;
    for (const v of council.votes) {
      if (!v.persona) continue;
      if (byPersona.has(v.persona)) continue;
      order.push(v.persona);
      byPersona.set(v.persona, {
        probability: v.probability > 0 ? v.probability : null,
        note: v.note,
      });
    }
  }
  return order.map((persona, i) => ({
    persona,
    probability: byPersona.get(persona)!.probability,
    note: byPersona.get(persona)!.note,
    hue: COUNCIL_SEAT_HUES[i % COUNCIL_SEAT_HUES.length]!,
  }));
}

/**
 * Per-round consensus trajectory: council consensus when the round
 * carried one, else the envelope's oracle probability.
 */
export function deriveConsensusTrajectory(
  envelopes: PythiaForecastEnvelope[],
): number[] {
  return envelopes.map((e) => e.council?.consensus ?? e.probability);
}

/** Spread band [min, max] over the latest council's votes; null w/o votes. */
export function deriveSpreadBand(council: PythiaCouncil | null): {
  lo: number;
  hi: number;
  split: boolean;
} | null {
  if (!council || council.votes.length === 0) return null;
  const ps = council.votes.map((v) => v.probability);
  return {
    lo: Math.min(...ps),
    hi: Math.max(...ps),
    // server-stamped split flag wins; the local threshold is the fallback
    split: council.split || Math.max(...ps) - Math.min(...ps) >= COUNCIL_SPLIT_THRESHOLD,
  };
}

export type CouncilVerdict = "split" | "likely" | "unlikely" | "pending";

/**
 * Terminal verdict chip: a real disagreement outranks direction (the
 * product surfaces split ≥0.30 as its own state — engine _SPLIT_TRACK).
 */
export function deriveVerdict(
  consensus: number | null,
  band: ReturnType<typeof deriveSpreadBand>,
): CouncilVerdict {
  if (consensus == null) return "pending";
  if (band?.split) return "split";
  return consensus >= 0.5 ? "likely" : "unlikely";
}
