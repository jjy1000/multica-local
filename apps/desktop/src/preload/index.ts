import { contextBridge, ipcRenderer } from "electron";
import { electronAPI } from "@electron-toolkit/preload";
import type { RuntimeConfigResult } from "../shared/runtime-config";
import type { FreezeBreadcrumb } from "../shared/freeze-breadcrumb";
import {
  RENDERER_ROUTE_CONTEXT_CHANNEL,
  type RendererRouteContextInput,
} from "../shared/renderer-route-context";
import {
  isNavigationGesture,
  NAVIGATION_GESTURE_CHANNEL,
  type NavigationGesture,
} from "../shared/navigation-gestures";

// Synchronously fetch app metadata from main at preload time so the renderer
// can pass it into CoreProvider during the initial render — the alternative
// (async ipc.invoke) would race the ApiClient construction in initCore and
// the first few HTTP requests would go out without X-Client-Version/OS.
function fetchAppInfo(): { version: string; os: "macos" | "windows" | "linux" | "unknown" } {
  try {
    const info = ipcRenderer.sendSync("app:get-info") as
      | { version: string; os: "macos" | "windows" | "linux" | "unknown" }
      | undefined;
    if (info && typeof info.version === "string" && typeof info.os === "string") return info;
  } catch {
    // fall through
  }
  // Fallback: derive OS from process.platform; version unknown.
  const p = process.platform;
  const os: "macos" | "windows" | "linux" | "unknown" =
    p === "darwin" ? "macos" : p === "win32" ? "windows" : p === "linux" ? "linux" : "unknown";
  return { version: "unknown", os };
}

function fetchRuntimeConfig(): RuntimeConfigResult {
  try {
    const result = ipcRenderer.sendSync("runtime-config:get") as RuntimeConfigResult | undefined;
    if (result && typeof result === "object" && "ok" in result) return result;
  } catch (err) {
    return {
      ok: false,
      error: {
        message: err instanceof Error ? err.message : String(err),
      },
    };
  }
  return { ok: false, error: { message: "Runtime config unavailable" } };
}

const appInfo = fetchAppInfo();
const runtimeConfig = fetchRuntimeConfig();

// Read the OS-preferred locale that main injected via additionalArguments.
// Zero IPC, zero blocking — process.argv is populated before preload runs.
function fetchSystemLocale(): string {
  const arg = process.argv.find((a) => a.startsWith("--multica-locale="));
  return arg?.split("=")[1] ?? "en";
}

const systemLocale = fetchSystemLocale();

const desktopAPI = {
  /** App version + normalized OS. Read once at preload time so the renderer
   *  can use it synchronously when initializing the API client. */
  appInfo,
  /** OS-preferred locale (BCP 47), passed from main via additionalArguments.
   *  Used by the renderer's LocaleAdapter as the system-preference signal. */
  systemLocale,
  /** Subscribe to OS language changes detected after boot. The renderer
   *  decides whether to act (no-op when the user has an explicit Settings
   *  choice). Returns an unsubscribe function. */
  onSystemLocaleChanged: (callback: (locale: string) => void) => {
    const handler = (_event: Electron.IpcRendererEvent, locale: string) =>
      callback(locale);
    ipcRenderer.on("locale:system-changed", handler);
    return () => {
      ipcRenderer.removeListener("locale:system-changed", handler);
    };
  },
  /** Validated runtime endpoint config, or a blocking config error. */
  runtimeConfig,
  /** Read + clear any freeze/crash breadcrumb left by a previous session, so
   *  the renderer can flush it to telemetry on boot. Returns null when there's
   *  nothing pending (the normal case). */
  getLastFreeze: (): FreezeBreadcrumb | null => {
    try {
      return ipcRenderer.sendSync("freeze:get-last") as FreezeBreadcrumb | null;
    } catch {
      return null;
    }
  },
  /** Listen for auth token delivered via deep link */
  onAuthToken: (callback: (token: string) => void) => {
    const handler = (_event: Electron.IpcRendererEvent, token: string) =>
      callback(token);
    ipcRenderer.on("auth:token", handler);
    return () => {
      ipcRenderer.removeListener("auth:token", handler);
    };
  },
  /** Listen for invitation IDs delivered via deep link */
  onInviteOpen: (callback: (invitationId: string) => void) => {
    const handler = (_event: Electron.IpcRendererEvent, invitationId: string) =>
      callback(invitationId);
    ipcRenderer.on("invite:open", handler);
    return () => {
      ipcRenderer.removeListener("invite:open", handler);
    };
  },
  /** Open a URL in the default browser */
  openExternal: (url: string) => ipcRenderer.invoke("shell:openExternal", url),
  /** Download a file by URL through Electron's native download system.
   *  Shows a save dialog and saves to disk. Unlike openExternal, this
   *  avoids browser rendering of HTML files on Linux.
   *  On non-desktop platforms this property is undefined. */
  downloadURL: (url: string) => ipcRenderer.invoke("file:download-url", url),
  /** Toggle immersive mode — hide macOS traffic lights for full-screen modals */
  setImmersiveMode: (immersive: boolean) =>
    ipcRenderer.invoke("window:setImmersive", immersive),
  /**
   * Show a native OS notification for a new inbox item. Fired from the
   * renderer only when the app is unfocused — in-focus feedback is the
   * inbox sidebar's unread styling. `slug`, `itemId`, and `issueKey` are
   * all round-tripped on click: slug pins routing to the source workspace
   * (the user may switch workspaces before clicking the banner), itemId
   * lets the renderer mark the row read, issueKey maps to the inbox URL
   * param.
   */
  showNotification: (payload: {
    slug: string;
    itemId: string;
    issueKey: string;
    title: string;
    body: string;
  }) => ipcRenderer.send("notification:show", payload),
  /**
   * Update the OS dock / taskbar unread badge. Pass 0 to clear. Values
   * above 99 render as "99+" (capping is handled in the main process).
   */
  setUnreadBadge: (count: number) =>
    ipcRenderer.send("badge:set", Math.max(0, Math.floor(count))),
  /**
   * Subscribe to "open this inbox row" requests sent by the main process
   * when the user clicks an OS notification banner. Returns an unsubscribe
   * function. The payload echoes the `slug`, `itemId`, and `issueKey` that
   * were passed to `showNotification`.
   */
  onInboxOpen: (
    callback: (payload: {
      slug: string;
      itemId: string;
      issueKey: string;
    }) => void,
  ) => {
    const handler = (
      _event: Electron.IpcRendererEvent,
      payload: { slug: string; itemId: string; issueKey: string },
    ) => callback(payload);
    ipcRenderer.on("inbox:open", handler);
    return () => {
      ipcRenderer.removeListener("inbox:open", handler);
    };
  },
  /** Listen for native macOS back/forward swipe gestures. */
  onNavigationGesture: (callback: (gesture: NavigationGesture) => void) => {
    const handler = (_event: Electron.IpcRendererEvent, gesture: unknown) => {
      if (isNavigationGesture(gesture)) callback(gesture);
    };
    ipcRenderer.on(NAVIGATION_GESTURE_CHANNEL, handler);
    return () => {
      ipcRenderer.removeListener(NAVIGATION_GESTURE_CHANNEL, handler);
    };
  },
  /** Report the renderer's memory-router path for recovery diagnostics. */
  setRendererRouteContext: (context: RendererRouteContextInput) =>
    ipcRenderer.send(RENDERER_ROUTE_CONTEXT_CHANNEL, context),
  /** Open the OS folder picker and return the chosen absolute path. */
  pickDirectory: (defaultPath?: string) =>
    ipcRenderer.invoke("local-directory:pick", defaultPath),
  /** Validate that a path is an existing readable+writable directory. */
  validateLocalDirectory: (path: string) =>
    ipcRenderer.invoke("local-directory:validate", path),
  /** Listen for Cmd/Ctrl+W tab-close requests from the main process.
   *  The renderer should close the active tab; if it was the last tab,
   *  call `closeWindow()` to dismiss the window. Returns an unsubscribe fn. */
  onCloseActiveTab: (callback: () => void) => {
    const handler = () => callback();
    ipcRenderer.on("tab:close-active", handler);
    return () => {
      ipcRenderer.removeListener("tab:close-active", handler);
    };
  },
  /** Listen for Cmd/Ctrl+, requests to open Settings. Only the main window
   *  subscribes — main delivers the chord there even when it was pressed in
   *  an issue window, because Settings is a tab. Returns an unsubscribe fn. */
  onOpenSettings: (callback: () => void) =>
    subscribeToMainRendererChannel("settings:open", () => callback()),
  /** Ask the main process to close the window (used after closing the last tab). */
  closeWindow: () => ipcRenderer.send("window:close"),
};

interface DaemonStatus {
  state:
    | "running"
    | "stopped"
    | "starting"
    | "stopping"
    | "installing_cli"
    | "cli_not_found"
    | "auth_expired";
  pid?: number;
  uptime?: string;
  daemonId?: string;
  deviceName?: string;
  agents?: string[];
  workspaceCount?: number;
  profile?: string;
  serverUrl?: string;
}

type DaemonReauthResult =
  | { ok: true }
  | { ok: false; reason: "session_invalid" }
  | { ok: false; reason: "transient"; message: string };

const daemonAPI = {
  start: (): Promise<{ success: boolean; error?: string }> =>
    ipcRenderer.invoke("daemon:start"),
  stop: (): Promise<{ success: boolean; error?: string }> =>
    ipcRenderer.invoke("daemon:stop"),
  restart: (): Promise<{ success: boolean; error?: string }> =>
    ipcRenderer.invoke("daemon:restart"),
  getStatus: (): Promise<DaemonStatus> =>
    ipcRenderer.invoke("daemon:get-status"),
  getHostName: (): Promise<string> =>
    ipcRenderer.invoke("daemon:get-host-name"),
  onStatusChange: (callback: (status: DaemonStatus) => void) => {
    const handler = (_: unknown, status: DaemonStatus) => callback(status);
    ipcRenderer.on("daemon:status", handler);
    return () => ipcRenderer.removeListener("daemon:status", handler);
  },
  setTargetApiUrl: (url: string): Promise<void> =>
    ipcRenderer.invoke("daemon:set-target-api-url", url),
  syncToken: (token: string, userId: string): Promise<void> =>
    ipcRenderer.invoke("daemon:sync-token", token, userId),
  clearToken: (): Promise<void> =>
    ipcRenderer.invoke("daemon:clear-token"),
  reauthenticate: (
    token: string,
    userId: string,
  ): Promise<DaemonReauthResult> =>
    ipcRenderer.invoke("daemon:reauthenticate", token, userId),
  isCliInstalled: (): Promise<boolean> =>
    ipcRenderer.invoke("daemon:is-cli-installed"),
  getPrefs: (): Promise<{ autoStart: boolean; autoStop: boolean }> =>
    ipcRenderer.invoke("daemon:get-prefs"),
  setPrefs: (prefs: Partial<{ autoStart: boolean; autoStop: boolean }>): Promise<{ autoStart: boolean; autoStop: boolean }> =>
    ipcRenderer.invoke("daemon:set-prefs", prefs),
  autoStart: (): Promise<void> =>
    ipcRenderer.invoke("daemon:auto-start"),
  retryInstall: (): Promise<void> =>
    ipcRenderer.invoke("daemon:retry-install"),
  startLogStream: () => ipcRenderer.send("daemon:start-log-stream"),
  stopLogStream: () => ipcRenderer.send("daemon:stop-log-stream"),
  onLogLine: (callback: (line: string) => void) => {
    const handler = (_: unknown, line: string) => callback(line);
    ipcRenderer.on("daemon:log-line", handler);
    return () => ipcRenderer.removeListener("daemon:log-line", handler);
  },
  openLogFile: (): Promise<{ success: boolean; error?: string }> =>
    ipcRenderer.invoke("daemon:open-log-file"),
};

const updaterAPI = {
  onUpdateAvailable: (callback: (info: { version: string; releaseNotes?: string }) => void) => {
    const handler = (_: unknown, info: { version: string; releaseNotes?: string }) => callback(info);
    ipcRenderer.on("updater:update-available", handler);
    return () => ipcRenderer.removeListener("updater:update-available", handler);
  },
  onDownloadProgress: (callback: (progress: { percent: number }) => void) => {
    const handler = (_: unknown, progress: { percent: number }) => callback(progress);
    ipcRenderer.on("updater:download-progress", handler);
    return () => ipcRenderer.removeListener("updater:download-progress", handler);
  },
  onUpdateDownloaded: (
    callback: (info: { version: string; releaseNotes?: string }) => void,
  ) => {
    const handler = (_: unknown, info: { version: string; releaseNotes?: string }) =>
      callback(info);
    ipcRenderer.on("updater:update-downloaded", handler);
    return () => ipcRenderer.removeListener("updater:update-downloaded", handler);
  },
  downloadUpdate: () => ipcRenderer.invoke("updater:download"),
  installUpdate: () => ipcRenderer.invoke("updater:install"),
  checkForUpdates: (): Promise<
    | { ok: true; currentVersion: string; latestVersion: string; available: boolean }
    | { ok: false; error: string }
  > => ipcRenderer.invoke("updater:check"),
};

// PR 1 (Stage C): serverAPI exposes the install-brew IPC + a status
// subscriber. The renderer can paint the "needs PostgreSQL" banner
// without polling.
const serverAPI = {
  getInstallInstructions: () =>
    ipcRenderer.invoke("server:get-install-instructions"),
  copyText: (text: string) => ipcRenderer.invoke("server:copy-text", text),
  openExternal: (url: string) =>
    ipcRenderer.invoke("server:open-external", url),
  getStatusWithHint: () => ipcRenderer.invoke("server:get-status-with-hint"),
  onStatusChanged: (callback: (status: unknown) => void) => {
    const handler = (_: unknown, status: unknown) => callback(status);
    ipcRenderer.on("server:status-changed", handler);
    return () => ipcRenderer.removeListener("server:status-changed", handler);
  },
  // PR 2 (Stage D-1): install instructions for the bundled native PG
  // path. The renderer shows a 4-step modal when the user picks
  // "Use bundled PG" from the banner.
  getPgInstallInstructions: () =>
    ipcRenderer.invoke("server:get-pg-install-instructions"),
  checkPgInstalled: (): Promise<boolean> =>
    ipcRenderer.invoke("pg:check-installed"),
  // PR 3 (Stage D-2): one-time migration prompt for users upgrading
  // from a v0.2.x Docker pgdata install. The renderer asks on mount
  // whether to offer migration, then triggers the flow on user
  // confirmation.
  shouldOfferMigration: (): Promise<{ hasDocker: boolean }> =>
    ipcRenderer.invoke("pg:get-migration-status"),
  runMigration: (
    confirmed: boolean,
  ): Promise<
    | {
        status: "migrated";
        dumpPath: string;
        dockerRows: number;
        nativeRows: number;
        durationMs: number;
      }
    | { status: "skipped"; reason: string }
  > => ipcRenderer.invoke("server:run-migration", confirmed),
};

// 0.3.8 Labs: experimental services surface (Pythia + Claude Science).
// The renderer calls experimentalAPI.startX/getX only when the user has
// enabled the matching Labs flag. When the flag is off the renderer
// code path never fires the call, so the underlying subprocess stays
// off — this matches the per-flag opt-in contract documented in
// server/internal/experimental/catalog.go:1-21.
//
// 0.3.20: keep the legacy .pythia surface (its channels are wired
// to dedicated managers with extra behaviour like rate limiting on
// pythia:proxy). The new generic invoke() helper routes through
// `experimental:<flagKey>:<verb>` so any catalog-only flag (e.g.
// code_canvas) gets IPC without an edit here.
const experimentalAPI = {
  pythia: {
    ensureUp: (): Promise<string> =>
      ipcRenderer.invoke("pythia:ensure-up"),
    stop: (): Promise<void> => ipcRenderer.invoke("pythia:stop"),
    getStatus: (): Promise<string> =>
      ipcRenderer.invoke("pythia:get-status"),
    getURL: (): Promise<string | null> =>
      ipcRenderer.invoke("pythia:get-url"),
    // 0.3.18 Phase 3: proxy POST /whatif /chat /predict from the
    // renderer to the loopback Pythia subprocess. The main process
    // owns allowlist + rate limit; the renderer only sees ok + body.
    proxy: (
      req: {
        path: "/whatif" | "/chat" | "/predict";
        method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
        body?: unknown;
        timeoutMs?: number;
      },
    ): Promise<{ ok: boolean; status: number; body: unknown }> =>
      ipcRenderer.invoke("pythia:proxy", req),
  },
  // 0.3.20: generic Labs invoke. Routes to the
  // `experimental:<flagKey>:<verb>` channel namespace managed by
  // apps/desktop/src/main/experimental/ipc-dispatcher.ts. Use this
  // for catalog-only flags where no legacy surface exists.
  invoke: (
    flagKey: string,
    verb: "get-status" | "get-url" | "ensure-up" | "stop",
  ): Promise<unknown> =>
    ipcRenderer.invoke(`experimental:${flagKey}:${verb}`),
  // 0.3.18 Labs safety net: list broken flags on Labs open, clear an
  // entry when the user clicks Restore. Mirrors the JSON wire shape
  // in server/internal/experimental/safety.go.
  safety: {
    list: (): Promise<
      Array<{
        flag_key: string;
        reason: "panic" | "5xx_burst" | "init_timeout";
        broken_at: string;
        context?: string;
        stack_hint?: string;
      }>
    > => ipcRenderer.invoke("experimental-safety:list"),
    clear: (
      flagKey: string,
    ): Promise<{ ok: boolean; error?: string }> =>
      ipcRenderer.invoke("experimental-safety:clear", flagKey),
  },
};

if (process.contextIsolated) {
  contextBridge.exposeInMainWorld("electron", electronAPI);
  contextBridge.exposeInMainWorld("desktopAPI", desktopAPI);
  contextBridge.exposeInMainWorld("daemonAPI", daemonAPI);
  contextBridge.exposeInMainWorld("updater", updaterAPI);
  contextBridge.exposeInMainWorld("serverAPI", serverAPI);
  contextBridge.exposeInMainWorld("experimentalAPI", experimentalAPI);
} else {
  // @ts-expect-error - fallback for non-isolated context
  window.electron = electronAPI;
  // @ts-expect-error - fallback for non-isolated context
  window.desktopAPI = desktopAPI;
  // @ts-expect-error - fallback for non-isolated context
  window.daemonAPI = daemonAPI;
  // @ts-expect-error - fallback for non-isolated context
  window.updater = updaterAPI;
  // @ts-expect-error - fallback for non-isolated context
  window.serverAPI = serverAPI;
  // @ts-expect-error - fallback for non-isolated context
  window.experimentalAPI = experimentalAPI;
}
