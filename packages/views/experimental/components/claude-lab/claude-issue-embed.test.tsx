/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import enExperimental from "../../../locales/en/experimental.json";
import { ClaudeIssueEmbed } from "./claude-issue-embed";

const hookState = vi.hoisted(() => ({
  tasks: [] as unknown[],
  status: "idle",
  latest: null as Record<string, unknown> | null,
  liveTask: null as Record<string, unknown> | null,
  isError: false,
  artifacts: [] as Array<{ id: string; name: string; kind: string; bytes: number; sha256: string; url: string; session_id: string }>,
}));
const rawRequestMock = vi.hoisted(() => vi.fn());
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
  api: { rawRequest: rawRequestMock, listAgents: listAgentsMock },
}));
vi.mock("../../../i18n", () => ({
  useT: () => ({
    t: (sel: (d: unknown) => unknown, opts?: Record<string, unknown>) => {
      const v = sel(enExperimental);
      if (typeof v !== "string") return undefined;
      // minimal {{var}} interpolation so retry/attempt labels resolve
      return opts
        ? v.replace(/\{\{(\w+)\}\}/g, (_, k) => String(opts[k] ?? `{{${k}}}`))
        : v;
    },
  }),
}));

// 0.5.132: the embed mounts ClaudeBrainCanvas, which resolves roster
// agents through useQuery(agentListOptions) — every render needs a
// QueryClientProvider and a mocked api.listAgents.
function renderEmbed() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ClaudeIssueEmbed wsId="ws-1" issueId="issue-1" />
    </QueryClientProvider>,
  );
}

describe("ClaudeIssueEmbed — 0.5.114", () => {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
    hookState.latest = null;
    hookState.liveTask = null;
    hookState.isError = false;
    rawRequestMock.mockResolvedValue(new Response("x"));
    listAgentsMock.mockResolvedValue([]);
  });

  it("collapses to null with no tasks and no artifacts (0-runs-ready law)", () => {
    const { container } = renderEmbed();
    expect(container).toBeEmptyDOMElement();
  });

  it("surfaces a fetch outage as an error strip with retry instead of the empty state (0.5.131)", () => {
    // Snapshot/artifacts fetch failures used to render identically to
    // "no runs" — the pill vanished and the embed showed the empty hint
    // with no retry affordance.
    hookState.isError = true;
    const { container } = renderEmbed();
    expect(container).not.toBeEmptyDOMElement();
    expect(screen.getByTestId("claude-issue-embed-error")).toBeTruthy();
    expect(screen.getByText(enExperimental.claude_lab.embed_error)).toBeTruthy();
    expect(screen.getByText(enExperimental.lab_output_panel.retry)).toBeTruthy();
  });

  it("run-status strip animates by state (open-science session-card pattern)", () => {
    // idle with artifacts present → no strip; running → shimmer sweep;
    // completed → emerald fill + check pop.
    hookState.tasks = [{ id: "t1" }];
    hookState.artifacts = [{ id: "a1", session_id: "s1", name: "r.md", kind: "md", bytes: 8, sha256: "x", url: "/x" }];

    hookState.status = "completed";
    const { unmount } = renderEmbed();
    const done = screen.getByTestId("claude-run-status-strip");
    expect(done.getAttribute("data-status")).toBe("completed");
    // the check pop lives in the header, not inside the strip element
    expect(document.querySelector(".claude-strip-pop")).not.toBeNull();
    unmount();

    hookState.status = "running";
    hookState.liveTask = { id: "t1", status: "running", attempt: 1, started_at: new Date().toISOString() };
    renderEmbed();
    const live = screen.getByTestId("claude-run-status-strip");
    expect(live.getAttribute("data-status")).toBe("running");
    expect(live.querySelector(".claude-strip-shimmer")).not.toBeNull();
  });

  it("renders artifact rows with name + download", async () => {
    hookState.tasks = [{ id: "t1" }];
    hookState.status = "completed";
    hookState.artifacts = [
      { id: "a1", session_id: "s1", name: "figure-1.png", kind: "png", bytes: 2048, sha256: "aa", url: "/x/a1" },
      { id: "a2", session_id: "s1", name: "report.csv", kind: "csv", bytes: 12, sha256: "bb", url: "/x/a2" },
    ];
    renderEmbed();
    await waitFor(() => expect(screen.getByTestId("claude-embed-artifacts")).toBeTruthy());
    expect(screen.getByText("figure-1.png")).toBeTruthy();
    expect(screen.getByText("report.csv")).toBeTruthy();
    // download buttons exist for every row
    expect(screen.getAllByText(enExperimental.claude_lab.embed_download)).toHaveLength(2);
  });

  it("shows the live progress row + pending hint while running with no artifacts", () => {
    hookState.tasks = [{ id: "t1" }];
    hookState.status = "running";
    hookState.liveTask = { id: "t1", status: "running", attempt: 1, started_at: new Date().toISOString() };
    hookState.artifacts = [];
    renderEmbed();
    // live progress row present with the working label + running header
    expect(screen.getByTestId("claude-embed-live-progress")).toBeTruthy();
    expect(screen.getByText(enExperimental.claude_lab.embed_working)).toBeTruthy();
    expect(screen.getByText(enExperimental.claude_lab.embed_running)).toBeTruthy();
    // running → guidance hint instead of the terminal "No artifacts yet"
    expect(screen.getByText(enExperimental.claude_lab.embed_artifacts_pending)).toBeTruthy();
  });

  it("shows the retrying label when the previous step failed and a retry is live", () => {
    hookState.tasks = [{ id: "t1" }, { id: "t2" }];
    hookState.status = "running";
    hookState.latest = { id: "t1", status: "failed" };
    hookState.liveTask = { id: "t2", status: "running", attempt: 2, started_at: new Date().toISOString() };
    hookState.artifacts = [];
    renderEmbed();
    expect(
      screen.getByText(
        enExperimental.claude_lab.embed_retrying.replace("{{n}}", "2"),
      ),
    ).toBeTruthy();
  });

  it("shows the queued label while the task has not been claimed", () => {
    hookState.tasks = [{ id: "t1" }];
    hookState.status = "queued";
    hookState.liveTask = { id: "t1", status: "queued", attempt: 1, created_at: new Date().toISOString() };
    hookState.artifacts = [];
    renderEmbed();
    expect(screen.getByText(enExperimental.claude_lab.embed_queued)).toBeTruthy();
  });

  it("keeps the terminal empty hint and no live row after completion", () => {
    hookState.tasks = [{ id: "t1" }];
    hookState.status = "completed";
    hookState.liveTask = null;
    hookState.artifacts = [];
    renderEmbed();
    expect(screen.queryByTestId("claude-embed-live-progress")).toBeNull();
    expect(screen.getByText(enExperimental.claude_lab.embed_artifacts_empty)).toBeTruthy();
  });
});

// 0.5.126 — artifact CONTENT renders beside the conversation. The
// common/markdown wrapper drags in the config store, workspace paths and
// the issue/project mention chips; stub it so these cases assert the
// embed's own fetch + disclosure behavior, not react-markdown's output.
vi.mock("../../../common/markdown", () => ({
  Markdown: ({ children }: { children: string }) => (
    <div data-testid="mock-markdown">{children}</div>
  ),
}));

describe("ClaudeIssueEmbed — 0.5.126 in-place artifact rendering", () => {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
    hookState.tasks = [{ id: "t1" }];
    hookState.status = "completed";
    hookState.latest = null;
    hookState.liveTask = null;
  });

  it("keeps a markdown artifact collapsed until its row is clicked", async () => {
    hookState.artifacts = [
      { id: "a1", session_id: "s1", name: "report.md", kind: "md", bytes: 900, sha256: "aa", url: "/x/a1" },
    ];
    rawRequestMock.mockResolvedValue(new Response("# Findings\n\nThe effect replicated."));
    renderEmbed();

    // Closed on mount: the report body is neither fetched nor mounted.
    // That is the contract that keeps this out of double-delivery with
    // the timeline report comment.
    expect(screen.queryByTestId("claude-embed-inline-markdown")).toBeNull();
    expect(rawRequestMock).not.toHaveBeenCalled();

    fireEvent.click(screen.getByTestId("claude-embed-toggle"));
    await waitFor(() =>
      expect(screen.getByTestId("claude-embed-inline-markdown")).toBeTruthy(),
    );
    expect(screen.getByTestId("mock-markdown").textContent).toContain("The effect replicated.");
    // Fetched through rawRequest (auth headers), never a bare <img>/fetch.
    expect(rawRequestMock.mock.calls[0]?.[0]).toContain(
      "/api/experimental/claude-science-runtime/artifacts/a1",
    );
  });

  it("collapses an expanded artifact again on a second click", async () => {
    hookState.artifacts = [
      { id: "a1", session_id: "s1", name: "report.md", kind: "md", bytes: 900, sha256: "aa", url: "/x/a1" },
    ];
    rawRequestMock.mockResolvedValue(new Response("body"));
    renderEmbed();
    const toggle = screen.getByTestId("claude-embed-toggle");
    fireEvent.click(toggle);
    await waitFor(() =>
      expect(screen.getByTestId("claude-embed-inline-markdown")).toBeTruthy(),
    );
    fireEvent.click(toggle);
    expect(screen.queryByTestId("claude-embed-inline-markdown")).toBeNull();
  });

  it("renders a CSV artifact as a table with a header row", async () => {
    hookState.artifacts = [
      { id: "a2", session_id: "s1", name: "results.csv", kind: "csv", bytes: 64, sha256: "bb", url: "/x/a2" },
    ];
    rawRequestMock.mockResolvedValue(
      new Response('gene,log2fc\nTP53,4.2\nBRCA1,2.8\n"KIF11, quoted",1.1'),
    );
    renderEmbed();
    fireEvent.click(screen.getByTestId("claude-embed-toggle"));
    await waitFor(() => expect(screen.getByTestId("claude-embed-csv")).toBeTruthy());
    expect(screen.getByText("gene")).toBeTruthy();
    expect(screen.getByText("log2fc")).toBeTruthy();
    expect(screen.getByText("4.2")).toBeTruthy();
    // Quoted cell keeps its comma instead of splitting into two columns.
    expect(screen.getByText("KIF11, quoted")).toBeTruthy();
  });

  it("gives no toggle to binary artifacts — they keep the download row", async () => {
    hookState.artifacts = [
      { id: "a1", session_id: "s1", name: "figure.png", kind: "png", bytes: 2048, sha256: "aa", url: "/x/a1" },
    ];
    rawRequestMock.mockResolvedValue(new Response("x"));
    renderEmbed();
    await waitFor(() => expect(screen.getByTestId("claude-embed-artifacts")).toBeTruthy());
    expect(screen.getByText("figure.png")).toBeTruthy();
    expect(screen.queryByTestId("claude-embed-toggle")).toBeNull();
  });

  it("gives no toggle to an oversized text artifact", async () => {
    hookState.artifacts = [
      { id: "a3", session_id: "s1", name: "huge.md", kind: "md", bytes: 5_000_000, sha256: "cc", url: "/x/a3" },
    ];
    rawRequestMock.mockResolvedValue(new Response("x"));
    renderEmbed();
    await waitFor(() => expect(screen.getByTestId("claude-embed-artifacts")).toBeTruthy());
    expect(screen.queryByTestId("claude-embed-toggle")).toBeNull();
  });

  it("falls back to the download row when the preview request fails", async () => {
    hookState.artifacts = [
      { id: "a1", session_id: "s1", name: "report.md", kind: "md", bytes: 900, sha256: "aa", url: "/x/a1" },
    ];
    rawRequestMock.mockResolvedValue(new Response("", { status: 500 }));
    renderEmbed();
    fireEvent.click(screen.getByTestId("claude-embed-toggle"));
    await waitFor(() =>
      expect(screen.getByTestId("claude-embed-inline-error")).toBeTruthy(),
    );
    expect(screen.getByText(enExperimental.claude_lab.embed_inline_error)).toBeTruthy();
    // The download affordance survives the failure.
    expect(screen.getAllByText(enExperimental.claude_lab.embed_download)).toHaveLength(1);
  });
});
