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
  artifacts: [] as Array<{ id: string; name: string; kind: string; bytes: number; sha256: string; url: string; session_id: string }>,
}));
const rawRequestMock = vi.hoisted(() => vi.fn());

vi.mock("../../hooks/use-claude-lab-issue", () => ({
  useClaudeLabIssue: () => ({
    tasks: hookState.tasks,
    latest: null,
    liveTask: null,
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
    t: (sel: (d: unknown) => unknown) => {
      const v = sel(enExperimental);
      return typeof v === "string" ? v : undefined;
    },
  }),
}));

describe("ClaudeIssueEmbed — 0.5.114", () => {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
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

  it("shows the empty-artifacts hint for a task with none", () => {
    hookState.tasks = [{ id: "t1" }];
    hookState.status = "running";
    hookState.artifacts = [];
    render(<ClaudeIssueEmbed wsId="ws-1" issueId="issue-1" />);
    expect(screen.getByText(enExperimental.claude_lab.embed_artifacts_empty)).toBeTruthy();
    expect(screen.getByText(enExperimental.claude_lab.embed_running)).toBeTruthy();
  });
});
