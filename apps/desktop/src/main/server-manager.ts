// Server lifecycle manager for the localized desktop build.
//
// The Multica desktop app is now self-contained: it ships a Go HTTP/WS
// backend (`server`) and a schema migrator (`migrate`) inside the DMG
// alongside the agent runtime CLI (`multica`). This module is the
// "bring up the backend" layer that lives between PG becoming
// reachable and the daemon registering against it.
//
// Lifecycle on app start (in order):
//
//   1. Resolve which server port + database URL the active profile
//      points at. The profile's `config.json` (set by `multica setup`)
//      carries the API URL the daemon will use; we derive the listen
//      port from it.
//   2. Make sure PostgreSQL is reachable. If a server is already
//      listening on the expected host:port we skip step 3. Otherwise
//      we try `docker compose up -d postgres` from the repo root —
//      pgvector/pgvector:pg17, identical to what `make dev` uses. If
//      docker is not on PATH we fail with a precise error so the
//      login screen can show "start docker or PG manually" instead
//      of hanging on a generic "Failed to fetch".
//   3. Run the bundled `migrate` binary to apply the latest schema.
//      The migrator is one-shot: it exits 0 on success and a non-zero
//      code on failure (so the server-manager surfaces a clear
//      "schema migration failed: …" error rather than a silent skip).
//   4. Spawn the bundled `server` binary in the background, redirect
//      stdout+stderr to the profile's server.log, and poll /health
//      until the server reports live. The poll has a generous
//      timeout (60s) because the embedded-Postgres alternative path
//      (when one is wired up later) can take ~10s to init.
//   5. From this point the existing daemon-manager can register the
//      runtime against the live server. The two managers are
//      independent — the daemon polls its own health port (19545)
//      and the server's /health is what we poll here.
//
// On `app.before-quit` the server is stopped via `server:stop` IPC
// (or, if the user opted in to keep the server alive, the manager
// is a no-op). The server is killed with SIGTERM, waited 5s, then
// SIGKILL — the same pattern daemon-manager uses.

import { execFile, spawn, spawnSync } from "node:child_process";
import { existsSync } from "node:fs";
import { mkdir, readFile, rename, stat, writeFile } from "node:fs/promises";
import { homedir } from "node:os";
import { join } from "node:path";
import * as net from "node:net";
import { app, ipcMain } from "electron";
import {
  checkNativePgInstalled,
  startNativePg,
  stopNativePg,
  downloadAndExtractPg,
  runMigrationFlow,
  migrationForwardSignal,
  resolveNativePaths,
  type PgProgressEvent,
} from "./pg-bootstrap";
import { parseRuntimeConfig, type RuntimeConfig } from "../shared/runtime-config";
import { desktopConfigPath } from "./runtime-config-loader";

/**
 * Run startNativePg but push progress events into the module-level
 * `currentState` so the renderer's progress modal can render. We do
 * NOT directly write IPC here — the index.ts polling loop already
 * serialises currentState and pushes it to the renderer every 2s.
 */
async function startNativePgWithProgress(): Promise<void> {
  if (checkNativePgInstalled()) {
    currentState = { state: "downloading", phase: "initdb", percent: 0 };
    await startNativePg();
    return;
  }
  // No binary — download + extract + initdb in one chained call.
  await downloadAndExtractPg((evt) => {
    currentState = {
      state: "downloading",
      phase: evt.phase,
      percent: evt.percent,
      bytesDone: evt.bytesDone,
      bytesTotal: evt.bytesTotal,
    };
  });
  currentState = { state: "downloading", phase: "initdb", percent: 0 };
  await startNativePg();
}

/**
 * Wrapper that the renderer can invoke to opt-in to the docker →
 * native migration. Idempotent via the sentinel file written inside
 * pg-bootstrap.runMigrationFlow.
 */
export async function runMigrationFlowIfNeeded(opts: {
  confirmed: boolean;
}): Promise<{ status: "migrated"; dumpPath: string; dockerRows: number; nativeRows: number; durationMs: number } |
            { status: "skipped"; reason: string }> {
  const result = await runMigrationFlow({
    confirmed: opts.confirmed,
    onProgress: (phase, percent) => {
      // Best-effort progress push — the IPC loop will skip identical
      // consecutive frames via its equality check on currentState.
      const last = currentState;
      if (last.state === "downloading" || last.state === "running") return;
      // We don't have a "migrating" state currently — log only.
      void phase;
      void percent;
    },
  });
  if ("skipped" in result) return { status: "skipped", reason: result.reason };
  // Successful migration — write forward signal to desktop.json.
  try {
    const cfg = (await readDesktopConfig()) ?? {
      schemaVersion: 1 as const,
      apiUrl: "http://localhost:8090",
      wsUrl: "ws://localhost:8090/ws",
      appUrl: "http://localhost:3000",
    };
    await writeDesktopConfig({ ...cfg, ...migrationForwardSignal(result.dumpPath) });
    await debugLog(`migration complete: ${result.dumpPath} (${result.dockerRows} rows)`);
  } catch (err) {
    await debugLog(`post-migration desktop.json update failed (non-fatal): ${(err as Error).message}`);
  }
  return {
    status: "migrated",
    dumpPath: result.dumpPath,
    dockerRows: result.dockerRows,
    nativeRows: result.nativeRows,
    durationMs: result.durationMs,
  };
}

/**
 * Read desktop.json and return the parsed config. On ENOENT (first
 * launch), returns a minimal config derived from the default. We
 * intentionally re-implement this small helper rather than reach for
 * the full runtime-config-loader because that one is opinionated
 * about dev vs packaged paths; server-manager only cares about
 * ~/.multica/desktop.json.
 */
async function readDesktopConfig(): Promise<RuntimeConfig | null> {
  try {
    const raw = await readFile(desktopConfigPath(), "utf-8");
    return parseRuntimeConfig(raw);
  } catch (err) {
    if ((err as NodeJS.ErrnoException).code === "ENOENT") return null;
    return null;
  }
}

/**
 * Write desktop.json. Used by the PR 2 backend-resolver to persist
 * `pgBackend` so subsequent launches can short-circuit the picker.
 */
async function writeDesktopConfig(cfg: RuntimeConfig): Promise<void> {
  await writeFile(desktopConfigPath(), JSON.stringify(cfg, null, 2) + "\n", {
    mode: 0o600,
  });
}

const SERVER_START_TIMEOUT_MS = 60_000;
const SERVER_STOP_TIMEOUT_MS = 5_000;
const PROBE_INTERVAL_MS = 500;
const PG_PROBE_TIMEOUT_MS = 1_500;

type ServerStatus =
  | { state: "stopped" }
  | { state: "starting" }
  | {
      state: "downloading";
      phase: PgProgressEvent["phase"];
      percent: number;
      bytesDone?: number;
      bytesTotal?: number;
    }
  | { state: "running"; port: number; pid: number; backend: PgBackend }
  | { state: "failed"; error: string; recoverable?: boolean; hint?: ServerStatusFailedHint; backend?: PgBackend };

type ServerStatusFailedHint =
  | "install-brew"
  | "wait-download"
  | "check-port"
  | "install-native"
  | "network";

let currentState: ServerStatus = { state: "stopped" };
let serverProcess: ReturnType<typeof spawn> | null = null;

// 0.3.1 P1.1: in-flight guard. Concurrent calls to ensureServerUp (e.g. the
// renderer's 2s status poll racing with a manual server:ensure-up IPC) both
// pass the currentState==="running" early-return check, then both run the
// full startServer sequence and orphan the first child. Coalesce into a
// single in-flight Promise — mirrors daemon-manager.ts:868 withGuard shape.
let inflightEnsure: Promise<ServerStatus> | null = null;

// 0.3.1 P1.2: stopping flag. When stopServerManager is in flight, in-flight
// ensureServerUp must NOT write currentState="running" for a server the
// caller is about to kill. Checked at the success path of runEnsureServerUp.
let stopping = false;

// Module-level debug log so we can trace server-manager activity even
// when macOS unified logging doesn't capture it (Electron's main
// process console output is invisible without stderr redirect).
const DEBUG_LOG = join(homedir(), ".multica", "server-manager.log");
async function debugLog(msg: string): Promise<void> {
  const line = `[${new Date().toISOString()}] ${msg}\n`;
  try {
    const { appendFile } = await import("node:fs/promises");
    await appendFile(DEBUG_LOG, line, "utf-8");
  } catch {
    /* ignore */
  }
}

// Mirror of the multica daemon's per-profile directory layout
// (~/.multica/profiles/<name>/). Kept private so we don't introduce a
// cross-module import surface; if the daemon-manager ever exports this,
// we can switch.
function profileDir(profile: string): string {
  return join(homedir(), ".multica", "profiles", profile);
}

function serverLogPath(profile: string): string {
  return join(profileDir(profile), "server.log");
}

// 50 MB log-rotation threshold (0.5.37 audit issues #2/#8: prevent unbounded growth).
const LOG_ROTATE_THRESHOLD_BYTES = 50 * 1024 * 1024;

// Rotate logPath → logPath.1 via POSIX-atomic rename when the live file exceeds
// the threshold. Existing `.1` is overwritten (fork's single-rotation retention).
async function rotateLogIfNeeded(logPath: string): Promise<void> {
  try {
    const stats = await stat(logPath);
    if (stats.size > LOG_ROTATE_THRESHOLD_BYTES) {
      await rename(logPath, `${logPath}.1`);
    }
  } catch (err) {
    if ((err as NodeJS.ErrnoException).code !== "ENOENT") {
      console.warn(`[server-manager] log rotation check failed for ${logPath}:`, err);
    }
  }
}

function serverEnvPath(profile: string): string {
  return join(profileDir(profile), ".env");
}

// pgProbeUrl is exported for tests. Production callers read it from
// module scope; the 0.5.31 P1 loopback-only guard is pinned by
// server-manager.test.ts::pgProbeUrl.
export function pgProbeUrl(): string {
  // The multica-server binary reads POSTGRES_* from the env first, then
  // falls back to DATABASE_URL. For probing we use the same defaults
  // that the docker-compose.yml used to use (v0.2.x legacy reference).
  //
  // 0.5.31 P1 (audit, DATABASE_URL leak): only accept a loopback
  // DATABASE_URL. The value is persisted verbatim to
  // ~/.multica/profiles/<name>/.env at first launch (serializeEnvFile),
  // so a stale/foreign DATABASE_URL in the caller's shell (e.g. a prod
  // DB, a cloud host, or a colleague's compose file) would be baked
  // into the server's config and the probe would reach outside the
  // machine. Fork-local contract: the server must stay on loopback.
  // Malformed URLs and any non-loopback host fall back to the default.
  const DEFAULT_URL =
    "postgres://multica:multica@127.0.0.1:5432/multica?sslmode=disable";
  const raw = process.env["DATABASE_URL"];
  if (!raw) return DEFAULT_URL;
  try {
    const u = new URL(raw);
    const host = u.hostname.toLowerCase();
    if (
      host === "localhost" ||
      host === "127.0.0.1" ||
      host === "::1" ||
      host === "[::1]"
    ) {
      return raw;
    }
  } catch {
    // Not a parseable URL — fall back to default (same as unset).
  }
  return DEFAULT_URL;
}

async function probePg(): Promise<boolean> {
  const url = pgProbeUrl();
  try {
    const u = new URL(url);
    return new Promise((resolve) => {
      const net = require("node:net") as typeof import("node:net");
      const sock = new net.Socket();
      let done = false;
      const finish = (ok: boolean) => {
        if (done) return;
        done = true;
        try {
          sock.destroy();
        } catch {
          /* ignore */
        }
        resolve(ok);
      };
      sock.setTimeout(PG_PROBE_TIMEOUT_MS);
      sock.once("timeout", () => finish(false));
      sock.once("error", () => finish(false));
      sock.connect(Number(u.port) || 5432, u.hostname, () => finish(true));
    });
  } catch {
    return false;
  }
}

async function waitForPg(maxMs = 30_000): Promise<void> {
  const start = Date.now();
  while (Date.now() - start < maxMs) {
    if (await probePg()) {
      // PR 3 (Stage D-2): no more docker exec pg_isready — we now use
      // the identity probe (probeMulticaPg) which verifies user=multica
      // + db=multica + pgcrypto extension. This catches the case where
      // 5432 is reachable but it's the user's unrelated brew PG.
      if (await probeMulticaPg()) return;
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error(`PostgreSQL did not become reachable on 5432 within ${maxMs / 1000}s`);
}

/** Resolve a resource shipped under resources/ in dev and packaged builds. */
function resolveResourcePath(...segments: string[]): string {
  // In a packaged app the resources are physically unpacked alongside
  // app.asar, not inside it. child_process APIs do not get asar
  // redirects, so we must point at the real files.
  if (app.isPackaged) {
    return join(process.resourcesPath, "app.asar.unpacked", "resources", ...segments);
  }
  return join(app.getAppPath(), "resources", ...segments);
}

function resolveServerBinary(): string {
  const name = process.platform === "win32" ? "server.exe" : "server";
  return resolveResourcePath("bin", name);
}

function resolveMigrateBinary(): string {
  const name = process.platform === "win32" ? "migrate.exe" : "migrate";
  return resolveResourcePath("bin", name);
}

function parseServerPort(apiUrl: string): number {
  try {
    const u = new URL(apiUrl);
    return Number(u.port) || 8090;
  } catch {
    return 8090;
  }
}

interface ServerEnv {
  PORT: string;
  DATABASE_URL: string;
  JWT_SECRET: string;
  MULTICA_PUBLIC_URL: string;
  // Fork overrides the upstream 2h default to fail-fast on stale queued
  // tasks (single-user desktop, not self-hosted). Override here only if a
  // future desktop workflow legitimately needs to wait longer than 5m
  // behind a long-running task.
  MULTICA_TASK_QUEUED_TTL?: string;
  POSTGRES_USER?: string;
  POSTGRES_PASSWORD?: string;
  POSTGRES_DB?: string;
}

async function buildServerEnv(profile: string, port: number): Promise<ServerEnv> {
  // Read the active profile's token so the server uses the same JWT
  // secret the desktop UI was issued. Falls back to a freshly-generated
  // 32-byte secret on first launch; the value is persisted so a
  // desktop reinstall doesn't invalidate live tokens.
  const envFile = serverEnvPath(profile);
  let env: ServerEnv;
  if (existsSync(envFile)) {
    const raw = await readFile(envFile, "utf-8");
    env = parseEnvFile(raw);
    // Pin PORT to the freshly-derived port so a stale or missing PORT
    // line in the persisted .env can't let a shell-leaked process.env.PORT
    // (or an old 8080) survive into the spawned server. Same class as
    // the daemon --server-url fix (commit e4a69d314, 0.5.27): the
    // desktop-owned URL must win over whatever the shell environment
    // happens to carry.
    if (env.PORT !== String(port)) env.PORT = String(port);
  } else {
    env = {
      PORT: String(port),
      DATABASE_URL: pgProbeUrl(),
      JWT_SECRET: randomHex(32),
      MULTICA_PUBLIC_URL: `http://localhost:${port}`,
      MULTICA_TASK_QUEUED_TTL: "5m",
    };
    await mkdir(profileDir(profile), { recursive: true });
    await writeFile(envFile, serializeEnvFile(env), { mode: 0o600 });
  }
  return env;
}

function parseEnvFile(raw: string): ServerEnv {
  const out: Record<string, string> = {};
  for (const line of raw.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) continue;
    const eq = trimmed.indexOf("=");
    if (eq <= 0) continue;
    const key = trimmed.slice(0, eq).trim();
    const value = trimmed.slice(eq + 1).trim().replace(/^['"]|['"]$/g, "");
    out[key] = value;
  }
  return out as unknown as ServerEnv;
}

function serializeEnvFile(env: ServerEnv): string {
  return [
    `# Generated by Multica desktop (server-manager) on first launch.`,
    `# Edit PORT or DATABASE_URL here to point the server elsewhere;`,
    `# the file is sourced before the bundled server is spawned.`,
    `PORT=${env.PORT}`,
    `DATABASE_URL=${env.DATABASE_URL}`,
    `JWT_SECRET=${env.JWT_SECRET}`,
    `MULTICA_PUBLIC_URL=${env.MULTICA_PUBLIC_URL}`,
    // Fork-specific override: single-user desktop fails fast on stale
    // queued tasks (5m) instead of upstream's 2h self-hosted default.
    `MULTICA_TASK_QUEUED_TTL=${env.MULTICA_TASK_QUEUED_TTL ?? "5m"}`,
    "",
  ].join("\n");
}

function randomHex(bytes: number): string {
  // No native `randomBytes` in the renderer; we use Node's crypto via
  // a tiny require to avoid pulling it into the top-level imports
  // (kept local for the spawn-time-only path).
  const nodeCrypto = require("node:crypto") as typeof import("node:crypto");
  return nodeCrypto.randomBytes(bytes).toString("hex");
}

/**
 * Run the bundled `migrate up` binary against the current PG.
 *
 * P0 structural guard: if `backend === "external"`, this function
 * REFUSES to run. The bundled migrate binary contains DROP TABLE
 * migrations (029, 046, 103) that are forward-only on a freshly-
 * initdb'd native pgdata but DESTRUCTIVE on any pre-existing schema.
 * This is the data-safety line — see memory file
 * `multica-0.3.0-standalone-2026-07-02.md` for the original incident.
 * The check lives here (not just in the caller) so a future refactor
 * of `ensureServerUp` cannot accidentally invoke migrate against an
 * external backend without tripping this error.
 *
 * Exported for testability — see server-manager.test.ts.
 */
export async function runMigrate(
  profile: string,
  env: ServerEnv,
  backend?: PgBackend,
): Promise<void> {
  if (backend === "external") {
    throw new Error(
      "runMigrate refused: backend=external. " +
        "External backends are owned by whoever set them up " +
        "(Docker pgdata, manual initdb, prior v0.3.0 install). " +
        "Running migrate would invoke DROP TABLE migrations (029/046/103) " +
        "and destroy the existing schema. No-op.",
    );
  }
  const bin = resolveMigrateBinary();
  if (!existsSync(bin)) {
    throw new Error(
      `migrate binary not found at ${bin} — the desktop bundle is incomplete. ` +
        "Re-install the app or run `make build` in apps/desktop/.",
    );
  }
  await new Promise<void>((resolve, reject) => {
    execFile(
      bin,
      ["up"],
      {
        cwd: profileDir(profile),
        env: { ...process.env, ...env, MULTICA_RESOURCES_DIR: resolveResourcePath() },
        timeout: 60_000,
      },
      (err, stdout, stderr) => {
        if (err) {
          reject(
            new Error(
              `migrate up failed: ${err.message}\nstdout: ${stdout}\nstderr: ${stderr}`,
            ),
          );
          return;
        }
        // log migrate output to server.log for traceability
        const log = serverLogPath(profile);
        require("node:fs/promises")
          .appendFile(
            log,
            `[migrate] ${new Date().toISOString()} ${stdout}\n${stderr}\n`,
          )
          .catch(() => undefined);
        resolve();
      },
    );
  });
}

async function startServer(profile: string, port: number): Promise<void> {
  const bin = resolveServerBinary();
  if (!existsSync(bin)) {
    throw new Error(
      `server binary not found at ${bin} — the desktop bundle is incomplete. ` +
        "Re-install the app or run `make build` in apps/desktop/.",
    );
  }
  let env = await buildServerEnv(profile, port);
  await mkdir(profileDir(profile), { recursive: true });
  // 0.3.15 ship: the install handler reads the claude_science manifest
  // from MULTICA_RESOURCES_DIR. The directory lives under the app's
  // own bundle (app.asar.unpacked/resources/), not the profile dir, so
  // we resolve it through resolveResourcePath and inject it here at
  // spawn time. We do NOT persist it to the per-profile env file —
  // that file is user-data and should not change between app upgrades.
  const manifestRoot = resolveResourcePath();
  const envWithResources: Record<string, string | undefined> = {
    ...env,
    MULTICA_RESOURCES_DIR: manifestRoot,
  };
  // 0.5.37 audit: rotate server.log if it exceeds 50 MB before opening the fd.
  await rotateLogIfNeeded(serverLogPath(profile));
  const logFd = await require("node:fs/promises").open(serverLogPath(profile), "a");
  // P2 fix (memory multica-0.3.2): the previous build opened the log but
  // never wrote a startup marker, so users (and us) couldn't tell whether
  // the spawn actually took. When the bundled server quietly drops its
  // stderr to a non-flushed pipe (e.g. inside an asar.unpacked binary that
  // doesn't fflush on every write), server.log stays 0 bytes and every
  // subsequent issue-debugging session starts blind. Write a single
  // structured marker that ALSO flushes the fd so we always see at least
  // one line per server lifetime.
  const bootMarker = `[server-manager] spawning bundled server at ${new Date().toISOString()} profile=${profile} port=${port}\n`;
  await logFd.write(bootMarker);
  await logFd.sync().catch((err) => {
    // Audit 0.3.2 P1: do NOT silently swallow sync errors — a failed
    // sync means the disk may be full or the profile dir just lost
    // write perms, and the user will be debugging a 0-byte server.log
    // without context. Log via console so it surfaces in the main
    // process's stderr and the renderer can pick it up via the
    // server-status banner.
    console.warn(
      `[server-manager] log fd sync failed for ${serverLogPath(profile)}:`,
      err instanceof Error ? err.message : String(err),
    );
  });
  const child = spawn(bin, [], {
    cwd: profileDir(profile),
    env: { ...process.env, ...envWithResources },
    stdio: ["ignore", logFd, logFd],
    detached: false,
  });
  serverProcess = child;
  child.on("exit", (code, signal) => {
    const log = `[server] exit code=${code} signal=${signal} at ${new Date().toISOString()}\n`;
    require("node:fs/promises")
      .appendFile(serverLogPath(profile), log)
      .catch(() => undefined);
    if (currentState.state === "running" || currentState.state === "starting") {
      currentState = { state: "stopped" };
    }
  });
}

async function waitForServer(port: number, maxMs = SERVER_START_TIMEOUT_MS): Promise<void> {
  const start = Date.now();
  const url = `http://127.0.0.1:${port}/health`;
  while (Date.now() - start < maxMs) {
    try {
      const res = await fetch(url, { signal: AbortSignal.timeout(2_000) });
      if (res.status < 500) return;
    } catch {
      /* not up yet */
    }
    await new Promise((r) => setTimeout(r, PROBE_INTERVAL_MS));
  }
  throw new Error(`server did not become healthy on :${port} within ${maxMs}ms`);
}

async function stopServer(): Promise<void> {
  if (serverProcess && serverProcess.exitCode === null) {
    const child = serverProcess;
    child.kill("SIGTERM");
    const exited = await Promise.race([
      new Promise<number>((resolve) => child.once("exit", (code) => resolve(code ?? 0))),
      new Promise<number>((resolve) => setTimeout(() => resolve(-1), SERVER_STOP_TIMEOUT_MS)),
    ]);
    if (exited === -1) {
      child.kill("SIGKILL");
    }
  }
  serverProcess = null;
  currentState = { state: "stopped" };
}

/**
 * Bring up the full backend stack for a given profile. Idempotent —
 * if the server is already running, the call short-circuits. If a
 * previous call is still in flight, the same Promise is returned so
 * concurrent IPC fires (renderer 2s poll + manual retry) coalesce
 * into one backend start.
 *
 * Returns a status object the caller (daemon-manager, IPC) can
 * surface to the renderer.
 */
export async function ensureServerUp(profile: string, apiUrl: string): Promise<ServerStatus> {
  await debugLog(`ensureServerUp called: profile=${profile} apiUrl=${apiUrl}`);
  // 0.3.1 P1.1: coalesce concurrent calls into one in-flight Promise.
  if (inflightEnsure) return inflightEnsure;
  if (currentState.state === "running") return currentState;
  inflightEnsure = (async () => {
    try {
      return await runEnsureServerUp(profile, apiUrl);
    } finally {
      inflightEnsure = null;
    }
  })();
  return inflightEnsure;
}

async function runEnsureServerUp(profile: string, apiUrl: string): Promise<ServerStatus> {
  // 0.3.1 P1.2: if stopServerManager is in flight, do not start anything.
  if (stopping) return currentState;
  currentState = { state: "starting" };
  const port = parseServerPort(apiUrl);

  // PR 3 (Stage D-2): simplified picker. Two outcomes only —
  //   "external"  : a multica PG is already on 5432 (manual initdb,
  //                  previous v0.3.0 install, brew PG with same creds).
  //   "native"    : we need to bring up our bundled Postgres.app.
  //                  startNativePg handles binary-missing → downloadAndExtractPg.
  let backend: "native" | "external" = "native";
  try {
    const cfg = await readDesktopConfig();
    const pgAlreadyReachable = await probeMulticaPg();
    // Narrow desktop.json's loose pgBackend string to the picker's
    // narrow union. Anything else (e.g. legacy "docker" from a v0.2.x
    // install) is ignored — we now always pick via the 2-way picker.
    const preferredRaw = cfg?.pgBackend;
    const preferred: "native" | "external" | "auto" | undefined =
      preferredRaw === "native" || preferredRaw === "external" || preferredRaw === "auto"
        ? preferredRaw
        : undefined;
    backend = pickPgBackend({
      pgReachable: pgAlreadyReachable,
      preferred,
    });
    await debugLog(`backend selected: ${backend} (pg=${pgAlreadyReachable})`);
  } catch (err) {
    await debugLog(`backend selection failed (continuing with native): ${(err as Error).message}`);
  }

  try {
    if (!(await probePg())) {
      // No PG on 5432. PR 3: native is the only path. startNativePg
      // internally handles missing-binary → downloadAndExtractPg →
      // extract → initdb → pg_ctl start. We also push status updates
      // so the renderer's progress modal can paint.
      await debugLog("PG probe failed; starting native PG (PR 3 path)");
      currentState = { state: "downloading", phase: "fetch", percent: 0 };
      await startNativePgWithProgress();
    }
    await debugLog("waiting for PG to be reachable");
    // R15: native cold start can take 20s+ (initdb + postmaster +
    // buffer cache init). 60s ceiling covers download + initdb + start.
    await waitForPg(60_000);
    await debugLog("PG reachable; running migrate");
    const env = await buildServerEnv(profile, port);
    // P0 safety: when we picked the external backend (a multica PG
    // already on 5432 — Docker pgdata, manual initdb, or a previous
    // v0.3.0 install), its schema is owned by whoever set it up.
    // The bundled migrate binary contains DROP TABLE migrations
    // (029, 046, 103) that are forward-only on a fresh native pgdata
    // but DESTRUCTIVE on a pre-existing schema the user has been
    // accumulating data in — running migrate against Docker pgdata
    // silently deletes daemon_pairing_session / runtime_usage /
    // task_usage_* tables. Always trust an external backend's
    // existing schema and skip migrate at the call site.
    // runMigrate() ALSO refuses `backend === "external"` defensively,
    // so this double-guard means any future caller is safe.
    if (backend === "external") {
      await debugLog("backend=external; skipping migrate (trust existing schema)");
    } else {
      // The PG port may be open before the database is truly ready to
      // accept queries (docker-proxy forwards the port immediately,
      // but PG inside the container is still initialising). Retry
      // migrate up to 3 times with a 2 s backoff — migrate is
      // idempotent on a fresh native pgdata, so re-running is safe.
      for (let attempt = 0; attempt < 3; attempt++) {
        try {
          await runMigrate(profile, env, backend);
          break;
        } catch (migErr) {
          if (attempt === 2) throw migErr;
          await debugLog(`migrate attempt ${attempt + 1} failed, retrying in 2s: ${(migErr as Error).message}`);
          await new Promise((r) => setTimeout(r, 2_000));
        }
      }
    }
    await debugLog("migrate done; spawning server");
    await startServer(profile, port);
    await waitForServer(port);
    // 0.3.1 P1.2: if stopServerManager is in flight, do NOT report
    // "running" for a server the caller is about to kill. The
    // in-flight child will be reaped by stopServer()'s SIGTERM
    // pathway. We do not re-throw — stopServerManager is a best-
    // effort cleanup and the server is genuinely up for a moment.
    if (stopping) {
      await debugLog("stopping=true observed after waitForServer; not reporting running");
      return currentState;
    }
    const pid = serverProcess?.pid ?? 0;
    currentState = { state: "running", port, pid, backend };
    await debugLog(`server is running: port=${port} pid=${pid} backend=${backend}`);

    // Persist the resolved backend to desktop.json so subsequent
    // launches can short-circuit the picker. Best-effort — failure
    // here is logged but does not surface to the user, since the
    // server is already up.
    try {
      const cfg = (await readDesktopConfig()) ?? {
        schemaVersion: 1 as const,
        apiUrl: apiUrl,
        wsUrl: apiUrl.replace(/^http/, "ws") + "/ws",
        appUrl: apiUrl.replace(/:\d+/, ":3000"),
      };
      if (cfg.pgBackend !== backend) {
        // Re-use the existing pgBackend field if it's a valid v0.3.0
        // value; otherwise default to "native" for the merge. Legacy
        // values like "docker" from v0.2.x are not persisted forward.
        const existing = cfg.pgBackend;
        const safeExisting: "native" | "external" | undefined =
          existing === "native" || existing === "external" ? existing : "native";
        await writeDesktopConfig({ ...cfg, pgBackend: safeExisting });
        await debugLog(`persisted pgBackend=${safeExisting} to desktop.json`);
      }
    } catch (err) {
      await debugLog(`failed to persist pgBackend (non-fatal): ${(err as Error).message}`);
    }

    return currentState;
  } catch (err) {
    const msg = (err as Error).message;
    await debugLog(`ensureServerUp failed: ${msg}`);
    // PR 3 (Stage D-2): Docker path is gone. The only actionable
    // hint is "install-native" (quarantine / version mismatch). For
    // download / network errors we surface "network".
    let hint: ServerStatusFailedHint | undefined;
    if (
      msg.toLowerCase().includes("quarantine") ||
      msg.toLowerCase().includes("version") ||
      msg.includes("native PG must be major version")
    ) {
      hint = "install-native";
    } else if (
      msg.includes("fetch failed") ||
      msg.includes("ENETUNREACH") ||
      msg.includes("ETIMEDOUT") ||
      msg.includes("ECONNREFUSED")
    ) {
      hint = "network";
    }

    currentState = hint
      ? { state: "failed", error: msg, recoverable: true, hint, backend }
      : { state: "failed", error: msg, backend };
    return currentState;
  }
}

export function getServerStatus(): ServerStatus {
  return currentState;
}

export async function stopServerManager(): Promise<void> {
  // 0.3.1 P1.2: set the stopping flag first so the in-flight
  // runEnsureServerUp observes it at the next await checkpoint and
  // refuses to write currentState="running". Then await any in-flight
  // ensureServerUp Promise (P1.1) so the child it spawned is registered
  // in serverProcess before we SIGTERM it. Finally clear stopping so
  // the next launch starts clean.
  stopping = true;
  try {
    if (inflightEnsure) {
      try {
        await inflightEnsure;
      } catch {
        /* swallow — we're tearing down anyway */
      }
    }
  } catch {
    /* ignore */
  }
  try {
    // PR 2 (Stage D-1): stop native PG first so the pg_ctl stop signal
    // doesn't race with the server's SIGTERM. The server holds open
    // connections to PG; killing server first can cause pg_ctl to log
    // "received fast shutdown request" warnings. Order matters.
    await stopNativePg();
  } catch {
    /* best-effort */
  }
  await stopServer();
  stopping = false;
}

export function setupServerManager(windowGetter: () => Electron.BrowserWindow | null): void {
  // Wire IPC so the renderer can poll the server status (used by the
  // login screen's "Failed to fetch" error path to distinguish
  // "server starting" from "PG not reachable" from "wrong port").
  void windowGetter;
  ipcMain.handle("server:get-status", () => getServerStatus());
  ipcMain.handle("server:ensure-up", async (_e, profile: string, apiUrl: string) => {
    return ensureServerUp(profile, apiUrl);
  });
  ipcMain.handle("server:stop", () => stopServerManager());
  // PR 2 (Stage D-1): allow the renderer to ask whether the native
  // PG binary is on disk so the install-brew banner can also offer
  // the Postgres.app path. We deliberately do NOT call any of the
  // start/init/stop helpers from the renderer — those are main-only.
  ipcMain.handle("pg:check-installed", () => checkNativePgInstalled());
  // PR 3 (Stage D-2) migration control plane lives in apps/desktop/src/main/index.ts
  // (`server:should-offer-migration` + `server:run-migration`). Do NOT
  // register `pg:run-migration` / `pg:get-migration-status` here —
  // they're dead code (preload wires the renderer to the index.ts
  // channels) and double-registration is exactly the anti-pattern that
  // caused `multica-ipc-registration-order.md` regression.
}

// -----------------------------------------------------------------------------
// PR 3 (Stage D-2): trivial 2-way backend picker + multica-identity probe
// -----------------------------------------------------------------------------
// The picker is intentionally tiny in v0.3.0 — the previous docker/local/
// native/auto precedence is gone. Two outcomes only:
//   "external" : a multica PG is already on 5432
//   "native"   : we need to bring up our bundled Postgres.app
//
// probeMulticaPg (unchanged from PR 1) still does the identity check so
// we never accidentally treat the user's brew PG as our PG.

export type PgBackend = "native" | "external";

export interface PickPgBackendArgs {
  pgReachable: boolean;
  preferred?: "native" | "external" | "auto";
}

/**
 * Pure function. No I/O. Decides which PG backend to use for this launch.
 *
 * Precedence:
 *   1. If a multica PG is already on 5432, use it ("external").
 *      This covers manual initdb, previous v0.3.0 installs, and
 *      the user's brew PG with matching creds + pgcrypto.
 *   2. Otherwise bring up native (download + extract + initdb + start).
 *
 * `preferred` is honoured only for forward-compat with users who
 * manually edited desktop.json — new code should always let the picker
 * decide.
 */
export function pickPgBackend(args: PickPgBackendArgs): PgBackend {
  if (args.pgReachable) return "external";
  if (args.preferred === "external") return "native"; // can't honour — fall through
  return "native";
}

/**
 * Pure-function-shaped TCP probe. Kept private; probeMulticaPg composes
 * this with an identity check.
 */
function probeTcp(port: number, timeoutMs: number): Promise<boolean> {
  return new Promise((resolve) => {
    const sock = new net.Socket();
    let done = false;
    const close = (result: boolean) => {
      if (!done) {
        done = true;
        sock.destroy();
        resolve(result);
      }
    };
    sock.setTimeout(timeoutMs);
    sock.once("connect", () => close(true));
    sock.once("timeout", () => close(false));
    sock.once("error", () => close(false));
    sock.connect(port, "127.0.0.1");
  });
}

/**
 * Probe 127.0.0.1:5432 and confirm it is actually the Multica PG
 * (current_user='multica' AND current_database='multica' AND
 * the pgcrypto extension is installed AND the schema_migrations
 * table exists). Without the schema_migrations check, we'd happily
 * connect to a user's unrelated brew PG that happens to have a
 * multica user/db/pgcrypto (e.g. dev environment) — `schema_migrations`
 * is created only by the multica migrate binary on first initdb,
 * so its presence is the most reliable multica-only signal.
 *
 * psql is resolved from $PATH first; falls back to the Homebrew shim
 * location that ships on Apple Silicon.
 */
export async function probeMulticaPg(): Promise<boolean> {
  if (!(await probeTcp(5432, PG_PROBE_TIMEOUT_MS))) return false;
  // Try several psql candidates — packaged app does not inherit
  // login shell PATH. PR 3 (Stage D-2): the bundled native PG is the
  // preferred probe target (we always just installed it), so we
  // check there first. Only if it's missing do we fall back to the
  // user's homebrew install.
  const candidates = [
    join(resolveNativePaths().pgBin, "psql"),
    "/opt/homebrew/opt/postgresql@17/bin/psql",
    "/opt/homebrew/opt/postgresql@16/bin/psql",
    "/opt/homebrew/opt/postgresql@15/bin/psql",
    "/opt/homebrew/bin/psql",
    "/usr/local/bin/psql",
  ];
  for (const psql of candidates) {
    try {
      if (!existsSync(psql)) continue;
      const res = spawnSync(
        psql,
        [
          "-U", "multica",
          "-d", "multica",
          "-h", "127.0.0.1",
          "-p", "5432",
          "-tAc",
          // 0.3.1 P1.6: 4th field — schema_migrations table exists.
          // Single SQL keeps the probe at one psql roundtrip.
          "SELECT current_user || '|' || current_database() || '|' || " +
            "CASE WHEN EXISTS(SELECT 1 FROM pg_extension WHERE extname='pgcrypto') THEN 't' ELSE 'f' END || " +
            "'|' || CASE WHEN EXISTS(SELECT 1 FROM information_schema.tables " +
            "  WHERE table_schema='public' AND table_name='schema_migrations') " +
            "THEN 't' ELSE 'f' END",
        ],
        {
          env: { ...process.env, PGPASSWORD: "multica" },
          encoding: "utf-8",
          timeout: 5_000,
        },
      );
      if (res.status !== 0) continue;
      const out = res.stdout.trim();
      const [user, db, pgcrypto, schemaMigrations] = out.split("|");
      if (
        user === "multica" &&
        db === "multica" &&
        pgcrypto === "t" &&
        schemaMigrations === "t"
      ) {
        return true;
      }
      return false;
    } catch {
      continue;
    }
  }
  return false;
}
