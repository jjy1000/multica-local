// IssueBreadcrumb.test.tsx (0.5.81 WL1 C1)
//
// Unit tests for the shared back-link strip. The wrapper supplies the
// full `en/experimental.json` resource block via I18nProvider so the
// arrow-expression selectors on `$.breadcrumb.*` resolve to the real
// translations (same convention as semantica-mode-banner.test.tsx),
// a fresh QueryClient (retry:false so an intentional pending case
// stays pending), and a stubbed NavigationAdapter carrying controlled
// search params — views may not mount a real router here.

import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";

import enExperimental from "../../locales/en/experimental.json";
import { NavigationProvider } from "../../navigation";
import type { NavigationAdapter } from "../../navigation/types";
import { IssueBreadcrumb } from "./issue-breadcrumb";

// Deterministic issue-detail source: override the query options factory
// itself rather than the api barrel, so the test does not depend on how
// deep the real module graph resolves.
const mockGetIssue = vi.fn();
vi.mock("@multica/core/issues/queries", () => ({
  issueDetailOptions: (_wsId: string, id: string) => ({
    queryKey: ["issue-detail-test", id],
    queryFn: () => mockGetIssue(id),
  }),
}));

vi.mock("@multica/core/platform", async () => {
  const actual = await vi.importActual<
    typeof import("@multica/core/platform")
  >("@multica/core/platform");
  return {
    ...actual,
    getCurrentSlug: () => "ws-slug",
    getCurrentWsId: () => "ws-uuid",
  };
});

type NavStub = NavigationAdapter;

function makeNavAdapter(search: string): NavStub {
  const stub = {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/experimental/pythia",
    searchParams: new URLSearchParams(search),
    // Mirror the real NavigationAdapter contract — returns a string for
    // the shareable URL. The desktop adapter returns the public URL
    // for the connected environment; tests only need a stable sentinel.
    getShareableUrl: vi.fn((p: string) => p),
  };
  return stub as unknown as NavStub;
}

function Wrapper({ nav, children }: { nav: NavStub; children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return (
    <QueryClientProvider client={client}>
      <I18nProvider locale="en" resources={{ en: { experimental: enExperimental } }}>
        <NavigationProvider value={nav}>{children}</NavigationProvider>
      </I18nProvider>
    </QueryClientProvider>
  );
}

function renderBreadcrumb(search: string, props?: Record<string, unknown>) {
  const nav = makeNavAdapter(search);
  const view = render(
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    <Wrapper nav={nav}><IssueBreadcrumb {...(props as any)} /></Wrapper>,
  );
  return { nav, ...view };
}

const ISSUE_FIXTURE = {
  id: "issue-1",
  workspace_id: "ws-uuid",
  title: "Fix login redirect loop",
  description: null,
  status: "in_progress",
  lab_source: null,
  assignee_id: null,
  created_at: "2026-08-27T00:00:00Z",
  updated_at: "2026-08-27T00:00:00Z",
};

describe("IssueBreadcrumb", () => {
  beforeEach(() => {
    mockGetIssue.mockReset();
  });

  it("renders nothing when unbound and infoHintWhenUnbound is off", () => {
    const { container } = renderBreadcrumb("");
    expect(container.firstChild).toBeNull();
    expect(mockGetIssue).not.toHaveBeenCalled();
  });

  it("renders the muted workspace-level hint when asked to", () => {
    renderBreadcrumb("", { infoHintWhenUnbound: true });
    expect(screen.getByText(/does not bind to a single task/i)).toBeTruthy();
  });

  it("resolves the bound issue title + status and pushes back to detail on click", async () => {
    mockGetIssue.mockResolvedValue(ISSUE_FIXTURE);
    const { nav } = renderBreadcrumb("?issue=issue-1");

    await waitFor(() =>
      expect(screen.getByText(/Fix login redirect loop/i)).toBeTruthy(),
    );
    expect(mockGetIssue).toHaveBeenCalledWith("issue-1");
    expect(screen.getByText(/in_progress/i)).toBeTruthy();

    fireEvent.click(
      screen.getByRole("button", { name: /back to task|fix login/i }),
    );
    expect(nav.push).toHaveBeenCalledTimes(1);
    expect(nav.push).toHaveBeenCalledWith("/ws-slug/issues/issue-1");
  });

  it("falls back to the generic label until the title resolves", async () => {
    mockGetIssue.mockReturnValue(new Promise(() => {}));
    const { nav } = renderBreadcrumb("?issue=issue-1");

    const button = await screen.findByRole("button");
    expect(button.textContent).toMatch(/loading/i);
    // Clicking early is legal — the detail page owns its own skeleton.
    fireEvent.click(button);
    expect(nav.push).toHaveBeenCalledWith("/ws-slug/issues/issue-1");
  });

  it("accepts an explicit issueId that overrides the search param absence", async () => {
    mockGetIssue.mockResolvedValue(ISSUE_FIXTURE);
    const { nav } = renderBreadcrumb("", { issueId: "prop-id" });

    await waitFor(() => expect(mockGetIssue).toHaveBeenCalledWith("prop-id"));
    fireEvent.click(await screen.findByRole("button"));
    expect(nav.push).toHaveBeenCalledWith("/ws-slug/issues/prop-id");
  });

  it("keeps the issue link clickable when the slug context exists but fails to resolve", async () => {
    // Deleted/gone issue: query errors out; the strip degrades to the
    // generic label and stays enabled so the user can still try the jump.
    mockGetIssue.mockRejectedValue(new Error("gone"));
    renderBreadcrumb("?issue=gone-id");

    const button = await screen.findByRole("button") as HTMLButtonElement;
    await waitFor(() => expect(button.disabled).toBe(false));
    expect(button.textContent).toMatch(/back to task/i);
  });
});
