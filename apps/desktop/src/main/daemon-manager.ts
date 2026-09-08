import { app, ipcMain, BrowserWindow, shell } from "electron";
import { execFile } from "child_process";
import { pickEnvForSpawn } from "./util/spawn-env";
import {
  readFile,
  writeFile,
  mkdir,
  rename,
  rm,
  open,
  stat,
} from "fs/promises";
import {
  existsSync,
  watchFile,
  unwatchFile,
  type StatsListener,
} from "fs";
import { join } from "path";
import { homedir, hostname } from "os";
import type { DaemonStatus, DaemonPrefs } from "../shared/daemon-types";
import { daemonStatusAlive } from "../shared/daemon-types";
import { ensureManagedCli, managedCliPath } from "./cli-bootstrap";
import { setRendererLogPath } from "./renderer-log";
import { decideVersionAction } from "./version-decision";
import {
  daemonLifecycleUnreachable,
  isDaemonExternallyManaged,
  normalizeHostOS,
} from "./daemon-os";
import {
  classifyAuthProbe,
  isAuthStatusError,
  type AuthProbeResult,
} from "./daemon-auth-probe";

const DEFAULT_HEALTH_PORT = 19514;
const POLL_INTERVAL_MS = 5_000;
const PREFS_PATH = join(homedir(), ".multica", "desktop_prefs.json");
const LOG_TAIL_RETRY_MS = 2_000;
const LOG_TAIL_MAX_RETRIES = 5;
// How long a start may sit in "starting" (with no /health) before we probe the
// token to find out whether login expired. The daemon's own startup can legitimately
// take a while (it renews the PAT and lists workspaces before serving /health), so we
// wait past the common case to avoid probing healthy-but-slow starts.
const AUTH_PROBE_GRACE_MS = 10_000;
// `multica daemon start` blocks until the daemon reports ready, polling /health
// for up to its own startup timeout (45s in server/cmd/multica/cmd_daemon.go) to
// cover cold-start agent-version detection. This execFile timeout MUST stay
// above that — otherwise Electron kills the CLI supervisor mid-startup and a
// healthy-but-slow start is misreported as a failure (the detached daemon child
// keeps running, so the UI flashes "stopped" then "running").
const DAEMON_START_EXEC_TIMEOUT_MS = 60_000;

const DEFAULT_PREFS: DaemonPrefs = { autoStart: true, autoStop: false };

interface ActiveProfile {
  name: string; // "" = default profile
  port: number;
}

let statusPollTimer: ReturnType<typeof setInterval> | null = null;
let logTailWatcher: { path: string; listener: StatsListener } | null = null;
let currentState: DaemonStatus["state"] = "installing_cli";
let getMainWindow: () => BrowserWindow | null = () => null;
let operationInProgress = false;
let cachedCliBinary: string | null | undefined = undefined;
let cliResolvePromise: Promise<string | null> | null = null;
let cachedCliBinaryVersion: string | null | undefined = undefined;
// Set when a CLI version mismatch was detected but the running daemon is
// busy executing tasks. The poll loop retries the check on each tick and
// fires the restart once active_task_count drops to 0.
let pendingVersionRestart = false;
let targetApiBaseUrl: string | null = null;
let activeProfile: ActiveProfile | null = null;

// Auth-probe state for the current start attempt. When a start fails to reach
// "running", we probe the daemon's token once (after AUTH_PROBE_GRACE_MS) to
// decide whether the cause is an expired/invalid login. `authExpired` is sticky
// until the next start attempt or a successful /health, so the UI keeps showing
// the re-login prompt instead of flapping back to "starting". See #3512.
let startingSince: number | null = null;
let authProbeDone = false;
let authExpired = false;

// Serialize all writes to any profile config file. Multiple paths
// (syncToken, resolveActiveProfile, clearToken, watch/unwatch handlers)
// may try to write concurrently; chaining them avoids interleaved writes
// corrupting the JSON.
let configWriteChain: Promise<void> = Promise.resolve();

// Keep the Go impl in sync: server/cmd/multica/cmd_daemon.go healthPortForProfile.
function healthPortForProfile(profile: string): number {
  if (!profile) return DEFAULT_HEALTH_PORT;
  let sum = 0;
  for (const b of Buffer.from(profile, "utf-8")) sum += b;
  return DEFAULT_HEALTH_PORT + 1 + (sum % 1000);
}

function profileDir(profile: string): string {
  return profile
    ? join(homedir(), ".multica", "profiles", profile)
    : join(homedir(), ".multica");
}

function profileConfigPath(profile: string): string {
  return join(profileDir(profile), "config.json");
}

function profileLogPath(profile: string): string {
  return join(profileDir(profile), "daemon.log");
}

// 50 MB log-rotation threshold (0.5.37 audit issues #2/#8: prevent unbounded growth).
const LOG_ROTATE_THRESHOLD_BYTES = 50 * 1024 * 1024;

// Rotate logPath → logPath.1 via POSIX-atomic rename when the live file exceeds
// the threshold. The renderer's log tail watcher (startLogTail) already handles
// "file rotated/truncated → restart from 0"; the in-flight Go daemon's open fd
// keeps writing to the .1 inode until the next daemon restart reopens daemon.log.
async function rotateLogIfNeeded(logPath: string): Promise<void> {
  try {
    const stats = await stat(logPath);
    if (stats.size > LOG_ROTATE_THRESHOLD_BYTES) {
      await rename(logPath, `${logPath}.1`);
    }
  } catch (err) {
    if ((err as NodeJS.ErrnoException).code !== "ENOENT") {
      console.warn(`[daemon-manager] log rotation check failed for ${logPath}:`, err);
    }
  }
}

// Sidecar file that records which Multica user the cached PAT in config.json
// was minted for. The Go CLI/daemon never read or write this file, so it
// survives Go-side config rewrites. Used to detect user switches and mint a
// fresh PAT instead of reusing a token that belongs to a previous user.
function profileUserIdPath(profile: string): string {
  return join(profileDir(profile), ".desktop-user-id");
}

async function readProfileUserId(profile: string): Promise<string | null> {
  try {
    const raw = await readFile(profileUserIdPath(profile), "utf-8");
    const trimmed = raw.trim();
    return trimmed || null;
  } catch {
    return null;
  }
}

async function writeProfileUserId(
  profile: string,
  userId: string,
): Promise<void> {
  await mkdir(profileDir(profile), { recursive: true });
  await writeFile(profileUserIdPath(profile), userId, "utf-8");
}

async function removeProfileUserId(profile: string): Promise<void> {
  try {
    await rm(profileUserIdPath(profile));
  } catch {
    // Already gone — nothing to do.
  }
}

function normalizeUrl(u: string): string {
  if (!u) return "";
  try {
    const parsed = new URL(u);
    return `${parsed.protocol}//${parsed.host}`.toLowerCase();
  } catch {
    return u.replace(/\/+$/, "").toLowerCase();
  }
}

function urlsMatch(a: string, b: string): boolean {
  const na = normalizeUrl(a);
  const nb = normalizeUrl(b);
  return na.length > 0 && na === nb;
}

function sendStatus(status: DaemonStatus): void {
  const win = getMainWindow();
  win?.webContents.send("daemon:status", status);
}

interface HealthPayload {
  status?: string;
  pid?: number;
  /** Daemon's runtime.GOOS. Absent on daemons older than the #3916 fix. */
  os?: string;
  uptime?: string;
  daemon_id?: string;
  device_name?: string;
  server_url?: string;
  cli_version?: string;
  active_task_count?: number;
  agents?: string[];
  workspaces?: unknown[];
}

async function fetchHealthAtPort(
  port: number,
): Promise<HealthPayload | null> {
  try {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 2_000);
    const res = await fetch(`http://127.0.0.1:${port}/health`, {
      signal: controller.signal,
    });
    clearTimeout(timeout);
    if (!res.ok) return null;
    return (await res.json()) as HealthPayload;
  } catch {
    return null;
  }
}

/**
 * Validates the daemon profile's token against the backend to find out whether
 * a stuck start is an auth problem. Hits the same endpoint `multica auth status`
 * uses (GET /api/me) with the exact token the daemon loads from config.json, so
 * the verdict matches what the daemon itself would get from the server.
 *
 * Only the HTTP status is inspected (never the body) so a future change to the
 * /api/me response shape can't break this — a 401 means the token is rejected,
 * a 2xx means it's fine, and a thrown request means the network is the problem,
 * not auth. See classifyAuthProbe for the full rule set.
 */
async function probeTokenValidity(profile: string): Promise<AuthProbeResult> {
  if (!targetApiBaseUrl) return "unknown";
  const cfg = await readProfileConfig(profile);
  const token = typeof cfg.token === "string" ? cfg.token : "";
  if (!token) return classifyAuthProbe({ noToken: true });
  try {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 4_000);
    const res = await fetch(`${targetApiBaseUrl.replace(/\/+$/, "")}/api/me`, {
      headers: { Authorization: `Bearer ${token}` },
      signal: controller.signal,
    });
    clearTimeout(timeout);
    return classifyAuthProbe({ status: res.status });
  } catch {
    return classifyAuthProbe({ networkError: true });
  }
}

// Desktop owns a dedicated CLI profile named after the target API host, so it
// never reads or writes the user's hand-configured profiles. Profile dir:
//   ~/.multica/profiles/desktop-<host>/
function deriveProfileName(targetUrl: string): string {
  try {
    const url = new URL(targetUrl);
    const host = url.host.replace(/:/g, "-").toLowerCase();
    return `desktop-${host}`;
  } catch {
    return "desktop";
  }
}

/**
 * Canonical desktop profile name for the current target API URL, or null
 * before `initTargetApiUrl`/`setupDaemonManager` has pinned one.
 *
 * Exported so subprocess spawners (pythia-manager) bind env to the SAME
 * profile the daemon uses instead of re-guessing by directory listing —
 * a guess that silently picked stale `desktop-*` leftovers (e.g. the
 * pre-fork `desktop-api.multica.ai` cloud profile, which has no token)
 * and left the subprocess without its runtime credentials (0.5.104).
 */
export function activeDesktopProfileName(): string | null {
  if (!targetApiBaseUrl) return null;
  const name = deriveProfileName(targetApiBaseUrl);
  return name === "desktop" ? null : name;
}

/**
 * Pure-function decision: should `syncToken` reuse the cached PAT, or
 * mint a fresh one?
 *
 * Inputs:
 *   - `cachedPatLooksValid`: the cached token is a non-empty `mul_…` string.
 *   - `userChanged`: the previousUserId on disk differs from the JWT's `sub`.
 *   - `probe`: result of `probeTokenValidity` (only meaningful when both
 *      previous conditions hold; pass `"unknown"` otherwise).
 *
 * Output: `true` if we should reuse the cached PAT as-is.
 *
 * The interesting case is `cachedPatLooksValid && !userChanged` with
 * `probe === "rejected"`: user_id matches but server has rejected the
 * PAT (e.g. PG restore wiped the user). The user-id-only check used
 * here pre-2026-07-03 missed this and shipped a dead PAT to the daemon.
 * The fix is to require `probe === "ok"` whenever we have a candidate
 * cached PAT to reuse.
 *
 * Exported for testability — see daemon-manager.test.ts.
 */
export function shouldAcceptCachedPat(args: {
  cachedPatLooksValid: boolean;
  userChanged: boolean;
  probe: AuthProbeResult;
}): boolean {
  if (!args.cachedPatLooksValid) return false;
  if (args.userChanged) return false;
  // Cached PAT + same user_id on disk, but we have to trust the server's
  // verdict, not just our local belief that the user is the same. probe
  // values: "ok" → reuse; "rejected"/"expired"/"no_token" → mint fresh;
  // "unknown" → network error, treat as "don't reuse" to fail safe.
  return args.probe === "ok";
}

async function readProfileConfig(
  profile: string,
): Promise<Record<string, unknown>> {
  try {
    const raw = await readFile(profileConfigPath(profile), "utf-8");
    const parsed = JSON.parse(raw);
    return parsed && typeof parsed === "object" ? parsed : {};
  } catch {
    return {};
  }
}

async function writeProfileConfig(
  profile: string,
  cfg: Record<string, unknown>,
): Promise<void> {
  const op = async () => {
    await mkdir(profileDir(profile), { recursive: true });
    await writeFile(
      profileConfigPath(profile),
      JSON.stringify(cfg, null, 2),
      "utf-8",
    );
  };
  const next = configWriteChain.catch(() => {}).then(op);
  configWriteChain = next.catch(() => {});
  return next;
}

/**
 * Returns the Desktop-owned profile for the current target API URL. Creates
 * the profile's config.json on demand with `server_url` pinned to the target.
 *
 * This function never falls back to the default profile, and never touches a
 * profile whose name doesn't start with `desktop-`, so the user's manually
 * configured CLI profiles are untouched.
 */
async function resolveActiveProfile(): Promise<ActiveProfile> {
  const target = targetApiBaseUrl;
  if (!target) return { name: "", port: DEFAULT_HEALTH_PORT };

  const name = deriveProfileName(target);
  const cfg = await readProfileConfig(name);

  if (cfg.server_url !== target) {
    cfg.server_url = target;
    await writeProfileConfig(name, cfg);
    console.log(`[daemon] initialized profile "${name}" → ${target}`);
  }

  return { name, port: healthPortForProfile(name) };
}

async function ensureActiveProfile(): Promise<ActiveProfile> {
  if (activeProfile) return activeProfile;
  activeProfile = await resolveActiveProfile();
  // First profile resolution pins the renderer-console capture path so any
  // later console-message from the BrowserWindow can stream to
  // ~/.multica/profiles/<active>/renderer.log (production safety net — see
  // renderer-log.ts). Idempotent: ensureActiveProfile returns the cached
  // profile on repeat calls, so this runs exactly once per session.
  setRendererLogPath(join(profileDir(activeProfile.name), "renderer.log"));
  return activeProfile;
}

function invalidateActiveProfile(): void {
  activeProfile = null;
}

async function fetchHealth(): Promise<DaemonStatus> {
  // While the CLI is being downloaded or has permanently failed, short-circuit
  // polling — there's nothing to probe yet and /health calls would just return
  // "stopped", which would overwrite the correct setup state in the UI.
  if (currentState === "installing_cli" || currentState === "cli_not_found") {
    return { state: currentState };
  }

  const active = await ensureActiveProfile();
  const data = await fetchHealthAtPort(active.port);

  if (!data || data.status !== "running") {
    // A start that never reaches "running" is the symptom; an expired/invalid
    // login is the most common cause and the one with no other signal (the
    // daemon exits before it can serve /health, so we can't read the reason
    // from it). Probe the token once per attempt, after a grace period, to
    // surface a re-login prompt instead of spinning on "starting" forever.
    if (
      currentState === "starting" &&
      !authExpired &&
      !authProbeDone &&
      startingSince !== null &&
      Date.now() - startingSince >= AUTH_PROBE_GRACE_MS
    ) {
      authProbeDone = true;
      if ((await probeTokenValidity(active.name)) === "auth_expired") {
        authExpired = true;
      }
    }
    // Sticky: once login is known-expired, keep reporting it (even after
    // currentState flips away from "starting") until the next start attempt or
    // a successful /health clears the flag.
    if (authExpired) {
      return { state: "auth_expired", profile: active.name };
    }
    // The daemon binds /health before preflight finishes and self-reports
    // "starting" until it's ready. Trust that over our own currentState, so a
    // daemon booting on its own — or started via the CLI — surfaces as
    // "starting" instead of "stopped".
    if (data?.status === "starting") {
      return { state: "starting", profile: active.name };
    }
    return {
      state: currentState === "starting" ? "starting" : "stopped",
      profile: active.name,
    };
  }

  // A live, authenticated daemon clears any prior auth-failure verdict so the
  // re-login prompt disappears once the user reconnects.
  authExpired = false;
  startingSince = null;

  // A running daemon whose OS differs from this host's is one we can't drive
  // via the native lifecycle CLI (e.g. Linux-in-WSL2 behind a Windows desktop,
  // reachable only over localhost forwarding). Surface it so the UI disables
  // the auto-start/auto-stop toggles instead of letting them silently no-op,
  // and so before-quit skips a stop that would never land. See #3916.
  const externallyManaged = isDaemonExternallyManaged(
    data.os,
    normalizeHostOS(process.platform),
  );

  // Safety: if we have a target URL and the daemon on our port reports a
  // different server_url, it's not "our" daemon — drop it and re-resolve.
  if (
    targetApiBaseUrl &&
    data.server_url &&
    !urlsMatch(data.server_url, targetApiBaseUrl)
  ) {
    invalidateActiveProfile();
    return { state: "stopped" };
  }

  return {
    state: "running",
    pid: data.pid,
    uptime: data.uptime,
    daemonId: data.daemon_id,
    deviceName: data.device_name,
    agents: data.agents ?? [],
    workspaceCount: Array.isArray(data.workspaces)
      ? data.workspaces.length
      : 0,
    profile: active.name,
    serverUrl: data.server_url,
    externallyManaged,
  };
}

function findCliOnPath(): string | null {
  const candidates = process.platform === "win32" ? ["multica.exe"] : ["multica"];
  const paths = (process.env["PATH"] ?? "").split(
    process.platform === "win32" ? ";" : ":",
  );
  if (process.platform === "darwin") {
    paths.push("/opt/homebrew/bin", "/usr/local/bin");
  }
  for (const name of candidates) {
    for (const dir of paths) {
      const full = join(dir, name);
      if (existsSync(full)) return full;
    }
  }
  return null;
}

/**
 * Returns the path to the CLI binary bundled inside the Desktop app.
 *
 * - Dev (`electron-vite dev`): `app.getAppPath()` → `apps/desktop`, resolving
 *   to `apps/desktop/resources/bin/multica`. `bundle-cli.mjs` populates this
 *   before dev starts, so iterating on Go changes is "make build → restart".
 * - Packaged: `app.getAppPath()` → `<Multica.app>/Contents/Resources/app.asar`.
 *   electron-builder's `asarUnpack: resources/**` extracts the binary to
 *   `app.asar.unpacked/`, so we swap the path segment to execute it.
 */
function bundledCliPath(): string {
  const binName = process.platform === "win32" ? "multica.exe" : "multica";
  return join(app.getAppPath(), "resources", "bin", binName).replace(
    "app.asar",
    "app.asar.unpacked",
  );
}

async function probeCliBinary(
  bin: string,
  source: "bundled" | "managed" | "path",
): Promise<string | null> {
  try {
    const stdout = await new Promise<string>((resolve, reject) => {
      execFile(
        bin,
        ["version", "--output", "json"],
        { timeout: 5_000 },
        (err, out) => {
          if (err) reject(err);
          else resolve(out);
        },
      );
    });
    const parsed = JSON.parse(stdout) as { version?: string };
    if (typeof parsed.version === "string" && parsed.version.length > 0) {
      return parsed.version;
    }
    console.warn(
      `[daemon] ignoring ${source} CLI at ${bin}: version output was missing or invalid`,
    );
    return null;
  } catch (err) {
    console.warn(`[daemon] ignoring ${source} CLI at ${bin}:`, err);
    return null;
  }
}

/**
 * Returns a usable `multica` binary path. Priority:
 *   1. Cached result from a previous successful resolve.
 *   2. Bundled binary shipped with the Desktop app (`bundle-cli.mjs`).
 *   3. Managed binary already installed in userData (`managedCliPath`).
 *   4. Download + install latest release into userData.
 *   5. `multica` on PATH (dev convenience / user-installed via brew).
 * Returns `null` only when all of the above fail.
 *
 * Bundled is preferred so Desktop iterates in lockstep with Go changes in
 * the same repo — avoids the 404 / stale-API problem when the Desktop's
 * TS side is ahead of the last published CLI release.
 *
 * This function is idempotent and safe to call concurrently — in-flight
 * installs are de-duplicated via `cliResolvePromise`.
 */
async function resolveCliBinary(): Promise<string | null> {
  if (cachedCliBinary !== undefined) return cachedCliBinary;
  if (cliResolvePromise) return cliResolvePromise;

  cliResolvePromise = (async () => {
    const bundled = bundledCliPath();
    if (existsSync(bundled)) {
      const version = await probeCliBinary(bundled, "bundled");
      if (version) {
        console.log(`[daemon] using bundled CLI at ${bundled}`);
        cachedCliBinary = bundled;
        cachedCliBinaryVersion = version;
        return bundled;
      }
    }

    const managed = managedCliPath();
    if (existsSync(managed)) {
      const version = await probeCliBinary(managed, "managed");
      if (version) {
        cachedCliBinary = managed;
        cachedCliBinaryVersion = version;
        return managed;
      }
    }

    try {
      const installed = await ensureManagedCli({
        forceInstall: existsSync(managed),
      });
      const version = await probeCliBinary(installed, "managed");
      if (version) {
        cachedCliBinary = installed;
        cachedCliBinaryVersion = version;
        return installed;
      }
      console.warn(
        `[daemon] managed CLI at ${installed} failed validation after install`,
      );
    } catch (err) {
      console.warn("[daemon] CLI auto-install failed, falling back to PATH:", err);
    }

    const onPath = findCliOnPath();
    if (onPath) {
      const version = await probeCliBinary(onPath, "path");
      if (version) {
        cachedCliBinary = onPath;
        cachedCliBinaryVersion = version;
        return onPath;
      }
    }

    cachedCliBinary = null;
    cachedCliBinaryVersion = null;
    return null;
  })();

  try {
    return await cliResolvePromise;
  } finally {
    cliResolvePromise = null;
  }
}

/**
 * Reads the version of the currently resolved CLI binary. Cached for the
 * process lifetime — the bundled binary doesn't change after bundle time.
 * Returns null on any failure (unknown `go` at bundle time, broken binary,
 * wrong-arch bundled binary, etc.) so callers can fail open.
 */
async function getCliBinaryVersion(): Promise<string | null> {
  if (cachedCliBinaryVersion !== undefined) return cachedCliBinaryVersion;
  const bin = await resolveCliBinary();
  if (!bin) {
    cachedCliBinaryVersion = null;
    return null;
  }
  cachedCliBinaryVersion = await probeCliBinary(bin, "path");
  return cachedCliBinaryVersion;
}

/**
 * Compares the running daemon's `cli_version` against the CLI binary we
 * would use to spawn a new one, and restarts only when safe. The decision
 * logic itself is in `version-decision.ts` (pure, unit-tested); this
 * wrapper handles the async plumbing and side effects.
 *
 * Restart is only fired when ALL of:
 *   - a daemon is actually running on the active profile's port
 *   - both sides report a version and the strings differ
 *   - `active_task_count` is 0 (no in-flight agent work would be killed)
 *
 * On a confirmed mismatch while the daemon is busy, `pendingVersionRestart`
 * is set; the poll loop retries this function on each 5s tick and will fire
 * the restart as soon as the daemon drains.
 */
async function ensureRunningDaemonVersionMatches(): Promise<
  "restarted" | "deferred" | "ok" | "not_running"
> {
  const active = await ensureActiveProfile();
  const running = await fetchHealthAtPort(active.port);

  // Don't try to version-match a daemon we can't restart (e.g. WSL2). Treat it
  // as up-to-date — restartDaemon would no-op anyway, and skipping here avoids
  // a misleading "restarting daemon" log on every auto-start. #3916.
  if (isDaemonExternallyManaged(running?.os, normalizeHostOS(process.platform))) {
    pendingVersionRestart = false;
    return "ok";
  }

  const bundled = await getCliBinaryVersion();
  const action = decideVersionAction(bundled, running);

  switch (action) {
    case "not_running":
      pendingVersionRestart = false;
      return "not_running";
    case "ok":
      pendingVersionRestart = false;
      return "ok";
    case "defer": {
      if (!pendingVersionRestart) {
        const activeTasks = running?.active_task_count ?? 0;
        console.log(
          `[daemon] CLI version mismatch (bundled=${bundled} running=${running?.cli_version}); deferring restart until ${activeTasks} active task(s) finish`,
        );
      }
      pendingVersionRestart = true;
      return "deferred";
    }
    case "restart":
      console.log(
        `[daemon] CLI version mismatch (bundled=${bundled} running=${running?.cli_version}) — restarting daemon`,
      );
      pendingVersionRestart = false;
      await restartDaemon();
      return "restarted";
  }
}

/**
 * Exchange the user's JWT for a long-lived PAT via POST /api/tokens. The
 * daemon needs a PAT (or `mul_` / `mdt_` token) because JWTs expire in 30
 * days and signatures are tied to a specific backend instance.
 */
async function mintPat(jwt: string): Promise<string> {
  if (!targetApiBaseUrl) {
    throw new Error("mint PAT: target API URL not set");
  }
  const url = `${targetApiBaseUrl.replace(/\/+$/, "")}/api/tokens`;
  const res = await fetch(url, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${jwt}`,
    },
    // Omit expires_in_days → server treats as null → non-expiring PAT.
    body: JSON.stringify({ name: "Multica Desktop" }),
  });
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    // Attach the status so callers can tell a genuine auth rejection (401 — the
    // session token is dead) apart from a transient failure (5xx, etc.) without
    // string-matching the message.
    throw Object.assign(
      new Error(`mint PAT failed: ${res.status} ${res.statusText} ${body}`),
      { status: res.status },
    );
  }
  const data = (await res.json()) as { token?: unknown };
  if (typeof data.token !== "string" || !data.token.startsWith("mul_")) {
    throw new Error("mint PAT: response missing token");
  }
  return data.token;
}

/**
 * Ensure the active profile's config.json has a usable token for the daemon.
 *
 * - Input from the renderer is the user's JWT (from localStorage) plus the
 *   current user's id, so we can detect session changes.
 * - If the profile already has a cached PAT (`mul_...`) AND the sidecar user
 *   id matches the caller, reuse it — minting fresh on every launch would
 *   accumulate garbage in the user's tokens page.
 * - On user mismatch (or first run) call POST /api/tokens with the JWT to
 *   mint a fresh PAT, overwriting any stale cached PAT. This is the critical
 *   path: without it, a previous user's PAT would be used by a new session.
 * - If the caller happens to pass a PAT directly, write it through.
 * - When we mint fresh and a daemon is already running, restart it so the
 *   new credentials take effect (the Go daemon reads config at startup).
 */
async function syncToken(
  tokenFromRenderer: string,
  userId: string,
): Promise<void> {
  const active = await ensureActiveProfile();
  const config = await readProfileConfig(active.name);
  const previousUserId = await readProfileUserId(active.name);
  const userChanged = Boolean(previousUserId) && previousUserId !== userId;
  const cachedPatLooksValid =
    typeof config.token === "string" && config.token.startsWith("mul_");

  // Self-heal: when we have a cached PAT but it's stale (e.g. the database
  // was wiped and restored, leaving the cached PAT pointing at a deleted
  // user; or the user previously logged in as a different name and the
  // cache still references that older user_id), the user_id mismatch
  // check above is not enough — the user_id can match while the PAT is
  // still server-side dead. Probe /api/me with the cached token BEFORE
  // reusing it. On 401 we fall through to mintPat() which produces a
  // fresh PAT bound to the current JWT's user.
  //
  // Without this check, the user-visible symptom is the
  // "agent's runtime is offline" banner with a daemon log full of
  // `auth token rejected by server` warnings. Hit on 2026-07-03 after
  // the v0.3.0 destructive-migration recovery: gold-dump restore wiped
  // the secondary user (e25b96e9) but kept the primary user (jyf);
  // config.json had the cached PAT + user_id of the now-deleted user.
  const cachedPatAcceptedByServer = await shouldAcceptCachedPat({
    cachedPatLooksValid,
    userChanged,
    probe:
      cachedPatLooksValid && !userChanged
        ? await probeTokenValidity(active.name)
        : "unknown",
  });
  if (cachedPatLooksValid && !userChanged && !cachedPatAcceptedByServer) {
    console.warn(
      `[daemon] cached PAT for profile "${active.name}" not accepted by server; will mint fresh`,
    );
  }

  const sameUserWithCachedPat =
    !userChanged &&
    previousUserId === userId &&
    cachedPatLooksValid &&
    cachedPatAcceptedByServer;

  let finalToken: string;
  if (tokenFromRenderer.startsWith("mul_")) {
    finalToken = tokenFromRenderer;
  } else if (sameUserWithCachedPat) {
    finalToken = config.token as string;
  } else {
    try {
      finalToken = await mintPat(tokenFromRenderer);
      console.log(
        `[daemon] minted PAT for profile "${active.name}" (user_changed=${userChanged} stale_pat=${!cachedPatAcceptedByServer && cachedPatLooksValid})`,
      );
    } catch (err) {
      console.error("[daemon] failed to mint PAT:", err);
      throw err;
    }
  }

  config.token = finalToken;
  if (targetApiBaseUrl) config.server_url = targetApiBaseUrl;
  await writeProfileConfig(active.name, config);
  await writeProfileUserId(active.name, userId);

  // If we just rotated credentials onto a running daemon, restart it so the
  // in-memory token in the Go process matches the new config. Two paths
  // trigger this:
  //   (a) `userChanged` — the active user swapped (login as a different name)
  //   (b) the cached PAT was rejected by the server (probe above) — the
  //       user_id may match while the PAT itself is dead (post-restore from
  //       a destructive-migration wipe; PAT revocation; etc.)
  const credentialsRotated =
    userChanged || (cachedPatLooksValid && !cachedPatAcceptedByServer);
  if (credentialsRotated) {
    try {
      const existing = await fetchHealthAtPort(active.port);
      if (daemonStatusAlive(existing?.status)) {
        console.log(
          `[daemon] credentials rotated (${userChanged ? "user_changed" : "stale_pat"}) — restarting daemon`,
        );
        void restartDaemon();
      }
    } catch (err) {
      console.warn("[daemon] restart-on-credential-rotation failed:", err);
    }
  }
}

async function loadPrefs(): Promise<DaemonPrefs> {
  try {
    const raw = await readFile(PREFS_PATH, "utf-8");
    const parsed = JSON.parse(raw);
    return { ...DEFAULT_PREFS, ...parsed };
  } catch {
    return { ...DEFAULT_PREFS };
  }
}

async function savePrefs(prefs: DaemonPrefs): Promise<void> {
  const dir = join(homedir(), ".multica");
  await mkdir(dir, { recursive: true });
  await writeFile(PREFS_PATH, JSON.stringify(prefs, null, 2), "utf-8");
}

async function clearToken(): Promise<void> {
  const active = await ensureActiveProfile();
  const config = await readProfileConfig(active.name);
  if ("token" in config) {
    delete config.token;
    await writeProfileConfig(active.name, config);
  }
  // Always drop the sidecar so a subsequent syncToken from any user is
  // treated as a fresh mint, not a reuse of a stale cached PAT.
  await removeProfileUserId(active.name);
}

// Result of a user-initiated daemon re-authentication. The distinction matters:
// only `session_invalid` justifies signing the user out of the whole app; a
// `transient` failure must keep them logged in so they can retry.
export type ReauthResult =
  | { ok: true }
  | { ok: false; reason: "session_invalid" }
  | { ok: false; reason: "transient"; message: string };

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/**
 * Recover the local daemon from the "auth_expired" state. Drops the stale
 * cached PAT, mints a fresh one from the current session token, and restarts
 * the daemon so it loads the new credential.
 *
 * Failures are classified rather than collapsed: a 401 from the mint means the
 * session token itself is dead (`session_invalid` → the renderer drives a full
 * re-login); anything else — mint 5xx, a network blip, a config write error, a
 * restart hiccup — is `transient`, leaving the user signed in so they can retry.
 * This mirrors the conservative classification the startup probe already uses.
 */
async function reauthenticate(
  token: string,
  userId: string,
): Promise<ReauthResult> {
  try {
    await clearToken();
    // syncToken mints a fresh PAT because clearToken just removed any cache.
    await syncToken(token, userId);
  } catch (err) {
    if (isAuthStatusError(err)) return { ok: false, reason: "session_invalid" };
    return { ok: false, reason: "transient", message: errorMessage(err) };
  }
  const restart = await restartDaemon();
  if (!restart.success) {
    return {
      ok: false,
      reason: "transient",
      message: restart.error ?? "failed to restart daemon",
    };
  }
  return { ok: true };
}

async function withGuard<T>(fn: () => Promise<T>): Promise<T | { success: false; error: string }> {
  if (operationInProgress) {
    return { success: false, error: "Another daemon operation is in progress" };
  }
  operationInProgress = true;
  try {
    return await fn();
  } finally {
    operationInProgress = false;
  }
}

function profileArgs(active: ActiveProfile): string[] {
  return active.name ? ["--profile", active.name] : [];
}

// Env passed to every CLI child so the daemon process knows it was spawned
// by the Desktop app. The server uses this to mark runtimes as managed and
// hide CLI self-update UI. Computed lazily so it picks up the PATH fix
// applied by fix-path in main/index.ts — as a top-level const it would
// snapshot process.env at import time, before that block runs.
function desktopSpawnEnv(): NodeJS.ProcessEnv {
  return pickEnvForSpawn({ MULTICA_LAUNCHED_BY: "desktop" });
}

// Single-flight guard for startDaemon. Without it, two near-simultaneous
// callers — bootstrapCli + tryAutoStartFromMain + syncToken IPC + a user-
// triggered `daemon:auto-start` — each spawn their own CLI supervisor and
// race for port 19545; the losing one logs "bind: address already in use"
// (10 events in 13h in daemon.log per the 0.5.36 client-log audit).
//
// The wrapper returns the in-flight promise to re-entrant callers instead
// of firing a second execFile, and clears the slot on settle so a later
// restartDaemon / explicit user "Start" can still launch a fresh daemon.
let startInFlight: Promise<{ success: boolean; error?: string }> | null = null;
async function startDaemon(): Promise<{ success: boolean; error?: string }> {
  if (startInFlight) return startInFlight;
  startInFlight = startDaemonImpl();
  try {
    return await startInFlight;
  } finally {
    startInFlight = null;
  }
}

async function startDaemonImpl(): Promise<{ success: boolean; error?: string }> {
  const bin = await resolveCliBinary();
  if (!bin) return { success: false, error: "multica CLI is not installed" };

  const active = await ensureActiveProfile();
  const existing = await fetchHealthAtPort(active.port);
  if (daemonStatusAlive(existing?.status)) {
    // A daemon is already up ("running") or booting ("starting") on this port —
    // don't spawn a second one (the CLI rejects that as "already running").
    // Let polling track it through to "running".
    pollOnce();
    return { success: true };
  }

  currentState = "starting";
  // Begin a fresh auth-probe window for this attempt.
  startingSince = Date.now();
  authProbeDone = false;
  authExpired = false;
  sendStatus({ state: "starting" });

  const args = ["daemon", "start", ...profileArgs(active)];
  // Pass --server-url explicitly so the daemon's URL resolution does not fall
  // back to ws://localhost:8080/ws (DefaultServerURL) when MULTICA_SERVER_URL
  // is unset. That fallback pointed GUI-launched daemons at SearXNG on 8080
  // whenever the user's shell leaked PORT=8080 into the GUI process — the
  // daemon crashed, the watchdog wrote `~/.multica/daemon-needs-spawn.txt`,
  // and every agent stayed in "queued". targetApiBaseUrl is the
  // F-027-allowlisted URL the GUI's renderer wired from desktop.json's
  // apiUrl, so passing it through makes the daemon use the same server the
  // GUI is talking to. (Fix for the "PORT env leak → all agents queued"
  // regression documented in memory 0.5.26.)
  if (targetApiBaseUrl) {
    args.push("--server-url", targetApiBaseUrl);
  }

  // 0.5.37 audit: rotate daemon.log if it exceeds 50 MB before spawning the CLI.
  await rotateLogIfNeeded(profileLogPath(active.name));
  return new Promise((resolve) => {
    execFile(
      bin,
      args,
      { timeout: DAEMON_START_EXEC_TIMEOUT_MS, env: desktopSpawnEnv() },
      (err) => {
        if (err) {
          currentState = "stopped";
          sendStatus({ state: "stopped" });
          resolve({ success: false, error: err.message });
          return;
        }
        // Stay in "starting" until pollOnce confirms /health — the CLI
        // returning 0 only means the supervisor was spawned, not that the
        // daemon process is already listening.
        pollOnce();
        resolve({ success: true });
      },
    );
  });
}

/**
 * Fresh boundary preflight for stop/restart: read the active profile's CURRENT
 * /health and decide whether the daemon runs somewhere the app can't drive
 * (WSL2 etc.). Done per call rather than off the poll cache, so a lifecycle op
 * never shells out to a CLI that can't reach the daemon's process — even on
 * paths that didn't just poll (e.g. restart-on-user-switch in syncToken, which
 * calls restartDaemon directly). See #3916.
 */
async function lifecycleBlockedByForeignDaemon(): Promise<boolean> {
  const active = await ensureActiveProfile();
  return daemonLifecycleUnreachable(
    async () => (await fetchHealthAtPort(active.port))?.os,
    normalizeHostOS(process.platform),
  );
}

export async function stopDaemon(): Promise<{ success: boolean; error?: string }> {
  // Central lifecycle guard: a daemon running in an environment we can't drive
  // (e.g. Linux in WSL2 behind a Windows desktop) can't be stopped by the
  // native CLI — it would act on the host process namespace and no-op, while
  // still flipping our state to "stopped". Bail as a successful no-op so every
  // caller (logout, quit, restart, the Runtime card) is covered in one place
  // rather than each remembering to check. Preflighted against live /health so
  // it holds even when no poll ran first. #3916.
  if (await lifecycleBlockedByForeignDaemon()) return { success: true };

  const bin = await resolveCliBinary();
  if (!bin) return { success: false, error: "multica CLI is not installed" };

  const active = await ensureActiveProfile();
  currentState = "stopping";
  // An explicit stop is a clean reset — drop any pending auth-failure verdict.
  authExpired = false;
  startingSince = null;
  sendStatus({ state: "stopping" });

  const args = ["daemon", "stop", ...profileArgs(active)];

  // F7 audit fix: stop the local /health poll timer and the log-tail file
  // watcher BEFORE sending SIGTERM to the daemon process. Otherwise
  // pollOnce keeps firing against a dying daemon, and the renderer log
  // stream keeps pushing updates through a closed WS — both generate
  // noisy error events right as the user is trying to quit. Order matters.
  stopPolling();
  stopLogTail();

  return new Promise((resolve) => {
    execFile(bin, args, { timeout: 15_000, env: desktopSpawnEnv() }, (err) => {
      if (err) {
        resolve({ success: false, error: err.message });
      } else {
        resolve({ success: true });
      }
      currentState = "stopped";
      sendStatus({ state: "stopped" });
    });
  });
}

async function restartDaemon(): Promise<{ success: boolean; error?: string }> {
  // Same central, live-preflighted guard as stopDaemon: we can neither stop nor
  // start a daemon we don't manage, so don't try (user-switch, reauth,
  // first-workspace, and any future restart caller all route through here).
  // #3916.
  if (await lifecycleBlockedByForeignDaemon()) return { success: true };
  const stopResult = await stopDaemon();
  if (!stopResult.success) return stopResult;
  return startDaemon();
}

async function pollOnce(): Promise<void> {
  const status = await fetchHealth();
  currentState = status.state;
  sendStatus(status);
  // Retry a deferred version-mismatch restart once the daemon drains.
  if (pendingVersionRestart && status.state === "running") {
    void ensureRunningDaemonVersionMatches();
    return;
  }
  // Auto-recovery safety net. When the daemon is missing entirely but the
  // user has autoStart enabled and the profile already has a token, give it
  // another chance to come up. This handles GUI restarts where the renderer's
  // useEffect did not fire (logged-in session where `user` reference did not
  // change) and the main-process bootstrapCli auto-start raced with profile
  // resolution. The tryAutoStartFromMain guard at the top of that function
  // makes this a no-op for fresh-from-scratch profiles (no token yet).
  // Throttle to one attempt every 30s to avoid hot loops if startDaemon fails.
  if (status.state !== "running") {
    maybeRecoverDaemon().catch((err) =>
      console.error("[daemon] recovery poll failed:", err),
    );
  }
}

let lastRecoverAttemptAt = 0;
const RECOVER_THROTTLE_MS = 30_000;

async function maybeRecoverDaemon(): Promise<void> {
  const now = Date.now();
  // 0.3.45.2 bug fix (P1#10): the throttle was a silent early-return.
  // Operators reading daemon-watchdog.log had no signal that the
  // recovery loop was skipping attempts. Log every skip so a long
  // string of "recover suppressed" lines is visible, and tag the
  // console output with the suppression reason so the same line is
  // useful in the dev console and the production log.
  if (now - lastRecoverAttemptAt < RECOVER_THROTTLE_MS) {
    const suppressMs = RECOVER_THROTTLE_MS - (now - lastRecoverAttemptAt);
    console.log(
      `[daemon] recovery suppressed (next attempt in ${Math.ceil(suppressMs / 1000)}s)`,
    );
    return;
  }
  lastRecoverAttemptAt = now;

  const prefs = await loadPrefs();
  if (!prefs.autoStart) {
    console.log("[daemon] recovery suppressed: autoStart disabled");
    return;
  }
  const active = await ensureActiveProfile();
  const cfg = await readProfileConfig(active.name);
  if (!cfg.token || typeof cfg.token !== "string" || !cfg.token.startsWith("mul_")) {
    console.log("[daemon] recovery suppressed: no mul_ token for active profile");
    return;
  }
  const bin = await resolveCliBinary();
  if (!bin) {
    console.log("[daemon] recovery suppressed: multica CLI binary not found");
    return;
  }
  console.log(
    "[daemon] pollOnce: daemon not running but autoStart enabled — restarting",
  );
  try {
    await startDaemon();
  } catch (err) {
    // 0.3.45.2 (P1#10): previously the startDaemon promise rejection
    // bubbled up uncaught and was only caught one level up (line
    // 1027) — a 1-2 minute thundering herd of recoveries after each
    // failed start. Log here so a single console.error pinpoints the
    // exact failure mode (permissions, port busy, bad token, etc).
    console.error("[daemon] startDaemon failed during recovery:", err);
    throw err;
  }
}

function startPolling(): void {
  if (statusPollTimer) return;
  pollOnce();
  statusPollTimer = setInterval(pollOnce, POLL_INTERVAL_MS);
}

/**
 * Ensures the CLI binary is available, then transitions into the normal
 * stopped/running state machine. Called once at startup and again on
 * user-triggered `daemon:retry-install`.
 */
async function bootstrapCli(): Promise<void> {
  const bin = await resolveCliBinary();
  if (!bin) {
    currentState = "cli_not_found";
    sendStatus({ state: "cli_not_found" });
    return;
  }
  currentState = "stopped";
  sendStatus({ state: "stopped" });
  startPolling();
  // Main-process auto-start guard. The renderer-side useEffect in App.tsx
  // triggers IPC `daemon:auto-start` only when the `user` reference changes
  // (login / logout / user-switch). When the user is already logged in at
  // GUI launch — localStorage holds a token and AuthInitializer's retry
  // ladder resolves to an existing user — that effect's dependency does
  // NOT change, so the daemon never starts on its own. Without this guard,
  // every GUI relaunch leaves the agent runtime orphaned and agent_task_queue
  // rows pile up as "queued" indefinitely. The renderer path remains as a
  // fallback for scenarios where the token hasn't been synced to disk yet.
  void tryAutoStartFromMain();
}

// tryAutoStartFromMain is the main-process counterpart to the renderer's
// `useEffect(() => window.daemonAPI.autoStart(), [user])` in App.tsx. It is
// invoked once after bootstrapCli resolves the bundled CLI binary, and only
// proceeds if the user has explicitly opted into auto-start AND the active
// profile already has a usable token persisted in config.json (otherwise the
// renderer's syncToken would race us and overwrite the token). On success it
// either delegates to ensureRunningDaemonVersionMatches (existing daemon
// but possibly stale CLI) or kicks off startDaemon.
async function tryAutoStartFromMain(): Promise<void> {
  try {
    const prefs = await loadPrefs();
    if (!prefs.autoStart) {
      console.log("[daemon] autoStart from main: skipped (prefs.autoStart=false)");
      return;
    }
    const bin = await resolveCliBinary();
    if (!bin) return;

    const active = await ensureActiveProfile();
    const cfg = await readProfileConfig(active.name);
    if (!cfg.token || typeof cfg.token !== "string" || !cfg.token.startsWith("mul_")) {
      console.log(
        `[daemon] autoStart from main: skipped (no mul_ token in profile "${active.name || "(default)"}"; renderer path will handle first-time login)`,
      );
      return;
    }

    const health = await fetchHealth();
    if (health.state === "running") {
      await ensureRunningDaemonVersionMatches();
      return;
    }
    console.log(
      "[daemon] autoStart from main: starting daemon (renderer useEffect did not fire — pre-existing logged-in session)",
    );
    await startDaemon();
  } catch (err) {
    // Auto-start from main is best-effort: never block setup, never throw
    // into the caller. A failure here still leaves the renderer path active,
    // and the user can always hit "Start" in the daemon settings tab.
    console.error("[daemon] autoStart from main failed:", err);
  }
}

// stopPolling/stopLogTail fire from within stopDaemon so the renderer
  // log stream stops before the WS closes. stopPolling also clears the
  // /health poll timer that pollOnce started — without it the dangling
  // setInterval keeps firing after the daemon process is gone, which
  // leaks file descriptors under long-lived app sessions.
  function stopPolling(): void {
    if (statusPollTimer) {
      clearInterval(statusPollTimer);
      statusPollTimer = null;
    }
  }

const LOG_TAIL_INITIAL_WINDOW_BYTES = 32 * 1024;
const LOG_TAIL_INITIAL_LINES = 200;
const LOG_TAIL_POLL_MS = 500;

async function readLogRange(
  path: string,
  startAt: number,
  length: number,
): Promise<string> {
  const handle = await open(path, "r");
  try {
    const buffer = Buffer.alloc(length);
    const { bytesRead } = await handle.read(buffer, 0, length, startAt);
    return buffer.subarray(0, bytesRead).toString("utf-8");
  } finally {
    await handle.close();
  }
}

function sendLines(win: BrowserWindow, text: string): void {
  const lines = text.split("\n").filter((line) => line.length > 0);
  for (const line of lines) {
    win.webContents.send("daemon:log-line", line);
  }
}

// Cross-platform tail -f replacement: read the tail of the file once, then
// poll its stat with fs.watchFile and forward any new bytes since the last
// known offset. watchFile works on macOS, Linux, and Windows; spawn("tail")
// would silently fail on Windows.
function startLogTail(win: BrowserWindow, retryCount = 0): void {
  stopLogTail();

  void ensureActiveProfile().then(async (active) => {
    const logPath = profileLogPath(active.name);
    if (!existsSync(logPath)) {
      if (retryCount < LOG_TAIL_MAX_RETRIES) {
        setTimeout(() => startLogTail(win, retryCount + 1), LOG_TAIL_RETRY_MS);
      }
      return;
    }

    let position = 0;
    try {
      const initialStats = await stat(logPath);
      const windowBytes = Math.min(
        initialStats.size,
        LOG_TAIL_INITIAL_WINDOW_BYTES,
      );
      const startAt = initialStats.size - windowBytes;
      if (windowBytes > 0) {
        const text = await readLogRange(logPath, startAt, windowBytes);
        const lines = text
          .split("\n")
          .filter((line) => line.length > 0)
          .slice(-LOG_TAIL_INITIAL_LINES);
        for (const line of lines) {
          win.webContents.send("daemon:log-line", line);
        }
      }
      position = initialStats.size;
    } catch (err) {
      console.warn("[daemon] log tail initial read failed:", err);
      return;
    }

    const listener: StatsListener = (curr) => {
      const target = getMainWindow();
      if (!target) return;
      // File rotated/truncated — restart from the new beginning.
      if (curr.size < position) position = 0;
      if (curr.size === position) return;
      const from = position;
      const length = curr.size - from;
      position = curr.size;
      readLogRange(logPath, from, length)
        .then((text) => sendLines(target, text))
        .catch((err) => {
          console.warn("[daemon] log tail read failed:", err);
        });
    };

    watchFile(logPath, { interval: LOG_TAIL_POLL_MS }, listener);
    logTailWatcher = { path: logPath, listener };
  });
}

function stopLogTail(): void {
  if (logTailWatcher) {
    unwatchFile(logTailWatcher.path, logTailWatcher.listener);
    logTailWatcher = null;
  }
}

// isAllowedTargetApiUrl validates a target API base URL for
// daemon:set-target-api-url (F-027). Only loopback (localhost / 127.0.0.1 /
// ::1) and private LAN subnets (10/8, 172.16/12, 192.168/16) over http(s)
// are allowed — the daemon mints auth tokens against this URL, so it must
// never be pointed at a public host, the wildcard bind 0.0.0.0, or a
// non-http scheme (file:// etc). null/empty (clear) is allowed.
export function isAllowedTargetApiUrl(raw: string | null | undefined): boolean {
  if (!raw) return true;
  let parsed: URL;
  try {
    parsed = new URL(raw);
  } catch {
    return false;
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") return false;
  let host = parsed.hostname.toLowerCase();
  if (!host) return false;
  // Strip IPv6 brackets, then normalize IPv4-mapped IPv6 (::ffff:127.0.0.1).
  // Node's URL parser rewrites the mapped v4 into hex pairs ("7f00:1"), so
  // both the dotted-decimal and the hex-pair forms are expanded.
  if (host.startsWith("[") && host.endsWith("]")) host = host.slice(1, -1);
  if (host.startsWith("::ffff:")) {
    const rest = host.slice("::ffff:".length);
    if (/^\d{1,3}(?:\.\d{1,3}){3}$/.test(rest)) {
      host = rest;
    } else {
      const groups = rest.split(":");
      if (groups.length === 2) {
        const v = (parseInt(groups[0], 16) << 16) | parseInt(groups[1], 16);
        host = `${(v >>> 24) & 255}.${(v >>> 16) & 255}.${(v >>> 8) & 255}.${v & 255}`;
      }
    }
  }
  if (host === "localhost" || host === "127.0.0.1" || host === "::1") return true;
  if (host === "0.0.0.0") return false; // wildcard bind, not a real backend
  return isPrivateIPv4(host);
}

// isPrivateIPv4 reports whether host is a private LAN IPv4 address:
// 10/8, 172.16/12, 192.168/16.
function isPrivateIPv4(host: string): boolean {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(host);
  if (!m) return false;
  const octets = m.slice(1).map(Number);
  if (octets.some((o) => o > 255)) return false;
  const [a, b] = octets;
  if (a === 10) return true;
  if (a === 172 && b >= 16 && b <= 31) return true;
  if (a === 192 && b === 168) return true;
  return false;
}

/**
 * Seed `targetApiBaseUrl` from the canonical desktop.json `apiUrl` so the
 * main-process auto-start path (which fires before the renderer IPC handler
 * `daemon:set-target-api-url` ever runs) gets the correct URL. Without this
 * seed the renderer-conditional fix in commit e4a69d314 (0.5.27) is a no-op
 * on cold launch: `targetApiBaseUrl === null` → `resolveActiveProfile()`
 * returns `DEFAULT_HEALTH_PORT` → the daemon falls back to
 * `daemon.DefaultServerURL = "ws://localhost:8080/ws"` (the SearXNG-on-8080
 * bug this whole class of fix was meant to prevent).
 *
 * Renderer IPC still wins — it calls `setTargetApiUrl` later and overwrites
 * this seed. We only seed when currently null so we never clobber a
 * renderer-provided value.
 *
 * Same F-027 allowlist as the renderer IPC: only loopback / private LAN
 * http(s) URLs are accepted.
 */
export function initTargetApiUrl(url: string): void {
  if (!isAllowedTargetApiUrl(url)) return;
  if (targetApiBaseUrl === null) targetApiBaseUrl = url;
}

export function setupDaemonManager(
  windowGetter: () => BrowserWindow | null,
): void {
  getMainWindow = windowGetter;

  ipcMain.handle("daemon:set-target-api-url", async (_e, url: string) => {
    // F-027: the target API URL drives daemon auth + token minting, so it
    // must point at a local/private backend only. Reject everything else.
    if (!isAllowedTargetApiUrl(url)) {
      return {
        ok: false,
        error: `target API URL rejected: only loopback / private http(s) URLs are allowed (got "${url}")`,
      };
    }
    const normalized = url || null;
    if (targetApiBaseUrl !== normalized) {
      console.log(`[daemon] target API URL set to ${normalized ?? "(none)"}`);
      targetApiBaseUrl = normalized;
      invalidateActiveProfile();
      await pollOnce();
    }
    return { ok: true };
  });
  ipcMain.handle("daemon:start", () => withGuard(() => startDaemon()));
  ipcMain.handle("daemon:stop", () => withGuard(() => stopDaemon()));
  ipcMain.handle("daemon:restart", () => withGuard(() => restartDaemon()));
  ipcMain.handle("daemon:get-status", () => fetchHealth());
  // The host's OS name, available regardless of daemon state. The Runtimes
  // page uses it as a fallback identity for "this machine" when no
  // app-managed daemon is reporting a device name (e.g. the daemon runs
  // out-of-band in WSL2). See desktop-runtimes-page.tsx.
  ipcMain.handle("daemon:get-host-name", () => hostname());
  ipcMain.handle(
    "daemon:sync-token",
    (_event, token: string, userId: string) => syncToken(token, userId),
  );
  ipcMain.handle("daemon:clear-token", () => clearToken());
  ipcMain.handle(
    "daemon:reauthenticate",
    (_event, token: string, userId: string) => reauthenticate(token, userId),
  );
  ipcMain.handle("daemon:is-cli-installed", async () => {
    const bin = await resolveCliBinary();
    return bin !== null;
  });
  ipcMain.handle("daemon:retry-install", async () => {
    cachedCliBinary = undefined;
    cliResolvePromise = null;
    // A retry-install may land a new CLI at a different version; drop the
    // cached version string so the next check re-reads the binary.
    cachedCliBinaryVersion = undefined;
    await bootstrapCli();
  });
  ipcMain.handle("daemon:get-prefs", () => loadPrefs());
  ipcMain.handle(
    "daemon:set-prefs",
    (_event, prefs: Partial<DaemonPrefs>) =>
      loadPrefs().then((cur) => {
        const merged = { ...cur, ...prefs };
        return savePrefs(merged).then(() => merged);
      }),
  );
  ipcMain.handle("daemon:auto-start", async () => {
    const prefs = await loadPrefs();
    if (!prefs.autoStart) return;
    const bin = await resolveCliBinary();
    if (!bin) return;
    const health = await fetchHealth();
    if (health.state === "running") {
      // Daemon is up but may be running an older CLI than the one we just
      // bundled. Restart it so the new binary actually takes effect.
      await ensureRunningDaemonVersionMatches();
      return;
    }
    await startDaemon();
  });

  ipcMain.on("daemon:start-log-stream", () => {
    const win = getMainWindow();
    if (win) startLogTail(win);
  });

  ipcMain.on("daemon:stop-log-stream", () => {
    stopLogTail();
  });

  // Reveal the daemon's log file in the user's default editor / Console
  // app. Acts as the escape hatch when the in-app log viewer isn't enough
  // (full history, complex search, copy-to-clipboard at scale).
  ipcMain.handle("daemon:open-log-file", async () => {
    const active = await ensureActiveProfile();
    const logPath = profileLogPath(active.name);
    if (!existsSync(logPath)) {
      return { success: false, error: "Log file not found yet" };
    }
    // shell.openPath returns "" on success, error string on failure.
    const error = await shell.openPath(logPath);
    return error === "" ? { success: true } : { success: false, error };
  });

  // First-run CLI install kicks off here. Status bar shows "Setting up…"
  // until the managed binary is on disk (instant on subsequent launches).
  currentState = "installing_cli";
  sendStatus({ state: "installing_cli" });
  void bootstrapCli();

  // F7 audit fix (memory 0.3.2-backlog): the before-quit handler lived
  // here AND in index.ts — they raced. index.ts now owns the single
  // ordered shutdown: stopServerManager → stopDaemon → app.quit. We
  // expose `stopDaemon` so index.ts can call it deterministically.
  // stopPolling/stopLogTail fire from inside stopDaemon itself so the
  // renderer log stream stops before the WS closes.
}
