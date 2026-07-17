// Pythia wire types — mirror engine/models.py contracts.
//
// Kept dependency-free so any flag-gated renderer code can import them
// without pulling MapLibre / d3 / etc. Phase 2 will swap the static
// SAMPLE_PREDICTIONS for the SSE-fed snapshot from /state/stream using
// these exact shapes.

export type PythiaHorizon = "24h" | "week" | "month" | "year";

export type PythiaPersona =
  | "Strategist"
  | "Economist"
  | "Naturalist"
  | "Skeptic";

export interface PythiaAgentVote {
  name: PythiaPersona | string;
  probability: number; // 0..1 — the persona's own estimate
  note: string; // one-to-two-sentence argument
}

export interface PythiaPrediction {
  id: string;
  title: string;
  horizon: PythiaHorizon;
  probability: number; // 0..1 consensus after swarm deliberation
  reasoning: string;
  location: string; // human place name
  lat: number | null;
  lng: number | null;
  agents: PythiaAgentVote[];
  base_probability: number | null; // oracle pre-swarm
  prev_probability: number | null; // last run's probability for momentum
  split: boolean; // sharp swarm disagreement
}

export interface PythiaWorldBrief {
  event_count: number;
  domains: Record<string, number>;
  text: string;
  top_events: string[];
}

export interface PythiaSnapshot {
  generating: boolean;
  loop_enabled: boolean;
  last_run_ms: number | null;
  world: PythiaWorldBrief | null;
  predictions: PythiaPrediction[];
}