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
    expect(storage.snapshot().multica_token).toBeUndefined();
  });
});