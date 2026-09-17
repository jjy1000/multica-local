// @vitest-environment node
import { describe, expect, it, vi } from "vitest";
import type { ApiClient } from "../api/client";
import { ApiError } from "../api/client";
import type { StorageAdapter, User } from "../types";
import { createAuthStore } from "./store";

const fakeUser: User = {
  id: "u1",
  name: "Alice",
  email: "alice@example.com",
  avatar_url: null,
} as User;

function makeStorage(initial: Record<string, string> = {}): StorageAdapter & {
  snapshot: () => Record<string, string>;
} {
  const data = { ...initial };
  return {
    getItem: (k) => data[k] ?? null,
    setItem: (k, v) => {
      data[k] = v;
    },
    removeItem: (k) => {
      delete data[k];
    },
    snapshot: () => ({ ...data }),
  };
}

function makeApi(getMe: () => Promise<User>): ApiClient {
  return {
    setToken: vi.fn(),
    getMe,
    // Only the methods touched by refreshMe are needed. Cast to ApiClient for
    // type compatibility — the store treats it opaquely.
  } as unknown as ApiClient;
}

// `refreshMe` is the surviving token-mode analogue of the old `initialize`
// call — both replay getMe against the active token. AuthInitializer no longer
// delegates to a store-side method because the retry ladder lives in the
// component; these tests pin the store's refresh contract.
describe("authStore.refreshMe — token mode", () => {
  it("keeps the user when getMe fails with a non-401 ApiError (e.g. 500)", async () => {
    const storage = makeStorage({ multica_token: "t" });
    const api = makeApi(() =>
      Promise.reject(new ApiError("server error", 500, "Internal Server Error")),
    );
    const store = createAuthStore({ api, storage });
    // Seed an existing authenticated session so the refresh has something to
    // keep. The retry ladder in AuthInitializer surfaces this kind of failure
    // to the recovery page rather than tearing down the user.
    store.getState().setUser(fakeUser);

    await store.getState().refreshMe();

    expect(store.getState().user).toEqual(fakeUser);
    expect(store.getState().status).toBe("authenticated");
    expect(storage.snapshot().multica_token).toBe("t");
  });

  it("keeps the user on a network failure (non-ApiError throw)", async () => {
    const storage = makeStorage({ multica_token: "t" });
    const api = makeApi(() => Promise.reject(new TypeError("fetch failed")));
    const store = createAuthStore({ api, storage });
    store.getState().setUser(fakeUser);

    await store.getState().refreshMe();

    expect(store.getState().user).toEqual(fakeUser);
    expect(store.getState().status).toBe("authenticated");
  });

  it("refreshes user fields when getMe succeeds", async () => {
    const storage = makeStorage({ multica_token: "t" });
    const api = makeApi(() => Promise.resolve(fakeUser));
    const store = createAuthStore({ api, storage });
    store.getState().setUser({ ...fakeUser, name: "Old" });

    await store.getState().refreshMe();

    expect(store.getState().user).toEqual(fakeUser);
    expect(store.getState().user?.name).toBe("Alice");
    expect(store.getState().status).toBe("authenticated");
  });
});

describe("authStore — recovery contract", () => {
  it("retryAuthentication flips status to 'authenticating' and bumps the generation counter", () => {
    const storage = makeStorage();
    const api = makeApi(() => Promise.resolve(fakeUser));
    const store = createAuthStore({ api, storage });
    store.setState({ status: "recovering", retryGeneration: 0 });

    store.getState().retryAuthentication();

    expect(store.getState().status).toBe("authenticating");
    expect(store.getState().isLoading).toBe(true);
    expect(store.getState().retryGeneration).toBe(1);

    // A second retry must increment monotonically so consumers can key off it.
    store.getState().retryAuthentication();
    expect(store.getState().retryGeneration).toBe(2);
  });

  it("logout clears the user, flips status to 'unauthenticated', and removes the token", () => {
    const storage = makeStorage({ multica_token: "t" });
    const api = makeApi(() => Promise.resolve(fakeUser));
    const store = createAuthStore({ api, storage });
    store.getState().setUser(fakeUser);

    store.getState().logout();

    expect(store.getState().user).toBeNull();
    expect(store.getState().status).toBe("unauthenticated");
    expect(store.getState().expired).toBe(false);
    expect(storage.snapshot().multica_token).toBeUndefined();
  });
});

describe("authStore.sessionExpired — involuntary session end (MUL-7028)", () => {
  it("ends the session when the server rejects the credential", () => {
    const storage = makeStorage({ multica_token: "t" });
    const api = makeApi(() => Promise.resolve(fakeUser));
    const onLogout = vi.fn();
    const store = createAuthStore({ api, storage, onLogout });

    store.setState({ user: fakeUser, status: "authenticated", isLoading: false });
    store.getState().sessionExpired();

    expect(storage.snapshot().multica_token).toBeUndefined();
    expect(api.setToken).toHaveBeenCalledWith(null);
    expect(onLogout).toHaveBeenCalledOnce();
    expect(store.getState().user).toBeNull();
    expect(store.getState().isLoading).toBe(false);
    expect(store.getState().status).toBe("unauthenticated");
    expect(store.getState().expired).toBe(true);
  });

  // Desktop's deep link writes the token, then verifies it. A rejected token
  // never leaves "unauthenticated", so an idempotence guard placed before the
  // credential teardown would return with the invalid token still in storage,
  // to be replayed at the next launch. The old 401 handler always removed it.
  it("drops a rejected token even when there was no session to end", async () => {
    const storage = makeStorage();
    const api = {
      setToken: vi.fn(),
      getMe: vi.fn().mockRejectedValue(new Error("unauthorized")),
    } as unknown as ApiClient;
    const store = createAuthStore({ api, storage });
    store.setState({ user: null, status: "unauthenticated", isLoading: false });

    await expect(
      store.getState().loginWithToken("stale-deep-link-token"),
    ).rejects.toThrow();
    // Stands in for the api client's 401 hook, which fires inside getMe.
    store.getState().sessionExpired();

    expect(storage.snapshot().multica_token).toBeUndefined();
    expect(api.setToken).toHaveBeenLastCalledWith(null);
  });

  it("does not claim a session expired for a client that never had one", () => {
    const storage = makeStorage();
    const api = makeApi(() => Promise.resolve(fakeUser));
    const store = createAuthStore({ api, storage, cookieAuth: true });

    // Boot-time identity probe on a first visit: still "authenticating",
    // no stored credential.
    store.getState().sessionExpired();

    expect(store.getState().status).toBe("unauthenticated");
    expect(store.getState().expired).toBe(false);
  });

  it("flags expiry when a stored token is rejected at boot", () => {
    const storage = makeStorage({ multica_token: "stale" });
    const api = makeApi(() => Promise.resolve(fakeUser));
    const store = createAuthStore({ api, storage });

    store.getState().sessionExpired();

    expect(store.getState().expired).toBe(true);
    expect(storage.snapshot().multica_token).toBeUndefined();
  });

  it("runs the expiry handler instead of the logout one when given both", () => {
    const storage = makeStorage({ multica_token: "t" });
    const api = makeApi(() => Promise.resolve(fakeUser));
    const onLogout = vi.fn();
    const onSessionExpired = vi.fn();
    const store = createAuthStore({ api, storage, onLogout, onSessionExpired });

    store.setState({ user: fakeUser, status: "authenticated", isLoading: false });
    store.getState().sessionExpired();

    // Desktop's logout teardown stops the local daemon, which is the wrong
    // answer for a session the user did not choose to end (MUL-7028).
    expect(onSessionExpired).toHaveBeenCalledOnce();
    expect(onLogout).not.toHaveBeenCalled();
  });

  it("still runs the logout teardown for an explicit logout", () => {
    const storage = makeStorage({ multica_token: "t" });
    const api = makeApi(() => Promise.resolve(fakeUser));
    const onLogout = vi.fn();
    const onSessionExpired = vi.fn();
    const store = createAuthStore({ api, storage, onLogout, onSessionExpired });

    store.setState({ user: fakeUser, status: "authenticated", isLoading: false });
    store.getState().logout();

    expect(onLogout).toHaveBeenCalledOnce();
    expect(onSessionExpired).not.toHaveBeenCalled();
  });

  it("treats a burst of parallel 401s as the one expiry it is", () => {
    const storage = makeStorage({ multica_token: "t" });
    const api = makeApi(() => Promise.resolve(fakeUser));
    const onLogout = vi.fn();
    const store = createAuthStore({ api, storage, onLogout });

    store.setState({ user: fakeUser, status: "authenticated", isLoading: false });
    store.getState().sessionExpired();
    store.getState().sessionExpired();
    store.getState().sessionExpired();

    expect(onLogout).toHaveBeenCalledOnce();
  });

  it("clears the expired notice once the user signs back in", async () => {
    const storage = makeStorage();
    const api = {
      setToken: vi.fn(),
      getMe: vi.fn().mockResolvedValue(fakeUser),
    } as unknown as ApiClient;
    const store = createAuthStore({ api, storage });

    store.setState({ user: fakeUser, status: "authenticated", isLoading: false });
    store.getState().sessionExpired();
    expect(store.getState().expired).toBe(true);

    await store.getState().loginWithToken("fresh-token");

    expect(store.getState().status).toBe("authenticated");
    expect(store.getState().expired).toBe(false);
  });
});