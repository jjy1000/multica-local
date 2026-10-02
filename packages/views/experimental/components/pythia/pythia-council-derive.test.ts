/**
 * @vitest-environment node
 */
import { describe, expect, it } from "vitest";
import type { PythiaForecastEnvelope } from "@multica/core/types/api";
import {
  COUNCIL_SPLIT_THRESHOLD,
  deriveConsensusTrajectory,
  deriveCouncilSeats,
  deriveSpreadBand,
  deriveVerdict,
  latestCouncil,
} from "./pythia-council-derive";

function envelope(over: Partial<PythiaForecastEnvelope> = {}): PythiaForecastEnvelope {
  return {
    id: "env",
    scenario: "s",
    narrative: "",
    probability: 0.5,
    confidence: 0.5,
    horizon: "",
    persona: "Oracle",
    lab_source: "pythia_oracle",
    ...over,
  } as PythiaForecastEnvelope;
}

const council = (votes: Array<[string, number]>, consensus: number | null, spread = 0.2, split = false) => ({
  council: { votes: votes.map(([persona, probability]) => ({ persona, probability, note: "" })), consensus, spread, split },
});

describe("latestCouncil / deriveCouncilSeats", () => {
  it("returns null when no envelope carries a council", () => {
    expect(latestCouncil([envelope(), envelope()])).toBeNull();
    expect(deriveCouncilSeats([envelope()])).toEqual([]);
  });

  it("seats carry each persona's most recent vote across rounds", () => {
    const envs = [
      envelope(council([["Strategist", 0.6], ["Skeptic", 0.2]], 0.4)),
      envelope(council([["Strategist", 0.55], ["Skeptic", 0.3], ["Economist", 0.47]], 0.45)),
    ];
    const seats = deriveCouncilSeats(envs);
    const byPersona = Object.fromEntries(seats.map((s) => [s.persona, s.probability]));
    // Strategist's round-2 vote (0.55) wins over round-1 (0.6)
    expect(byPersona.Strategist).toBe(0.55);
    expect(byPersona.Skeptic).toBe(0.3);
    expect(byPersona.Economist).toBe(0.47);
    // hue assignment is stable by first-seen order
    expect(seats[0]!.hue).toContain("purple");
  });
});

describe("deriveConsensusTrajectory", () => {
  it("falls back to the oracle probability when a round has no council", () => {
    const envs = [
      envelope({ probability: 0.3 }),
      envelope(council([["S", 0.5]], 0.44)),
    ];
    expect(deriveConsensusTrajectory(envs)).toEqual([0.3, 0.44]);
  });
});

describe("deriveSpreadBand + deriveVerdict", () => {
  it("band spans min-max and honours the server split flag over the threshold", () => {
    const tight = { votes: [{ persona: "a", probability: 0.44, note: "" }, { persona: "b", probability: 0.48, note: "" }], consensus: 0.46, spread: 0.04, split: false };
    const band = deriveSpreadBand(tight);
    expect(band?.lo).toBe(0.44);
    expect(band?.hi).toBe(0.48);
    expect(band?.split).toBe(false);

    const stamped = { ...tight, split: true };
    expect(deriveSpreadBand(stamped)?.split).toBe(true);

    const wide = { votes: [{ persona: "a", probability: 0.2, note: "" }, { persona: "b", probability: 0.6, note: "" }], consensus: 0.4, spread: 0.4, split: false };
    expect(deriveSpreadBand(wide)?.split).toBe(true);
    expect(COUNCIL_SPLIT_THRESHOLD).toBe(0.3);
  });

  it("verdict: split outranks direction; null consensus stays pending", () => {
    expect(deriveVerdict(0.75, { lo: 0.2, hi: 0.9, split: true })).toBe("split");
    expect(deriveVerdict(0.75, { lo: 0.7, hi: 0.8, split: false })).toBe("likely");
    expect(deriveVerdict(0.25, { lo: 0.2, hi: 0.3, split: false })).toBe("unlikely");
    expect(deriveVerdict(null, null)).toBe("pending");
  });
});
