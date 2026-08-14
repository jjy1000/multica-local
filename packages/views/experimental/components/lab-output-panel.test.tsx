import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { I18nProvider } from "@multica/core/i18n/react";
import { EMPTY_LAB_CONTEXT } from "@multica/core/api/schemas";
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

const mythosRun = {
  run_id: "run-1",
  status: "completed",
  mode: "enhancer",
  started_at: "2026-08-14T00:00:00Z",
  problem: "How do we grow?",
  iterations: 3,
  completed_at: null,
  final_issue_id: null,
  coda_conclusions: [
    { key: "verdict", value: "ship it" },
    { key: "risk", value: "low", confidence: 0.9 },
  ],
};

const superviseState = {
  run_id: "run-1",
  phase: "supervising",
  started_at: "2026-08-14T00:00:00Z",
  last_check_at: "2026-08-14T00:00:00Z",
  last_tick_duration_ms: 120,
  total_ticks: 5,
  sub_tasks_total: 4,
  sub_tasks_done: 2,
  latest_reflection: "on track",
  latest_reflection_iter: 2,
};

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
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <QueryClientProvider client={client}>
      <I18nProvider locale="en" resources={{ en: { experimental: enExperimental } }}>
        {children}
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

function renderMythosPanel(labMode?: "sole" | "enhancer") {
  return render(
    <LabOutputPanel
      wsId="ws-1"
      issueId="issue-1"
      labSource="mythos_swarm"
      labMode={labMode}
    />,
    { wrapper: Wrapper },
  );
}

function renderPythiaPanel() {
  return render(
    <LabOutputPanel wsId="ws-1" issueId="issue-1" labSource="pythia_oracle" />,
    { wrapper: Wrapper },
  );
}

function renderCodeCanvasPanel() {
  return render(
    <LabOutputPanel wsId="ws-1" issueId="issue-1" labSource="code_canvas" />,
    { wrapper: Wrapper },
  );
}

beforeEach(() => {
  mockRawRequest.mockReset();
});

describe("LabOutputPanel", () => {
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

  it("shows the code_canvas input and empty history state", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(200, []));
    renderCodeCanvasPanel();

    await waitFor(() =>
      expect(
        screen.getByText("No saved artifacts yet — render some code above."),
      ).toBeInTheDocument(),
    );
    expect(screen.getByRole("button", { name: "Render & save" })).toBeInTheDocument();
    expect(screen.getByLabelText("Code")).toBeInTheDocument();
    expect(screen.getByLabelText("Language")).toBeInTheDocument();
  });

  it("renders saved code_canvas artifacts as sandboxed iframes", async () => {
    const artifacts = [
      {
        id: "a-1",
        code: "print(1)",
        language: "python",
        html: "<html><body>one</body></html>",
        created_at: "2026-08-14T00:00:00Z",
      },
    ];
    mockRawRequest.mockResolvedValue(makeResponse(200, artifacts));
    renderCodeCanvasPanel();

    await waitFor(() =>
      expect(screen.getByTitle("python · 2026-08-14T00:00:00Z")).toBeInTheDocument(),
    );
    const iframe = screen.getByTitle("python · 2026-08-14T00:00:00Z");
    expect(iframe).toHaveAttribute("sandbox", "");
    expect(iframe.getAttribute("srcDoc")).toContain("<body>one</body>");
  });

  it("renders and persists a code_canvas artifact, then refreshes history", async () => {
    const artifact = {
      id: "a-1",
      code: "print(1)",
      language: "python",
      html: "<html><body>one</body></html>",
      created_at: "2026-08-14T00:00:00Z",
    };
    let saved = false;
    mockRawRequest.mockImplementation(async (path: string, init?: RequestInit) => {
      if (path === "/experimental/code-canvas/render") {
        return makeResponse(200, "<html><body>one</body></html>");
      }
      if (init?.method === "POST") {
        saved = true;
        return makeResponse(201, artifact);
      }
      return makeResponse(200, saved ? [artifact] : []);
    });
    renderCodeCanvasPanel();

    await waitFor(() =>
      expect(
        screen.getByText("No saved artifacts yet — render some code above."),
      ).toBeInTheDocument(),
    );

    screen.getByRole("button", { name: "Render & save" }).click();

    await waitFor(() =>
      expect(screen.getByTitle("python · 2026-08-14T00:00:00Z")).toBeInTheDocument(),
    );

    expect(mockRawRequest).toHaveBeenCalledWith(
      "/experimental/code-canvas/render",
      expect.objectContaining({ method: "POST" }),
    );
    expect(mockRawRequest).toHaveBeenCalledWith(
      "/api/experimental/code-canvas/issues/issue-1/artifacts",
      expect.objectContaining({
        method: "POST",
        body: expect.stringContaining("fibonacci"),
      }),
    );
  });

  it("shows an inline error bar and retries for code_canvas history", async () => {
    mockRawRequest.mockRejectedValueOnce(new Error("boom"));
    mockRawRequest.mockResolvedValueOnce(makeResponse(200, []));
    renderCodeCanvasPanel();

    await waitFor(() =>
      expect(screen.getByText("Failed to load lab output")).toBeInTheDocument(),
    );

    screen.getByRole("button", { name: "Retry" }).click();

    await waitFor(() =>
      expect(
        screen.getByText("No saved artifacts yet — render some code above."),
      ).toBeInTheDocument(),
    );
    expect(mockRawRequest).toHaveBeenCalledTimes(2);
  });

  it("returns null for a non-A-class lab source", () => {
    const { container } = render(
      <LabOutputPanel wsId="ws-1" issueId="issue-1" labSource="llm_wiki_bridge" />,
      { wrapper: Wrapper },
    );

    expect(container).toBeEmptyDOMElement();
    expect(mockRawRequest).not.toHaveBeenCalled();
  });

  it("shows the latest mythos run problem and conclusions", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(200, [mythosRun]));
    renderMythosPanel("sole");

    await waitFor(() => expect(screen.getByText("How do we grow?")).toBeInTheDocument());
    expect(screen.getByText("Problem")).toBeInTheDocument();
    expect(screen.getByText("Conclusions")).toBeInTheDocument();
    expect(screen.getByText("verdict:")).toBeInTheDocument();
    expect(screen.getByText("ship it")).toBeInTheDocument();
    expect(screen.getByText("1 runs")).toBeInTheDocument();
  });

  it("shows the empty state when mythos has no runs", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(200, []));
    renderMythosPanel();

    await waitFor(() => expect(screen.getByText("No runs yet")).toBeInTheDocument());
  });

  it("renders the supervise section in enhancer mode and POSTs a tick", async () => {
    mockRawRequest.mockImplementation((path: string) => {
      if (path.includes("/mythos-runs")) return Promise.resolve(makeResponse(200, [mythosRun]));
      return Promise.resolve(makeResponse(200, superviseState));
    });
    renderMythosPanel("enhancer");

    await waitFor(() => expect(screen.getByText("supervising")).toBeInTheDocument());
    expect(screen.getByText("2/4")).toBeInTheDocument();
    expect(screen.getByText("on track")).toBeInTheDocument();

    screen.getByRole("button", { name: "Check now" }).click();

    await waitFor(() =>
      expect(mockRawRequest).toHaveBeenCalledWith(
        expect.stringContaining("/tick"),
        expect.objectContaining({ method: "POST" }),
      ),
    );
  });

  it("shows a fallback when the latest run has no problem text", async () => {
    const runNoProblem = { ...mythosRun, problem: "" };
    mockRawRequest.mockResolvedValue(makeResponse(200, [runNoProblem]));
    renderMythosPanel("sole");

    await waitFor(() => expect(screen.getByText("No problem description")).toBeInTheDocument());
    expect(screen.queryByText(mythosRun.run_id)).not.toBeInTheDocument();
  });

  it("disables the check button when the supervise phase is terminal", async () => {
    mockRawRequest.mockImplementation((path: string) => {
      if (path.includes("/mythos-runs")) return Promise.resolve(makeResponse(200, [mythosRun]));
      return Promise.resolve(makeResponse(200, { ...superviseState, phase: "done" }));
    });
    renderMythosPanel("enhancer");

    await waitFor(() => expect(screen.getByText("done")).toBeInTheDocument());
    expect(screen.getByRole("button", { name: "Check now" })).toBeDisabled();
  });

  it("shows the empty state when pythia has no runs", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(200, []));
    renderPythiaPanel();

    await waitFor(() => expect(screen.getByText("No runs yet")).toBeInTheDocument());
  });

  it("renders the latest pythia run frames", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(200, [pythiaRun]));
    renderPythiaPanel();

    await waitFor(() => expect(screen.getByText(/Adoption grows/)).toBeInTheDocument());
    expect(screen.getByText("2 runs")).toBeInTheDocument();
    expect(screen.getByText("42%")).toBeInTheDocument();
    expect(screen.getByText("Users double within a month")).toBeInTheDocument();
  });

  it("marks a synthetic failover frame as mock data", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(200, [pythiaRun]));
    renderPythiaPanel();

    await waitFor(() => expect(screen.getByText("mock data")).toBeInTheDocument());
  });

  it("shows the empty state when the pythia runs endpoint 404s", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(404, { error: "not found" }));
    renderPythiaPanel();

    await waitFor(() => expect(screen.getByText("No runs yet")).toBeInTheDocument());
    expect(screen.queryByText("Failed to load lab output")).not.toBeInTheDocument();
  });

  it("shows an inline error bar and retries for pythia", async () => {
    mockRawRequest.mockRejectedValueOnce(new Error("boom"));
    mockRawRequest.mockResolvedValueOnce(makeResponse(200, [pythiaRun]));
    renderPythiaPanel();

    await waitFor(() =>
      expect(screen.getByText("Failed to load lab output")).toBeInTheDocument(),
    );

    screen.getByRole("button", { name: "Retry" }).click();

    await waitFor(() => expect(screen.getByText(/Adoption grows/)).toBeInTheDocument());
    expect(mockRawRequest).toHaveBeenCalledTimes(2);
  });
});
