/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useClaudeLabIssue } from "./use-claude-lab-issue";

const apiMock = vi.hoisted(() => ({
  getAgentTaskSnapshot: vi.fn(),
  listClaudeScienceArtifactsByIssue: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({ api: apiMock }));

const task = (over: Partial<Record<string, unknown>>) => ({
  id: "t1",
  agent_id: "agent-1",
  runtime_id: "rt-1",
  issue_id: "issue-1",
  status: "completed",
  priority: 0,
  dispatched_at: null,
  started_at: null,
  completed_at: null,
  result: null,
  error: null,
  created_at: "2026-09-24T00:00:00Z",
  ...over,
});

function wrapper({ children }: { children: React.ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

describe("useClaudeLabIssue — status derivation (0.5.114)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMock.listClaudeScienceArtifactsByIssue.mockResolvedValue([]);
  });

  it("idle when the issue has no lab tasks", async () => {
    apiMock.getAgentTaskSnapshot.mockResolvedValue([
      task({ id: "other", issue_id: "issue-2" }),
    ]);
    const { result } = renderHook(() => useClaudeLabIssue("ws-1", "issue-1"), { wrapper });
    await waitFor(() => expect(result.current.status).toBe("idle"));
    expect(result.current.tasks).toHaveLength(0);
  });

  it("running wins over a completed task", async () => {
    apiMock.getAgentTaskSnapshot.mockResolvedValue([
      task({ id: "done", status: "completed", completed_at: "2026-09-24T01:00:00Z" }),
      task({ id: "live", status: "running", started_at: "2026-09-24T02:00:00Z" }),
    ]);
    const { result } = renderHook(() => useClaudeLabIssue("ws-1", "issue-1"), { wrapper });
    await waitFor(() => expect(result.current.status).toBe("running"));
    expect(result.current.hasLive).toBe(true);
  });

  it("queued/dispatched map to queued", async () => {
    apiMock.getAgentTaskSnapshot.mockResolvedValue([
      task({ id: "q", status: "dispatched" }),
    ]);
    const { result } = renderHook(() => useClaudeLabIssue("ws-1", "issue-1"), { wrapper });
    await waitFor(() => expect(result.current.status).toBe("queued"));
  });

  it("latest terminal task surfaces after completion (pill persistence)", async () => {
    apiMock.getAgentTaskSnapshot.mockResolvedValue([
      task({ id: "done", status: "completed", completed_at: "2026-09-24T03:00:00Z" }),
    ]);
    const { result } = renderHook(() => useClaudeLabIssue("ws-1", "issue-1"), { wrapper });
    await waitFor(() => expect(result.current.status).toBe("completed"));
    expect(result.current.latest?.id).toBe("done");
  });

  it("failed status propagates", async () => {
    apiMock.getAgentTaskSnapshot.mockResolvedValue([
      task({ id: "boom", status: "failed", completed_at: "2026-09-24T03:00:00Z" }),
    ]);
    const { result } = renderHook(() => useClaudeLabIssue("ws-1", "issue-1"), { wrapper });
    await waitFor(() => expect(result.current.status).toBe("failed"));
  });
});
