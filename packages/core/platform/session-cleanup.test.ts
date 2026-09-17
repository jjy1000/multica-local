/**
 * @vitest-environment jsdom
 */
// jsdom rather than node: the cleanup writes an expiring `document.cookie`,
// and under node it would take the `typeof document === "undefined"` branch
// and pass without ever exercising that line.
import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import type { StorageAdapter, Workspace } from "../types";
import { workspaceKeys } from "../workspace/queries";
import { clearClientSessionData } from "./session-cleanup";

function makeStorage(
  initial: Record<string, string> = {},
): StorageAdapter & { snapshot: () => Record<string, string> } {
  const values = { ...initial };
  return {
    getItem: (key) => values[key] ?? null,
    setItem: (key, value) => {
      values[key] = value;
    },
    removeItem: (key) => {
      delete values[key];
    },
    keys: () => Object.keys(values),
    snapshot: () => ({ ...values }),
  };
}

describe("clearClientSessionData", () => {
  // The scenario this exists for: user A's session dies, the login form the
  // expiry lands on is used by B, and A and B share a workspace — so the
  // stale-tab validator that runs after login finds nothing to prune and
  // every one of these keys would otherwise still be A's.
  it("leaves nothing of the previous session for the next user to find", () => {
    const storage = makeStorage({
      // Workspace-scoped persisted state, under the active slug.
      "multica_issue_draft:acme": '{"title":"A private draft"}',
      "multica:chat:activeSessionId:acme": "session-1",
      // Desktop tab layout, whose paths carry slugs and issue ids.
      multica_tabs: '[{"path":"/acme/issues/secret-issue"}]',
      // Untouched: not owned by the session.
      multica_locale: "zh-Hans",
    });
    document.cookie = "last_workspace_slug=acme; path=/";

    const queryClient = new QueryClient();
    queryClient.setQueryData(workspaceKeys.list(), [
      { id: "ws-1", slug: "acme" },
    ] as Workspace[]);
    queryClient.setQueryData(["issues", "ws-1"], [{ id: "issue-1" }]);

    clearClientSessionData(queryClient, storage);

    // Persisted layer.
    expect(storage.snapshot()).toEqual({ multica_locale: "zh-Hans" });
    // Server-state layer. Every query has staleTime: Infinity, so anything
    // left here would render for B and never refetch away.
    expect(queryClient.getQueryData(["issues", "ws-1"])).toBeUndefined();
    expect(queryClient.getQueryData(workspaceKeys.list())).toBeUndefined();
    expect(document.cookie).not.toContain("last_workspace_slug=acme");
  });

  it("clears every workspace the session had, not just the active one", () => {
    const storage = makeStorage({
      "multica_navigation:acme": "1",
      "multica_navigation:globex": "2",
    });
    const queryClient = new QueryClient();
    queryClient.setQueryData(workspaceKeys.list(), [
      { id: "ws-1", slug: "acme" },
      { id: "ws-2", slug: "globex" },
    ] as Workspace[]);

    clearClientSessionData(queryClient, storage);

    expect(storage.snapshot()).toEqual({});
  });

  // A cold start rejected at the identity probe has an empty Query cache, so
  // there is no workspace list to read slugs from. Asserting only the global
  // `multica_tabs` here would pass while every per-workspace key survived —
  // the exact hole that let a stale-token launch leak A's drafts to B.
  it("clears workspace-scoped keys even with no workspace list to enumerate", () => {
    const storage = makeStorage({
      "multica_issue_draft:acme": '{"title":"A private draft"}',
      "multica:chat:activeSessionId:acme": "session-1",
      multica_tabs: "[]",
    });
    const queryClient = new QueryClient();
    expect(queryClient.getQueryData(workspaceKeys.list())).toBeUndefined();

    clearClientSessionData(queryClient, storage);

    expect(storage.snapshot()).toEqual({});
  });

  // Adapters that cannot list their keys degrade to the workspace list the
  // Query cache holds — narrower, but everything this process resolved.
  it("falls back to the cached workspace list when the adapter cannot enumerate", () => {
    const removed: Record<string, true> = {};
    const storage: StorageAdapter = {
      getItem: () => null,
      setItem: () => {},
      removeItem: (k) => {
        removed[k] = true;
      },
    };
    const queryClient = new QueryClient();
    queryClient.setQueryData(workspaceKeys.list(), [
      { id: "ws-1", slug: "acme" },
    ] as Workspace[]);

    clearClientSessionData(queryClient, storage);

    expect(removed["multica_issue_draft:acme"]).toBe(true);
    expect(queryClient.getQueryData(workspaceKeys.list())).toBeUndefined();
  });
});
