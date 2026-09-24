/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import enExperimental from "../../../locales/en/experimental.json";
import { ClaudeIssueEmbed } from "./claude-issue-embed";

const hookState = vi.hoisted(() => ({
  tasks: [] as unknown[],
  status: "idle",
  latest: null as Record<string, unknown> | null,
  liveTask: null as Record<string, unknown> | null,
  artifacts: [] as Array<{ id: string; name: string; kind: string; bytes: number; sha256: string; url: string; session_id: string }>,
}));
const rawRequestMock = vi.hoisted(() => vi.fn());

vi.mock("../../hooks/use-claude-lab-issue", () => ({
  useClaudeLabIssue: () => ({
    tasks: hookState.tasks,
    latest: hookState.latest,
    liveTask: hookState.liveTask,
    status: hookState.status,
    hasLive: hookState.status === "running" || hookState.status === "queued",
    artifacts: hookState.artifacts,
  }),
}));
vi.mock("@multica/core/api", () => ({
  api: { rawRequest: rawRequestMock },
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

describe("ClaudeIssueEmbed — 0.5.114", () => {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
    hookState.latest = null;
    hookState.liveTask = null;
    rawRequestMock.mockResolvedValue(new Response("x"));
  });

  it("collapses to null with no tasks and no artifacts (0-runs-ready law)", () => {
    const { container } = render(<ClaudeIssueEmbed wsId="ws-1" issueId="issue-1" />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders artifact rows with name + download", async () => {
    hookState.tasks = [{ id: "t1" }];
    hookState.status = "completed";
    hookState.artifacts = [
      { id: "a1", session_id: "s1", name: "figure-1.png", kind: "png", bytes: 2048, sha256: "aa", url: "/x/a1" },
      { id: "a2", session_id: "s1", name: "report.csv", kind: "csv", bytes: 12, sha256: "bb", url: "/x/a2" },
    ];
    render(<ClaudeIssueEmbed wsId="ws-1" issueId="issue-1" />);
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
    render(<ClaudeIssueEmbed wsId="ws-1" issueId="issue-1" />);
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
    render(<ClaudeIssueEmbed wsId="ws-1" issueId="issue-1" />);
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
    render(<ClaudeIssueEmbed wsId="ws-1" issueId="issue-1" />);
    expect(screen.getByText(enExperimental.claude_lab.embed_queued)).toBeTruthy();
  });

  it("keeps the terminal empty hint and no live row after completion", () => {
    hookState.tasks = [{ id: "t1" }];
    hookState.status = "completed";
    hookState.liveTask = null;
    hookState.artifacts = [];
    render(<ClaudeIssueEmbed wsId="ws-1" issueId="issue-1" />);
    expect(screen.queryByTestId("claude-embed-live-progress")).toBeNull();
    expect(screen.getByText(enExperimental.claude_lab.embed_artifacts_empty)).toBeTruthy();
  });
});
