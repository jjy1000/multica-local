/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, beforeEach, vi } from "vitest";
import { cleanup, render, waitFor } from "@testing-library/react";
import { CausalGraphInstallAutoStart } from "./causal-graph-install-autostart";

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
describe("CausalGraphInstallAutoStart — 0.5.119 default-on ensure-install", () => {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
    mockResponses();
  });

  it("no-calls when the flag is disabled", async () => {
    state.enabled = false;
    state.wsId = "ws-disabled";
    render(<CausalGraphInstallAutoStart />);
    await new Promise((r) => setTimeout(r, 30));
    expect(rawRequestSpy).not.toHaveBeenCalled();
  });

  it("no-calls without an active workspace", async () => {
    state.enabled = true;
    state.wsId = null;
    render(<CausalGraphInstallAutoStart />);
    await new Promise((r) => setTimeout(r, 30));
    expect(rawRequestSpy).not.toHaveBeenCalled();
  });

  it("status-only when the team is already installed", async () => {
    state.enabled = true;
    state.wsId = "ws-installed";
    state.statusInstalled = true;
    render(<CausalGraphInstallAutoStart />);
    await waitFor(() => expect(rawRequestSpy).toHaveBeenCalledTimes(1));
    expect(String(rawRequestSpy.mock.calls[0][0])).toContain(
      "/api/experimental-resources/causal_graph/status",
    );
    expect(
      rawRequestSpy.mock.calls.some((c) => String(c[0]).endsWith("/install")),
    ).toBe(false);
  });

  it("POSTs install when never installed", async () => {
    state.enabled = true;
    state.wsId = "ws-fresh";
    state.statusInstalled = false;
    render(<CausalGraphInstallAutoStart />);
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

  it("POSTs install when the agent team is stale (<3)", async () => {
    state.enabled = true;
    state.wsId = "ws-stale";
    rawRequestSpy.mockImplementation(() =>
      Promise.resolve(
        new Response(
          JSON.stringify({
            installed: true,
            counts: [{ resource_type: "agent", total: 2 }],
          }),
          { status: 200, headers: { "content-type": "application/json" } },
        ),
      ),
    );
    render(<CausalGraphInstallAutoStart />);
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
});
