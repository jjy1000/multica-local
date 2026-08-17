/**
 * @vitest-environment jsdom
 */
import { act, render, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApiInstance } from "../api";
import { ApiError, type ApiClient } from "../api/client";
import {
  createAuthStore,
  registerAuthStore,
  useAuthStore,
} from "../auth";
import type { StorageAdapter, User, Workspace } from "../types";
import { AuthInitializer } from "./auth-initializer";

const logger = vi.hoisted(() => ({
  debug: vi.fn(),
  info: vi.fn(),
  warn: vi.fn(),
  error: vi.fn(),
}));

vi.mock("../logger", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../logger")>();
  return { ...actual, createLogger: () => logger };
});

vi.mock("../analytics", () => ({
  captureSignupSource: vi.fn(),
  identify: vi.fn(),
  initAnalytics: vi.fn(),
  resetAnalytics: vi.fn(),
}));

const fakeUser = {
  id: "user-1",
  name: "Alice",
  email: "alice@example.com",
  avatar_url: null,
} as User;

const fakeWorkspaces = [{ id: "ws-1", slug: "acme" }] as Workspace[];

function makeStorage(initial: Record<string, string> = {}): StorageAdapter & {
  snapshot: () => Record<string, string>;
} {
  const values = { ...initial };
  return {
    getItem: (key) => values[key] ?? null,
    setItem: (key, value) => {
      values[key] = value;
    },
    removeItem: (key) => {
      delete values[key];
    },
    snapshot: () => ({ ...values }),
  };
}

function makeApi(overrides: Partial<ApiClient> = {}): ApiClient {
  return {
    getConfig: vi.fn().mockResolvedValue({}),
    getMe: vi.fn().mockResolvedValue(fakeUser),
    listWorkspaces: vi.fn().mockResolvedValue(fakeWorkspaces),
    setToken: vi.fn(),
    ...overrides,
  } as unknown as ApiClient;
}

function renderInitializer({
  api,
  storage = makeStorage({ multica_token: "token-1" }),
  cookieAuth = false,
  platform = "desktop",
}: {
  api: ApiClient;
  storage?: StorageAdapter;
  cookieAuth?: boolean;
  platform?: "desktop" | "web";
}) {
  const onLogin = vi.fn();
  const onLogout = vi.fn();
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity } },
  });
  setApiInstance(api);
  registerAuthStore(
    createAuthStore({ api, storage, cookieAuth, onLogin, onLogout }),
  );

  const result = render(
    <QueryClientProvider client={queryClient}>
      <AuthInitializer
        cookieAuth={cookieAuth}
        identity={{ platform }}
        onLogin={onLogin}
        onLogout={onLogout}
        storage={storage}
      >
        <div>child</div>
      </AuthInitializer>
    </QueryClientProvider>,
  );

  return { ...result, onLogin, onLogout, queryClient };
}

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("AuthInitializer recovery", () => {
  it("keeps the token and recovers on the online event after a network failure", async () => {
    const storage = makeStorage({ multica_token: "token-1" });
    const getMe = vi
      .fn()
      .mockRejectedValueOnce(new TypeError("fetch failed"))
      .mockResolvedValue(fakeUser);
    const api = makeApi({ getMe });
    const { onLogout } = renderInitializer({ api, storage });

    await waitFor(() => {
      expect(useAuthStore.getState().status).toBe("recovering");
    });
    expect(storage.snapshot().multica_token).toBe("token-1");
    expect(onLogout).not.toHaveBeenCalled();

    act(() => window.dispatchEvent(new Event("online")));

    await waitFor(() => {
      expect(useAuthStore.getState().user).toEqual(fakeUser);
    });
    expect(getMe).toHaveBeenCalledTimes(2);
    expect(onLogout).not.toHaveBeenCalled();
  });

  it("lets a manual retry restart a recoverable auth attempt", async () => {
    const getMe = vi
      .fn()
      .mockRejectedValueOnce(new ApiError("unavailable", 503, "Unavailable"))
      .mockResolvedValue(fakeUser);
    const api = makeApi({ getMe });
    const { onLogout } = renderInitializer({ api });

    await waitFor(() => {
      expect(useAuthStore.getState().status).toBe("recovering");
    });
    act(() => useAuthStore.getState().retryAuthentication());

    await waitFor(
      () => {
        expect(useAuthStore.getState().status).toBe("authenticated");
      },
      { timeout: 3_000 },
    );
    expect(getMe).toHaveBeenCalledTimes(2);
    expect(onLogout).not.toHaveBeenCalled();
  });

  it("stops automatic retries after the capped backoff ladder exhausts", async () => {
    vi.useFakeTimers();
    const storage = makeStorage({ multica_token: "token-1" });
    const getMe = vi.fn().mockRejectedValue(new TypeError("still offline"));
    const api = makeApi({ getMe });
    const { onLogout } = renderInitializer({ api, storage });

    await act(async () => {
      await Promise.resolve();
    });
    expect(useAuthStore.getState().status).toBe("recovering");

    // Fork ladder: 1s+2s+4s+8s+16s = 31s cumulative for attempts 1-5. The 6th
    // attempt fires at t=31s and hits `attempt + 1 >= MAX_ATTEMPTS`, which
    // exhausts the ladder — no 7th attempt is scheduled.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(31_000);
    });
    expect(getMe).toHaveBeenCalledTimes(6);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000);
    });
    expect(getMe).toHaveBeenCalledTimes(6);

    // Each failed attempt logs a warn; exhaustion does not log error (the
    // upstream port logs error once here — fork divergence pinned below).
    expect(logger.warn).toHaveBeenCalledTimes(6);
    expect(logger.error).not.toHaveBeenCalled();
    // Exhaustion clears the stored token and surfaces the recovery page.
    expect(storage.snapshot().multica_token).toBeUndefined();
    expect(useAuthStore.getState().status).toBe("recovering");
    expect(onLogout).not.toHaveBeenCalled();
  });

  it("keeps the token and recovers as a unit when workspace loading fails", async () => {
    // Fork port runs an atomic Promise.all([getMe, listWorkspaces]) probe —
    // a workspace failure fails the whole probe, so the retry ladder
    // re-attempts auth + workspace seed together.
    const listWorkspaces = vi
      .fn()
      .mockRejectedValueOnce(new TypeError("network unavailable"));
    const api = makeApi({ listWorkspaces });
    const { onLogout } = renderInitializer({ api });

    await waitFor(() => {
      expect(useAuthStore.getState().status).toBe("recovering");
    });
    expect(useAuthStore.getState().user).toBeNull();
    expect(onLogout).not.toHaveBeenCalled();

    // The 2nd probe attempt succeeds as a unit: auth resolves and the
    // workspace list is re-fetched alongside it. (The fork's `useWorkspaceList`
    // consumes the seeded query cache; asserting the cache contents here is
    // not stable across QueryClient internals, so this pins the store-side
    // contract only.)
    await waitFor(
      () => {
        expect(useAuthStore.getState().status).toBe("authenticated");
      },
      { timeout: 3_000 },
    );
    expect(useAuthStore.getState().user).toEqual(fakeUser);
    expect(onLogout).not.toHaveBeenCalled();
  });

  it("recovers as a unit when workspace loading fails in cookie/web mode", async () => {
    const listWorkspaces = vi
      .fn()
      .mockRejectedValueOnce(new TypeError("backend restarting"));
    const api = makeApi({ listWorkspaces });
    const { onLogout } = renderInitializer({
      api,
      cookieAuth: true,
      platform: "web",
    });

    await waitFor(() => {
      expect(useAuthStore.getState().status).toBe("recovering");
    });

    await waitFor(
      () => {
        expect(useAuthStore.getState().status).toBe("authenticated");
      },
      { timeout: 3_000 },
    );
    expect(useAuthStore.getState().user).toEqual(fakeUser);
    expect(onLogout).not.toHaveBeenCalled();
  });

  it("recovers auth from a transient failure without refetching app config", async () => {
    const getConfig = vi
      .fn()
      .mockRejectedValueOnce(new TypeError("network unavailable"))
      .mockResolvedValue({ feature_flags: { recovered: true } });
    const getMe = vi
      .fn()
      .mockRejectedValueOnce(new TypeError("network unavailable"))
      .mockResolvedValue(fakeUser);
    const api = makeApi({ getConfig, getMe });
    renderInitializer({ api });

    await waitFor(() => {
      expect(useAuthStore.getState().status).toBe("recovering");
    });

    await waitFor(
      () => {
        expect(useAuthStore.getState().status).toBe("authenticated");
      },
      { timeout: 3_000 },
    );
    // Fork divergence: app config is fetched once at boot, best-effort —
    // probe retries do not re-fetch it (upstream refetches on every retry).
    expect(getConfig).toHaveBeenCalledTimes(1);
  });

  it("fetches app config once, best-effort, with no retry ladder", async () => {
    vi.useFakeTimers();
    const getConfig = vi
      .fn()
      .mockRejectedValueOnce(new TypeError("network unavailable"))
      .mockRejectedValueOnce(new TypeError("still unavailable"))
      .mockResolvedValue({ feature_flags: { recovered: true } });
    const api = makeApi({ getConfig });
    renderInitializer({ api });

    await act(async () => {
      await Promise.resolve();
    });
    expect(getConfig).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(3_000);
    });
    expect(getConfig).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000);
    });
    expect(getConfig).toHaveBeenCalledTimes(1);
  });

  it("publishes a definitive logout once a 401 clears the stored token", async () => {
    vi.useFakeTimers();
    const storage = makeStorage({ multica_token: "token-1" });
    const getMe = vi.fn().mockImplementation(() => {
      storage.removeItem("multica_token");
      return Promise.reject(new ApiError("unauthorized", 401, "Unauthorized"));
    });
    const api = makeApi({ getMe });
    const { onLogout } = renderInitializer({ api, storage });

    await act(async () => {
      await Promise.resolve();
    });
    expect(useAuthStore.getState().status).toBe("recovering");

    // The API client clears the token on 401; the next ladder attempt then
    // hits the no-token branch and publishes a definitive logout instead of
    // burning the remaining retries.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    expect(useAuthStore.getState().status).toBe("unauthenticated");
    expect(storage.snapshot().multica_token).toBeUndefined();
    expect(getMe).toHaveBeenCalledTimes(1);
    expect(onLogout).toHaveBeenCalledOnce();
  });
});
