/**
 * @vitest-environment jsdom
 */
// pythia-round-view council tests (0.5.131, MiroFish action-card port):
// the expanded vote sheet renders as typed action cards — stance badge
// derived from the persona's estimate vs the oracle baseline — with the
// stance thresholds unit-pinned.

import { describe, it, expect, vi, beforeEach } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import enExperimental from "../../../locales/en/experimental.json";
import { PythiaCouncilPanel, voteStance } from "./pythia-round-view";
import type { PythiaCouncil } from "@multica/core/types/api";

vi.mock("../../../i18n", () => ({
  useT: () => ({
    t: (sel: (d: unknown) => unknown, opts?: Record<string, unknown>) => {
      const v = sel(enExperimental);
      if (typeof v !== "string") return undefined;
      return opts
        ? v.replace(/\{\{(\w+)\}\}/g, (_, k) => String(opts[k] ?? `{{${k}}}`))
        : v;
    },
  }),
}));

describe("voteStance (pure)", () => {
  it("buckets a persona's estimate against the oracle baseline at ±5pp", () => {
    expect(voteStance({ probability: 0.7 }, 0.5)).toBe("support");
    expect(voteStance({ probability: 0.55 }, 0.5)).toBe("support"); // exactly +5pp
    expect(voteStance({ probability: 0.3 }, 0.5)).toBe("challenge");
    expect(voteStance({ probability: 0.45 }, 0.5)).toBe("challenge"); // exactly -5pp
    expect(voteStance({ probability: 0.52 }, 0.5)).toBe("neutral");
    expect(voteStance({ probability: 0.48 }, 0.5)).toBe("neutral");
  });

  it("returns null without a baseline — no fabricated stance", () => {
    expect(voteStance({ probability: 0.9 }, null)).toBeNull();
    expect(voteStance({ probability: 0.9 }, undefined)).toBeNull();
  });
});

function makeCouncil(): PythiaCouncil {
  return {
    consensus: 0.55,
    split: false,
    spread: 0.37,
    votes: [
      { persona: "Strategist", probability: 0.72, note: "momentum favors it" },
      { persona: "Skeptic", probability: 0.35, note: "evidence thin" },
      { persona: "Economist", probability: 0.52, note: "in line with base" },
    ],
  };
}

describe("PythiaCouncilPanel action cards", () => {
  beforeEach(() => cleanup());

  it("renders typed stance badges per persona once expanded", () => {
    render(<PythiaCouncilPanel council={makeCouncil()} baseProbability={0.5} />);
    fireEvent.click(screen.getByTestId("pythia-council").querySelector("button")!);
    const cards = screen.getAllByTestId("pythia-council-vote");
    expect(cards).toHaveLength(3);
    const stances = cards.map((c) => c.getAttribute("data-stance"));
    expect(stances).toEqual(["support", "challenge", "neutral"]);
    expect(screen.getByText(enExperimental.pythia_lab.council_act_support)).toBeTruthy();
    expect(screen.getByText(enExperimental.pythia_lab.council_act_challenge)).toBeTruthy();
    expect(screen.getByText(enExperimental.pythia_lab.council_act_neutral)).toBeTruthy();
  });

  it("omits stance badges entirely when no baseline is available", () => {
    render(<PythiaCouncilPanel council={makeCouncil()} baseProbability={null} />);
    fireEvent.click(screen.getByTestId("pythia-council").querySelector("button")!);
    for (const card of screen.getAllByTestId("pythia-council-vote")) {
      expect(card.getAttribute("data-stance")).toBe("none");
    }
  });
});
