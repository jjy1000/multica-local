// Regression coverage for the 0.5.81 swarm-topology lab fixes:
//
//   1. The standalone sidebar entry renders <SwarmTopologyView /> bare
//      (routes.tsx passes no props), so the view must self-resolve the
//      workspace singleton — otherwise PastRunsPanel never loads and the
//      BootstrapForm submit stays disabled forever ("点击进入后无法使用").
//
//   2. Arriving with ?issue=<id> (from the issue-detail status pill /
//      create-issue redirect) must resume the run bound to that issue via
//      the reverse lookup instead of showing an empty BootstrapForm
//      ("可视化点击结果跳转后无法交付对应的结果").

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";

import { SwarmTopologyView } from "./swarm-topology-view";

type RequestLog = Array<{ method: string; path: string }>;

interface Route {
  match: (path: string) => boolean;
  /** HTTP status to respond with; defaults to 200 with `body`. */
  status?: number;
  body?: unknown;
}

let routes: Route[] = [];
const requests: RequestLog = [];
let currentWsId: string | null = "ws-1";

vi.mock("@multica/core/platform", () => ({
  getCurrentWsId: () => currentWsId,
}));

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
  } as unknown as Response;
}

vi.mock("@multica/core/api", () => ({
  api: {
    rawRequest: vi.fn(async (path: string, init?: { method?: string }) => {
      requests.push({ method: init?.method ?? "GET", path });
      const route = routes.find((r) => r.match(path));
      if (!route) return jsonResponse({ error: "no route" }, 404);
      if (route.status !== undefined) return jsonResponse(route.body ?? {}, route.status);
      return jsonResponse(route.body ?? []);
    }),
    searchIssues: vi.fn(async () => ({ issues: [] })),
    getIssue: vi.fn(async () => ({ id: "ISSUE-1", title: "", identifier: "MUL-1" })),
  },
}));

// Minimal dictionary matching every i18n selector the swarm view walks;
// `status` stays an empty lookup so unknown statuses fall back to the enum.
const dict: Record<string, unknown> = {
  title: "Swarm",
  description: "",
  back: "Back",
  live: { title: "", phase: "", active_roles: "", completed_roles: "", run_id: "" },
  roles: { title: "", name: "", status: "", current_step: "", last_heartbeat: "" },
  past_runs: { title: "", description: "", empty: "" },
  bootstrap: {
    label: "",
    description: "",
    issue_label: "",
    issue_helper: "",
    problem_label: "",
    problem_placeholder: "",
    max_hours_label: "",
    max_hours_helper: "",
    workspace_label: "",
    bootstrapping: "",
    submit: "",
  },
  status: {},
};

vi.mock("@multica/views/i18n", () => ({
  useT: () => ({
    t: (sel: (d: Record<string, unknown>) => unknown) => sel(dict),
  }),
}));

vi.mock("@multica/views/experimental", () => ({
  SwarmTopologyGraph: ({ roles }: { roles: unknown[] }) => (
    <div data-testid="swarm-graph">{roles.length}</div>
  ),
  SwarmInterruptBar: () => <div data-testid="swarm-interrupt-bar" />,
}));

function renderView(initialPath: string) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[initialPath]}>
        <SwarmTopologyView />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  currentWsId = "ws-1";
});

afterEach(() => {
  routes = [];
  vi.clearAllMocks();
});

describe("SwarmTopologyView", () => {
  it("self-resolves the workspace singleton and fetches past runs on the bare sidebar route", async () => {
    routes = [
      {
        match: (p) => p.startsWith("/api/experimental/swarm-topology/runs?"),
        body: [],
      },
    ];
    requests.length = 0;

    renderView("/experimental/swarm-topology");

    // Workspace resolved via getCurrentWsId(): the PastRunsPanel query
    // targets that workspace (impossible before the fix — the route passed
    // no workspaceId prop and the view had no fallback, so the history
    // panel stayed permanently empty behind "请先选择或创建一个工作区").
    await screen.findByTestId("swarm-topology-bootstrap");
    expect(screen.queryByTestId("swarm-topology-active")).toBeNull();
    await waitFor(() =>
      expect(requests.some((r) => r.path.includes("workspace_id=ws-1"))).toBe(true),
    );
  });

  it("resumes the run bound to ?issue=<id> instead of rendering the bootstrap form", async () => {
    routes = [
      {
        match: (p) => p === "/api/issues/ISSUE-1/swarm-runs",
        body: { id: "run-bound-1", status: "running", current_phase: "monitoring" },
      },
      {
        match: (p) =>
          p.startsWith("/api/experimental/swarm-topology/runs/run-bound-1/state"),
        body: {
          run_id: "run-bound-1",
          status: "running",
          current_phase: "monitoring",
          roles: [{ id: "role-1", role_name: "designer", status: "ready" }],
          active_role_count: 1,
          completed_role_count: 0,
        },
      },
      {
        match: (p) => p.startsWith("/api/experimental/swarm-topology/runs?"),
        body: [],
      },
    ];
    requests.length = 0;

    renderView("/experimental/swarm-topology?issue=ISSUE-1");

    // Active run surfaces (bound-run lookup → state query); the empty
    // bootstrap form must be gone.
    await screen.findByTestId("swarm-topology-active");
    expect(screen.queryByTestId("swarm-topology-bootstrap")).toBeNull();
    expect(requests.some((r) => r.path.endsWith("/issues/ISSUE-1/swarm-runs"))).toBe(true);
    expect(requests.some((r) => r.path.includes("/runs/run-bound-1/state"))).toBe(true);
  });

  it("falls back to the bootstrap form when the issue has no bound run (404)", async () => {
    routes = [
      {
        match: (p) => p.endsWith("/swarm-runs"),
        status: 404,
        body: { error: "not found" },
      },
    ];

    renderView("/experimental/swarm-topology?issue=ISSUE-9");

    // No run yet → user still lands in a usable bootstrap state rather
    // than a dead page.
    expect(await screen.findByTestId("swarm-topology-bootstrap")).toBeTruthy();
    expect(screen.queryByTestId("swarm-topology-active")).toBeNull();
  });
});
