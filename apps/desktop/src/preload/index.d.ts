import { ElectronAPI } from "@electron-toolkit/preload";
import type { RuntimeConfigResult } from "../shared/runtime-config";
import type { NavigationGesture } from "../shared/navigation-gestures";
import type { RendererRouteContextInput } from "../shared/renderer-route-context";
import type { FreezeBreadcrumb } from "../shared/freeze-breadcrumb";

interface DesktopAPI {
  /** App version + normalized OS, captured synchronously at preload time. */
  appInfo: {
    version: string;
    os: "macos" | "windows" | "linux" | "unknown";
  };
  /** OS-preferred locale (BCP 47) injected by main via additionalArguments. */
  systemLocale: string;
  /** Subscribe to OS language changes detected after boot. Returns an unsubscribe function. */
  onSystemLocaleChanged: (callback: (locale: string) => void) => () => void;
  /** Validated runtime endpoint config, or a blocking config error. */
  runtimeConfig: RuntimeConfigResult;
  /** Read + clear any freeze/crash breadcrumb from a previous session, so the
   *  renderer can flush it to telemetry on boot. Null when nothing's pending. */
  getLastFreeze: () => FreezeBreadcrumb | null;
  /** Listen for auth token delivered via deep link. Returns an unsubscribe function. */
  onAuthToken: (callback: (token: string) => void) => () => void;
  /** Listen for invitation IDs delivered via deep link. Returns an unsubscribe function. */
  onInviteOpen: (callback: (invitationId: string) => void) => () => void;
  /** Open a URL in the default browser. */
  openExternal: (url: string) => Promise<void>;
  /** Download a file by URL through Electron's native download system.
   *  Shows a native save dialog. On non-desktop platforms this is undefined. */
  downloadURL: (url: string) => Promise<void>;
  /** Hide macOS traffic lights for full-screen modals; restore when false. */
  setImmersiveMode: (immersive: boolean) => Promise<void>;
  /** Show a native OS notification for a new inbox item. */
  showNotification: (payload: {
    slug: string;
    itemId: string;
    issueKey: string;
    title: string;
    body: string;
  }) => void;
  /** Update the OS dock / taskbar unread badge. Pass 0 to clear. */
  setUnreadBadge: (count: number) => void;
  /** Listen for "open inbox row" requests from notification clicks. Returns an unsubscribe function. */
  onInboxOpen: (
    callback: (payload: {
      slug: string;
      itemId: string;
      issueKey: string;
    }) => void,
  ) => () => void;
  /** Listen for native macOS back/forward swipe gestures. Returns an unsubscribe function. */
  onNavigationGesture: (callback: (gesture: NavigationGesture) => void) => () => void;
  /** Report the renderer's memory-router path for recovery diagnostics. */
  setRendererRouteContext: (context: RendererRouteContextInput) => void;
  /** Open the OS folder picker and return the chosen absolute path.
   *  Used by the Project settings "Add local directory" flow. */
  pickDirectory: (
    defaultPath?: string,
  ) => Promise<{
    ok: boolean;
    path?: string;
    basename?: string;
    reason?: "cancelled" | "no_window" | "error";
    error?: string;
  }>;
  /** Validate that a path is an existing readable+writable directory.
   *  Mirrors the daemon's runtime check so the user sees errors before submit. */
  validateLocalDirectory: (
    path: string,
  ) => Promise<{
    ok: boolean;
    reason?:
      | "not_absolute"
      | "not_found"
      | "not_a_directory"
      | "not_readable"
      | "not_writable"
      | "error";
    error?: string;
  }>;
  /** Listen for Cmd/Ctrl+W tab-close requests from the main process.
   *  Returns an unsubscribe function. */
  onCloseActiveTab: (callback: () => void) => () => void;
  /** Listen for Cmd/Ctrl+, requests to open Settings, delivered to the main
   *  window whichever window had focus. Returns an unsubscribe function. */
  onOpenSettings: (callback: () => void) => () => void;
  /** Ask the main process to close the window. */
  closeWindow: () => void;
}

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

interface DaemonPrefs {
  autoStart: boolean;
  autoStop: boolean;
}

type DaemonReauthResult =
  | { ok: true }
  | { ok: false; reason: "session_invalid" }
  | { ok: false; reason: "transient"; message: string };

interface DaemonAPI {
  start: () => Promise<{ success: boolean; error?: string }>;
  stop: () => Promise<{ success: boolean; error?: string }>;
  restart: () => Promise<{ success: boolean; error?: string }>;
  getStatus: () => Promise<DaemonStatus>;
  getHostName: () => Promise<string>;
  onStatusChange: (callback: (status: DaemonStatus) => void) => () => void;
  setTargetApiUrl: (url: string) => Promise<void>;
  syncToken: (token: string, userId: string) => Promise<void>;
  clearToken: () => Promise<void>;
  reauthenticate: (
    token: string,
    userId: string,
  ) => Promise<DaemonReauthResult>;
  isCliInstalled: () => Promise<boolean>;
  getPrefs: () => Promise<DaemonPrefs>;
  setPrefs: (prefs: Partial<DaemonPrefs>) => Promise<DaemonPrefs>;
  autoStart: () => Promise<void>;
  retryInstall: () => Promise<void>;
  startLogStream: () => void;
  stopLogStream: () => void;
  onLogLine: (callback: (line: string) => void) => () => void;
  openLogFile: () => Promise<{ success: boolean; error?: string }>;
}

interface UpdaterAPI {
  onUpdateAvailable: (callback: (info: { version: string; releaseNotes?: string }) => void) => () => void;
  onDownloadProgress: (callback: (progress: { percent: number }) => void) => () => void;
  onUpdateDownloaded: (
    callback: (info: { version: string; releaseNotes?: string }) => void,
  ) => () => void;
  downloadUpdate: () => Promise<void>;
  installUpdate: () => Promise<void>;
  checkForUpdates: () => Promise<
    | { ok: true; currentVersion: string; latestVersion: string; available: boolean }
    | { ok: false; error: string }
  >;
}

// PR 1 (Stage C): serverAPI is a thin facade over a handful of IPC
// channels added to the main process. The renderer uses it to (1) pull
// install instructions once on mount, (2) copy the brew command, (3)
// open brew.sh in the user's browser, and (4) subscribe to live
// status changes (used by the install-brew banner).
interface PgInstallInstructions {
  pgAppUrl: string;
  requiredVersion: string;
  pgHome: string;
  pgBin: string;
  manualSteps: string[];
}

interface ServerAPI {
  getInstallInstructions: () => Promise<{
    brewCommand: string;
    brewUrl: string;
    pgAppUrl: string;
  }>;
  copyText: (text: string) => Promise<void>;
  openExternal: (url: string) => Promise<void>;
  getStatusWithHint: () => Promise<unknown>;
  onStatusChanged: (callback: (status: unknown) => void) => () => void;
  // PR 2 (Stage D-1): the renderer's "Use bundled PG" button pulls
  // the manual install procedure and Postgres.app URL. checkPgInstalled
  // is queried on mount to decide whether the user already dropped
  // the binary in (in which case we hide the prompt and let the
  // backend try the native path silently).
  getPgInstallInstructions: () => Promise<PgInstallInstructions>;
  checkPgInstalled: () => Promise<boolean>;
  // PR 3 (Stage D-2): one-time migration prompt for users upgrading
  // from a v0.2.x Docker pgdata install. The banner calls
  // shouldOfferMigration on mount; when the user clicks "Migrate" it
  // calls runMigration(true) which kicks off pg_dump | pg_restore in
  // the main process. Both are queried through `serverAPI` because
  // they sit on the existing server-status IPC channel, not the
  // generic pg:check-installed path.
  shouldOfferMigration: () => Promise<{ hasDocker: boolean }>;
  runMigration: (
    confirmed: boolean,
  ) => Promise<
    | {
        status: "migrated";
        dumpPath: string;
        dockerRows: number;
        nativeRows: number;
        durationMs: number;
      }
    | { status: "skipped"; reason: string }
  >;
}

declare global {
  interface Window {
    electron: ElectronAPI;
    desktopAPI: DesktopAPI;
    daemonAPI: DaemonAPI;
    updater: UpdaterAPI;
    serverAPI: ServerAPI;
    // 0.3.8 Labs: experimental services. Each submodule returns JSON
    // strings (status) or URL strings (null when not ready). The view
    // pages ClaudeScienceView / PythiaView own the polling cycle.
    experimentalAPI: ExperimentalAPI;
  }
}

// ExperimentalAPI is exposed as `window.experimentalAPI` for any
// renderer code that needs the loopback URL of an experimental
// service. Each submodule mirrors the IPC channels registered in
// claude-science-manager.ts and pythia-manager.ts.
interface ExperimentalAPI {
  pythia: {
    ensureUp(): Promise<string>;
    stop(): Promise<void>;
    getStatus(): Promise<string>;
    getURL(): Promise<string | null>;
    // 0.3.18 Phase 3: forward /whatif /chat /predict to the loopback
    // Pythia subprocess via the main process. Returns the raw
    // response shape; renderer handles ok=false as a friendly error.
    proxy(req: {
      path: "/whatif" | "/chat" | "/predict";
      method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
      body?: unknown;
      timeoutMs?: number;
    }): Promise<{ ok: boolean; status: number; body: unknown }>;
  };
  // 0.3.20: generic Labs invoke. Routes to the
  // `experimental:<flagKey>:<verb>` channel namespace. Use for
  // catalog-only flags without a dedicated manager surface.
  invoke(
    flagKey: string,
    verb: "get-status" | "get-url" | "ensure-up" | "stop",
  ): Promise<unknown>;
}

export {};
