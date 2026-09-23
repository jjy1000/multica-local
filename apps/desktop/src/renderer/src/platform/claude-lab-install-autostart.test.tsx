/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, beforeEach, vi } from "vitest";
import { cleanup, render, waitFor } from "@testing-library/react";
import { ClaudeLabInstallAutoStart } from "./claude-lab-install-autostart";

const state = vi.hoisted(() => ({
  enabled: false,
  wsId: "ws-a" as string | null,
  statusInstalled: true,
}));
const rawRequestSpy = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/experimental", () => ({
  useExperimentalFlag: () => state.enabled,
}));
vi.mock("@multica/core/hooks", () => ({
  useCurrentWorkspaceId: () => state.wsId,
}));
vi.mock("@multica/core/api", () => ({
  api: {
    rawRequest: rawRequestSpy,
  },
}));

function mockResponses() {
  rawRequestSpy.mockImplementation(() =>
    Promise.resolve(
      new Response(JSON.stringify({ installed: state.statusInstalled }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    ),
  );
}

// The component's attempted-guard is module state keyed by workspace
// ID — each test uses a fresh workspace ID so the guard never skips.
describe("ClaudeLabInstallAutoStart — 0.5.114 default-on ensure-install", () => {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
    mockResponses();
  });

  it("no-calls when the flag is disabled", async () => {
    state.enabled = false;
    state.wsId = "ws-disabled";
    render(<ClaudeLabInstallAutoStart />);
    await new Promise((r) => setTimeout(r, 30));
    expect(rawRequestSpy).not.toHaveBeenCalled();
  });

  it("no-calls without an active workspace", async () => {
    state.enabled = true;
    state.wsId = null;
    render(<ClaudeLabInstallAutoStart />);
    await new Promise((r) => setTimeout(r, 30));
    expect(rawRequestSpy).not.toHaveBeenCalled();
  });

  it("status-only when the payload is already installed", async () => {
    state.enabled = true;
    state.wsId = "ws-installed";
    state.statusInstalled = true;
    render(<ClaudeLabInstallAutoStart />);
    await waitFor(() => expect(rawRequestSpy).toHaveBeenCalledTimes(1));
    expect(String(rawRequestSpy.mock.calls[0][0])).toContain(
      "/api/experimental-resources/claude_science_lab/status",
    );
    // no install POST for an already-installed workspace
    expect(
      rawRequestSpy.mock.calls.some((c) => String(c[0]).endsWith("/install")),
    ).toBe(false);
  });

  it("POSTs install when installed but the agent payload is stale (<6)", async () => {
    state.enabled = true;
    state.wsId = "ws-stale";
    rawRequestSpy.mockImplementation(() =>
      Promise.resolve(
        new Response(
          JSON.stringify({
            installed: true,
            counts: [{ resource_type: "agent", total: 5 }],
          }),
          { status: 200, headers: { "content-type": "application/json" } },
        ),
      ),
    );
    render(<ClaudeLabInstallAutoStart />);
    await waitFor(() => {
      expect(
        rawRequestSpy.mock.calls.some(
          (c) =>
            String(c[0]).endsWith("/install") &&
            (c[1] as RequestInit | undefined)?.method === "POST",
        ),
      ).toBe(true);
    });
  });

  it("POSTs the idempotent install when status reports not-installed", async () => {
    state.enabled = true;
    state.wsId = "ws-fresh";
    state.statusInstalled = false;
    render(<ClaudeLabInstallAutoStart />);
    await waitFor(() => {
      expect(
        rawRequestSpy.mock.calls.some(
          (c) =>
            String(c[0]).endsWith("/install") &&
            (c[1] as RequestInit | undefined)?.method === "POST",
        ),
      ).toBe(true);
    });
    // status probe precedes the install
    expect(String(rawRequestSpy.mock.calls[0][0])).toContain("/status");
  });

  it("re-arms after an install failure (guard released)", async () => {
    state.enabled = true;
    state.wsId = "ws-fail";
    rawRequestSpy.mockImplementation(() =>
      Promise.reject(new Error("boom")),
    );
    const { unmount } = render(<ClaudeLabInstallAutoStart />);
    await waitFor(() => expect(rawRequestSpy).toHaveBeenCalled());
    unmount();
    // a remount in the same session retries because the failure
    // released the per-workspace guard
    render(<ClaudeLabInstallAutoStart />);
    await waitFor(() => expect(rawRequestSpy.mock.calls.length).toBeGreaterThan(1));
  });
});
