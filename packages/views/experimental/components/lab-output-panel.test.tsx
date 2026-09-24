import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { I18nProvider } from "@multica/core/i18n/react";
import { EMPTY_LAB_CONTEXT } from "@multica/core/api/schemas";
import type { NavigationAdapter } from "../../navigation";
import { NavigationProvider } from "../../navigation";
import enExperimental from "../../locales/en/experimental.json";
import { LabOutputPanel } from "./lab-output-panel";

// Keep the real parseWithFallback (and everything else in the barrel) and
// override only api.rawRequest so the component's 404/error handling can be
// driven deterministically. getBaseUrl is stubbed in case an attachment
// renderer resolves a URL.
const mockRawRequest = vi.fn();
vi.mock("@multica/core/api", async () => {
  const actual = await vi.importActual<typeof import("@multica/core/api")>(
    "@multica/core/api",
  );
  return {
    ...actual,
    api: {
      rawRequest: (...args: unknown[]) => mockRawRequest(...args),
      getBaseUrl: () => "http://localhost:8090",
    },
  };
});

// 0.5.104: PythiaPanel resolves the lab flag itself (flag-off honesty —
// the server 404s every pythia endpoint while the flag is off, which used
// to render as the misleading stuck panel). The mock defaults to enabled
// so pre-existing pythia renders keep the normal flow; the flag-off test
// flips the holder.
const flagState = vi.hoisted(() => ({
  data: [{ key: "pythia_oracle", enabled: true }] as
    | Array<{ key: string; enabled: boolean }>
    | undefined,
}));
vi.mock("@multica/core/experimental", async () => {
  const actual = await vi.importActual<
    typeof import("@multica/core/experimental")
  >("@multica/core/experimental");
  return {
    ...actual,
    useExperimentalFlags: () => ({ data: flagState.data }),
  };
});

function makeResponse(status: number, body: unknown): Response {
  return {
    status,
    ok: status >= 200 && status < 300,
    json: async () => body,
    text: async () => (typeof body === "string" ? body : JSON.stringify(body)),
  } as Response;
}

const dataContext = {
  issue: {
    id: "issue-1",
    workspace_id: "ws-1",
    title: "T",
    description: null,
    status: "todo",
    lab_source: "claude_science_lab",
    lab_mode: null,
    assignee_id: null,
    created_at: "2026-08-14T00:00:00Z",
    updated_at: "2026-08-14T00:00:00Z",
  },
  agent: null,
  tasks: [
    {
      id: "task-1",
      status: "completed",
      trigger_summary: null,
      error: null,
      failure_reason: null,
      result_summary: "Summary content",
      result_attachments: [{ kind: "md", name: "notes.md", data: "hello" }],
      result_predictions: [{ round: 1, scenario: "A", probability: 0.6, confidence: 0.8 }],
      result_code_blocks: [{ language: "ts", filename: "x.ts", code: "const a = 1" }],
      created_at: "2026-08-14T00:00:00Z",
      dispatched_at: null,
      started_at: null,
      completed_at: null,
      duration_ms: null,
    },
  ],
  comments: [],
  chat_session_id: null,
  lab_seq: 3,
  server_time: "2026-08-14T00:00:00Z",
};

const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });

const pythiaRun = {
  id: "prun-1",
  rounds: 2,
  source: "oracle",
  created_at: "2026-08-14T00:00:00Z",
  envelopes: [
    {
      id: "p_1",
      scenario: "Adoption grows",
      narrative: "Users double within a month",
      probability: 0.42,
      confidence: 0.71,
      horizon: "week",
      persona: "strategist",
      lab_source: "oracle",
      createdAt: "2026-08-14T00:00:00Z",
    },
    {
      id: "p_2",
      scenario: "Fallback path",
      narrative: "Synthetic fallback forecast",
      probability: 0.6,
      confidence: 0.5,
      horizon: "month",
      persona: "analyst",
      lab_source: "synthetic",
      synthetic_oracle_failover: true,
      createdAt: "2026-08-14T00:00:00Z",
    },
  ],
};

function Wrapper({ children }: { children: ReactNode }) {
  // useNavigation(); the adapter stub here mirrors the active-route query
  // string shape without needing react-router in the tree.
  const nav: NavigationAdapter = {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/experimental/claude-lab",
    searchParams: new URLSearchParams("?issue=issue-1"),
    getShareableUrl: (p: string) => p,
  };
  return (
    <QueryClientProvider client={client}>
      <I18nProvider locale="en" resources={{ en: { experimental: enExperimental } }}>
        <NavigationProvider value={nav}>{children}</NavigationProvider>
      </I18nProvider>
    </QueryClientProvider>
  );
}

function renderPanel() {
  return render(
    <LabOutputPanel wsId="ws-1" issueId="issue-1" labSource="claude_science_lab" />,
    { wrapper: Wrapper },
  );
}


function renderPythiaPanel() {
  return render(
    <LabOutputPanel wsId="ws-1" issueId="issue-1" labSource="pythia_oracle" />,
    { wrapper: Wrapper },
  );
}


beforeEach(() => {
  mockRawRequest.mockReset();
  flagState.data = [{ key: "pythia_oracle", enabled: true }];
});

describe("LabOutputPanel", () => {
  it("pythia panel says the lab is disabled when the flag is off (never the stuck panel)", async () => {
    flagState.data = [{ key: "pythia_oracle", enabled: false }];
    // The server 404s the runs poll while the flag is off — exactly the
    // condition that used to render the misleading "引擎未启动" stuck
    // panel with a silently-failing retry button.
    mockRawRequest.mockResolvedValue(makeResponse(404, { error: "not found" }));
    renderPythiaPanel();

    expect(
      screen.getByTestId("lab-output-panel-pythia-flag-off"),
    ).toBeInTheDocument();
    expect(screen.getByText("Pythia lab is disabled")).toBeInTheDocument();
    expect(
      screen.queryByTestId("lab-output-panel-pythia-stuck"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /retry/i }),
    ).not.toBeInTheDocument();
  });

  it("shows a loading skeleton, then the latest task output", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(200, dataContext));
    renderPanel();

    expect(screen.getByTestId("lab-output-panel-loading")).toBeInTheDocument();

    await waitFor(() => expect(screen.getByText("Summary content")).toBeInTheDocument());
    expect(screen.getByText("3 runs")).toBeInTheDocument();
    expect(screen.getByText("Summary")).toBeInTheDocument();
    expect(screen.getByText("Artifacts")).toBeInTheDocument();
    expect(screen.getByText("Predictions")).toBeInTheDocument();
    expect(screen.getByText("Code")).toBeInTheDocument();
  });

  it("shows the empty state when the lab has no runs", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(200, EMPTY_LAB_CONTEXT));
    renderPanel();

    await waitFor(() => expect(screen.getByText("No runs yet")).toBeInTheDocument());
  });

  it("shows an inline error bar and retries on click", async () => {
    mockRawRequest.mockRejectedValueOnce(new Error("boom"));
    mockRawRequest.mockResolvedValueOnce(makeResponse(200, dataContext));
    renderPanel();

    await waitFor(() =>
      expect(screen.getByText("Failed to load lab output")).toBeInTheDocument(),
    );

    screen.getByRole("button", { name: "Retry" }).click();

    await waitFor(() => expect(screen.getByText("Summary content")).toBeInTheDocument());
    expect(mockRawRequest).toHaveBeenCalledTimes(2);
  });

  it("shows the closed state when the context endpoint 404s", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(404, { error: "not found" }));
    renderPanel();

    await waitFor(() => expect(screen.getByText("Experiment is closed")).toBeInTheDocument());
  });

  it("shows the error summary when the latest run failed", async () => {
    const failedContext = {
      ...dataContext,
      tasks: [
        {
          id: "task-failed",
          status: "failed",
          trigger_summary: null,
          error: "the run exploded",
          failure_reason: null,
          result_summary: null,
          created_at: "2026-08-14T00:00:00Z",
          dispatched_at: null,
          started_at: null,
          completed_at: null,
          duration_ms: null,
        },
      ],
    };
    mockRawRequest.mockResolvedValue(makeResponse(200, failedContext));
    renderPanel();

    await waitFor(() => expect(screen.getByText("the run exploded")).toBeInTheDocument());
    expect(screen.queryByText("No runs yet")).not.toBeInTheDocument();
  });





  it("returns null for a non-A-class lab source", () => {
    const { container } = render(
      <LabOutputPanel wsId="ws-1" issueId="issue-1" labSource="llm_wiki_bridge" />,
      { wrapper: Wrapper },
    );

    expect(container).toBeEmptyDOMElement();
    expect(mockRawRequest).not.toHaveBeenCalled();
  });






  async function openHistoryTab() {
    // The tabs are icon + label buttons with `aria-pressed`, not `aria-label`.
    // Wait for the panel to leave the loading skeleton (those nodes have no
    // `role="button"`), then find the tab by text content.
    const historyTab = await waitFor(() =>
        screen
            .getAllByRole("button")
            .find((b) => /history/i.test(b.textContent ?? "")) ??
        null,
      { timeout: 2000 },
    );
    if (!historyTab) throw new Error("history tab button not found");
    historyTab.click();
  }

  it("shows the empty state when pythia has no runs", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(200, []));
    renderPythiaPanel();

    // 0.5.113: live animation moved to the issue-main-pane embed; the
    // property panel's history tab (the only run-scoped surface left)
    // shows the empty hint when there are no runs.
    openHistoryTab();
    await waitFor(() => expect(screen.getByText("No runs yet")).toBeInTheDocument());
  });

  it("renders the latest pythia run frames", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(200, [pythiaRun]));
    renderPythiaPanel();

    // 0.5.113: the property-panel history tab shows the run row's kind
    // + rounds/source; the live animation (timelines, council, report
    // markdown) moved to the issue-main-pane embed and is asserted there.
    await openHistoryTab();
    await waitFor(() =>
      expect(screen.getByTestId("pythia-history-row")).toBeInTheDocument(),
    );
    expect(screen.getByText("2 · oracle")).toBeInTheDocument();
  });

  it("marks a synthetic failover frame as mock data", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(200, [pythiaRun]));
    renderPythiaPanel();

    // 0.5.113: the synthetic failover flag rides on each per-round envelope,
    // which now lives in the main-pane embed; the history row here only
    // shows kind/status/rounds/source. Make sure the history row renders.
    await openHistoryTab();
    await waitFor(() =>
      expect(screen.getByTestId("pythia-history-row")).toBeInTheDocument(),
    );
  });

  it("shows the empty state when the pythia runs endpoint 404s", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(404, { error: "not found" }));
    renderPythiaPanel();

    await openHistoryTab();
    await waitFor(() => expect(screen.getByText("No runs yet")).toBeInTheDocument());
    expect(screen.queryByText("Failed to load lab output")).not.toBeInTheDocument();
  });

  it("shows an inline error bar and retries for pythia", async () => {
    // The hook fires an initial fetch that errors; clicking the retry
    // button re-runs the same queryFn. mockRawRequest resolves the *most
    // recent* mock; queue success before the error so the first call
    // lands on the error (LIFO) and the retry lands on success.
    //
    // 0.5.113 note: the panel no longer auto-renders run rows in the
    // default tab — they live in the history tab. We don't wait for the
    // row here because the hook's idle refetchInterval is 60 s; we just
    // confirm the retry re-fetches (call count goes 1 → 2) and the error
    // bar disappears.
    mockRawRequest.mockResolvedValue(makeResponse(200, [pythiaRun]));
    mockRawRequest.mockRejectedValueOnce(new Error("boom"));
    renderPythiaPanel();

    await waitFor(() =>
      expect(screen.getByText("Failed to load lab output")).toBeInTheDocument(),
    );
    expect(mockRawRequest).toHaveBeenCalledTimes(1);

    screen.getByRole("button", { name: "Retry" }).click();

    // After the retry click the panel re-renders from the same QueryClient
    // entry; the running state of that entry cycles error → loading →
    // success without us driving further clicks. The retry click itself
    // synchronously fires runsQuery.refetch, which counts as the second
    // call regardless of how long the resolved response takes to land.
    await waitFor(() =>
      expect(
        screen.queryByText("Failed to load lab output"),
      ).not.toBeInTheDocument(),
    );
    expect(mockRawRequest).toHaveBeenCalledTimes(2);
  });

  // ── timesfm (0.5.82 WL2) — mirrors the pythia trio ────────────────────
  // The compact reader shares the pythia panel's loading/error/empty
  // strings but reads through useTimesfmForecastRuns (limit=10, 404→[],
  // ICP-1: no manual trigger button anywhere).






});
