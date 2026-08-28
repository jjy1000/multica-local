// IssueCausalGraphIcon.test.tsx (0.5.83 WL3; 0.5.86 declutter pins)
//
// ICP-5 smoke pins for the issue-header causal affordance:
//   - flag OFF  → renders NOTHING (hidden entirely, never nags);
//   - flag ON + no nodes yet → renders NOTHING (nothing to preview);
//   - flag ON + subgraph data → renders the trigger icon with the
//     causal-graph aria label.
// 0.5.86: the popup defaults to depth 1, and selecting 深度 2 renders
// the depth-1 map plus a "+N nodes · M edges" summary row instead of
// the dense second ring.
// The hook layer (404-as-CausalFlagOffError, polling) is covered in
// packages/core; here the hook module is mocked at its surface.

import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";

import enCausalGraph from "../../locales/en/causal-graph.json";
import { NavigationProvider } from "../../navigation";
import type { NavigationAdapter } from "../../navigation/types";
import { IssueCausalGraphIcon } from "./issue-causal-icon";

const mockState = vi.hoisted(() => ({
  flagEnabled: true,
  subgraphs: {
    1: undefined as
      | {
          data: { nodes: Array<{ id: string; type: string; label: string; issue_id: string | null; description: string | null }>; edges?: Array<{ id: string }> } | undefined;
          isPending: boolean;
          isError: boolean;
          error: unknown;
        }
      | undefined,
    2: undefined as
      | {
          data: { nodes: Array<{ id: string; type: string; label: string; issue_id: string | null; description: string | null }>; edges?: Array<{ id: string }> } | undefined;
          isPending: boolean;
          isError: boolean;
          error: unknown;
        }
      | undefined,
  },
}));

const idleSubgraph = {
  data: undefined,
  isPending: false,
  isError: false,
  error: null as unknown,
};

vi.mock("@multica/core/experimental", async () => {
  const actual = await vi.importActual<
    typeof import("@multica/core/experimental")
  >("@multica/core/experimental");
  return {
    ...actual,
    useExperimentalFlag: () => mockState.flagEnabled,
    // The component fetches depth 1 for the drawn map and depth 2 only
    // for the "+N nodes · M edges" summary count.
    useCausalSubgraph: (issueId: string | null, depth?: number) => {
      if (issueId === null) return idleSubgraph;
      return (depth ?? 1) === 2 ? mockState.subgraphs[2] : mockState.subgraphs[1];
    },
  };
});

function makeNav(): NavigationAdapter {
  return {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/issues/issue-1",
    searchParams: new URLSearchParams(),
    getShareableUrl: (p: string) => p,
  };
}

function renderIcon() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider locale="en" resources={{ en: { "causal-graph": enCausalGraph } }}>
        <NavigationProvider value={makeNav()}>
          <IssueCausalGraphIcon issueId="issue-1" />
        </NavigationProvider>
      </I18nProvider>
    </QueryClientProvider>,
  );
}

function mockNode(id: string, label: string) {
  return { id, type: "action", label, issue_id: "issue-1", description: null };
}

beforeEach(() => {
  mockState.flagEnabled = true;
  mockState.subgraphs = { 1: undefined, 2: undefined };
});

describe("IssueCausalGraphIcon", () => {
  it("renders nothing when the causal_graph flag is off (ICP-5)", () => {
    mockState.flagEnabled = false;
    const { container } = renderIcon();
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing while the issue has no causal nodes yet", () => {
    mockState.subgraphs[1] = { data: { nodes: [] }, isPending: false, isError: false, error: null };
    const { container } = renderIcon();
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing on a subgraph error (flag-off 404 lands here)", () => {
    mockState.subgraphs[1] = { data: undefined, isPending: false, isError: true, error: new Error("x") };
    const { container } = renderIcon();
    expect(container).toBeEmptyDOMElement();
  });

  it("renders the trigger icon with data present", () => {
    mockState.subgraphs[1] = {
      data: { nodes: [mockNode("n1", "run research")] },
      isPending: false,
      isError: false,
      error: null,
    };
    renderIcon();
    expect(screen.getByRole("button", { name: /causal graph/i })).toBeInTheDocument();
  });

  it("defaults to depth 1: no depth-2 summary row until 深度 2 is selected (0.5.86)", async () => {
    mockState.subgraphs[1] = {
      data: { nodes: [mockNode("n1", "run research")], edges: [{ id: "e1" }] },
      isPending: false,
      isError: false,
      error: null,
    };
    mockState.subgraphs[2] = {
      data: {
        nodes: [mockNode("n1", "run research"), mockNode("n2", "write report")],
        edges: [{ id: "e1" }, { id: "e2" }],
      },
      isPending: false,
      isError: false,
      error: null,
    };
    renderIcon();
    // Open the popup (content mounts through a portal), still at the
    // default depth 1 — the depth-2 extras row is absent.
    fireEvent.click(screen.getByRole("button", { name: /causal graph/i }));
    await screen.findByRole("button", { name: "2" });
    expect(screen.queryByText(/\+\d+ nodes/)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "2" }));
    // Exactly the depth-2 EXTRAS: 1 extra node, 1 extra edge.
    expect(await screen.findByText(/\+1 nodes · 1 edges at depth 2/)).toBeInTheDocument();
  });
});
