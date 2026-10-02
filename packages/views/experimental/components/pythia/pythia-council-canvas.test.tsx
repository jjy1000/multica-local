/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { PythiaForecastEnvelope } from "@multica/core/types/api";
import enExperimental from "../../../locales/en/experimental.json";
import { PythiaCouncilCanvas } from "./pythia-council-canvas";

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

function env(over: Partial<PythiaForecastEnvelope> = {}): PythiaForecastEnvelope {
  return {
    id: "e",
    scenario: "",
    narrative: "",
    probability: 0.5,
    confidence: 0.5,
    horizon: "",
    persona: "Oracle",
    lab_source: "pythia_oracle",
    ...over,
  } as PythiaForecastEnvelope;
}

const withCouncil = (consensus: number): PythiaForecastEnvelope =>
  env({
    probability: consensus,
    council: {
      votes: [
        { persona: "Strategist", probability: consensus + 0.1, note: "" },
        { persona: "Skeptic", probability: consensus - 0.1, note: "" },
      ],
      consensus,
      spread: 0.2,
      split: false,
    },
  });

describe("PythiaCouncilCanvas — 0.5.132", () => {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("renders the dial with the latest council consensus and one seat per persona", () => {
    render(
      <PythiaCouncilCanvas
        envelopes={[withCouncil(0.4), withCouncil(0.62)]}
        totalRounds={3}
        running
      />,
    );
    const dial = screen.getByTestId("pythia-council-dial");
    expect(dial.getAttribute("data-consensus")).toBe("0.62");
    const seats = screen.getAllByTestId("pythia-council-seat");
    expect(seats.map((s) => s.getAttribute("data-persona"))).toEqual(["Strategist", "Skeptic"]);
    // running → header shows R2/3
    expect(screen.getByTestId("pythia-council-canvas").getAttribute("data-running")).toBe("true");
    expect(screen.getByText("R2/3")).toBeTruthy();
    // per-round trajectory strip present
    expect(screen.getByTestId("pythia-council-traj")).toBeTruthy();
  });

  it("completed run swaps the running badge for a verdict chip", () => {
    render(
      <PythiaCouncilCanvas envelopes={[withCouncil(0.72)]} totalRounds={1} running={false} />,
    );
    const root = screen.getByTestId("pythia-council-canvas");
    expect(root.getAttribute("data-running")).toBe("false");
    expect(root.getAttribute("data-verdict")).toBe("likely");
    expect(screen.getByText(enExperimental.pythia_lab.council_verdict_likely)).toBeTruthy();
  });

  it("split council outranks direction (engine _SPLIT_TRACK semantics)", () => {
    const split = env({
      probability: 0.8,
      council: {
        votes: [
          { persona: "Bull", probability: 0.95, note: "" },
          { persona: "Bear", probability: 0.35, note: "" },
        ],
        consensus: 0.8,
        spread: 0.6,
        split: true,
      },
    });
    render(<PythiaCouncilCanvas envelopes={[split]} totalRounds={1} running={false} />);
    expect(screen.getByTestId("pythia-council-canvas").getAttribute("data-verdict")).toBe("split");
  });

  it("collapses via the header toggle and honours defaultOpen={false}", () => {
    render(
      <PythiaCouncilCanvas envelopes={[withCouncil(0.5)]} totalRounds={1} running={false} defaultOpen={false} />,
    );
    expect(screen.queryByTestId("pythia-council-stage")).toBeNull();
    fireEvent.click(screen.getByTestId("pythia-council-toggle"));
    expect(screen.getByTestId("pythia-council-stage")).toBeTruthy();
  });

  it("zero envelopes → standby chamber: fixed engine roster seats, empty dial (0.5.133)", () => {
    render(<PythiaCouncilCanvas envelopes={[]} totalRounds={0} running={false} />);
    const seats = screen.getAllByTestId("pythia-council-seat");
    expect(seats.map((s) => s.getAttribute("data-persona"))).toEqual([
      "Strategist", "Economist", "Naturalist", "Skeptic",
    ]);
    expect(seats.every((s) => s.querySelector(".council-seat-idle-ring"))).toBe(true);
    expect(screen.getByTestId("pythia-council-dial").getAttribute("data-consensus")).toBe("");
    // verdict stays pending — idle roster is presentation, not fabricated data
    expect(screen.getByTestId("pythia-council-canvas").getAttribute("data-verdict")).toBe("pending");
  });

  it("styles both themes via the scoped --council-* variables", () => {
    render(<PythiaCouncilCanvas envelopes={[withCouncil(0.5)]} totalRounds={1} running />);
    const style = screen.getByTestId("pythia-council-canvas").querySelector("style")!.textContent ?? "";
    expect(style).toContain(".dark .council-chamber");
    expect(style).toContain("@media (prefers-reduced-motion: reduce)");
  });
});
