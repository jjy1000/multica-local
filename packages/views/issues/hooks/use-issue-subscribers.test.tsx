/**
 * @vitest-environment jsdom
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { setApiInstance } from "@multica/core/api";
import { ApiError } from "@multica/core/api/client";
import type { ApiClient } from "@multica/core/api/client";
import type { IssueSubscriber } from "@multica/core/types";

const toastError = vi.fn();
const toastSuccess = vi.fn();
vi.mock("sonner", () => ({
  toast: {
    error: (msg: string) => toastError(msg),
    success: (msg: string) => toastSuccess(msg),
  },
}));

// Return the key path so an assertion can tell the two failure messages apart
// without depending on the English copy.
vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (sel: (d: Record<string, Record<string, string>>) => string) =>
      sel(
        new Proxy(
          {},
          {
            get: (_t, section: string) =>
              new Proxy(
                {},
                { get: (_s, key: string) => `${section}.${key}` },
              ),
          },
        ) as Record<string, Record<string, string>>,
      ),
  }),
}));

vi.mock("@multica/core/realtime", () => ({
  useWSEvent: () => undefined,
  useWSReconnect: () => undefined,
}));

import { useIssueSubscribers } from "./use-issue-subscribers";

function wrapper(queryClient: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
  };
}

function renderSubscribers() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return {
    ...renderHook(() => useIssueSubscribers("issue-1", "user-1"), {
      wrapper: wrapper(queryClient),
    }),
    queryClient,
  };
}

/**
 * Fork port of upstream #6380 (MUL-5714): the fork has no subtree-unsubscribe
 * concept, so the ported surface is (1) subscriptionKnown gating, (2) the
 * serialized direct toggle, and (3) the failure toast — without which an
 * optimistic rollback is indistinguishable from a dead button.
 */
describe("useIssueSubscribers (fork port of #6380)", () => {
  afterEach(() => {
    cleanup();
    toastError.mockClear();
    toastSuccess.mockClear();
  });

  it("gates the subscribed verdict on the query resolving", async () => {
    let resolveList: (v: IssueSubscriber[]) => void;
    setApiInstance({
      listIssueSubscribers: () =>
        new Promise((resolve) => {
          resolveList = resolve;
        }),
    } as unknown as ApiClient);

    const { result } = renderSubscribers();
    // Before the query resolves, nothing may render as "not subscribed" and a
    // click may not fire a subscribe.
    expect(result.current.subscriptionKnown).toBe(false);

    resolveList!([
      {
        issue_id: "issue-1",
        user_type: "member",
        user_id: "user-1",
        reason: "manual",
        created_at: "2026-08-06T00:00:00Z",
      },
    ]);
    await waitFor(() => expect(result.current.subscriptionKnown).toBe(true));
    expect(result.current.isSubscribed).toBe(true);
  });

  it("surfaces a failed toggle with an error toast instead of a silent rollback", async () => {
    setApiInstance({
      listIssueSubscribers: async () => [],
      subscribeToIssue: async () => {
        throw new ApiError("boom", 500, "boom");
      },
    } as unknown as ApiClient);

    const { result } = renderSubscribers();
    await waitFor(() => expect(result.current.subscriptionKnown).toBe(true));

    result.current.toggleSubscribe();
    await waitFor(() => expect(toastError).toHaveBeenCalledTimes(1));
    expect(toastError).toHaveBeenCalledWith(
      "detail.subscription_update_failed",
    );
  });

  it("serializes overlapping toggles so the optimistic snapshot cannot collide", async () => {
    let subscribedCalls = 0;
    setApiInstance({
      listIssueSubscribers: async () => [],
      subscribeToIssue: async () => {
        subscribedCalls += 1;
      },
    } as unknown as ApiClient);

    const { result } = renderSubscribers();
    await waitFor(() => expect(result.current.subscriptionKnown).toBe(true));

    // Two clicks in the same tick: React Query flushes isPending in a
    // microtask, so without the in-flight ref both would reach the mutation.
    result.current.toggleSubscribe();
    result.current.toggleSubscribe();

    // Give both microtask chains a chance to fire; only one mutation may run.
    await new Promise((r) => setTimeout(r, 50));
    expect(subscribedCalls).toBe(1);
  });

  it("is subscribed once the resolved list contains the current user", async () => {
    setApiInstance({
      listIssueSubscribers: async () => [
        {
          issue_id: "issue-1",
          user_type: "member",
          user_id: "user-1",
          reason: "mention",
          created_at: "2026-08-06T00:00:00Z",
        },
      ],
    } as unknown as ApiClient);

    const { result } = renderSubscribers();
    await waitFor(() => expect(result.current.isSubscribed).toBe(true));
    expect(result.current.subscriptionKnown).toBe(true);
  });
});
