/**
 * @vitest-environment jsdom
 */
// pythia-issue-embed tests (0.5.131) — pin the embed's state contract:
//   - null collapse (no runs + no live stream)
//   - live embed renders the round timeline, the pending-round slots
//     derived from totalRounds, AND the probability trajectory (the
//     chart existed since 0.5.111 but was only wired in 0.5.131)
//   - the optimistic stream shows live even before the runs list knows
//     the run (the post-start flash fix)
//   - the history fold renders a localized timestamp (not raw RFC3339)
//     and, once expanded, the trajectory of the completed run
// The hook (usePythiaIssueLab) is mocked here — its SSE/reducer side has
// its own unit tests (use-pythia-issue-lab.test.ts).

import { describe, it, expect, vi, beforeEach } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import enExperimental from "../../../locales/en/experimental.json";
import { PythiaIssueEmbed } from "./pythia-issue-embed";

const hookState = vi.hoisted(() => ({
  runs: [] as Array<Record<string, unknown>>,
  stream: null as Record<string, unknown> | null,
  hasLiveRun: false,
}));

const IDLE_STREAM = vi.hoisted(() => ({
  runId: null,
  status: "idle",
  runKind: "initial",
  variables: "",
  totalRounds: 0,
  envelopes: [],
  report: "",
}));

vi.mock("../../hooks/use-pythia-issue-lab", () => ({
  usePythiaIssueLab: () => ({
    runs: hookState.runs,
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
    stream: hookState.stream ?? IDLE_STREAM,
    hasLiveRun: hookState.hasLiveRun,
    start: vi.fn(),
    cancel: vi.fn(),
  }),
}));

vi.mock("../../../i18n", () => ({
  useLocale: () => "en-US",
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

function envelope(id: string, probability: number, confidence = 0.6) {
  return {
    id,
    scenario: `scenario ${id}`,
    narrative: "n",
    probability,
    confidence,
    horizon: "week",
    persona: "oracle",
    lab_source: "pythia_oracle",
  };
}

function runningStream(runId: string, totalRounds: number, envelopes: unknown[]) {
  return {
    runId,
    status: "running",
    runKind: "initial",
    variables: "",
    totalRounds,
    envelopes,
    report: "",
  };
}

describe("PythiaIssueEmbed — 0.5.131", () => {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
    hookState.runs = [];
    hookState.stream = null;
    hookState.hasLiveRun = false;
  });

  it("idle lab-bound issue renders the council chamber in standby (0.5.133)", () => {
    // The full null-collapse is gone: with no runs and no live stream the
    // embed still mounts — the chamber seats the fixed engine roster with
    // a standby orbit instead of reading as "not implemented".
    render(<PythiaIssueEmbed wsId="ws-1" issueId="issue-1" />);
    expect(screen.getByTestId("pythia-issue-embed")).toBeTruthy();
    expect(screen.getByTestId("pythia-council-canvas")).toBeTruthy();
    const seats = screen.getAllByTestId("pythia-council-seat");
    expect(seats.map((s) => s.getAttribute("data-persona"))).toEqual([
      "Strategist", "Economist", "Naturalist", "Skeptic",
    ]);
    expect(screen.getByTestId("pythia-council-dial").getAttribute("data-consensus")).toBe("");
    expect(screen.getByText(enExperimental.pythia_lab.embed_idle)).toBeTruthy();
  });

  it("live embed renders timeline, pending slots from planned rounds, and the trajectory", () => {
    hookState.stream = runningStream("run-1", 4, [envelope("e1", 0.4), envelope("e2", 0.55)]);
    hookState.hasLiveRun = true;
    hookState.runs = [{ id: "run-1", status: "running", rounds: 4, created_at: "2026-01-02T03:04:05Z", envelopes: [] }];
    render(<PythiaIssueEmbed wsId="ws-1" issueId="issue-1" />);
    expect(screen.getByTestId("pythia-embed-live")).toBeTruthy();
    // trajectory needs >=2 envelopes — wired in 0.5.131
    expect(screen.getByTestId("pythia-trajectory")).toBeTruthy();
    // 4 planned rounds - 2 landed = 2 pending placeholder slots
    expect(screen.getAllByTestId("pythia-round-pending")).toHaveLength(2);
    expect(screen.getAllByTestId("pythia-round-card").length).toBeGreaterThanOrEqual(2);
  });

  it("shows the live embed optimistically before the runs list knows the run (flash fix)", () => {
    // start() returns, the SSE subscription is up, but the runs query has
    // not refetched yet — the embed must not flash to history/null.
    hookState.stream = runningStream("run-9", 3, []);
    hookState.hasLiveRun = true;
    hookState.runs = [];
    render(<PythiaIssueEmbed wsId="ws-1" issueId="issue-1" />);
    expect(screen.getByTestId("pythia-embed-live")).toBeTruthy();
    expect(screen.queryByTestId("pythia-embed-history")).toBeNull();
  });

  it("history fold localizes the timestamp and reveals the trajectory when expanded", () => {
    const run = {
      id: "run-2",
      status: "completed",
      rounds: 2,
      created_at: "2026-01-02T03:04:05Z",
      envelopes: [envelope("e1", 0.4), envelope("e2", 0.55)],
    };
    hookState.runs = [run];
    hookState.hasLiveRun = false;
    render(<PythiaIssueEmbed wsId="ws-1" issueId="issue-1" />);
    const history = screen.getByTestId("pythia-embed-history");
    // localized, not raw RFC3339
    expect(history.textContent ?? "").not.toContain("T03:04:05");
    expect(screen.queryByTestId("pythia-trajectory")).toBeNull();
    fireEvent.click(history.querySelector("button")!);
    expect(screen.getByTestId("pythia-trajectory")).toBeTruthy();
    expect(screen.getAllByTestId("pythia-round-card")).toHaveLength(2);
  });
});
