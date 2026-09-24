// lab-progress-card.test.tsx (0.5.86)
//
// Per-lab progress card in the issue Labs section: idle / running / done /
// failed / engine-down states for pythia (mocked endpoints),
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
  it("renders done with rounds + source summary and NO deep link (0.5.112 passive monitor)", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(200, [pythiaRun]));
    renderCard();

    await waitFor(() => expect(card().dataset.state).toBe("done"));
    expect(card().textContent).toContain("2 rounds");
    expect(card().textContent).toContain("oracle");
    // 0.5.112: the /experimental/pythia page is a passive monitor — the
    // card no longer deep-links into it.
    expect(cardHref()).toBeNull();
  });

  it("renders a RUNNING card while the async run row is in flight (0.5.112)", async () => {
    mockRawRequest.mockResolvedValue(
      makeResponse(200, [{ ...pythiaRun, status: "running" }]),
    );
    renderCard();

    await waitFor(() => expect(card().dataset.state).toBe("running"));
    expect(cardHref()).toBeNull();
  });

  it("renders idle with NO issue-scoped link when no runs exist and nothing was triggered", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(200, []));
    renderCard();

    await waitFor(() => expect(card().dataset.state).toBe("idle"));
    expect(card().textContent).toContain("Not run");
    expect(cardHref()).toBeNull();
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

  it("renders the muted auxiliary row for llm_wiki_bridge", () => {
    for (const lab of ["llm_wiki_bridge"]) {
      const { unmount } = renderCard({ labSource: lab });
      expect(
        screen.getByTestId("lab-progress-card-auxiliary"),
      ).toBeInTheDocument();
      unmount();
    }
  });


  it("renders the plugin card for user_* sources (0.5.112 property-panel integration)", async () => {
    // The card polls the plugin's artifact index; a resolved (empty) list
    // renders the idle plugin card instead of the old null.
    mockRawRequest.mockResolvedValue(makeResponse(200, []));
    render(
      <LabProgressCard
        issueId="issue-1"
        workspaceId="ws-1"
        labSource="user_my_plugin"
        flagEnabled={true}
      />,
      { wrapper: Wrapper },
    );
    await waitFor(() => expect(screen.getByTestId("lab-progress-card").dataset.state).toBe("idle"));
    expect(screen.getByTestId("lab-progress-card").textContent).toContain("enabled");
  });

  it("suppresses the click-through when the flag is disabled but still shows the state", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(200, [pythiaRun]));
    renderCard({ flagEnabled: false });

    await waitFor(() => expect(card().dataset.state).toBe("done"));
    expect(cardHref()).toBeNull();
  });
});

