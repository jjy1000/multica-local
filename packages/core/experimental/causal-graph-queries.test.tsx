// @vitest-environment jsdom
// 0.5.83 WL3 — causal graph client coverage.
//
//   1. Zod schema + parseWithFallback shapes: a well-formed subgraph
//      parses with null optionals preserved, a drifted payload degrades
//      to the fallback, nulls stay nulls (abstract nodes).
//   2. useCausalSubgraph gating + transport: disabled without an issue
//      id; the uniform-404 flag-off answer surfaces as a
//      CausalFlagOffError with retries disabled (ICP-5: degrade, never
//      nag); a well-formed answer parses.
//   3. useCausalGraphPath: 404 (unreachable) resolves to null — an
//      answer, not an error.

import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import {
  CausalNodeSchema,
  CausalSubgraphSchema,
} from "../api/schemas";
import { parseWithFallback } from "../api/schema";

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

import {
  CausalFlagOffError,
  useCausalGraphPath,
  useCausalSubgraph,
} from "./causal-graph-queries";

function makeResponse(status: number, body: unknown): Response {
  return {
    status,
    ok: status >= 200 && status < 300,
    json: async () => body,
  } as unknown as Response;
}

const wellFormedSubgraph = {
  issue_id: "issue-1",
  depth: 2,
  nodes: [
    {
      id: "node-a",
      workspace_id: "ws-1",
      issue_id: "issue-1",
      type: "action",
      label: "run research",
      description: null,
      metadata: {},
      provenance: { source: "task_enqueue", dedup_key: "task_action:t1" },
      created_at: "2026-08-27T00:00:00Z",
      created_by: "system",
      lab_source: null,
      lab_run_id: null,
      status: "active",
      last_observed_at: "2026-08-27T00:00:00Z",
    },
    {
      id: "node-b",
      workspace_id: "ws-1",
      issue_id: null,
      type: "constraint",
      label: "root",
      metadata: {},
      provenance: {},
      created_at: "2026-08-27T00:00:00Z",
      status: "active",
      last_observed_at: "2026-08-27T00:00:00Z",
    },
  ],
  edges: [
    {
      id: "edge-1",
      workspace_id: "ws-1",
      from_node_id: "node-b",
      to_node_id: "node-a",
      type: "enables",
      weight: 1,
      confidence: null,
      metadata: {},
      provenance: {},
      created_at: "2026-08-27T00:00:00Z",
      proposed_by: null,
      status: "active",
    },
  ],
};

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

describe("Causal schemas / parseWithFallback shapes", () => {
  it("parses a well-formed subgraph and preserves null optionals", () => {
    const parsed = CausalSubgraphSchema.parse(wellFormedSubgraph);
    expect(parsed.nodes).toHaveLength(2);
    expect(parsed.nodes[1]?.description).toBeNull();
    expect(parsed.nodes[1]?.issue_id).toBeNull();
    expect(parsed.edges[0]?.proposed_by).toBeNull();
    expect(parsed.edges[0]?.confidence).toBeNull();
  });

  it("applies defaults for absent optional fields", () => {
    const parsed = CausalNodeSchema.parse({ id: "n1", workspace_id: "ws", type: "outcome", label: "L" });
    expect(parsed.status).toBe("active");
    expect(parsed.metadata).toEqual({});
    expect(parsed.provenance).toEqual({});
  });

  it("degrades a drifted payload to the fallback instead of throwing", () => {
    const fallback = parseWithFallback(
      [{ nodes: "not-a-list" }],
      CausalSubgraphSchema,
      { issue_id: "issue-1", depth: 2, nodes: [], edges: [] },
      { endpoint: "GET /api/causal-graph/subgraph" },
    );
    expect(fallback.nodes).toEqual([]);
  });
});

describe("useCausalSubgraph", () => {
  it("does not fetch when no issue id is present", async () => {
    const { result } = renderHook(() => useCausalSubgraph(null), { wrapper });
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(mockRawRequest).not.toHaveBeenCalled();
  });

  it("fetches and parses the subgraph for a bound issue", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(200, wellFormedSubgraph));
    const { result } = renderHook(() => useCausalSubgraph("issue-1"), { wrapper });
    await waitFor(() => expect(result.current.data?.nodes).toHaveLength(2));
    expect(mockRawRequest).toHaveBeenCalledWith(
      expect.stringContaining("/api/causal-graph/subgraph?issue_id=issue-1&depth=2"),
    );
  });

  it("surfaces the flag-off 404 as CausalFlagOffError without retries", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(404, { error: "not found" }));
    const { result } = renderHook(() => useCausalSubgraph("issue-1"), { wrapper });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error).toBeInstanceOf(CausalFlagOffError);
    // retry disabled: exactly one transport call for the failing fetch.
    expect(mockRawRequest).toHaveBeenCalledTimes(1);
  });
});

describe("useCausalGraphPath", () => {
  it("resolves the unreachable 404 to null data (an answer, not an error)", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(404, { error: "no causal path" }));
    const { result } = renderHook(() => useCausalGraphPath("a", "b"), { wrapper });
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.data).toBeNull();
    expect(result.current.isError).toBe(false);
  });

  it("parses the ordered path payload", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(200, {
      nodes: wellFormedSubgraph.nodes,
      edges: wellFormedSubgraph.edges,
    }));
    const { result } = renderHook(() => useCausalGraphPath("node-b", "node-a"), { wrapper });
    await waitFor(() => expect(result.current.data?.nodes).toHaveLength(2));
    expect(result.current.data?.edges[0]?.type).toBe("enables");
  });
});
