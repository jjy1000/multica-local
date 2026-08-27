// TimesFM per-issue forecast run hooks (0.5.82 WL2).
//
// Read-side client for the TimesFM lab's record-listing endpoint:
//   GET /api/experimental/timesfm/forecast/issue/runs?issue_id=<id>&limit=N
//
// Contract notes:
//   - The route is gated by RequireExperimentalFlag("timesfm") on the
//     server, so a disabled flag surfaces as a uniform 404 (indistinguishable
//     from a nonexistent route). We degrade that to an empty list, mirroring
//     the pythia runs read in LabOutputPanel — "flag off" and "no runs yet"
//     render the same empty state, and the panel never errors on it.
//   - GET /runs works even when the engine subprocess is down (ICP-2:
//     the records view is a pure DB read — the lab view must render rows
//     without a live engine). A non-404 failure (502/503/500) is a real
//     error and is re-thrown so the caller can show its error bar.
//   - No WS coverage for this lab yet, so the query polls at the 5s
//     canonical cadence (cross-cutting lab-class contract, same beat as
//     the live Mythos/Pythia panel queries).
//   - Package boundaries respected: no react-dom, no localStorage, no
//     process.env — transport goes through the api client (rawRequest).

import { useQuery } from "@tanstack/react-query";
import { api, parseWithFallback } from "../api";
import { TimesfmForecastRunListSchema } from "../api/schemas";
import type { TimesfmForecastRun } from "../types/api";

/**
 * Cache-key family for the timesfm lab queries, styled after issueKeys:
 * `all` is the prefix for bulk invalidation, `runs` is the full key.
 */
export const timesfmKeys = {
  all: ["timesfm"] as const,
  runs: (issueId: string, limit = 10) =>
    [...timesfmKeys.all, "forecast-runs", issueId, limit] as const,
};

// Lab-class queries without WS coverage poll at the 5s canonical cadence.
const TIMESFM_POLL_INTERVAL_MS = 5_000;

/**
 * List persisted forecast runs for an issue, newest first (default last
 * 10 — the server clamps limit to [1, 100]). Enabled only when an issue
 * id is present; renders as an empty list pre-binding instead of firing
 * a request with a blank issue_id.
 */
export function useTimesfmForecastRuns(
  issueId: string | null | undefined,
  limit = 10,
) {
  return useQuery({
    queryKey: timesfmKeys.runs(issueId ?? "", limit),
    queryFn: async (): Promise<TimesfmForecastRun[]> => {
      const r = await api.rawRequest(
        `/api/experimental/timesfm/forecast/issue/runs?issue_id=${encodeURIComponent(issueId ?? "")}&limit=${limit}`,
      );
      // 404 is the flag-off signal (uniform guard response), not an error.
      if (r.status === 404) return [];
      if (!r.ok) throw new Error(`timesfm forecast runs ${r.status}`);
      const raw: unknown = await r.json();
      return parseWithFallback<TimesfmForecastRun[]>(
        raw,
        TimesfmForecastRunListSchema,
        [],
        { endpoint: "GET /api/experimental/timesfm/forecast/issue/runs" },
      );
    },
    enabled: Boolean(issueId),
    refetchInterval: TIMESFM_POLL_INTERVAL_MS,
  });
}
