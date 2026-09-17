"use client";

import { useEffect, useRef, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { getApi } from "../api";
import { useAuthStore } from "../auth";
import {
  captureSignupSource,
  identify as identifyAnalytics,
  initAnalytics,
} from "../analytics";
import { configStore } from "../config";
import { workspaceKeys } from "../workspace/queries";
import { createLogger } from "../logger";
import { clearClientSessionData } from "./session-cleanup";
import { defaultStorage } from "./storage";
import { setCurrentWorkspace } from "./workspace-storage";
import type { ClientIdentity } from "./types";
import type { StorageAdapter } from "../types/storage";
import type { User } from "../types";

const logger = createLogger("auth");

// MUL-6254: transient desktop startup failures (DB not ready, daemon not bound,
// first-boot race) must not leave the user on a permanent "Loading…" screen.
// Retry ladder mirrors the existing daemon-reauth path: 1s, 2s, 4s, 8s, 16s, 30s.
const RETRY_DELAYS_MS = [1_000, 2_000, 4_000, 8_000, 16_000, 30_000];
// Capped total ≈ 61s (without an "online" event accelerating the next attempt).
const MAX_ATTEMPTS = RETRY_DELAYS_MS.length;

export function AuthInitializer({
  children,
  onLogin,
  storage = defaultStorage,
  cookieAuth,
  identity,
}: {
  children: ReactNode;
  onLogin?: () => void;
  // No `onLogout`: every unauthenticated exit below delegates to the auth
  // store's own teardown, which is where the logout / session-expiry
  // callbacks live.
  storage?: StorageAdapter;
  cookieAuth?: boolean;
  identity?: ClientIdentity;
}) {
  const qc = useQueryClient();

  useEffect(() => {
    const api = getApi();

    // Stamp attribution before anything else — the signup event (server-side)
    // reads this cookie, so it has to be present before the user hits submit.
    captureSignupSource();

    // Fetch app config (CDN domain, PostHog key, feature flags, …) in the
    // background — non-blocking. Treated as a recovery warmup: every retry
    // re-fetches it so a server that came back up between attempts gets its
    // settings applied without waiting for a manual reload.
    const fetchConfig = () =>
      api
        .getConfig()
        .then((cfg) => {
          if (cfg.cdn_domain) {
            configStore.getState().setCdnConfig({
              cdnDomain: cfg.cdn_domain,
              // Old servers omit this — false keeps the previous behavior.
              cdnSigned: cfg.cdn_signed === true,
            });
          }
          configStore.getState().setAuthConfig({
            allowSignup: cfg.allow_signup,
            googleClientId: cfg.google_client_id,
            // Old servers omit this field — treat that as "creation allowed"
            // (the managed-cloud default) rather than blocking the UI.
            workspaceCreationDisabled: cfg.workspace_creation_disabled === true,
            vcsIntegrationAvailable: cfg.vcs_integration_available === true,
          });
          configStore.getState().setDaemonConfig({
            daemonServerUrl: cfg.daemon_server_url,
            daemonAppUrl: cfg.daemon_app_url,
          });
          configStore
            .getState()
            .setFeatureFlags(
              (cfg.feature_flags as Record<string, boolean> | undefined) ?? {},
            );
          if (typeof cfg.server_version === "string") {
            configStore.getState().setServerVersion(cfg.server_version);
          }
          if (cfg.posthog_key) {
            initAnalytics({
              key: cfg.posthog_key,
              host: cfg.posthog_host || "",
              appVersion: identity?.version,
              environment: cfg.analytics_environment,
            });
          }
        })
        .catch(() => {
          /* config is optional — legacy file card matching degrades gracefully */
        });

    fetchConfig();

    const onAuthSuccess = (user: User) => {
      onLogin?.();
      useAuthStore.setState({
        user,
        isLoading: false,
        status: "authenticated",
        expired: false,
      });
      identifyAnalytics(user.id, { email: user.email, name: user.name });
    };

    const onAuthFailure = () => {
      // One teardown for every dead session, whether it died at boot or
      // under a running shell. The api client's 401 hook has normally run
      // this already by the time we get here; the action is idempotent.
      useAuthStore.getState().sessionExpired();
    };

    // Run the auth probe + (optional) workspace seed under the retry ladder.
    // We resolve on either a successful auth (return out of the effect) or a
    // genuine logout failure (also return — the user must explicitly retry).
    // transient failures schedule the next attempt and stay in "recovering".
    const sleep = (ms: number) =>
      new Promise<void>((resolve) => setTimeout(resolve, ms));

    let cancelled = false;
    let timeoutId: ReturnType<typeof setTimeout> | null = null;

    const clearScheduledRetry = () => {
      if (timeoutId !== null) {
        clearTimeout(timeoutId);
        timeoutId = null;
      }
    };

    const runAuthProbe = async (attempt: number): Promise<void> => {
      if (cancelled) return;

      const token = cookieAuth ? null : storage.getItem("multica_token");

      // If we have no token and we're past the first attempt, treat the user
      // as logged out — the only way forward is for them to type a username.
      // This avoids burning 61s of retries for a fresh install.
      if (!cookieAuth && !token && attempt > 0) {
        onAuthFailure();
        return;
      }

      try {
        if (token) {
          api.setToken(token);
        }

        const [user, wsList] = await Promise.all([
          api.getMe(),
          api.listWorkspaces(),
        ]);

        if (cancelled) return;
        onAuthSuccess(user);
        // Seed React Query cache so the URL-driven layout can resolve the
        // slug without a second fetch.
        qc.setQueryData(workspaceKeys.list(), wsList);
      } catch (err) {
        if (cancelled) return;
        logger.warn(`auth probe attempt ${attempt + 1} failed`, err);

        if (attempt + 1 >= MAX_ATTEMPTS) {
          // Out of retries — surface as a recovery state so the desktop shell
          // can render the retry page instead of leaving the user on a blank
          // "Loading…" screen.
          api.setToken(null);
          setCurrentWorkspace(null, null);
          storage.removeItem("multica_token");
          useAuthStore.setState({
            user: null,
            isLoading: false,
            status: "recovering",
          });
          return;
        }

        // Mark recovering so the UI can show the recovery page during long
        // back-off delays (the 30s tail in particular).
        useAuthStore.setState({ status: "recovering" });

        const delay = RETRY_DELAYS_MS[attempt] ?? 0;
        await sleep(delay);
        if (cancelled) return;
        return runAuthProbe(attempt + 1);
      }
    };

    // Kick off the first attempt immediately.
    runAuthProbe(0);

    // MUL-6254: when the OS reports connectivity restored while we're in the
    // retry ladder, fire the next attempt immediately rather than waiting for
    // the current back-off window to expire. The renderer (chromium) and the
    // Electron main process both surface `online` so this works in both modes.
    const handleOnline = () => {
      if (cancelled) return;
      if (useAuthStore.getState().status !== "recovering") return;
      clearScheduledRetry();
      // Bump retryGeneration so the AuthStatus consumers re-render against the
      // fresh attempt; the store tracks a monotonic generation counter.
      useAuthStore.getState().retryAuthentication();
      runAuthProbe(0);
    };
    window.addEventListener("online", handleOnline);

    return () => {
      cancelled = true;
      clearScheduledRetry();
      window.removeEventListener("online", handleOnline);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // A session that ends leaves its whole client side behind: a warmed query
  // cache whose every entry has `staleTime: Infinity`, per-workspace drafts
  // and chat selection, the desktop tab layout. The screen it lands on offers
  // a login form, and the next person to use it is not guaranteed to be the
  // one who was just signed in — on a shared machine a colleague would inherit
  // all of it, and sharing a workspace with them means the tab validator finds
  // nothing to drop. So an expiry erases exactly what a logout erases; only
  // the auth teardown differs (the auth store's `sessionExpired`), plus what
  // belongs to a process outliving the session — Desktop's daemon.
  //
  // Keyed on being unauthenticated rather than on a transition out of
  // `authenticated`, because the case that leaks worst never passes through
  // `authenticated` at all: the app starts, the stale token on disk is
  // rejected at the identity probe, and the previous session's drafts sit
  // there waiting for whoever signs in next.
  //
  // This cannot sign anyone out. Reaching `unauthenticated` IS the decision to
  // end the session, and only two things reach it: a 401 on a credential we
  // presented, or having no credential at all. A client that cannot reach the
  // server — offline, DNS down, 5xx — settles on `recovering` and keeps both
  // its token and everything below (the retry ladder above only publishes
  // `unauthenticated` through `onAuthFailure`, which means the credential
  // itself was refused or absent).
  const authStatus = useAuthStore((state) => state.status);
  const clearedForThisSession = useRef(false);
  useEffect(() => {
    if (authStatus === "authenticated") {
      clearedForThisSession.current = false;
      return;
    }
    if (authStatus !== "unauthenticated" || clearedForThisSession.current) {
      return;
    }
    clearedForThisSession.current = true;
    clearClientSessionData(qc, storage);
  }, [authStatus, qc, storage]);

  return <>{children}</>;
}