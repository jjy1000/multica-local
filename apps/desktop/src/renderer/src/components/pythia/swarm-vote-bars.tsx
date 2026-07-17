// SwarmVoteBars — horizontal bars per persona + consensus gauge.
//
// Mirrors engine/swarm.py: each persona casts a probability vote,
// consensus is weighted by Brier history. Until Phase 2 wires
// /state/stream we render the raw per-agent probabilities; the
// consensus probability shown in the parent card is the prediction's
// already-deliberated `probability` field.

import type { PythiaAgentVote, PythiaPrediction } from "./types";
import { useT } from "@multica/views/i18n";

const PERSONA_COLOR: Record<string, string> = {
  Strategist: "bg-sky-400",
  Economist: "bg-amber-400",
  Naturalist: "bg-emerald-400",
  Skeptic: "bg-fuchsia-400",
};

const PERSONA_ORDER = ["Strategist", "Economist", "Naturalist", "Skeptic"] as const;
type PersonaId = (typeof PERSONA_ORDER)[number];

// Translation table for the persona label shown next to each bar.
// Persona ids remain English (they are the wire identifier the engine
// returns); only the user-visible label is mapped.
const PERSONA_LABEL_KEY: Record<PersonaId, "persona_strategist" | "persona_economist" | "persona_naturalist" | "persona_skeptic"> = {
  Strategist: "persona_strategist",
  Economist: "persona_economist",
  Naturalist: "persona_naturalist",
  Skeptic: "persona_skeptic",
};

export interface SwarmVoteBarsProps {
  agents: ReadonlyArray<PythiaAgentVote>;
  consensus: number;
  split: boolean;
}

export function SwarmVoteBars({ agents, consensus, split }: SwarmVoteBarsProps) {
  const { t } = useT("pythia");
  // Stable order so the persona rows don't reshuffle between renders.
  const ordered = [...agents].sort(
    (a, b) =>
      PERSONA_ORDER.indexOf(a.name as PersonaId) -
      PERSONA_ORDER.indexOf(b.name as PersonaId),
  );

  function personaLabel(name: string): string {
    const key = PERSONA_LABEL_KEY[name as PersonaId];
    if (!key) return name;
    return t(($) => $.pythia[key]) || name;
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-baseline justify-between text-xs">
        <span className="font-medium text-foreground">
          {t(($) => $.pythia.swarm_consensus)}
          <span
            className={
              split
                ? "ml-2 rounded bg-fuchsia-500/15 px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-fuchsia-300"
                : "ml-2 rounded bg-emerald-500/15 px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-emerald-300"
            }
          >
            {split ? t(($) => $.pythia.swarm_split) : t(($) => $.pythia.swarm_aligned)}
          </span>
        </span>
        <span className="font-mono text-muted-foreground">
          {(consensus * 100).toFixed(0)}%
        </span>
      </div>

      <div className="flex flex-col gap-1.5">
        {ordered.map((a) => {
          const width = `${(a.probability * 100).toFixed(0)}%`;
          const color = PERSONA_COLOR[a.name] ?? "bg-slate-400";
          const label = personaLabel(a.name);
          return (
            <div key={a.name} className="flex flex-col gap-0.5">
              <div className="flex items-baseline justify-between text-[11px]">
                <span className="text-muted-foreground">{label}</span>
                <span className="font-mono text-muted-foreground">
                  {(a.probability * 100).toFixed(0)}%
                </span>
              </div>
              <div className="h-1.5 w-full rounded-full bg-muted">
                <div
                  className={`h-full rounded-full ${color}`}
                  style={{ width }}
                  aria-label={`${label} ${width}`}
                />
              </div>
              <p className="text-[11px] leading-snug text-muted-foreground">
                {a.note}
              </p>
            </div>
          );
        })}
      </div>
    </div>
  );
}

export function _internal_personaOrder(): ReadonlyArray<string> {
  return PERSONA_ORDER;
}

// Re-export for caller ergonomics; avoid circular import.
export type { PythiaPrediction };