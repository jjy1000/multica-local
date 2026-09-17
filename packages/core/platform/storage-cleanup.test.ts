// @vitest-environment node
import { describe, it, expect, vi } from "vitest";
import {
  clearAllWorkspaceStorage,
  clearWorkspaceStorage,
} from "./storage-cleanup";

describe("clearWorkspaceStorage", () => {
  it("removes all workspace-scoped keys for given wsId", () => {
    const adapter = {
      getItem: vi.fn(),
      setItem: vi.fn(),
      removeItem: vi.fn(),
    };

    clearWorkspaceStorage(adapter, "ws_123");

    expect(adapter.removeItem).toHaveBeenCalledWith("multica_issue_draft:ws_123");
    expect(adapter.removeItem).toHaveBeenCalledWith("multica_issues_view:ws_123");
    expect(adapter.removeItem).toHaveBeenCalledWith("multica_issues_scope:ws_123");
    expect(adapter.removeItem).toHaveBeenCalledWith("multica_my_issues_view:ws_123");
    expect(adapter.removeItem).toHaveBeenCalledWith("multica:chat:selectedAgentId:ws_123");
    expect(adapter.removeItem).toHaveBeenCalledWith("multica:chat:activeSessionId:ws_123");
    expect(adapter.removeItem).toHaveBeenCalledWith("multica:chat:drafts:ws_123");
    expect(adapter.removeItem).toHaveBeenCalledWith("multica:chat:expanded:ws_123");
    expect(adapter.removeItem).toHaveBeenCalledWith("multica_navigation:ws_123");
    expect(adapter.removeItem).toHaveBeenCalledTimes(9);
  });
});

describe("clearAllWorkspaceStorage", () => {
  function makeAdapter(values: Record<string, string>) {
    return {
      getItem: (k: string) => values[k] ?? null,
      setItem: (k: string, v: string) => {
        values[k] = v;
      },
      removeItem: (k: string) => {
        delete values[k];
      },
      keys: () => Object.keys(values),
      snapshot: () => ({ ...values }),
    };
  }

  // The point of enumerating: a cold start rejected at the identity probe
  // never loaded a workspace list, so it cannot name a single slug.
  it("removes workspace-scoped keys for slugs the caller never knew", () => {
    const adapter = makeAdapter({
      "multica_issue_draft:acme": "1",
      "multica_issue_draft:globex": "2",
      "multica_navigation:initech": "3",
      "multica:chat:activeSessionId:acme": "4",
      // Not session state — a device preference and another app's key.
      multica_locale: "zh-Hans",
      unrelated_key: "keep",
    });

    expect(clearAllWorkspaceStorage(adapter)).toBe(true);

    expect(adapter.snapshot()).toEqual({
      multica_locale: "zh-Hans",
      unrelated_key: "keep",
    });
  });

  it("reports when the adapter cannot enumerate, instead of silently doing nothing", () => {
    const adapter = {
      getItem: vi.fn(),
      setItem: vi.fn(),
      removeItem: vi.fn(),
    };

    expect(clearAllWorkspaceStorage(adapter)).toBe(false);
    expect(adapter.removeItem).not.toHaveBeenCalled();
  });
});
