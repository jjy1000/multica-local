/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { Agent, AgentTask } from "@multica/core/types/agent";
import enClaudeLab from "../../../locales/en/claude-lab.json";
import { ClaudeBrainCanvas } from "./claude-brain-canvas";

const hookState = vi.hoisted(() => ({
  tasks: [] as AgentTask[],
  status: "idle",
  latest: null as AgentTask | null,
  liveTask: null as AgentTask | null,
  isError: false,
  artifacts: [] as Array<{ id: string }>,
}));
const listAgentsMock = vi.hoisted(() => vi.fn());

vi.mock("../../hooks/use-claude-lab-issue", () => ({
  useClaudeLabIssue: () => ({
    tasks: hookState.tasks,
    latest: hookState.latest,
    liveTask: hookState.liveTask,
    status: hookState.status,
    hasLive: hookState.status === "running" || hookState.status === "queued",
    artifacts: hookState.artifacts,
    isError: hookState.isError,
    refetch: vi.fn(),
  }),
}));
vi.mock("@multica/core/api", () => ({
  api: { listAgents: listAgentsMock },
}));
vi.mock("../../../i18n", () => ({
  useT: () => ({
    t: (sel: (d: unknown) => unknown, opts?: Record<string, unknown>) => {
      const v = sel(enClaudeLab);
      if (typeof v !== "string") return undefined;
      return opts
        ? v.replace(/\{\{(\w+)\}\}/g, (_, k) => String(opts[k] ?? `{{${k}}}`))
        : v;
    },
  }),
}));

const ROSTER_IDS: Record<string, string> = {
  research: "a-research",
  critique: "a-critique",
  ml: "a-ml",
  physics: "a-physics",
  biology: "a-biology",
  write: "a-write",
};

function rosterAgents(): Agent[] {
  return Object.entries(ROSTER_IDS).map(([name, id]) =>
    ({ id, name, description: `${name} prompt` }) as unknown as Agent,
  );
}

function task(
  agentId: string,
  status: AgentTask["status"],
  over: Partial<AgentTask> = {},
): AgentTask {
  return {
    id: `task-${agentId}-${status}`,
    agent_id: agentId,
    status,
    created_at: "2026-10-03T10:00:00Z",
    started_at: "2026-10-03T10:00:02Z",
    completed_at: null,
    error: null,
    trigger_summary: "调研群体智能方法",
    attempt: 1,
    ...over,
  } as unknown as AgentTask;
}

function renderBrain(props: Partial<Parameters<typeof ClaudeBrainCanvas>[0]> = {}) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <ClaudeBrainCanvas wsId="ws-1" issueId="issue-1" {...props} />
    </QueryClientProvider>,
  );
}

function nodeByKey(key: string) {
  return screen
    .getAllByTestId("claude-brain-node")
    .find((n) => n.getAttribute("data-key") === key)!;
}

describe("ClaudeBrainCanvas — 0.5.132", () => {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
    hookState.tasks = [];
    hookState.status = "idle";
    hookState.latest = null;
    hookState.liveTask = null;
    hookState.isError = false;
    hookState.artifacts = [];
    listAgentsMock.mockResolvedValue(rosterAgents());
  });

  it("renders all six roster nodes with real snapshot state on each", async () => {
    hookState.status = "running";
    hookState.liveTask = task("a-research", "running");
    hookState.tasks = [
      hookState.liveTask,
      { ...task("a-critique", "completed"), completed_at: "2026-10-03T10:04:00Z" },
    ];
    renderBrain();

    await waitFor(() => {
      expect(nodeByKey("research").getAttribute("data-state")).toBe("running");
    });
    expect(nodeByKey("critique").getAttribute("data-state")).toBe("done");
    expect(nodeByKey("ml").getAttribute("data-state")).toBe("idle");
    expect(screen.getAllByTestId("claude-brain-node")).toHaveLength(6);
    // live core + phase derived from the running research role
    expect(screen.getByTestId("claude-brain-core").getAttribute("data-live")).toBe("true");
    expect(screen.getByTestId("claude-brain-phases").getAttribute("data-phase")).toBe("1");
    // the running edge animates; an idle edge does not (0.5.136: the
    // testid sits on the wrapping <g> — the state class is on the path)
    const edges = screen.getAllByTestId("claude-brain-edge");
    const researchEdge = edges.find((e) => e.getAttribute("data-key") === "research")!;
    expect(researchEdge.querySelector("path")!.getAttribute("class")).toContain("brain-edge-active");
    const mlEdge = edges.find((e) => e.getAttribute("data-key") === "ml")!;
    expect(mlEdge.querySelector("path")!.getAttribute("class")).toContain("brain-edge-idle");
  });

  it("0.5.136 ambience — running edge rides a comet, live core glows, stage carries the emerald wash", async () => {
    hookState.status = "running";
    hookState.liveTask = task("a-research", "running");
    hookState.tasks = [hookState.liveTask];
    renderBrain();
    await waitFor(() =>
      expect(nodeByKey("research").getAttribute("data-state")).toBe("running"),
    );

    const researchEdge = screen
      .getAllByTestId("claude-brain-edge")
      .find((e) => e.getAttribute("data-key") === "research")!;
    expect(researchEdge.querySelector("animateMotion")).toBeTruthy();

    const core = screen.getByTestId("claude-brain-core");
    expect(core.querySelector(".brain-core-hot")).toBeTruthy();

    const stage = screen.getByTestId("claude-brain-stage");
    expect(stage.getAttribute("style") ?? "").toContain("var(--brain-glow)");

    const style =
      screen.getByTestId("claude-brain-canvas").querySelector("style")!.textContent ?? "";
    expect(style).toContain(".brain-edge-ping");
    expect(style).toContain(".brain-icon-bob");
    expect(style).toContain(".brain-shake");
  });

  it("expands by default even when idle (0.5.133 — the idle brain IS the visual)", async () => {
    renderBrain();
    await waitFor(() => {
      expect(screen.getByTestId("claude-brain-stage")).toBeTruthy();
    });
    expect(screen.getAllByTestId("claude-brain-node")).toHaveLength(6);
    // and the toggle still folds it away
    fireEvent.click(screen.getByTestId("claude-brain-toggle"));
    expect(screen.queryByTestId("claude-brain-stage")).toBeNull();
  });

  it("defaults open while a run is live (embed behaviour)", async () => {
    hookState.status = "running";
    hookState.liveTask = task("a-research", "running");
    hookState.tasks = [hookState.liveTask];
    renderBrain();
    await waitFor(() => {
      expect(screen.getByTestId("claude-brain-stage")).toBeTruthy();
    });
  });

  it("clicking a node pins its detail card; clicking again dismisses", async () => {
    hookState.status = "running";
    hookState.liveTask = task("a-research", "running", { trigger_summary: "复现消融实验" });
    hookState.tasks = [hookState.liveTask];
    renderBrain();
    await waitFor(() => expect(nodeByKey("research").getAttribute("data-state")).toBe("running"));

    fireEvent.click(nodeByKey("research"));
    const detail = screen.getByTestId("claude-brain-detail");
    expect(detail.getAttribute("data-key")).toBe("research");
    expect(screen.getByText("复现消融实验")).toBeTruthy();

    fireEvent.click(nodeByKey("research"));
    // dismissal runs through AnimatePresence (0.22s exit) — wait it out
    await waitFor(
      () => expect(screen.queryByTestId("claude-brain-detail")).toBeNull(),
      { timeout: 2000 },
    );
  });

  it("workbench variant shows the recent-runs strip and artifact count", async () => {
    hookState.status = "completed";
    hookState.tasks = [
      { ...task("a-write", "completed"), completed_at: "2026-10-03T10:06:00Z" },
      { ...task("a-research", "completed"), completed_at: "2026-10-03T10:04:00Z" },
    ];
    hookState.artifacts = [{ id: "x1" }, { id: "x2" }, { id: "x3" }] as never;
    renderBrain({ variant: "workbench", defaultOpen: true });

    await waitFor(() => {
      expect(screen.getByTestId("claude-brain-recent-runs")).toBeTruthy();
    });
    // artifact count chip interpolates n=3
    expect(screen.getByText("3 artifacts")).toBeTruthy();
    // completed run saturates the pipeline
    expect(screen.getByTestId("claude-brain-phases").getAttribute("data-phase")).toBe("4");
  });

  it("styles itself for both themes via the --brain-* custom properties", async () => {
    renderBrain({ defaultOpen: true });
    await waitFor(() => expect(screen.getByTestId("claude-brain-canvas")).toBeTruthy());
    const canvas = screen.getByTestId("claude-brain-canvas");
    expect(canvas.className).toContain("claude-brain");
    // the mounted stylesheet carries light values plus a .dark override
    const style = canvas.querySelector("style")!.textContent ?? "";
    expect(style).toContain("--brain-line:rgba(9,9,11,.16)");
    expect(style).toContain(".dark .claude-brain");
    expect(style).toContain("@media (prefers-reduced-motion: reduce)");
  });
});
