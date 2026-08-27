// @vitest-environment jsdom

// ExecutionLogSection inbound deep-link integration (0.5.81 post-ship
// audit P2). The mirrored direction of LabRunLink: an issue-detail URL
// carrying ?run=<taskId> must force-open the collapsed past-runs bucket
// when the target is terminal, scroll the row into view, and highlight
// it. Nothing previously exercised this against the full section — only
// the hook in isolation — even though the receiver contract is the
// back-half of the ICP-3 promise.

import { cleanup, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { AgentTask } from "@multica/core/types";
import type { NavigationAdapter } from "../../navigation";
import { NavigationProvider } from "../../navigation";
import { renderWithI18n } from "../../test/i18n";

const mockState = vi.hoisted(() => ({
  listTasksByIssue: vi.fn(),
}));

vi.mock("@multica/core/api", async () => {
  const actual = await vi.importActual<typeof import("@multica/core/api")>(
    "@multica/core/api",
  );
  return {
    ...actual,
    api: {
      ...actual.api,
      listTasksByIssue: (...args: unknown[]) => mockState.listTasksByIssue(...args),
    },
  };
});

vi.mock("@multica/core/chat/queries", () => ({
  taskMessagesOptions: vi.fn(),
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => <span data-testid="actor-avatar" />,
}));

vi.mock("../../common/task-transcript", () => ({
  TranscriptButton: ({ title }: { title?: string }) => (
    <button type="button">{title ?? "Transcript"}</button>
  ),
}));

vi.mock("./terminate-task-confirm-dialog", () => ({
  TerminateTaskConfirmDialog: () => null,
}));

import { ExecutionLogSection } from "./execution-log-section";

function makeCompletedTask(): AgentTask {
  return {
    id: "task-deep-link-target",
    agent_id: "agent-1",
    runtime_id: "runtime-1",
    issue_id: "issue-1",
    status: "completed",
    priority: 0,
    dispatched_at: null,
    started_at: "2026-06-08T08:00:00Z",
    completed_at: "2026-06-08T08:04:56Z",
    result: null,
    error: null,
    created_at: "2026-06-08T08:00:00Z",
    trigger_summary: "Deep-linked run trigger text",
  };
}

let frameHandles = new Map<number, FrameRequestCallback>();
let frameSeq = 0;

function drainFrames(maxPasses = 30) {
  let passes = 0;
  while (frameHandles.size > 0 && passes < maxPasses) {
    const pending = [...frameHandles.values()];
    frameHandles.clear();
    pending.forEach((cb) => cb(passes));
    passes++;
  }
}

function navStub(search: string): NavigationAdapter {
  return {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/ws-slug/issues/issue-1",
    searchParams: new URLSearchParams(search),
    getShareableUrl: (p: string) => p,
  };
}

function ui(search: string) {
  return (
    <QueryClientProvider client={new QueryClient()}>
      <NavigationProvider value={navStub(search)}>
        <ExecutionLogSection issueId="issue-1" identifier="ISSUE-1" />
      </NavigationProvider>
    </QueryClientProvider>
  );
}

beforeEach(() => {
  cleanup();
  frameHandles = new Map();
  frameSeq = 0;
  vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => {
    frameSeq += 1;
    frameHandles.set(frameSeq, cb);
    return frameSeq;
  });
  vi.stubGlobal("cancelAnimationFrame", (id: number) => {
    frameHandles.delete(id);
  });
  Element.prototype.scrollIntoView = vi.fn();
  mockState.listTasksByIssue.mockResolvedValue([makeCompletedTask()]);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("ExecutionLogSection ?run= deep links", () => {
  it("force-opens the past-runs bucket and highlights the target row", async () => {
    renderWithI18n(ui("?issue=issue-1&run=task-deep-link-target"));

    // Row is visible without any user interaction → the collapsed bucket
    // was force-opened by linkedIsPast.
    await waitFor(() =>
      expect(screen.getByText("Deep-linked run trigger text")).toBeInTheDocument(),
    );

    drainFrames();

    // The matching row carries the highlight ring paired with the scroll.
    const row = screen
      .getByText("Deep-linked run trigger text")
      .closest('[class*="ring-primary"]');
    expect(row).not.toBeNull();
    expect(Element.prototype.scrollIntoView).toHaveBeenCalledWith({
      block: "center",
      behavior: "smooth",
    });
  });

  it("keeps the past bucket collapsed for URLs without ?run=", async () => {
    renderWithI18n(ui(""));

    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: /Show past runs \(1\)/ }),
      ).toBeInTheDocument(),
    );
    expect(
      screen.queryByText("Deep-linked run trigger text"),
    ).not.toBeInTheDocument();
    expect(Element.prototype.scrollIntoView).not.toHaveBeenCalled();
  });
});
