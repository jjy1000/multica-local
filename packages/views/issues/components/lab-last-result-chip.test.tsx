// LabLastResultChip contract tests (0.5.81 post-ship audit P2).
//
// The chip is the flagship jump-out emitter for claude_science_lab: it
// must render only when a terminal run with a summary exists, must emit
// the ?issue=&run= deep link toward the claude-lab view (whose PlanTimeline
// consumes it — audit P1 receiver), and must stay silent for other lab
// sources (audit P3: the mount site was narrowed to match this reality).

import { describe, expect, it, vi, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactElement } from "react";
import type { NavigationAdapter } from "../../navigation";
import { NavigationProvider } from "../../navigation";
import { renderWithI18n } from "../../test/i18n";
import { LabLastResultChip } from "./lab-last-result-chip";

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

const claudeContext = {
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
      id: "task-latest",
      status: "completed",
      trigger_summary: null,
      error: null,
      failure_reason: null,
      result_summary: "Forecasts point to a 12% lift next cycle.",
      result_attachments: [],
      result_predictions: [],
      result_code_blocks: [],
      created_at: "2026-08-14T01:00:00Z",
      dispatched_at: null,
      started_at: null,
      completed_at: null,
      duration_ms: null,
    },
    // In-flight newer task — must NOT win "latest" (chip shows terminal runs).
    {
      id: "task-inflight",
      status: "running",
      trigger_summary: null,
      error: null,
      failure_reason: null,
      result_summary: null,
      result_attachments: [],
      result_predictions: [],
      result_code_blocks: [],
      created_at: "2026-08-14T02:00:00Z",
      dispatched_at: null,
      started_at: "2026-08-14T02:00:01Z",
      completed_at: null,
      duration_ms: null,
    },
  ],
  comments: [],
  chat_session_id: null,
  lab_seq: 2,
  server_time: "2026-08-14T02:00:00Z",
};

function navStub(): NavigationAdapter {
  return {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/ws-slug/issues/issue-1",
    searchParams: new URLSearchParams(""),
    getShareableUrl: (p: string) => p,
  };
}

function ui(labSource: string): ReactElement {
  return (
    <QueryClientProvider client={new QueryClient()}>
      <NavigationProvider value={navStub()}>
        <LabLastResultChip wsId="ws-1" issueId="issue-1" labSource={labSource} />
      </NavigationProvider>
    </QueryClientProvider>
  );
}

beforeEach(() => {
  mockRawRequest.mockReset();
});

describe("LabLastResultChip", () => {
  it("renders the latest TERMINAL run summary with its deep link", async () => {
    mockRawRequest.mockResolvedValue(makeResponse(200, claudeContext));
    const view = renderWithI18n(ui("claude_science_lab"));

    const chip = await view.findByTestId("lab-last-result-chip");
    expect(chip.textContent).toContain(
      "Forecasts point to a 12% lift next cycle.",
    );

    const link = screen.getByRole("link");
    expect(link.getAttribute("href")).toBe(
      "/experimental/claude-lab?issue=issue-1&run=task-latest",
    );
  });

  it("renders nothing for non-claude sources and issues no fetch", () => {
    const { container } = renderWithI18n(ui("mythos_swarm"));
    expect(container).toBeEmptyDOMElement();
    expect(mockRawRequest).not.toHaveBeenCalled();
  });

  it("renders nothing when no terminal run has a summary", async () => {
    const noneTerminal = structuredClone(claudeContext);
    // Drop the completed run entirely; the remaining in-flight task must
    // not masquerade as a deliverable.
    noneTerminal.tasks = noneTerminal.tasks.filter((t) => t.status !== "completed");
    mockRawRequest.mockResolvedValue(makeResponse(200, noneTerminal));
    const { container } = renderWithI18n(ui("claude_science_lab"));
    // Wait for the query to resolve, then assert the chip stayed null
    // (the raw fetch fired but produced nothing renderable).
    await waitFor(() => expect(mockRawRequest).toHaveBeenCalledTimes(1));
    expect(container.querySelector('[data-testid="lab-last-result-chip"]')).toBeNull();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });
});
