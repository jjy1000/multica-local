// Issue causal graph read hooks (0.5.83 WL3).
//
// Read-side client for the gated causal-graph surface (contract:
// .omc/wl3-server-contract.md in the 0583 worktree; server handlers in
// server/internal/handler/causal_graph.go):
//   - GET /api/causal-graph/subgraph?issue_id=<id>&depth=N
//   - GET /api/causal-graph/path?from=<nodeId>&to=<nodeId>
//
// Contract notes:
//   - Off-flag the routes answer a uniform 404 (experimental_guard.go),
//     indistinguishable from a nonexistent route. The hooks degrade a
//     404 to `undefined` data with NO error — callers hide the causal
//     affordance on 404/error (ICP-5: causal awareness is passive and
//     flag-gated; it must never nag).
//   - No WS coverage for this lab: the subgraph poll runs at the 5s
//     canonical cadence while the flag feed matters, backing off to a
//     60s idle beat once the caller stops rendering it (enabled gate).
//   - The path read is one-shot per (from, to) pair — graphs do not
//     move fast enough to poll a BFS answer; staleTime keeps refocuses
//     cheap.
//   - Package boundaries respected: no react-dom, no localStorage, no
//     process.env — transport goes through the api client (rawRequest).

import { useQuery } from "@tanstack/react-query";
import { api, parseWithFallback } from "../api";
import { CausalPathSchema, CausalSubgraphSchema } from "../api/schemas";
import type { CausalPath, CausalSubgraph } from "../types/api";

/** Cache-key family for the causal graph queries (issueKeys style). */
export const causalGraphKeys = {
  all: ["causal-graph"] as const,
  subgraph: (issueId: string, depth: number) =>
    [...causalGraphKeys.all, "subgraph", issueId, depth] as const,
  path: (from: string, to: string) =>
    [...causalGraphKeys.all, "path", from, to] as const,
};

// Lab-class queries without WS coverage poll at the 5s canonical
// cadence (cross-cutting lab-class contract).
const CAUSAL_POLL_INTERVAL_MS = 5_000;

/**
 * N-hop causal neighbourhood of an issue, undirected, active edges
 * only. Disabled without an issue id; a 404 (flag off) degrades to a
 * query error-free `undefined` so callers can hide the affordance.
 */
export function useCausalSubgraph(issueId: string | null | undefined, depth = 2) {
  return useQuery({
    queryKey: causalGraphKeys.subgraph(issueId ?? "", depth),
    queryFn: async (): Promise<CausalSubgraph> => {
      const r = await api.rawRequest(
        `/api/causal-graph/subgraph?issue_id=${encodeURIComponent(issueId ?? "")}&depth=${depth}`,
      );
      if (r.status === 404) {
        // Flag-off guard — degrade instead of erroring (ICP-5).
        throw new CausalFlagOffError();
      }
      if (!r.ok) throw new Error(`causal subgraph ${r.status}`);
      const raw: unknown = await r.json();
      return parseWithFallback<CausalSubgraph>(raw, CausalSubgraphSchema, {
        issue_id: issueId ?? "",
        depth,
        nodes: [],
        edges: [],
      }, { endpoint: "GET /api/causal-graph/subgraph" });
    },
    enabled: Boolean(issueId),
    retry: (count, error) => error instanceof CausalFlagOffError ? false : count < 2,
    // A flag-off answer stays stable — stop the poll instead of
    // hammering a 404 every 5 seconds (ICP-5: never nag).
    refetchInterval: (query) =>
      query.state.error instanceof CausalFlagOffError
        ? false
        : CAUSAL_POLL_INTERVAL_MS,
  });
}

/** Marker for the uniform-404 flag-off answer (never user-visible). */
export class CausalFlagOffError extends Error {
  constructor() {
    super("causal_graph flag is off");
    this.name = "CausalFlagOffError";
  }
}

/**
 * Directed shortest path between two nodes. One-shot per pair
 * (staleTime 60s); a 404 (unreachable OR flag off) surfaces as `undefined`
 * data with the error cleared — callers render "no path".
 */
export function useCausalGraphPath(from: string | null, to: string | null) {
  return useQuery({
    queryKey: causalGraphKeys.path(from ?? "", to ?? ""),
    queryFn: async (): Promise<CausalPath | null> => {
      const r = await api.rawRequest(
        `/api/causal-graph/path?from=${encodeURIComponent(from ?? "")}&to=${encodeURIComponent(to ?? "")}`,
      );
      if (r.status === 404) return null; // unreachable: an answer, not an error
      if (!r.ok) throw new Error(`causal path ${r.status}`);
      const raw: unknown = await r.json();
      return parseWithFallback<CausalPath>(raw, CausalPathSchema, { nodes: [], edges: [] }, {
        endpoint: "GET /api/causal-graph/path",
      });
    },
    enabled: Boolean(from) && Boolean(to),
    staleTime: 60_000,
  });
}
