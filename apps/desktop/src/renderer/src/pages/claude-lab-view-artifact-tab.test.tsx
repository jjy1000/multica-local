/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, beforeEach, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";

// ArtifactTab reads the workspace run history, names each row's owning
// issue, jumps to it, and deletes it. All four assertions below target
// request shapes and testids rather than copy, so a locale edit cannot
// turn this file red without a behavior change.

const WS = "11111111-2222-3333-4444-555555555555";
const ISSUE_A = "aaaaaaaa-0000-0000-0000-000000000001";
const ISSUE_B = "bbbbbbbb-0000-0000-0000-000000000002";

const rawRequestSpy = vi.hoisted(() => vi.fn());
const pushSpy = vi.hoisted(() => vi.fn());
const mutationRef = vi.hoisted(() => ({ current: null as unknown }));
const queryClientMock = vi.hoisted(() => ({
  cancelQueries: vi.fn(async () => {}),
  getQueryData: vi.fn(() => undefined),
  setQueryData: vi.fn(),
  invalidateQueries: vi.fn(async () => {}),
}));
const sessionsRef = vi.hoisted(() => ({
  current: {
    sessions: [] as Array<Record<string, unknown>>,
    total: 0,
  },
}));
const issuesRef = vi.hoisted(() => ({
  current: { issues: [] as Array<Record<string, unknown>> },
}));
const queryOptsRef = vi.hoisted(() => ({
  current: [] as Array<{ queryKey: unknown[]; queryFn?: () => Promise<unknown> }>,
}));

function row(over: Record<string, unknown> = {}) {
  return {
    id: "aaaaaaaa-1111-2222-3333-444444444444",
    workspace_id: WS,
    agent_id: "cccccccc-0000-0000-0000-000000000003",
    issue_id: ISSUE_A,
    language: "python",
    status: "completed",
    exit_code: 0,
    duration_ms: 1234,
    created_at: "2026-09-28T10:00:00Z",
    started_at: "2026-09-28T10:00:01Z",
    finished_at: "2026-09-28T10:00:03Z",
    stdout: null,
    stderr: null,
    lab_source: "claude_science_lab",
    ...over,
  };
}

vi.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey: unknown[]; queryFn?: () => Promise<unknown> }) => {
    queryOptsRef.current.push(opts);
    const key = String(opts.queryKey[0]);
    if (key.includes("runtime-sessions-history")) {
      return { data: sessionsRef.current, isLoading: false };
    }
    if (key.includes("claude-lab-issues")) {
      return { data: issuesRef.current, isLoading: false };
    }
    return { data: undefined, isLoading: false };
  },
  useMutation: (opts: unknown) => {
    mutationRef.current = opts;
    return {
      // Mirror react-query's lifecycle so onMutate / mutationFn /
      // onSettled (and the onError rollback branch) all run — the
      // optimistic-update contract is only observable through them.
      mutate: (v: unknown) => {
        const o = opts as {
          mutationFn: (x: unknown) => Promise<void>;
          onMutate: (x: unknown) => Promise<unknown>;
          onError?: (e: unknown, v: unknown, ctx: unknown) => void;
          onSettled?: (d: unknown, v: unknown, ctx: unknown) => void;
        };
        let ctx: unknown;
        void o
          .onMutate(v)
          .then((c) => {
            ctx = c;
            return o.mutationFn(v);
          })
          .then(() => o.onSettled?.(undefined, v, ctx))
          .catch((e: unknown) => {
            o.onError?.(e, v, ctx);
            o.onSettled?.(undefined, v, ctx);
          });
      },
    };
  },
  useQueryClient: () => queryClientMock,
}));

vi.mock("@multica/core/api", () => ({
  api: { rawRequest: (url: string, init?: RequestInit) => rawRequestSpy(url, init) },
}));

vi.mock("@multica/core/platform", () => ({
  getCurrentWsId: () => WS,
  getCurrentSlug: () => "test-ws",
}));

vi.mock("@multica/core/experimental", () => ({
  useExperimentalFlag: () => true,
}));

vi.mock("@multica/core/paths", () => ({
  paths: {
    workspace: (slug: string) => ({
      issueDetail: (id: string) => `/w/${slug}/issues/${id}`,
    }),
  },
}));

vi.mock("@multica/views/navigation", () => ({
  useNavigation: () => ({ push: pushSpy, back: vi.fn() }),
}));

// The t() selector source contains the key path (`($) =>
// $.session_delete`), so rendering it as text makes assertions target
// the i18n key actually requested instead of a placeholder string.
vi.mock("@multica/views/i18n", () => ({
  useT: () => ({
    t: (sel: unknown, opts?: Record<string, unknown>) =>
      opts ? `${String(sel)} ${JSON.stringify(opts)}` : String(sel),
  }),
}));

vi.mock("motion/react", () => ({
  motion: new Proxy({}, { get: () => () => null }),
  AnimatePresence: ({ children }: { children?: React.ReactNode }) => children,
  useReducedMotion: () => false,
}));

vi.mock("@multica/views/experimental/components", () => ({
  ExperimentalArtifactView: () => <div data-testid="artifact-view" />,
  LabChatPanelContainer: () => null,
  LabProgressCard: () => null,
}));

import { ArtifactTab } from "./claude-lab-view";

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
  sessionsRef.current = { sessions: [row()], total: 1 };
  issuesRef.current = {
    issues: [
      { id: ISSUE_A, number: 12, title: "Differential expression repro", status: "done" },
      { id: ISSUE_B, number: 31, title: "Ligand binding screen", status: "in_progress" },
    ],
  };
  queryClientMock.getQueryData.mockReturnValue(undefined);
  // 204 is the DELETE endpoint's answer; the constructor rejects a body
  // on null-body statuses, so pass null explicitly.
  rawRequestSpy.mockResolvedValue(new Response(null, { status: 204 }));
  queryOptsRef.current = [];
});

describe("ArtifactTab — 0.5.126 run history", () => {
  it("lists the WORKSPACE run history, not just the selected issue", async () => {
    // The 0.3.45.8 by-issue list could never name another issue, which
    // made a per-row jump a no-op. Drive the real queryFn and assert the
    // endpoint it actually calls.
    render(<ArtifactTab wsId={WS} selectedIssueId={ISSUE_A} />);
    const history = queryOptsRef.current.find((o) =>
      String(o.queryKey[0]).includes("runtime-sessions-history"),
    );
    expect(history).toBeTruthy();
    rawRequestSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ sessions: [], total: 0 })),
    );
    await history?.queryFn?.();
    const url = rawRequestSpy.mock.calls[0]?.[0] as string;
    expect(url).toBe(
      `/api/experimental/claude-science-runtime/sessions?workspace_id=${WS}`,
    );
    expect(url).not.toContain("by-issue");
    expect(url).not.toContain("issue_id=");
  });

  it("names each row with its issue number and title", () => {
    sessionsRef.current = {
      sessions: [row(), row({ id: "dddddddd-1111-2222-3333-444444444444", issue_id: ISSUE_B })],
      total: 2,
    };
    render(<ArtifactTab wsId={WS} selectedIssueId={ISSUE_A} />);
    expect(screen.getByTestId("claude-lab-run-history")).toBeTruthy();
    expect(screen.getByText("#12")).toBeTruthy();
    expect(screen.getByText("Differential expression repro")).toBeTruthy();
    expect(screen.getByText("#31")).toBeTruthy();
    expect(screen.getByText("Ligand binding screen")).toBeTruthy();
  });

  it("jumps to the row's owning issue when the row is clicked", () => {
    sessionsRef.current = {
      sessions: [row({ id: "dddddddd-1111-2222-3333-444444444444", issue_id: ISSUE_B })],
      total: 1,
    };
    render(<ArtifactTab wsId={WS} selectedIssueId={ISSUE_A} />);
    fireEvent.click(screen.getByText("Ligand binding screen"));
    expect(pushSpy).toHaveBeenCalledWith(`/w/test-ws/issues/${ISSUE_B}`);
  });

  it("floats the selected issue's runs to the top without hiding the others", () => {
    sessionsRef.current = {
      sessions: [
        row({ id: "dddddddd-1111-2222-3333-444444444444", issue_id: ISSUE_B }),
        row(),
      ],
      total: 2,
    };
    render(<ArtifactTab wsId={WS} selectedIssueId={ISSUE_A} />);
    const names = screen
      .getByTestId("claude-lab-run-history")
      .querySelectorAll("li");
    // Focus first: the selected issue's row leads even though the
    // backend returns created_at DESC.
    expect(names[0]?.textContent).toContain("Differential expression repro");
    expect(names[1]?.textContent).toContain("Ligand binding screen");
  });

  it("confirms before deleting, then DELETEs the session endpoint", async () => {
    render(<ArtifactTab wsId={WS} selectedIssueId={ISSUE_A} />);
    // Nothing is sent on the first click — it arms the confirm.
    fireEvent.click(screen.getByTestId("claude-lab-delete"));
    expect(rawRequestSpy).not.toHaveBeenCalled();
    expect(screen.getByTestId("claude-lab-delete-confirm")).toBeTruthy();

    fireEvent.click(screen.getByTestId("claude-lab-delete-confirm"));
    await vi.waitFor(() =>
      expect(rawRequestSpy).toHaveBeenCalledWith(
        `/api/experimental/claude-science-runtime/sessions/aaaaaaaa-1111-2222-3333-444444444444`,
        { method: "DELETE" },
      ),
    );
    expect(queryClientMock.invalidateQueries).toHaveBeenCalled();
  });

  it("disables the row jump for a session with no bound issue", () => {
    sessionsRef.current = { sessions: [row({ issue_id: null })], total: 1 };
    render(<ArtifactTab wsId={WS} selectedIssueId={ISSUE_A} />);
    fireEvent.click(screen.getByText("completed"));
    expect(pushSpy).not.toHaveBeenCalled();
  });

  it("still offers deletion for an unbound session", async () => {
    sessionsRef.current = { sessions: [row({ issue_id: null })], total: 1 };
    render(<ArtifactTab wsId={WS} selectedIssueId={ISSUE_A} />);
    fireEvent.click(screen.getByTestId("claude-lab-delete"));
    fireEvent.click(screen.getByTestId("claude-lab-delete-confirm"));
    await vi.waitFor(() => expect(rawRequestSpy).toHaveBeenCalled());
  });

  it("falls back to a placeholder label when the bound issue is gone", () => {
    sessionsRef.current = {
      sessions: [row({ issue_id: "ffffffff-0000-0000-0000-000000000009" })],
      total: 1,
    };
    render(<ArtifactTab wsId={WS} selectedIssueId={ISSUE_A} />);
    expect(screen.getByText(/session_issue_gone/)).toBeTruthy();
    expect(screen.queryByText(/\$\.title/)).toBeNull();
  });

  it("renders without a selected issue — history is workspace-scoped", () => {
    render(<ArtifactTab wsId={WS} selectedIssueId={null} />);
    expect(screen.getByTestId("claude-lab-run-history")).toBeTruthy();
    expect(screen.getByText("Differential expression repro")).toBeTruthy();
  });
});
