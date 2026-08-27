"use client";

// labRunHref / LabRunLink contract tests (0.5.81 post-ship audit P2).
//
// Nothing previously pinned the emitted URL shape — which is exactly how
// the claude-lab missing-receiver gap (audit P1: emitters sent ?run= to a
// view that ignored it) shipped unnoticed. These pin the pure mapping
// (FLAG_ROUTE_SUFFIX resolution, user_* slug rewrite, falsy-run omission)
// plus a minimal render smoke so the anchor can never silently lose its
// href halves.

import { describe, expect, it, vi } from "vitest";
import type { NavigationAdapter } from "../../navigation";
import { NavigationProvider } from "../../navigation";
import { renderWithI18n } from "../../test/i18n";
import { LabRunLink, labRunHref } from "./lab-run-link";

function navStub(): NavigationAdapter {
  return {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/ws-slug/issues/issue-1",
    searchParams: new URLSearchParams(""),
    getShareableUrl: (p: string) => p,
  };
}

describe("labRunHref", () => {
  it("maps every built-in FLAG_ROUTE_SUFFIX key", () => {
    expect(labRunHref("claude_science_lab", "i1", "r1")).toBe(
      "/experimental/claude-lab?issue=i1&run=r1",
    );
    expect(labRunHref("pythia_oracle", "i1", "r1")).toBe(
      "/experimental/pythia?issue=i1&run=r1",
    );
    expect(labRunHref("mythos_swarm", "i1", "r1")).toBe(
      "/experimental/mythos?issue=i1&run=r1",
    );
    expect(labRunHref("swarm_topology", "i1", null)).toBe(
      "/experimental/swarm-topology?issue=i1",
    );
    expect(labRunHref("code_canvas", "i1")).toBe(
      "/experimental/code-canvas?issue=i1",
    );
    expect(labRunHref("llm_wiki_bridge", "i1", "r1")).toBe(
      "/experimental/llm-wiki?issue=i1&run=r1",
    );
    expect(labRunHref("semantica", "i1", "r1")).toBe(
      "/experimental/semantica-explorer?issue=i1&run=r1",
    );
  });

  it("routes user_* plugins through the generic shell, prefix stripped", () => {
    expect(labRunHref("user_my_plugin", "i1", "r2")).toBe(
      "/experimental/plugin/my_plugin?issue=i1&run=r2",
    );
  });

  it("returns undefined for unknown or legacy keys (no dedicated view)", () => {
    expect(labRunHref("agent_self_optimization", "i1", "r1")).toBeUndefined();
    expect(labRunHref(null, "i1", "r1")).toBeUndefined();
    expect(labRunHref(undefined, "i1", "r1")).toBeUndefined();
    expect(labRunHref("", "i1", "r1")).toBeUndefined();
  });

  it("omits the ?run= half for falsy run ids instead of dead-linking", () => {
    // Receiver-less labs pass no runId; the contract is that the param
    // disappears entirely rather than pointing at a nonexistent target.
    expect(labRunHref("code_canvas", "i1", null)).toBe(
      "/experimental/code-canvas?issue=i1",
    );
    expect(labRunHref("code_canvas", "i1", "")).toBe(
      "/experimental/code-canvas?issue=i1",
    );
  });

  it("URL-encodes both params via URLSearchParams semantics", () => {
    const href = labRunHref("mythos_swarm", "issue with space", "run/slash");
    expect(href).toBe("/experimental/mythos?issue=issue+with+space&run=run%2Fslash");
  });
});

describe("<LabRunLink>", () => {
  it("renders an anchor carrying the canonical href", () => {
    const { getByRole } = renderWithI18n(
      <NavigationProvider value={navStub()}>
        <LabRunLink flagKey="claude_science_lab" issueId="issue-9" runId="task-7" />
      </NavigationProvider>,
    );
    const anchor = getByRole("link");
    expect(anchor.getAttribute("href")).toBe(
      "/experimental/claude-lab?issue=issue-9&run=task-7",
    );
  });

  it("returns null when the flag key has no dedicated view", () => {
    const { container } = renderWithI18n(
      <NavigationProvider value={navStub()}>
        <LabRunLink flagKey="unknown_lab" issueId="issue-9" runId="task-7" />
      </NavigationProvider>,
    );
    expect(container).toBeEmptyDOMElement();
  });
});
