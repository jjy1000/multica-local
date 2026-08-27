// 0.5.82 WL2 — TimesfmView smoke coverage.
//
// The timesfm-lab route is the records-only ICP-2/ICP-3 surface: it binds
// the issue via ?issue=, lists persisted timesfm_forecast_run rows
// newest-first (works engine-down), and honours ?run= deep links by
// highlighting + auto-expanding the matching row. These tests pin:
//
//   1. Run rows render with horizon + provenance badges; the newest run
//      auto-expands its quantile-band chart.
//   2. A ?run= deep link marks the matching row (aria-current) and
//      auto-expands it — the LabRunLink receiver contract.
//   3. Empty history renders the assignee-driven empty hint (ICP-1:
//      no manual trigger in the view).
//   4. Fetch failure renders the error bar with a retry affordance.
//   5. Flag-off renders the disabled placeholder (flag-gated bypass).

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";

import { TimesfmView } from "./timesfm-view";
import { NavigationProvider } from "@multica/views/navigation";
import type { NavigationAdapter } from "@multica/views/navigation";

type RequestLog = Array<{ method: string; path: string }>;

let runsResponse: unknown = [];
let runsStatus = 200;
let runsReject = false;
let flagEnabled = true;
const requests: RequestLog = [];

vi.mock("@multica/core/platform", () => ({
  getCurrentWsId: () => "ws-1",
  getCurrentSlug: () => "ws",
}));

vi.mock("@multica/core/api", async () => {
  const actual = await vi.importActual<typeof import("@multica/core/api")>(
    "@multica/core/api",
  );
  return {
    ...actual,
    api: {
      ...actual.api,
      rawRequest: vi.fn(async (path: string, init?: { method?: string }) => {
        requests.push({ method: init?.method ?? "GET", path });
        if (runsReject) throw new Error("boom");
        return {
          status: runsStatus,
          ok: runsStatus >= 200 && runsStatus < 300,
          json: async () => runsResponse,
        } as unknown as Response;
      }),
    },
  };
});

vi.mock("@multica/core/experimental", async () => {
  const actual = await vi.importActual<
    typeof import("@multica/core/experimental")
  >("@multica/core/experimental");
  return {
    ...actual,
    useExperimentalFlag: () => flagEnabled,
  };
});

// Minimal dictionary matching every i18n selector the timesfm view walks,
// with naive {{param}} interpolation so run-count assertions read naturally.
const dict: Record<string, unknown> = {
  title: "TimesFM Forecast Lab",
  flag_off: "TimesFM is disabled.",
  unbound_title: "No issue bound",
  unbound_hint: "",
  records_title: "Forecast records",
  run_count: "{{runs}} runs",
  loading: "Loading records…",
  load_failed: "Failed to load forecast records",
  retry: "Retry",
  empty: "No forecast runs yet.",
  empty_hint: "",
  weights_hint: "",
  horizon_label: "horizon",
  series_count: "",
  series_label: "series {{index}}",
  chart_legend: "median · 80% band · 90% band",
  no_series: "",
  dates_label: "",
};

vi.mock("@multica/views/i18n", () => ({
  useT: () => ({
    t: (sel: (d: Record<string, unknown>) => unknown, params?: Record<string, string>) => {
      const raw = sel(dict);
      if (typeof raw !== "string" || !params) return raw;
      return Object.entries(params).reduce(
        (acc, [k, v]) => acc.replaceAll(`{{${k}}}`, v),
        raw,
      );
    },
  }),
}));

vi.mock("@multica/views/experimental/components", async () => {
  // The shared breadcrumb is stubbed like in the swarm suite — this file
  // tracks view logic, not the strip. useDeepLinkRun stays real: it is the
  // ICP-3 receiver contract under test here.
  const actual = await vi.importActual<
    typeof import("@multica/views/experimental/components")
  >("@multica/views/experimental/components");
  return {
    ...actual,
    IssueBreadcrumb: () => <div data-testid="issue-breadcrumb" />,
  };
});

const run1 = {
  id: "run-1",
  horizons: 3,
  provenance: "model",
  created_at: "2026-08-27T10:00:00Z",
  result: {
    series: [
      {
        point: [10, 11, 12],
        quantiles: {
          lower_90: [9, 10, 11],
          lower_80: [9.5, 10.5, 11.5],
          median: [10, 11, 12],
          upper_80: [10.5, 11.5, 12.5],
          upper_90: [11, 12, 13],
        },
        provenance: "model",
      },
    ],
    provenance: "model",
    model_present: true,
    horizon: 3,
  },
};

const run2 = {
  id: "run-2",
  horizons: 24,
  provenance: "seasonal_naive",
  created_at: "2026-08-27T11:00:00Z",
  result: {
    series: [{ point: [1, 2], provenance: "seasonal_naive" }],
    provenance: "seasonal_naive",
    model_present: false,
    horizon: 24,
  },
};

function makeNav(search: string): NavigationAdapter {
  return {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/experimental/timesfm-lab",
    searchParams: new URLSearchParams(search),
    getShareableUrl: (p: string) => p,
  };
}

function renderView(initialPath: string) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const search = initialPath.split("?")[1] ?? "";
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[initialPath]}>
        <NavigationProvider value={makeNav(search)}>
          <TimesfmView />
        </NavigationProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  runsResponse = [];
  runsStatus = 200;
  runsReject = false;
  flagEnabled = true;
  requests.length = 0;
  // jsdom does not implement scrollIntoView; useDeepLinkRun calls it when
  // the deep-linked row mounts.
  Element.prototype.scrollIntoView = vi.fn();
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("TimesfmView", () => {
  it("lists persisted runs newest-first with provenance badges; click expands the chart", async () => {
    runsResponse = [run2, run1]; // newest first, per the server contract
    renderView("/experimental/timesfm-lab?issue=issue-1");

    await waitFor(() =>
      expect(screen.getByText("2 runs")).toBeInTheDocument(),
    );
    expect(screen.getByTestId("timesfm-run-row-run-1")).toBeInTheDocument();
    expect(screen.getByTestId("timesfm-run-row-run-2")).toBeInTheDocument();
    // Provenance badges render verbatim from the DB CHECK set.
    expect(screen.getAllByText("seasonal_naive").length).toBeGreaterThan(0);
    // Requests hit the runs endpoint scoped to the bound issue.
    expect(requests.some((r) => r.path.includes("issue_id=issue-1"))).toBe(true);

    // Expansion contract: nothing is expanded without a deep link or a
    // click (a LabRunLink jump means "show me this run"; a plain visit is
    // a quiet ledger). Clicking the newest row expands its chart.
    expect(screen.queryByText(/median · 80% band · 90% band/)).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId("timesfm-run-row-run-1"));
    expect(screen.getByText(/median · 80% band · 90% band/)).toBeInTheDocument();
    // Row badge + expanded per-series badge can repeat the same label.
    expect(screen.getAllByText("model").length).toBeGreaterThan(0);
  });

  it("highlights and auto-expands the deep-linked run (?run=)", async () => {
    runsResponse = [run2, run1];
    renderView("/experimental/timesfm-lab?issue=issue-1&run=run-2");

    await waitFor(() =>
      expect(screen.getByTestId("timesfm-run-row-run-2")).toHaveAttribute(
        "aria-current",
        "true",
      ),
    );
    expect(screen.getByTestId("timesfm-run-row-run-1")).not.toHaveAttribute(
      "aria-current",
    );
    // The deep-linked run is the one expanded, not the newest.
    expect(screen.getByText(/series 1/)).toBeInTheDocument();
  });

  it("shows the assignee-driven empty state when the issue has no runs", async () => {
    runsResponse = [];
    renderView("/experimental/timesfm-lab?issue=issue-1");

    await waitFor(() =>
      expect(screen.getByText("No forecast runs yet.")).toBeInTheDocument(),
    );
    expect(screen.queryByText(/Retry/)).not.toBeInTheDocument();
  });

  it("shows an error bar with retry when the runs fetch fails", async () => {
    runsReject = true;
    renderView("/experimental/timesfm-lab?issue=issue-1");

    await waitFor(() =>
      expect(
        screen.getByText("Failed to load forecast records"),
      ).toBeInTheDocument(),
    );
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
  });

  it("renders the flag-off placeholder without fetching", () => {
    flagEnabled = false;
    renderView("/experimental/timesfm-lab?issue=issue-1");

    expect(screen.getByText("TimesFM is disabled.")).toBeInTheDocument();
    expect(requests).toHaveLength(0);
  });
});
