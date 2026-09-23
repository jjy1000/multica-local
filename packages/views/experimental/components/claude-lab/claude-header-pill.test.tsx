/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import enExperimental from "../../../locales/en/experimental.json";
import zhExperimental from "../../../locales/zh-Hans/experimental.json";
import jaExperimental from "../../../locales/ja/experimental.json";
import koExperimental from "../../../locales/ko/experimental.json";
import { ClaudeHeaderPill } from "./claude-header-pill";

const hookState = vi.hoisted(() => ({
  tasks: [] as unknown[],
  status: "idle",
}));
vi.mock("../../hooks/use-claude-lab-issue", () => ({
  useClaudeLabIssue: () => ({
    tasks: hookState.tasks,
    latest: null,
    liveTask: null,
    status: hookState.status,
    hasLive: hookState.status === "running" || hookState.status === "queued",
    artifacts: [],
  }),
}));
vi.mock("../../../i18n", () => ({
  useT: () => ({
    t: (sel: (d: unknown) => unknown) => {
      const v = sel(enExperimental);
      return typeof v === "string" ? v : undefined;
    },
  }),
}));

// 0.5.114 contract: every claude_lab key the components reference must
// exist (non-empty) in all 4 locales — labs-tab full-mapping pattern.
const CLAUDE_LAB_KEYS = [
  "pill_running",
  "pill_queued",
  "pill_done",
  "pill_failed",
  "pill_cancelled",
  "pill_idle",
  "embed_title",
  "embed_running",
  "embed_status_done",
  "embed_artifacts_empty",
  "embed_download",
] as const;

describe("ClaudeHeaderPill — 0.5.114", () => {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("i18n: all claude_lab keys non-empty in all 4 locales", () => {
    const dicts = {
      en: enExperimental.claude_lab,
      "zh-Hans": zhExperimental.claude_lab,
      ja: jaExperimental.claude_lab,
      ko: koExperimental.claude_lab,
    } as Record<string, Record<string, string> | undefined>;
    for (const [lang, dict] of Object.entries(dicts)) {
      expect(dict, lang).toBeTruthy();
      for (const key of CLAUDE_LAB_KEYS) {
        expect(String(dict?.[key] ?? "").length, `${lang}.claude_lab.${key}`).toBeGreaterThan(0);
      }
    }
  });

  it("renders the running label and status attr", () => {
    hookState.tasks = [{ id: "t1" }];
    hookState.status = "running";
    render(<ClaudeHeaderPill wsId="ws-1" issueId="issue-1" />);
    const pill = screen.getByTestId("claude-header-pill");
    expect(pill.getAttribute("data-status")).toBe("running");
    expect(pill.textContent).toContain(enExperimental.claude_lab.pill_running);
  });

  it("renders the completed label", () => {
    hookState.tasks = [{ id: "t1" }];
    hookState.status = "completed";
    render(<ClaudeHeaderPill wsId="ws-1" issueId="issue-1" />);
    expect(screen.getByTestId("claude-header-pill").textContent).toContain(
      enExperimental.claude_lab.pill_done,
    );
  });

  it("collapses to null when the issue has no lab tasks", () => {
    hookState.tasks = [];
    hookState.status = "idle";
    render(<ClaudeHeaderPill wsId="ws-1" issueId="issue-1" />);
    expect(screen.queryByTestId("claude-header-pill")).toBeNull();
  });
});
