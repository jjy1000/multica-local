// @vitest-environment node
// Tests for the WhatIf → PythiaPrediction adapter. The Pythia wire
// shape uses `statement` for the prediction title and may omit
// agents[] (swarm deliberation is optional). The renderer UI type
// expects `title` and a stable id.

import { describe, expect, test } from "vitest";
import { normalizeWhatIfPredictions } from "./whatif-panel";

describe("normalizeWhatIfPredictions", () => {
  test("maps statement → title and assigns stable ids", () => {
    const out = normalizeWhatIfPredictions([
      { statement: "Oil > $95 by Friday", horizon: "24h", probability: 0.6 },
      { statement: "EURUSD drops 2%", horizon: "week", probability: 0.45 },
    ]);
    expect(out).toHaveLength(2);
    expect(out[0].title).toBe("Oil > $95 by Friday");
    expect(out[0].id).toMatch(/^whatif-/);
    expect(out[1].id).toMatch(/^whatif-/);
    expect(out[0].id).not.toBe(out[1].id);
  });

  test("preserves provided id when present", () => {
    const out = normalizeWhatIfPredictions([
      { id: "abc", statement: "x", horizon: "week", probability: 0.5 },
    ]);
    expect(out[0].id).toBe("whatif-abc");
  });

  test("defaults missing probability to 0", () => {
    const out = normalizeWhatIfPredictions([
      { statement: "y", horizon: "month" },
    ]);
    expect(out[0].probability).toBe(0);
  });

  test("defaults missing horizon to 'week'", () => {
    const out = normalizeWhatIfPredictions([
      { statement: "z", probability: 0.5 },
    ]);
    expect(out[0].horizon).toBe("week");
  });

  test("passes through lat/lng only when numbers", () => {
    const out = normalizeWhatIfPredictions([
      { statement: "a", horizon: "24h", probability: 0.5, lat: "north", lng: 100 },
      { statement: "b", horizon: "24h", probability: 0.5, lat: 10, lng: 20 },
    ]);
    expect(out[0].lat).toBeNull();
    expect(out[0].lng).toBe(100);
    expect(out[1].lat).toBe(10);
    expect(out[1].lng).toBe(20);
  });

  test("skips non-object entries without throwing", () => {
    const out = normalizeWhatIfPredictions([
      null,
      "string",
      42,
      { statement: "ok", horizon: "week", probability: 0.5 },
    ]);
    expect(out).toHaveLength(1);
    expect(out[0].title).toBe("ok");
  });

  test("splits=false default; base_probability / prev_probability null default", () => {
    const out = normalizeWhatIfPredictions([
      { statement: "x", horizon: "week", probability: 0.5 },
    ]);
    expect(out[0].split).toBe(false);
    expect(out[0].base_probability).toBeNull();
    expect(out[0].prev_probability).toBeNull();
    expect(out[0].agents).toEqual([]);
  });

  test("passes through agents when present", () => {
    const out = normalizeWhatIfPredictions([
      {
        statement: "x",
        horizon: "week",
        probability: 0.5,
        agents: [{ name: "Skeptic", probability: 0.4, note: "base rate"}],
      },
    ]);
    expect(out[0].agents).toHaveLength(1);
    expect(out[0].agents[0].name).toBe("Skeptic");
  });
});