// IssueCausalGraphIcon.test.tsx (0.5.83 WL3)
//
// ICP-5 smoke pins for the issue-header causal affordance:
//   - flag OFF  → renders NOTHING (hidden entirely, never nags);
//   - flag ON + no nodes yet → renders NOTHING (nothing to preview);
//   - flag ON + subgraph data → renders the trigger icon with the
//     causal-graph aria label.
// The hook layer (404-as-CausalFlagOffError, polling) is covered in
// packages/core; here the hook module is mocked at its surface.

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";

import enCausalGraph from "../../locales/en/causal-graph.json";
import { NavigationProvider } from "../../navigation";
import type { NavigationAdapter } from "../../navigation/types";
import { IssueCausalGraphIcon } from "./issue-causal-icon";

const mockState = vi.hoisted(() => ({
  flagEnabled: true,
  subgraph: {
    data: undefined as
      | { nodes: Array<{ id: string; type: string; label: string; issue_id: string | null; description: string | null }> }
      | undefined,
    isPending: false,
    isError: false,
    error: null as unknown,
  },
}));

vi.mock("@multica/core/experimental", async () => {
  const actual = await vi.importActual<
    typeof import("@multica/core/experimental")
  >("@multica/core/experimental");
  return {
    ...actual,
    useExperimentalFlag: () => mockState.flagEnabled,
    useCausalSubgraph: () => mockState.subgraph,
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

beforeEach(() => {
  mockState.flagEnabled = true;
  mockState.subgraph = {
    data: undefined,
    isPending: false,
    isError: false,
    error: null,
  };
});

describe("IssueCausalGraphIcon", () => {
  it("renders nothing when the causal_graph flag is off (ICP-5)", () => {
    mockState.flagEnabled = false;
    const { container } = renderIcon();
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing while the issue has no causal nodes yet", () => {
    mockState.subgraph = { data: { nodes: [] }, isPending: false, isError: false, error: null };
    const { container } = renderIcon();
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing on a subgraph error (flag-off 404 lands here)", () => {
    mockState.subgraph = { data: undefined, isPending: false, isError: true, error: new Error("x") };
    const { container } = renderIcon();
    expect(container).toBeEmptyDOMElement();
  });

  it("renders the trigger icon with data present", () => {
    mockState.subgraph = {
      data: {
        nodes: [
          { id: "n1", type: "action", label: "run research", issue_id: "issue-1", description: null },
        ],
      },
      isPending: false,
      isError: false,
      error: null,
    };
    renderIcon();
    expect(screen.getByRole("button", { name: /causal graph/i })).toBeInTheDocument();
  });
});
