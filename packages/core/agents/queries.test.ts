// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { AgentTask } from "../types";
import { agentTasksKeys, agentTasksOptions } from "./queries";

vi.mock("../api", () => ({ api: { listAgentTasksPage: vi.fn() } }));

const clients: QueryClient[] = [];
function client() {
  const result = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(result);
  return result;
}
function page(ids: string[], nextCursor: string | null = null) {
  const tasks = ids.map((id): AgentTask => ({
    id, agent_id: "agent-1", runtime_id: "runtime-1", issue_id: "",
    status: "completed", priority: 0, dispatched_at: null, started_at: null,
    completed_at: "2026-09-24T00:00:00Z", created_at: "2026-09-24T00:00:00Z",
    result: null, error: null,
  }));
  return { tasks, nextCursor };
}

afterEach(() => {
  clients.splice(0).forEach((queryClient) => queryClient.clear());
  vi.resetAllMocks();
});

describe("agent history query lifecycle", () => {
  it("shares concurrent readers without eagerly fetching the next page", async () => {
    const queryClient = client();
    const fetchPage = vi.mocked(api.listAgentTasksPage);
    fetchPage.mockResolvedValue(page(["first"], "older"));
    const options = agentTasksOptions("workspace-1", "agent-1");
    const [first, second] = await Promise.all([
      queryClient.fetchInfiniteQuery(options),
      queryClient.fetchInfiniteQuery(options),
    ]);
    expect(fetchPage).toHaveBeenCalledTimes(1);
    expect(fetchPage).toHaveBeenCalledWith("agent-1", {
      limit: 200, before: undefined, signal: expect.any(AbortSignal),
    });
    expect(first).toEqual(second);
    expect(first.pages).toHaveLength(1);
    expect(first.pages[0]?.nextCursor).toBe("older");
  });

  it("isolates cached pages by workspace and agent", async () => {
    const queryClient = client();
    const fetchPage = vi.mocked(api.listAgentTasksPage);
    fetchPage.mockResolvedValueOnce(page(["workspace-1-agent-1"]))
      .mockResolvedValueOnce(page(["workspace-2-agent-1"]))
      .mockResolvedValueOnce(page(["workspace-1-agent-2"]));
    const first = await queryClient.fetchInfiniteQuery(agentTasksOptions("workspace-1", "agent-1"));
    const otherWorkspace = await queryClient.fetchInfiniteQuery(agentTasksOptions("workspace-2", "agent-1"));
    const otherAgent = await queryClient.fetchInfiniteQuery(agentTasksOptions("workspace-1", "agent-2"));
    const cached = await queryClient.fetchInfiniteQuery(agentTasksOptions("workspace-1", "agent-1"));
    expect(fetchPage).toHaveBeenCalledTimes(3);
    expect(cached).toEqual(first);
    expect(otherWorkspace.pages[0]?.tasks[0]?.id).toBe("workspace-2-agent-1");
    expect(otherAgent.pages[0]?.tasks[0]?.id).toBe("workspace-1-agent-2");
  });

  it("keeps the detail key namespaced under the shared agentTasksKeys", () => {
    // The WS task-prefix invalidation in useRealtimeSync walks
    // agentTasksKeys; the paged query must live under the same subtree or
    // a task event would no longer refresh open history.
    expect(agentTasksKeys.detail("ws-1", "agent-1")).toEqual([
      "workspaces", "ws-1", "agent-tasks", "agent-1",
    ]);
  });
});
