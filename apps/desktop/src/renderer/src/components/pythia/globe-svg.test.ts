// @vitest-environment node
// Tests for the equirectangular projection and leading-persona helper
// used by GlobeSVG. Pure functions, no DOM / React.

import { describe, expect, test } from "vitest";
import { projectLngLat, leadingPersona } from "./globe-svg";
import type { PythiaPrediction } from "./types";

describe("projectLngLat", () => {
  test("(0, 0) maps to viewBox center", () => {
    const [x, y] = projectLngLat(0, 0);
    // W=720, H=360 → (360, 180)
    expect(x).toBeCloseTo(360, 5);
    expect(y).toBeCloseTo(180, 5);
  });

  test("(-180, 90) maps to top-left corner", () => {
    const [x, y] = projectLngLat(-180, 90);
    expect(x).toBeCloseTo(0, 5);
    expect(y).toBeCloseTo(0, 5);
  });

  test("(180, -90) maps to bottom-right corner", () => {
    const [x, y] = projectLngLat(180, -90);
    expect(x).toBeCloseTo(720, 5);
    expect(y).toBeCloseTo(360, 5);
  });

  test("(0, 0) and (0, 1) differ by exactly W/360 in x", () => {
    const [x0] = projectLngLat(0, 0);
    const [x1] = projectLngLat(1, 0);
    expect(x1 - x0).toBeCloseTo(720 / 360, 5);
  });

  test("y is the negative of (lat/180) * H — north is up", () => {
    const [, yNorth] = projectLngLat(0, 45);
    const [, ySouth] = projectLngLat(0, -45);
    expect(yNorth).toBeLessThan(ySouth);
  });

  test("(180, 0) and (-180, 0) sit on opposite edges (sanity)", () => {
    const [xEast] = projectLngLat(180, 0);
    const [xWest] = projectLngLat(-180, 0);
    expect(xEast).toBeCloseTo(720, 5);
    expect(xWest).toBeCloseTo(0, 5);
  });
});

describe("leadingPersona", () => {
  const basePrediction = (agents: PythiaPrediction["agents"]): PythiaPrediction => ({
    id: "t",
    title: "t",
    horizon: "week",
    probability: 0.5,
    reasoning: "",
    location: "",
    lat: 0,
    lng: 0,
    agents,
    base_probability: null,
    prev_probability: null,
    split: false,
  });

  test("returns the persona with the highest probability", () => {
    const p = basePrediction([
      { name: "Strategist", probability: 0.4, note: "" },
      { name: "Economist", probability: 0.7, note: "" },
      { name: "Naturalist", probability: 0.2, note: "" },
      { name: "Skeptic", probability: 0.5, note: "" },
    ]);
    expect(leadingPersona(p)).toBe("Economist");
  });

  test("falls back to 'Skeptic' when agents[] is empty (what-if case)", () => {
    const p = basePrediction([]);
    expect(leadingPersona(p)).toBe("Skeptic");
  });

  test("breaks ties by first occurrence (stable)", () => {
    const p = basePrediction([
      { name: "Strategist", probability: 0.5, note: "" },
      { name: "Economist", probability: 0.5, note: "" },
    ]);
    expect(leadingPersona(p)).toBe("Strategist");
  });
});