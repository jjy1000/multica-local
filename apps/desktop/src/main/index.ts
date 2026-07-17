import { app, BrowserWindow, clipboard, dialog, ipcMain, nativeImage, Notification, screen, shell } from "electron";
import { homedir } from "os";
import { join } from "path";
import { electronApp, optimizer, is } from "@electron-toolkit/utils";
import fixPath from "fix-path";
import { setupAutoUpdater } from "./updater";
import { setupServerManager, ensureServerUp, getServerStatus } from "./server-manager";
import { setupDaemonManager, stopDaemon } from "./daemon-manager";
import { stopServerManager } from "./server-manager";
import {
  setupPythiaIPC,
  setupPythiaProxyIPC,
  stopPythiaManager,
} from "./pythia-manager";
// 0.3.22: claude_science-manager is gone — claude_science_lab is an
// inline flag with no subprocess manager. IPC handlers are auto-
// registered by setupExperimentalIPC below via the generic
// experimental:<flagKey>:<verb> namespace.
// 0.3.19 P2: generic Labs IPC dispatcher. Registers
// `experimental:<flagKey>:<verb>` channels per catalog entry so
// new flags do not need an edit in this file. The legacy
// per-manager IPC (pythia:* / claude-science:*) stays for the
// 0.3.18 wire contract — both surfaces coexist until the renderer
// is updated to call the generic channels (PR 3).
import { setupExperimentalIPC, stopAllExperimentalManagers, loadFlagDescriptors } from "./experimental/ipc-dispatcher";
import { setupLocalDirectory } from "./local-directory";
import { openExternalSafely, downloadURLSafely } from "./external-url";
import {
  loadBrokenFlagEntries,
  clearBroken,
} from "./experimental-safety";
import { installContextMenu } from "./context-menu";
import { handleAppShortcut } from "./keyboard-shortcuts";
import { installNavigationGestures } from "./navigation-gestures";
import { getAppVersion } from "./app-version";
import { loadRuntimeConfig } from "./runtime-config-loader";
import type { RuntimeConfigResult } from "../shared/runtime-config";
import {
  PG_APP_URL,
  PG_APP_REQUIRED_VERSION,
  resolveNativePaths,
} from "./pg-bootstrap";
import {
  RENDERER_ROUTE_CONTEXT_CHANNEL,
  sanitizeRendererRouteContext,
  type RendererRouteContext,
} from "../shared/renderer-route-context";
import {
  createElectronReloadPrompt,
  installRendererRecoveryHandlers,
  type RendererRecoveryWindow,
} from "./renderer-recovery";
import {
  writeFreezeBreadcrumb,
  readAndClearFreezeBreadcrumb,
  clearFreezeBreadcrumb,
} from "./freeze-breadcrumb";

// Bundled icon used for dock/taskbar branding. macOS/Windows production
// builds let the OS pick up the icon from the .app bundle / .exe resources,
// but Linux production needs an explicit BrowserWindow `icon` — AppImage
// direct-launch doesn't register the .desktop entry, so GNOME has no path
// from the running window to the hicolor icon and falls back to the
// theme default. Consumed in createWindow() (all platforms in dev, Linux
// in prod) and the macOS dev dock branch.
//
// `asarUnpack: resources/**` in electron-builder.yml extracts the icon to
// `app.asar.unpacked/`, but `__dirname` resolves into `app.asar/`. The
// Linux native window-icon code path expects a real filesystem path
// (unlike Electron's nativeImage loader which transparently reads from
// asar), so swap the segment — same pattern as bundledCliPath() in
// daemon-manager.ts. In dev `__dirname` has no `app.asar`, so the replace
// is a no-op.
const BUNDLED_ICON_PATH = join(__dirname, "../../resources/icon.png").replace(
  "app.asar",
  "app.asar.unpacked",
);

// macOS/Linux GUI launches inherit a minimal PATH from launchd that omits
// the user's shell config (~/.zshrc, Homebrew, nvm, ~/.local/bin, etc.).
// Run the user's login shell once to recover the real PATH so the bundled
// multica CLI can find agent binaries like claude/codex/opencode. Must run
// before any child_process.spawn / execFile call in the main process —
// ES module imports are hoisted, so this block executes before createWindow
// or any daemon-manager spawn.
if (process.platform !== "win32") {
  fixPath();
  // Fallback: prepend common install locations in case fix-path came up
  // short (broken shell rc, non-interactive $SHELL, missing entries). Safe
  // to duplicate — PATH lookups short-circuit on first match.
  const fallbackPaths = [
    "/opt/homebrew/bin",
    "/usr/local/bin",
    join(homedir(), ".local/bin"),
  ];
  process.env.PATH = `${fallbackPaths.join(":")}:${process.env.PATH ?? ""}`;
}

const PROTOCOL = "multica";

// Where the main process parks a freeze/crash breadcrumb until the next
// renderer boot flushes it to telemetry. Lives in userData so it survives a
// force-quit. Resolved lazily — app.getPath is only valid after `ready`.
function freezeBreadcrumbPath(): string {
  return join(app.getPath("userData"), "last-client-failure.json");
}

let mainWindow: BrowserWindow | null = null;
let latestRendererRouteContext: RendererRouteContext | null = null;
let runtimeConfigResult: RuntimeConfigResult = {
  ok: false,
  error: { message: "Runtime config has not loaded yet" },
};

// --- Deep link helpers ---------------------------------------------------

function handleDeepLink(url: string): void {
  try {
    const parsed = new URL(url);
    if (parsed.protocol !== `${PROTOCOL}:`) return;

    // multica://auth/callback?token=<jwt>
    if (parsed.hostname === "auth" && parsed.pathname === "/callback") {
      const token = parsed.searchParams.get("token");
      if (token && mainWindow) {
        mainWindow.webContents.send("auth:token", token);
      }
      return;
    }

    // multica://invite/<invitationId>
    // Dispatched from the web invite page when the user chooses "Open in
    // desktop app". The renderer opens the invite overlay — no tab, no
    // route persistence, so deep-linking the same invite twice stays safe.
    if (parsed.hostname === "invite") {
      const id = parsed.pathname.replace(/^\//, "");
      if (id && mainWindow) {
        mainWindow.webContents.send("invite:open", decodeURIComponent(id));
      }
      return;
    }
  } catch {
    // Ignore malformed URLs
  }
}

// --- Window creation -----------------------------------------------------

// Tracks the OS-preferred language as last seen by the running process.
// Updated on each window-focus check so we can emit a `locale:system-changed`
// event to the renderer when the user changes their OS language without
// quitting the app — without restart, app.getPreferredSystemLanguages()
// would still report the boot value forever.
let lastKnownSystemLocale = "en";

function getSystemLocale(): string {
  return app.getPreferredSystemLanguages()[0] ?? "en";
}

// Returns true when the given rectangle intersects at least one connected
// display's workArea by at least one pixel on each axis. The check is
// deliberately permissive (1-px overlap on both axes) so a window that
// straddles the boundary between two displays stays put — only windows
// entirely outside every display are pulled back. The y=2134 bug we saw on
// 2026-07-14 shipped a window 2px from the left edge and 1334px below the
// 800-tall workArea; both axes missed, so the guard correctly fired.
function rectIsOnscreen(bounds: { x: number; y: number; width: number; height: number }): boolean {
  for (const display of screen.getAllDisplays()) {
    const wa = display.workArea;
    const xOverlap = Math.max(0, Math.min(bounds.x + bounds.width, wa.x + wa.width) - Math.max(bounds.x, wa.x));
    const yOverlap = Math.max(0, Math.min(bounds.y + bounds.height, wa.y + wa.height) - Math.max(bounds.y, wa.y));
    if (xOverlap > 0 && yOverlap > 0) return true;
  }
  return false;
}

// Re-centers the window on the display nearest to its current (offscreen)
// position so users with disconnected external monitors do not end up with
// an invisible window. Without this guard, Electron's persisted Local State
// restores stale bounds that no longer intersect any active workArea.
function ensureWindowOnscreen(window: BrowserWindow): void {
  if (!window || window.isDestroyed()) return;
  const bounds = window.getBounds();
  const onscreen = rectIsOnscreen(bounds);
  if (onscreen) return;
  const nearest = screen.getDisplayNearestPoint({ x: bounds.x, y: bounds.y });
  const wa = nearest.workArea;
  const width = Math.min(bounds.width, wa.width);
  const height = Math.min(bounds.height, wa.height);
  window.setBounds({
    x: Math.round(wa.x + (wa.width - width) / 2),
    y: Math.round(wa.y + (wa.height - height) / 2),
    width,
    height,
  });
}

function createWindow(): void {
  // Pass the OS-preferred language to the renderer via additionalArguments
  // instead of a sync IPC call. process.argv is available to the preload
  // script before the first network request, so the renderer's i18next
  // instance can initialize with the right locale on the very first paint.
  const systemLocale = getSystemLocale();
  lastKnownSystemLocale = systemLocale;

  mainWindow = new BrowserWindow({
    width: 1280,
    height: 800,
    minWidth: 900,
    minHeight: 600,
    titleBarStyle: "hiddenInset",
    trafficLightPosition: { x: 16, y: 17 },
    show: false,
    autoHideMenuBar: true,
    // Windows/Linux pick up the window/taskbar icon from this option.
    // On macOS it's ignored (dock comes from app.dock.setIcon below).
    // Linux production needs this explicitly because AppImage direct-launch
    // does not install a .desktop entry, so the WM has no other path to
    // the bundled icon; without it Ubuntu falls back to the theme default.
    ...(is.dev || process.platform === "linux"
      ? { icon: BUNDLED_ICON_PATH }
      : {}),
    webPreferences: {
      preload: join(__dirname, "../preload/index.js"),
      sandbox: false,
      webSecurity: false,
      // Required for the Chromium PDF viewer (PDFium) to activate inside
      // iframes — used by the attachment preview modal for application/pdf
      // files. Default is false in Electron; without it <iframe src=*.pdf>
      // renders blank.
      //
      // Security trade-off, accepted intentionally:
      //   1. This window already runs with `webSecurity: false` + `sandbox: false`,
      //      so `plugins: true` does NOT meaningfully widen the renderer's
      //      attack surface beyond what is already accepted.
      //   2. The only PDFs that reach an iframe here are signed CloudFront URLs
      //      we ourselves issued (see useDownloadAttachment); user-supplied URLs
      //      are routed through `setWindowOpenHandler` → `openExternalSafely` and
      //      cannot land in this renderer.
      //   3. Chromium's PDFium plugin is itself sandboxed inside its own process
      //      and only handles the `application/pdf` MIME — it does not expose
      //      Flash, Java, or other historical plugin surfaces.
      //
      // If we ever tighten `webSecurity` / `sandbox`, revisit this by hosting
      // the PDF viewer in a dedicated BrowserView with `plugins: true` scoped
      // to that view, keeping the main renderer plugin-free.
      plugins: true,
      // Required to render Electron's <webview> tag in the renderer. The
      // experimental Claude Science view mounts a real Chromium instance
      // (separate process, partition-isolated cookies) instead of an
      // iframe. Without this flag Chromium strips <webview> from the DOM
      // silently and the renderer falls back to whatever HTML Chromium
      // emits for an unknown element — which is invisible to the user.
      webviewTag: true,
      additionalArguments: [`--multica-locale=${systemLocale}`],
    },
  });
  const window = mainWindow;
  latestRendererRouteContext = null;

  // Off-screen clamp runs immediately after construction. Electron 39 on
  // macOS may restore stale bounds from the system window-state cache
  // (NOT our Local State — that file does not exist for this app) before
  // ready-to-show fires, so the only safe window to apply the clamp is
  // synchronously after the BrowserWindow returns. ready-to-show's clamp
  // remains as a safety net for bounds restored during the load cycle.
  ensureWindowOnscreen(window);

  window.on("closed", () => {
    if (mainWindow === window) {
      mainWindow = null;
      latestRendererRouteContext = null;
    }
  });

  // Strip Origin header from WebSocket upgrade requests so the server's
  // origin whitelist doesn't reject connections from localhost dev origins.
  window.webContents.session.webRequest.onBeforeSendHeaders(
    { urls: ["wss://*/*", "ws://*/*"] },
    (details, callback) => {
      delete details.requestHeaders["Origin"];
      callback({ requestHeaders: details.requestHeaders });
    },
  );

  window.on("ready-to-show", () => {
    // Off-screen guard runs before show(): if the bounds restored from
    // Local State fall outside every connected display (e.g. the user
    // disconnected an external monitor since last close), re-center on
    // the nearest display. Electron persists bounds via Local State,
    // not our code, so this is the only place we can intercept.
    ensureWindowOnscreen(window);
    window.show();
  });

  // Re-check on every move/resize so a window dragged onto a screen that
  // is then disconnected does not get persisted offscreen and break the
  // next launch. The display-listener approach (below) catches the same
  // case without polling; this handler stays as a safety net for OS-level
  // reconfiguration that does not fire `display-removed`.
  window.on("move", () => ensureWindowOnscreen(window));
  window.on("resize", () => ensureWindowOnscreen(window));

  // Detect OS language changes while the app is running. Electron has no
  // dedicated event for this on any platform, so we poll on focus regain —
  // catches the common case where users switch System Settings → Language
  // and bring the app back. The renderer decides whether to act (it ignores
  // the signal when the user has an explicit Settings choice).
  window.on("focus", () => {
    const current = getSystemLocale();
    if (current === lastKnownSystemLocale) return;
    lastKnownSystemLocale = current;
    window.webContents.send("locale:system-changed", current);
  });

  window.webContents.setWindowOpenHandler((details) => {
    openExternalSafely(details.url);
    return { action: "deny" };
  });

  // Window-level keyboard shortcuts. Calling preventDefault here prevents
  // both the renderer keydown AND the application menu accelerator, so
  // anything we own here (reload-block, zoom, tab-close) is the sole handler
  // for that combination — no double-fire with the macOS default View menu.
  window.webContents.on("before-input-event", (event, input) => {
    const result = handleAppShortcut(input, window.webContents);
    if (result === "close-tab") {
      event.preventDefault();
      window.webContents.send("tab:close-active");
    } else if (result) {
      event.preventDefault();
    }
  });

  // Dev-mode renderer diagnostics. When the renderer crashes hard enough
  // that DevTools can't be opened (white screen with no clickable surface),
  // the only way to recover the actual JS error is to forward it from the
  // main process to the terminal running `make dev`. Without these, the
  // user sees only the daemon-manager polling noise (`Render frame was
  // disposed before WebFrameMain could be accessed`) which is a downstream
  // symptom, not the cause.
  //
  // Gated by `is.dev` to keep production stderr clean — packaged builds
  // don't have a terminal anyway, and we ship to crash-reporting separately.
  if (is.dev) {
    const log = (tag: string, ...args: unknown[]) =>
      process.stderr.write(`[renderer ${tag}] ${args.map(String).join(" ")}\n`);

    // Forward every renderer-side console.* call. The detail object also
    // carries source URL + line — included so a thrown stack trace from
    // window.onerror is traceable back to a file.
    window.webContents.on("console-message", (details) => {
      const { level, message, sourceId, lineNumber } = details;
      log(level, `${message} (${sourceId}:${lineNumber})`);
    });

    // Fires when loadURL / loadFile can't reach its target (dev server
    // not up yet, network blip, file missing). errorCode is a Chromium
    // net error number; -3 = ABORTED is normal during HMR and skipped.
    window.webContents.on(
      "did-fail-load",
      (_event, errorCode, errorDescription, validatedURL, isMainFrame) => {
        if (errorCode === -3) return;
        log(
          "did-fail-load",
          `code=${errorCode} desc=${errorDescription} url=${validatedURL} mainFrame=${isMainFrame}`,
        );
      },
    );

  }

  // When a connected display goes away (laptop unplugged from external
  // monitor, second screen powered off), any window that was on it would
  // otherwise stay at the now-orphan coordinates until the next launch —
  // and at that point Electron's persisted Local State restores the
  // offscreen bounds, repeating the y=2134-style bug we shipped on
  // 2026-07-14. Re-clamp immediately so the user keeps the window on
  // the surviving display while the app is still running.
  screen.on("display-removed", () => {
    if (mainWindow && !mainWindow.isDestroyed()) {
      ensureWindowOnscreen(mainWindow);
    }
  });

  installRendererRecoveryHandlers(window as unknown as RendererRecoveryWindow, {
    isDev: is.dev,
    showReloadPrompt: createElectronReloadPrompt((options) =>
      dialog.showMessageBox(window, options),
    ),
    getDiagnosticContext: () => ({
      windowUrl: window.webContents.getURL(),
      ...(latestRendererRouteContext
        ? { desktopRoute: latestRendererRouteContext }
        : {}),
    }),
    // Only persist in production: a true hang/crash can't report itself, so we
    // write a breadcrumb and the next renderer boot flushes it to PostHog. Dev
    // is excluded to keep field telemetry clean.
    persistBreadcrumb: is.dev
      ? undefined
      : (payload) =>
          writeFreezeBreadcrumb(freezeBreadcrumbPath(), {
            kind: payload.kind,
            context: payload.context,
            ts: Date.now(),
            version: getAppVersion(),
          }),
    clearBreadcrumb: is.dev
      ? undefined
      : () => clearFreezeBreadcrumb(freezeBreadcrumbPath()),
  });

  installContextMenu(window.webContents);
  installNavigationGestures(window);

  if (is.dev && process.env["ELECTRON_RENDERER_URL"]) {
    window.loadURL(process.env["ELECTRON_RENDERER_URL"]);
  } else {
    window.loadFile(join(__dirname, "../renderer/index.html"));
  }
}

// --- Dev / production isolation -------------------------------------------
// Give dev mode a separate app name and userData path so it gets its own
// single-instance lock file and doesn't conflict with the packaged production
// app. Must run BEFORE requestSingleInstanceLock() because the lock location
// is derived from the userData path. (Same approach VS Code uses for
// Stable / Insiders coexistence.)

// DESKTOP_APP_SUFFIX lets parallel worktrees run dev Electron side-by-side
// without fighting for the shared single-instance lock. The suffix is
// appended to the app name + userData path, so each worktree gets its own
// lock file. Default (no env var) keeps behavior unchanged — the common
// single-worktree case still lands at "Multica Canary".
const DEV_APP_NAME = process.env.DESKTOP_APP_SUFFIX
  ? `Multica Canary ${process.env.DESKTOP_APP_SUFFIX}`
  : "Multica Canary";

if (is.dev) {
  app.setName(DEV_APP_NAME);
  app.setPath("userData", join(app.getPath("appData"), DEV_APP_NAME));
} else {
  // Pin the production app name in code. Electron's Linux WM_CLASS is set
  // from app.getName() when the first BrowserWindow is realized; the
  // packaged ASAR's package.json `productName` already steers app.getName()
  // to "Multica", but anchoring it here makes WM_CLASS ↔ StartupWMClass
  // (declared in electron-builder.yml) survive a regression in
  // productName / the build pipeline. Must run before requestSingleInstanceLock().
  app.setName("Multica");
}

// --- Protocol registration -----------------------------------------------

if (process.defaultApp) {
  // In dev, register with the path to the electron binary + app path
  app.setAsDefaultProtocolClient(PROTOCOL, process.execPath, [
    app.getAppPath(),
  ]);
} else {
  app.setAsDefaultProtocolClient(PROTOCOL);
}

// --- Single instance lock ------------------------------------------------

const gotTheLock = app.requestSingleInstanceLock();

if (!gotTheLock) {
  app.quit();
} else {
  // Windows/Linux: second instance passes deep link via argv
  app.on("second-instance", (_event, argv) => {
    if (mainWindow) {
      if (mainWindow.isMinimized()) mainWindow.restore();
      mainWindow.focus();
    }

    // On Windows the deep link URL is the last argv entry
    const deepLinkUrl = argv.find((arg) => arg.startsWith(`${PROTOCOL}://`));
    if (deepLinkUrl) handleDeepLink(deepLinkUrl);
  });

  app.whenReady().then(async () => {
    const viteEnv = import.meta.env as ImportMetaEnv & {
      readonly VITE_API_URL?: string;
      readonly VITE_WS_URL?: string;
      readonly VITE_APP_URL?: string;
    };

    runtimeConfigResult = await loadRuntimeConfig({
      isDev: is.dev,
      // electron-vite exposes VITE_* on import.meta.env for the main process;
      // keep dev URL overrides on the same source the renderer used before
      // runtime config moved endpoint resolution into main/preload.
      env: {
        apiUrl: viteEnv.VITE_API_URL,
        wsUrl: viteEnv.VITE_WS_URL,
        appUrl: viteEnv.VITE_APP_URL,
      },
    });

    electronApp.setAppUserModelId(
      is.dev ? "ai.multica.desktop.dev" : "ai.multica.desktop",
    );

    // macOS: replace the default Electron dock icon with the bundled logo
    // so the Canary dev build is visually distinct from a stock Electron
    // run. `app.dock` is macOS-only — guard the call.
    if (is.dev && process.platform === "darwin" && app.dock) {
      const icon = nativeImage.createFromPath(BUNDLED_ICON_PATH);
      if (!icon.isEmpty()) app.dock.setIcon(icon);
    }

    app.on("browser-window-created", (_, window) => {
      optimizer.watchWindowShortcuts(window);
    });

    // IPC: open URL in default browser (used by renderer for Google login).
    // All scheme-allowlist enforcement lives in openExternalSafely — this
    // is the single audit point for renderer-controlled URLs reaching the
    // OS shell under the app's intentional webSecurity: false + sandbox:
    // false configuration.
    ipcMain.handle("shell:openExternal", (_event, url: string) => {
      return openExternalSafely(url);
    });

    // Renderer requests window close (e.g. Cmd+W on last tab).
    ipcMain.on("window:close", () => {
      mainWindow?.close();
    });

    ipcMain.handle("file:download-url", (_event, url: string) => {
      if (!mainWindow) {
        console.warn("[download] ignored file:download-url — mainWindow torn down");
        return;
      }
      downloadURLSafely(mainWindow, url);
    });

    // Sync IPC: app version + normalized OS for preload. Sync (not invoke) so
    // preload can attach the values to `desktopAPI.appInfo` before any renderer
    // code reads them, ensuring the very first HTTP request from the renderer
    // already carries X-Client-Version and X-Client-OS.
    ipcMain.on("app:get-info", (event) => {
      const p = process.platform;
      const os = p === "darwin" ? "macos" : p === "win32" ? "windows" : p === "linux" ? "linux" : "unknown";
      event.returnValue = { version: getAppVersion(), os };
    });

    // Sync IPC: read + clear any freeze/crash breadcrumb left by a previous
    // session. The renderer flushes it to telemetry on boot (it couldn't be
    // reported when it happened — the renderer was hung or gone). Read-and-
    // clear so a failure reports exactly once.
    ipcMain.on("freeze:get-last", (event) => {
      event.returnValue = readAndClearFreezeBreadcrumb(freezeBreadcrumbPath());
    });

    // 0.3.18 Labs safety net IPC. The renderer queries the broken
    // flag set on Labs tab open (so it can render badges + disable
    // toggles), and invokes clear on the user's "Restore" action.
    // We deliberately do NOT auto-reload after a clear — the user
    // must restart the app for the flag's code path to come back
    // online. This is by design: a flag that was just broken has
    // high prior of being broken again, and a hot-reload would just
    // re-trip the safety net mid-request.
    ipcMain.handle("experimental-safety:list", async () => {
      return await loadBrokenFlagEntries();
    });
    ipcMain.handle("experimental-safety:clear", async (_event, flagKey: string) => {
      if (typeof flagKey !== "string" || flagKey === "") {
        return { ok: false, error: "invalid flag key" };
      }
      await clearBroken(flagKey);
      return { ok: true };
    });

    // Sync IPC: preload exposes the validated runtime config before renderer
    // boot. If desktop.json exists but is invalid, renderer receives the
    // blocking error and must not silently fall back to the cloud defaults.
    ipcMain.on("runtime-config:get", (event) => {
      event.returnValue = runtimeConfigResult;
    });

    ipcMain.on(RENDERER_ROUTE_CONTEXT_CHANNEL, (event, context: unknown) => {
      if (!mainWindow || event.sender !== mainWindow.webContents) return;
      const sanitized = sanitizeRendererRouteContext(context);
      if (!sanitized) return;
      latestRendererRouteContext = sanitized;
    });

    // IPC: toggle immersive mode — hides the macOS traffic lights so full-screen
    // modals (e.g. create-workspace) can place UI in the top-left corner
    // without fighting the native window controls' hit-test.
    ipcMain.handle("window:setImmersive", (_event, immersive: boolean) => {
      if (process.platform !== "darwin") return;
      mainWindow?.setWindowButtonVisibility(!immersive);
    });

    // IPC: show a native OS notification for a new inbox item. The renderer
    // only fires this when the app is unfocused (it gates on
    // `document.hasFocus()`), so we don't fight macOS foreground suppression
    // here. Clicking the banner focuses the main window and routes to the
    // inbox item via a renderer-side listener.
    ipcMain.on(
      "notification:show",
      (
        _event,
        {
          slug,
          itemId,
          issueKey,
          title,
          body,
        }: {
          slug: string;
          itemId: string;
          issueKey: string;
          title: string;
          body: string;
        },
      ) => {
        if (!Notification.isSupported()) return;
        const notification = new Notification({ title, body });
        notification.on("click", () => {
          if (!mainWindow) return;
          if (mainWindow.isMinimized()) mainWindow.restore();
          mainWindow.show();
          mainWindow.focus();
          // Ship the full context back — the renderer pins the route to the
          // source workspace (slug), marks the row read (itemId), and uses
          // issueKey as the ?issue=<…> selector.
          mainWindow.webContents.send("inbox:open", {
            slug,
            itemId,
            issueKey,
          });
        });
        notification.show();
      },
    );

    // IPC: update the dock / taskbar unread badge. Values above 99 render as
    // "99+". macOS is the primary target (user-visible dock badge); Linux
    // Unity launchers also respect `setBadgeCount`. Windows' taskbar overlay
    // needs a pre-rendered PNG and is deferred — the OS notification + the
    // in-app inbox sidebar cover the core UX there for now.
    ipcMain.on("badge:set", (_event, rawCount: number) => {
      const count = Math.max(0, Math.floor(rawCount));
      if (process.platform === "darwin") {
        const label = count === 0 ? "" : count > 99 ? "99+" : String(count);
        app.dock?.setBadge(label);
      } else {
        app.setBadgeCount(count);
      }
    });

    createWindow();

    setupAutoUpdater(() => mainWindow);
    // server-manager must wire its IPC handlers BEFORE daemon-manager
    // so the renderer can poll `server:get-status` while the daemon is
    // still booting. The actual server bring-up runs in the background
    // here — daemon-manager will keep retrying its own register call
    // against /api/daemon/register until the server is up.
    setupServerManager(() => mainWindow);
    void (async () => {
      try {
        if (!runtimeConfigResult.ok) {
          console.error(
            "[server-manager] runtime config not available:",
            runtimeConfigResult.error,
          );
          return;
        }
        const apiUrl = runtimeConfigResult.config.apiUrl;
        if (apiUrl.includes("localhost") || apiUrl.includes("127.0.0.1")) {
          // Profile dir name derived from the API URL, sanitised for the
          // filesystem. The daemon-manager uses a similar derivation
          // ("desktop-" + sanitised URL) — server-manager mirrors it
          // but without the desktop- prefix because it shares the
          // ~/.multica/profiles/ tree with the daemon. Collision
          // between the two is harmless: each writes to different
          // sub-files (config.json vs server.log vs .env) and the
          // server's port in the URL is what both bind to.
          const sanitised = apiUrl
            .replace(/^https?:\/\//, "")
            .replace(/[^a-zA-Z0-9]+/g, "-")
            .replace(/^-+|-+$/g, "") || "local";
          const profile = sanitised;
          const result = await ensureServerUp(profile, apiUrl);
          if (result.state !== "running") {
            console.error(
              "[server-manager] failed to bring up server:",
              (result as { state: "failed"; error: string }).error,
            );
          }
        }
      } catch (err) {
        console.error("[server-manager] ensureServerUp threw:", err);
      }
    })();
    setupDaemonManager(() => mainWindow);
    // Experimental flag IPC. Brings the Pythia subprocess up on demand
    // when the pythia_oracle flag is enabled; sees no traffic when the
    // flag is off because no renderer code path calls pythia:ensure-up.
    setupPythiaIPC(() => mainWindow);
    // Phase 3 (0.3.18+): proxy POST /whatif /chat /predict from the
    // renderer to the loopback Pythia subprocess. The renderer must
    // never reach the loopback directly (Electron contextIsolation
    // + CSP), so the WhatIfPanel drives this channel instead.
    setupPythiaProxyIPC();
    // 0.3.22: Claude Lab (`claude_science_lab`) is an inline flag with
    // no subprocess manager. IPC for `claude_science_lab:*` is auto-
    // registered by setupExperimentalIPC from the catalog descriptor
    // list (see experimental/ipc-dispatcher.ts). The renderer never
    // calls a manager for an inline flag, so no per-flag setup is
    // needed here.
    // 0.3.20: merge the server catalog into the manager-factory
    // descriptor list so catalog-only edits (e.g. adding a subprocess
    // flag like code_canvas) light up IPC + proxy automatically.
    // The fetch is best-effort: a server-down boot still gets the
    // 3 hard-coded static flags. Same apiUrl as server-manager
    // uses above; no extra config plumbing.
    //
    // 0.3.21 R2 fix: await the catalog merge, then re-call
    // setupExperimentalIPC so the merged flag list registers IPC
    // handlers. ipcMain.handle is overwrite-semantic — calling
    // setupExperimentalIPC twice keeps the 3 static-flag handlers
    // working AND adds handlers for the 6 catalog-only flags that
    // were previously dropped ("no handler registered" on
    // experimental:<flagKey>:get-status).
    await loadFlagDescriptors(async () => {
      const apiUrl = runtimeConfigResult.ok
        ? runtimeConfigResult.config.apiUrl
        : "http://localhost:8090";
      const r = await fetch(`${apiUrl}/api/experimental-flags`);
      if (!r.ok) return [];
      const body = (await r.json()) as { flags?: Array<{
        key: string;
        runtime_kind?: string;
        title?: { en?: string; zh?: string };
      }> };
      return (body.flags ?? []).map((f) => ({
        key: f.key,
        runtime_kind: f.runtime_kind,
        label: f.title?.en ?? `experimental_${f.key}`,
      }));
    });
    // 0.3.19 P2: generic Labs IPC dispatcher. New flags register
    // their channels here without an edit to index.ts. Called
    // once with the merged descriptor list (static + catalog)
    // so all 9 flags get their 4-channel handler set.
    setupExperimentalIPC(() => mainWindow);

    // PR 1 (Stage C): wire serverAPI IPC + install-brew dialog. Renderer
    // subscribes to status changes; when the status carries
    // hint="install-brew", we offer a one-shot dialog with the exact
    // brew command the user needs to run. We deliberately do NOT do
    // anything more invasive (no auto-running brew, no automated
    // download) — Stage D will add the latter.
    installBrewDialogHandlers();
    publishInitialServerStatus();

    // P0-2 fix: kill the bundled Go server on app quit so the server port is
    // freed. Otherwise a stale server survives the GUI and the next launch
    // collides, triggering a 30s retry storm on the renderer (see audit
    // 2026-06-30).
    //
    // F7 audit fix (memory 0.3.2-backlog): this is now the SINGLE ordered
    // shutdown handler. daemon-manager used to also register its own
    // before-quit for stopDaemon, and the two listeners raced — sometimes
    // the server got SIGKILL before the daemon finished draining its WS,
    // leaving port 8090 closed mid-broadcast. Order matters:
    //   1. stopServerManager — bundled Go server (SIGTERM, 5s grace, then SIGKILL)
    //   2. stopDaemon       — bundled `multica daemon start` (no-op if external)
    //   3. app.quit()       — let Electron tear the rest down
    // stopDaemon reads prefs.autoStop internally, so honor that gate without
    // short-circuiting. per CLAUDE.md "Self-contained Backend" spec.
    let isQuitting = false;
    app.on("before-quit", async (event) => {
      if (isQuitting) return;
      isQuitting = true;
      event.preventDefault();
      try {
        await stopServerManager();
      } catch {
        // Best-effort — SIGKILL will catch anything stuck
      }
      try {
        await stopDaemon();
      } catch {
        // Best-effort — external daemon is a no-op
      }
      try {
        await stopPythiaManager();
      } catch {
        // Best-effort — manager may not have been started
      }
      // 0.3.22: no claude_science_manager — claude_science_lab is inline.
      // 0.3.19 P2: stop every Labs subprocess manager the
      // dispatcher knows about. New subprocess flags no longer need
      // an edit here.
      try {
        await stopAllExperimentalManagers();
      } catch {
        // Best-effort — dispatcher swallows per-manager errors.
      }
      app.quit();
    });
    setupLocalDirectory(() => mainWindow);

    // macOS: deep link arrives via open-url event
    app.on("open-url", (_event, url) => {
      if (mainWindow) {
        if (mainWindow.isMinimized()) mainWindow.restore();
        mainWindow.focus();
      }
      handleDeepLink(url);
    });

    app.on("activate", () => {
      if (BrowserWindow.getAllWindows().length === 0) createWindow();
    });
  });

  // Check argv for deep link on cold start (Windows/Linux)
  const deepLinkArg = process.argv.find((arg) =>
    arg.startsWith(`${PROTOCOL}://`),
  );
  if (deepLinkArg) {
    app.whenReady().then(() => handleDeepLink(deepLinkArg));
  }
}

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") app.quit();
});

// -----------------------------------------------------------------------------
// PR 1 (Stage C): serverAPI + install-brew dialog
// -----------------------------------------------------------------------------
// These are intentionally hoisted out of createWindow so they are
// registered exactly once at app startup. The dialog is gated on
// `installBrewDialogShown` so we don't pester the user with repeated
// modals when they click "Later" — they get one chance per launch, then
// they can fall back to the renderer-side banner or the docs.
//
// All IPC handlers here are *additive*. The existing
// `server:get-status`/`server:ensure-up`/`server:stop` handlers stay
// untouched for back-compat with current renderer code.

const BREW_INSTALL_CMD =
  "brew install postgresql@17 && brew services start postgresql@17";
const BREW_URL = "https://brew.sh";
const POSTGRES_APP_URL = "https://postgresapp.com/downloads.html";

// PR 3 (Stage D-2): cancelled-download flag. The renderer's progress
// modal can call `pg:cancel-download` to abort an in-flight download.
let lastDialogMigrationShown = false;

function installBrewDialogHandlers(): void {
  ipcMain.handle("server:get-install-instructions", () => ({
    brewCommand: BREW_INSTALL_CMD,
    brewUrl: BREW_URL,
    pgAppUrl: POSTGRES_APP_URL,
  }));

  ipcMain.handle("server:copy-text", (_event, text: string) => {
    if (typeof text === "string" && text.length > 0 && text.length < 4096) {
      clipboard.writeText(text);
    }
  });

  ipcMain.handle("server:open-external", (_event, url: string) => {
    if (
      typeof url === "string" &&
      (url.startsWith("https://") || url.startsWith("http://")) &&
      url.length < 2048
    ) {
      shell.openExternal(url);
    }
  });

  // Push current status once at startup so the renderer can paint its
  // banner without polling. Subsequent transitions will be pushed from
  // a polling loop below.
  ipcMain.handle("server:get-status-with-hint", () => getServerStatus());

  // PR 2 (Stage D-1, retained for v0.3.0): render-side modal asks for
  // the install path and a 4-step procedure. PR 3 normally auto-downloads
  // the binary, so this is the fallback path when auto-download fails
  // (xattr/quarantine/version-mismatch).
  ipcMain.handle("server:get-pg-install-instructions", () => {
    const paths = resolveNativePaths();
    return {
      pgAppUrl: PG_APP_URL,
      requiredVersion: PG_APP_REQUIRED_VERSION,
      pgHome: paths.pgHome,
      pgBin: paths.pgBin,
      manualSteps: [
        `Download Postgres.app ${PG_APP_REQUIRED_VERSION} from postgresapp.com`,
        `Open the .dmg and drag Postgres.app to /Applications`,
        `Right-click Postgres.app → Show Package Contents → Contents/Versions/`,
        `Copy the entire "17" folder to ${paths.pgHome} (you may need to create the parent dirs)`,
        `In a terminal: chmod +x ${paths.pgBin}/* && xattr -dr com.apple.quarantine ${paths.pgHome}`,
        `Restart Multica — the app will run initdb on first launch`,
      ],
    };
  });

  // PR 3: renderer asks the user to opt in to docker → native migration
  // via a native dialog. We poll docker state from the main process so
  // the renderer doesn't need direct docker CLI access.
  ipcMain.handle("server:should-offer-migration", async () => {
    const { checkDockerPgdata } = await import("./pg-bootstrap");
    const hasDocker = await checkDockerPgdata().catch(() => false);
    return { hasDocker };
  });
  ipcMain.handle("server:run-migration", async (_e, confirmed: boolean) => {
    const { runMigrationFlowIfNeeded } = await import("./server-manager");
    return runMigrationFlowIfNeeded({ confirmed });
  });
  ipcMain.handle("pg:cancel-download", () => {
    // Lazy import avoids a cycle when pg-bootstrap is loaded.
    void import("./pg-bootstrap").then((m) => m.cancelDownload());
  });
}

function publishInitialServerStatus(): void {
  const send = () => {
    const win = mainWindow;
    if (!win || win.isDestroyed()) return;
    try {
      win.webContents.send("server:status-changed", getServerStatus());
    } catch {
      /* webContents may not be ready yet; renderer will pull via
         server:get-status-with-hint once it mounts. */
    }
  };
  // 1) Fire once after a short delay so the renderer has time to wire up.
  setTimeout(send, 1_500);
  // 2) Then poll every 2s while the window is alive. Cheap, gated by
  //    a status equality check so we only push on actual transitions.
  let lastSerialized = "";
  setInterval(() => {
    const win = mainWindow;
    if (!win || win.isDestroyed()) return;
    const status = getServerStatus();
    const serialized = JSON.stringify(status);
    if (serialized === lastSerialized) return;
    lastSerialized = serialized;
    try {
      win.webContents.send("server:status-changed", status);
    } catch {
      /* ignore */
    }
    // PR 3 (Stage D-2): Docker install hint is gone. Only the
    // network/install-native hints remain. The renderer drives the
    // migration dialog itself based on server:should-offer-migration;
    // we don't auto-popup because that path is opt-in.
    if (
      status.state === "failed" &&
      "hint" in status &&
      (status.hint === "install-brew" || status.hint === "install-native") &&
      !lastDialogMigrationShown
    ) {
      // Re-use the legacy dialog for backwards-compat but the copy
      // now points at native-install only (no brew).
      lastDialogMigrationShown = true;
      showInstallBrewDialog(win).catch(() => undefined);
    }
  }, 2_000);
}

async function showInstallBrewDialog(win: BrowserWindow): Promise<void> {
  // PR 3 fallback: when the auto-download fails AND the user has no
  // working native binary, surface a one-shot modal pointing at the
  // Postgres.app manual install path. No "brew" buttons — we don't
  // recommend docker or brew any more.
  const r = await dialog.showMessageBox(win, {
    type: "info",
    buttons: ["打开 postgresapp.com", "查看安装步骤", "稍后"],
    defaultId: 0,
    cancelId: 2,
    title: "Multica 需要重新安装 PostgreSQL",
    message: "PostgreSQL 自动下载或解压失败",
    detail:
      "Multica 已不再附带 Docker 路径。请从 postgresapp.com 手动下载 17.4 后重试安装。",
    noLink: true,
  });
  if (r.response === 0) {
    shell.openExternal(POSTGRES_APP_URL);
  } else if (r.response === 1) {
    // The renderer can fetch the detailed modal via
    // server:get-pg-install-instructions; here we just open the URL.
    shell.openExternal(POSTGRES_APP_URL);
  }
}
