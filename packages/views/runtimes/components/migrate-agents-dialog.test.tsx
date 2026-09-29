// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import type { Agent, AgentRuntime } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";
import enAgents from "../../locales/en/agents.json";

const TEST_RESOURCES = {
  en: { common: enCommon, runtimes: enRuntimes, agents: enAgents },
};

const bulkMove = vi.fn().mockResolvedValue({
  moved_count: 2,
  cleared_model_count: 0,
  cleared_thinking_count: 0,
  agent_ids: [],
});

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/api", () => ({
  api: {
    bulkMoveAgentRuntime: (...args: unknown[]) => bulkMove(...args),
    listAgents: vi.fn().mockResolvedValue([]),
  },
}));

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

import { MigrateAgentsDialog } from "./migrate-agents-dialog";
import { api } from "@multica/core/api";

function makeRuntime(overrides: Partial<AgentRuntime>): AgentRuntime {
  return {
    id: "rt",
    workspace_id: "ws-1",
    daemon_id: null,
    name: "Runtime",
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "online",
    device_info: "",
    metadata: {},
    owner_id: "user-1",
    visibility: "private",
    last_seen_at: null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

function makeAgent(id: string, runtimeId: string): Agent {
  return {
    id,
    workspace_id: "ws-1",
    runtime_id: runtimeId,
    name: `agent-${id}`,
    description: "",
    instructions: "",
    avatar_url: null,
    runtime_mode: "local",
    runtime_config: {},
    custom_args: [],
    visibility: "private",
    status: "idle",
    max_concurrent_tasks: 1,
    model: "",
    owner_id: "user-1",
    skills: [],
    created_at: "2026-04-01T00:00:00Z",
    updated_at: "2026-04-01T00:00:00Z",
    archived_at: null,
    archived_by: null,
  };
}

const CLAUDE = makeRuntime({ id: "rt-claude", name: "Claude Runtime", provider: "claude" });
const OPENCODE = makeRuntime({ id: "rt-opencode", name: "Opencode Runtime", provider: "opencode" });

function renderDialog(
  source: AgentRuntime,
  opts: { runtimes?: AgentRuntime[]; agents?: Agent[] } = {},
) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  qc.setQueryData(["runtimes", "ws-1", "list"], opts.runtimes ?? [CLAUDE, OPENCODE]);
  qc.setQueryData(["workspaces", "ws-1", "agents"], opts.agents ?? []);
  // staleTime 0 means the dialog refetches on mount and the resolved mock
  // overwrites the seeded cache — the mock must agree with the seed.
  (api.listAgents as ReturnType<typeof vi.fn>).mockResolvedValue(opts.agents ?? []);
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <QueryClientProvider client={qc}>
        <MigrateAgentsDialog open onOpenChange={() => {}} source={source} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("MigrateAgentsDialog", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    bulkMove.mockResolvedValue({
      moved_count: 2,
      cleared_model_count: 0,
      cleared_thinking_count: 0,
      agent_ids: [],
    });
  });
  afterEach(() => {
    cleanup();
    document.body.innerHTML = "";
  });

  it("lists other runtimes as targets, excluding the source", () => {
    renderDialog(CLAUDE);
    expect(screen.getByText("Opencode Runtime")).toBeInTheDocument();
    expect(screen.queryByText("Claude Runtime")).not.toBeInTheDocument();
  });

  it("counts the agents bound to the source runtime", () => {
    renderDialog(CLAUDE, {
      agents: [makeAgent("a1", "rt-claude"), makeAgent("a2", "rt-claude"), makeAgent("a3", "rt-opencode")],
    });
    // The summary strip reads "2 agents · <reset hint>" as one element;
    // match the count segment by substring, not exact text.
    expect(screen.getByText(/2 agents/)).toBeInTheDocument();
    // The other owner's agent is not part of the migration.
    expect(screen.queryByText(/1 agent|3 agents/)).toBeNull();
  });

  it("keeps confirm disabled until a target is picked", () => {
    renderDialog(CLAUDE, { agents: [makeAgent("a1", "rt-claude")] });
    const confirm = screen.getByRole("button", { name: "Migrate" }) as HTMLButtonElement;
    expect(confirm.disabled).toBe(true);
  });

  it("posts the bulk move with the picked target and archived included", async () => {
    renderDialog(CLAUDE, {
      agents: [makeAgent("a1", "rt-claude"), makeAgent("a2", "rt-claude")],
    });
    fireEvent.click(screen.getByText("Opencode Runtime"));
    fireEvent.click(screen.getByRole("button", { name: "Migrate" }));
    await waitFor(() => expect(bulkMove).toHaveBeenCalledTimes(1));
    expect(bulkMove).toHaveBeenCalledWith({
      from_runtime_id: "rt-claude",
      to_runtime_id: "rt-opencode",
      include_archived: true,
    });
  });
});
