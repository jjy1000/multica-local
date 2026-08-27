// @vitest-environment jsdom
// 0.5.82 WL2 — TimesFM forecast-runs client coverage.
//
// Two layers, mirroring the pythia schema + panel split:
//   1. Zod schema + parseWithFallback shapes — the lenient-parse contract:
//      a valid run list parses, an engine/DB shape drift degrades to the
//      fallback instead of throwing, and a known-good empty list stays a
//      list (not null/undefined).
//   2. useTimesfmForecastRuns gating + transport: disabled without an
//      issue id, 404 (flag off) degrades to [], and a non-404 failure
//      surfaces as a query error.

import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import {
  TimesfmForecastResultSchema,
  TimesfmForecastRunListSchema,
  TimesfmForecastRunSchema,
} from "../api/schemas";
import { parseWithFallback } from "../api/schema";
import type { TimesfmForecastRun } from "../types/api";

const mockRawRequest = vi.fn();
vi.mock("../api", async () => {
  const actual = await vi.importActual<typeof import("../api")>("../api");
  return {
    ...actual,
    api: {
      ...actual.api,
      rawRequest: (...args: unknown[]) => mockRawRequest(...args),
    },
  };
});

import { timesfmKeys, useTimesfmForecastRuns } from "./timesfm-queries";

// A realistic run row matching the migration-275 wire shape: the result
// JSONB is the engine's raw answer (single-series, full quantile map).
const engineResult = {
  series: [
    {
      point: [10.2, 10.4, 10.1],
      quantiles: {
        lower_90: [9.1, 9.3, 9.0],
        lower_80: [9.5, 9.7, 9.4],
        median: [10.2, 10.4, 10.1],
        upper_80: [10.9, 11.1, 10.8],
        upper_90: [11.3, 11.5, 11.2],
      },
      provenance: "model",
      dates: ["2026-08-28", "2026-08-29", "2026-08-30"],
    },
  ],
  provenance: "model",
  model_present: true,
  horizon: 3,
};

const runRow = {
  id: "run-1",
  horizons: 3,
  provenance: "model",
  created_at: "2026-08-27T00:00:00Z",
  result: engineResult,
};

function makeResponse(status: number, body: unknown): Response {
  return {
    status,
    ok: status >= 200 && status < 300,
    json: async () => body,
  } as unknown as Response;
}

describe("TimesfmForecastRunListSchema / parseWithFallback shapes", () => {
  it("parses a well-formed run list and preserves the quantile map", () => {
    const parsed = TimesfmForecastRunListSchema.parse([runRow]);
    expect(parsed).toHaveLength(1);
    expect(parsed[0].id).toBe("run-1");
    expect(parsed[0].result?.series[0].quantiles?.median).toEqual([10.2, 10.4, 10.1]);
    expect(parsed[0].result?.model_present).toBe(true);
  });

  it("applies defaults for absent optional fields (dates, quantiles)", () => {
    const parsed = TimesfmForecastRunSchema.parse({
      id: "run-2",
      horizons: 24,
      provenance: "seasonal_naive",
      created_at: "2026-08-27T01:00:00Z",
      result: {
        series: [{ point: [1, 2, 3] }],
        provenance: "seasonal_naive",
        model_present: false,
        horizon: 24,
      },
    });
    expect(parsed.result?.series[0].quantiles).toBeUndefined();
    expect(parsed.result?.series[0].provenance).toBe("");
    expect(parsed.result?.series[0].dates).toBeUndefined();
  });

  it("degrades a drifted payload to the fallback instead of throwing", () => {
    // id missing entirely + result as a bare string (legacy/corrupt row)
    const drifted = [{ horizons: "not-a-number", result: "oops" }];
    const fallback = parseWithFallback<TimesfmForecastRun[]>(
      drifted,
      TimesfmForecastRunListSchema,
      [],
      { endpoint: "GET /api/experimental/timesfm/forecast/issue/runs" },
    );
    expect(fallback).toEqual([]);
  });

  it("keeps a known-good empty array as an empty array", () => {
    const parsed = parseWithFallback<TimesfmForecastRun[]>(
      [],
      TimesfmForecastRunListSchema,
      [],
      { endpoint: "GET /api/experimental/timesfm/forecast/issue/runs" },
    );
    expect(parsed).toEqual([]);
  });

  it("keeps unknown provenance labels as plain strings (CHECK-set drift)", () => {
    const parsed = TimesfmForecastResultSchema.parse({
      ...engineResult,
      provenance: "some_future_label",
    });
    expect(parsed.provenance).toBe("some_future_label");
  });
});

describe("useTimesfmForecastRuns", () => {
  function wrapper({ children }: { children: ReactNode }) {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    return (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
  }

  beforeEach(() => {
    mockRawRequest.mockReset();
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  it("does not fetch when no issue id is present", async () => {
    const { result } = renderHook(() => useTimesfmForecastRuns(null), { wrapper });
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(mockRawRequest).not.toHaveBeenCalled();
    expect(result.current.data).toBeUndefined();
  });

  it("fetches and parses runs for a bound issue", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(200, [runRow]));
    const { result } = renderHook(() => useTimesfmForecastRuns("issue-1"), { wrapper });

    await waitFor(() => expect(result.current.data).toHaveLength(1));
    expect(result.current.data?.[0].id).toBe("run-1");
    expect(mockRawRequest).toHaveBeenCalledWith(
      expect.stringContaining(
        "/api/experimental/timesfm/forecast/issue/runs?issue_id=issue-1&limit=10",
      ),
    );
  });

  it("degrades a 404 (flag off) to an empty list", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(404, { error: "not found" }));
    const { result } = renderHook(() => useTimesfmForecastRuns("issue-1"), { wrapper });

    await waitFor(() => expect(result.current.isFetched).toBe(true));
    expect(result.current.data).toEqual([]);
    expect(result.current.isError).toBe(false);
  });

  it("surfaces a non-404 failure as a query error", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(503, { error: "engine down" }));
    const { result } = renderHook(() => useTimesfmForecastRuns("issue-1"), { wrapper });

    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.data).toBeUndefined();
  });

  it("builds the cache-key family issue-first", () => {
    expect(timesfmKeys.all).toEqual(["timesfm"]);
    expect(timesfmKeys.runs("issue-1")).toEqual([
      "timesfm",
      "forecast-runs",
      "issue-1",
      10,
    ]);
    expect(timesfmKeys.runs("issue-1", 25)).toEqual([
      "timesfm",
      "forecast-runs",
      "issue-1",
      25,
    ]);
  });
});
