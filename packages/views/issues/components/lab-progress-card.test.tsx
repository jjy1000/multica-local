// lab-progress-card.test.tsx (0.5.86)
//
// Per-lab progress card in the issue Labs section: idle / running / done /
// failed / engine-down states for pythia + timesfm (mocked endpoints),
// live-snapshot states for claude_science_lab, the auxiliary muted row for
// causal_graph, and click-through href correctness (issue-scoped vs
// ?run=-scoped deep links). Mirrors the mocking patterns of
// lab-output-panel.test.tsx: real parseWithFallback + schemas, only
// api.rawRequest overridden.

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor, cleanup } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { I18nProvider } from "@multica/core/i18n/react";
import type { NavigationAdapter } from "../../navigation";
import { NavigationProvider } from "../../navigation";
import enIssues from "../../locales/en/issues.json";
import { LabProgressCard } from "./lab-progress-card";
import {
  pythiaTriggerKey,
  PYTHIA_IN_PROGRESS_WINDOW_MS,
  TIMESFM_RECENT_RUN_WINDOW_MS,
} from "../../experimental/components/lab-run-heuristics";

// Keep the real parseWithFallback (and everything else in the barrel) and
// override only api.rawRequest so each card state is driven deterministically.
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

const pythiaRun = {
  id: "prun-1",
  rounds: 2,
  source: "oracle",
  created_at: "2026-08-14T00:00:00Z",
  envelopes: [],
};

const timesfmRun = {
  id: "tfn-1",
  horizons: 24,
  provenance: "model",
  created_at: "2026-08-14T00:00:00Z",
  result: { series: [], provenance: "model", model_present: true, horizon: 24 },
};

const mythosRun = {
  run_id: "mrun-1",
  status: "running",
  mode: "enhancer",
  started_at: "2026-08-14T00:00:00Z",
  problem: "How do we grow?",
  iterations: 3,
  completed_at: null,
  final_issue_id: null,
  coda_conclusions: [],
};

function Wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const nav: NavigationAdapter = {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/issues/issue-1",
    searchParams: new URLSearchParams(),
    getShareableUrl: (p: string) => p,
  };
  return (
    <QueryClientProvider client={client}>
      <I18nProvider locale="en" resources={{ en: { issues: enIssues } }}>
        <NavigationProvider value={nav}>{children}</NavigationProvider>
      </I18nProvider>
    </QueryClientProvider>
  );
}

function renderCard(
  props: Partial<Parameters<typeof LabProgressCard>[0]> = {},
) {
  return render(
    <LabProgressCard
      issueId="issue-1"
      workspaceId="ws-1"
      labSource="pythia_oracle"
      flagEnabled={true}
      {...props}
    />,
    { wrapper: Wrapper },
  );
}

function card(): HTMLElement {
  return screen.getByTestId("lab-progress-card");
}

function cardHref(): string | null {
  return card().querySelector("a")?.getAttribute("href") ?? null;
}

beforeEach(() => {
  mockRawRequest.mockReset();
  window.sessionStorage.clear();
});

afterEach(() => {
  cleanup();
});

describe("LabProgressCard — pythia_oracle", () => {
  it("renders done with rounds + source summary and the run-scoped deep link", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(200, [pythiaRun]));
    renderCard();

    await waitFor(() => expect(card().dataset.state).toBe("done"));
    expect(card().textContent).toContain("2 rounds");
    expect(card().textContent).toContain("oracle");
    expect(cardHref()).toBe("/experimental/pythia?issue=issue-1&run=prun-1");
  });

  it("renders idle with the issue-scoped link when no runs exist and nothing was triggered", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(200, []));
    renderCard();

    await waitFor(() => expect(card().dataset.state).toBe("idle"));
    expect(card().textContent).toContain("Not run");
    expect(cardHref()).toBe("/experimental/pythia?issue=issue-1");
  });

  it("renders running from the shared sessionStorage trigger heuristic (fresh trigger, no rows)", async () => {
    window.sessionStorage.setItem(
      pythiaTriggerKey("ws-1", "issue-1"),
      String(Date.now() - 5_000),
    );
    mockRawRequest.mockResolvedValue(makeResponse(200, []));
    renderCard();

    await waitFor(() => expect(card().dataset.state).toBe("running"));
  });

  it("renders engine_down when the trigger went stale with no rows (stuck oracle)", async () => {
    window.sessionStorage.setItem(
      pythiaTriggerKey("ws-1", "issue-1"),
      String(Date.now() - (PYTHIA_IN_PROGRESS_WINDOW_MS + 10_000)),
    );
    mockRawRequest.mockResolvedValue(makeResponse(200, []));
    renderCard();

    await waitFor(() => expect(card().dataset.state).toBe("engine_down"));
    expect(card().textContent).toContain("Engine not running");
  });

  it("renders failed when the runs endpoint errors (non-404)", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(500, { error: "boom" }));
    renderCard();

    await waitFor(() => expect(card().dataset.state).toBe("failed"));
  });
});

describe("LabProgressCard — timesfm", () => {
  it("renders done with horizons + honest provenance and the run-scoped deep link", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(200, [timesfmRun]));
    renderCard({ labSource: "timesfm" });

    await waitFor(() => expect(card().dataset.state).toBe("done"));
    expect(card().textContent).toContain("H24");
    expect(card().textContent).toContain("model");
    expect(cardHref()).toBe(
      "/experimental/timesfm-lab?issue=issue-1&run=tfn-1",
    );
  });

  it("renders running (recency heuristic) when the latest run is younger than the recent-run window", async () => {
    mockRawRequest.mockResolvedValue(
      makeResponse(200, [
        {
          ...timesfmRun,
          created_at: new Date().toISOString(),
        },
      ]),
    );
    renderCard({ labSource: "timesfm" });

    await waitFor(() => expect(card().dataset.state).toBe("running"));
    // The run data still rides along — a persisted row is a completed run.
    expect(card().textContent).toContain("H24");
  });

  it("renders idle when no runs exist (absence is idle, not engine-down — GET /runs works engine-down)", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(200, []));
    renderCard({ labSource: "timesfm" });

    await waitFor(() => expect(card().dataset.state).toBe("idle"));
    expect(cardHref()).toBe("/experimental/timesfm-lab?issue=issue-1");
  });

  it("renders engine_down when the runs endpoint answers 5xx", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(503, { error: "down" }));
    renderCard({ labSource: "timesfm" });

    await waitFor(() => expect(card().dataset.state).toBe("engine_down"));
  });
});

describe("LabProgressCard — mythos_swarm", () => {
  it("renders running with the current-loop count from `iterations` (current_loop; no max on the wire)", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(200, [mythosRun]));
    renderCard({ labSource: "mythos_swarm" });

    await waitFor(() => expect(card().dataset.state).toBe("running"));
    expect(card().textContent).toContain("iteration 3");
    expect(card().textContent).toContain("enhancer");
    expect(cardHref()).toBe("/experimental/mythos?issue=issue-1&run=mrun-1");
  });

  it("renders done for a completed run with the problem summary", async () => {
    mockRawRequest.mockResolvedValue(
      makeResponse(200, [{ ...mythosRun, status: "completed" }]),
    );
    renderCard({ labSource: "mythos_swarm" });

    await waitFor(() => expect(card().dataset.state).toBe("done"));
    expect(card().textContent).toContain("How do we grow?");
  });

  it("renders failed for aborted runs", async () => {
    mockRawRequest.mockResolvedValue(
      makeResponse(200, [{ ...mythosRun, status: "aborted" }]),
    );
    renderCard({ labSource: "mythos_swarm" });

    await waitFor(() => expect(card().dataset.state).toBe("failed"));
  });

  it("renders idle when no runs exist", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(200, []));
    renderCard({ labSource: "mythos_swarm" });

    await waitFor(() => expect(card().dataset.state).toBe("idle"));
  });
});

describe("LabProgressCard — claude_science_lab (snapshot-driven)", () => {
  it("renders running from the live snapshot flags with the issue-scoped link", () => {
    renderCard({
      labSource: "claude_science_lab",
      live: { running: true, queued: false, failed: false, cancelled: false },
    });
    expect(card().dataset.state).toBe("running");
    expect(cardHref()).toBe("/experimental/claude-lab?issue=issue-1");
  });

  it("renders failed from the live flags", () => {
    renderCard({
      labSource: "claude_science_lab",
      live: { running: false, queued: false, failed: true, cancelled: false },
    });
    expect(card().dataset.state).toBe("failed");
  });

  it("renders idle when nothing is in flight", () => {
    renderCard({
      labSource: "claude_science_lab",
      live: { running: false, queued: false, failed: false, cancelled: true },
    });
    expect(card().dataset.state).toBe("idle");
  });
});

describe("LabProgressCard — auxiliary + uncovered labs", () => {
  it("renders the muted auxiliary row for causal_graph with no click-through", () => {
    renderCard({ labSource: "causal_graph" });
    expect(
      screen.getByTestId("lab-progress-card-auxiliary"),
    ).toBeInTheDocument();
    expect(
      screen.getByTestId("lab-progress-card-auxiliary").textContent,
    ).toContain("Auxiliary plugin");
    expect(
      screen.getByTestId("lab-progress-card-auxiliary").querySelector("a"),
    ).toBeNull();
    expect(screen.queryByTestId("lab-progress-card")).toBeNull();
  });

  it("renders the muted auxiliary row for llm_wiki_bridge and semantica", () => {
    for (const lab of ["llm_wiki_bridge", "semantica"]) {
      const { unmount } = renderCard({ labSource: lab });
      expect(
        screen.getByTestId("lab-progress-card-auxiliary"),
      ).toBeInTheDocument();
      unmount();
    }
  });

  it("renders nothing for swarm_topology (pill covers it) and code_canvas / user plugins", () => {
    for (const lab of ["swarm_topology", "code_canvas", "user_my_plugin"]) {
      const { container } = render(
        <LabProgressCard
          issueId="issue-1"
          workspaceId="ws-1"
          labSource={lab}
          flagEnabled={true}
        />,
        { wrapper: Wrapper },
      );
      expect(container).toBeEmptyDOMElement();
      cleanup();
    }
  });

  it("suppresses the click-through when the flag is disabled but still shows the state", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(200, [pythiaRun]));
    renderCard({ flagEnabled: false });

    await waitFor(() => expect(card().dataset.state).toBe("done"));
    expect(cardHref()).toBeNull();
  });
});

describe("LabProgressCard — timesfm recency window contract", () => {
  it("keeps the recency window at the documented 2 minutes", () => {
    expect(TIMESFM_RECENT_RUN_WINDOW_MS).toBe(120_000);
  });

  it("treats unparseable / future-skewed timestamps as not recent", async () => {
    mockRawRequest.mockResolvedValue(
      makeResponse(200, [
        { ...timesfmRun, created_at: "not-a-timestamp" },
      ]),
    );
    renderCard({ labSource: "timesfm" });

    await waitFor(() => expect(card().dataset.state).toBe("done"));
  });
});
