// lab-run-heuristics.test.ts (0.5.86)
//
// Pure-function pins for the lab running-state heuristics shared between
// LabOutputPanel's PythiaPanel and the issue-side LabProgressCard. The
// sessionStorage key especially MUST stay byte-identical — older tabs wrote
// under this exact name (0.5.59).

import { describe, it, expect } from "vitest";
import {
  PYTHIA_IN_PROGRESS_WINDOW_MS,
  TIMESFM_RECENT_RUN_WINDOW_MS,
  derivePythiaTriggerState,
  isTimesfmRunRecent,
  pythiaTriggerKey,
  readPythiaTriggeredAt,
  markPythiaTriggered,
} from "./lab-run-heuristics";

describe("pythiaTriggerKey / read / mark", () => {
  it("keeps the 0.5.59 key format byte-identical", () => {
    expect(pythiaTriggerKey("ws-1", "issue-1")).toBe(
      "pythia-triggered-ws-1-issue-1",
    );
  });

  it("round-trips through sessionStorage and returns null when absent/corrupt", () => {
    // jsdom-backed sessionStorage via the vitest jsdom environment.
    markPythiaTriggered("ws-1", "issue-1", 1_234);
    expect(readPythiaTriggeredAt("ws-1", "issue-1")).toBe(1_234);

    window.sessionStorage.setItem(pythiaTriggerKey("ws-2", "issue-1"), "abc");
    expect(readPythiaTriggeredAt("ws-2", "issue-1")).toBeNull();
    expect(readPythiaTriggeredAt("ws-3", "issue-1")).toBeNull();
  });
});

describe("derivePythiaTriggerState", () => {
  const now = 1_000_000;

  it("is idle with no trigger timestamp, regardless of rows", () => {
    expect(derivePythiaTriggerState(null, false, now)).toBe("idle");
    expect(derivePythiaTriggerState(null, true, now)).toBe("idle");
  });

  it("is idle once rows arrived — the timestamp is ignored (we have data)", () => {
    expect(derivePythiaTriggerState(now - 5_000, true, now)).toBe("idle");
    expect(derivePythiaTriggerState(now - 999_999, true, now)).toBe("idle");
  });

  it("is in_progress within the trigger window and stuck after it", () => {
    expect(
      derivePythiaTriggerState(now - (PYTHIA_IN_PROGRESS_WINDOW_MS - 1), false, now),
    ).toBe("in_progress");
    expect(
      derivePythiaTriggerState(now - PYTHIA_IN_PROGRESS_WINDOW_MS, false, now),
    ).toBe("stuck");
    expect(
      derivePythiaTriggerState(now - (PYTHIA_IN_PROGRESS_WINDOW_MS + 60_000), false, now),
    ).toBe("stuck");
  });
});

describe("isTimesfmRunRecent", () => {
  it("is true inside the 2-minute recency window", () => {
    const now = 1_000_000;
    const iso = (ms: number) => new Date(ms).toISOString();
    expect(isTimesfmRunRecent(iso(now - TIMESFM_RECENT_RUN_WINDOW_MS + 1), now)).toBe(true);
    expect(isTimesfmRunRecent(iso(now - 1), now)).toBe(true);
  });

  it("is false outside the window, for garbage input, and for far-future timestamps", () => {
    const now = 1_000_000;
    const iso = (ms: number) => new Date(ms).toISOString();
    expect(isTimesfmRunRecent(iso(now - TIMESFM_RECENT_RUN_WINDOW_MS - 1), now)).toBe(false);
    expect(isTimesfmRunRecent("not-a-timestamp", now)).toBe(false);
    expect(isTimesfmRunRecent(undefined, now)).toBe(false);
    expect(isTimesfmRunRecent(iso(now + 10 * 60_000), now)).toBe(false);
  });
});
