// Native PostgreSQL bootstrap for the Multica desktop app.
//
// This module owns the entire "native" PG lifecycle for the standalone
// (Docker-free) Multica build. It is intentionally written in the same
// shape as `cli-bootstrap.ts` so the two code paths share a mental model:
// a small async surface, no Electron coupling at the top level (only
// `app.getPath("userData")` is consumed), and pure-function-like helpers
// where possible.
//
// The "native" backend ships Postgres.app binaries that the app downloads
// from the PostgresApp/PostgresApp GitHub release on first launch (see
// `resources/pg/manifest.json`). After the first successful download
// the binary lives at:
//
//   ~/Library/Application Support/Multica/pg/17.4/
//     bin/postgres
//     bin/initdb
//     bin/pg_ctl
//     bin/pg_dump
//     bin/pg_restore
//     lib/...
//
// On boot, `startNativePg()` either reuses an existing pgdata directory
// (idempotent), runs `initdb` once, then `pg_ctl start`. Everything is
// idempotent — re-launching the app reuses the existing pgdata dir.
//
// For the v0.2.x → v0.3.0 migration we expose `runMigrationFlow` which
// detects an existing Docker `multica_postgres-1` container, runs
// `pg_dump` from inside it, and `pg_restore`s the result into the
// freshly-initialised native pgdata. The Docker volume is left intact
// for 30 days as a rollback safety net (the user's dump file is also
// kept permanently under `~/.multica/backups/`).

import { execFile, execFileSync, spawn, spawnSync } from "node:child_process";
import { createReadStream, createWriteStream } from "node:fs";
import { statSync } from "node:fs";
import {
  appendFileSync,
  existsSync,
  readFileSync,
  unlinkSync,
  writeFileSync,
  openSync,
  closeSync,
  renameSync,
} from "node:fs";
import { mkdir, rm } from "node:fs/promises";
import { tmpdir, homedir } from "node:os";
import { dirname, join } from "node:path";
import { pipeline } from "node:stream/promises";
import { Readable } from "node:stream";
import * as net from "node:net";
import { app } from "electron";

/**
 * The Postgres.app version we expect to find in `pg/17.4/`. Locked to
 * match the tarball we'll ship in Stage D-2 — if the user has 17.5 or
 * 16.4 installed, we refuse to start and tell them to re-download. This
 * is intentional: schema migration behaviour diverges across minor
 * versions, and we'd rather fail loudly than silently corrupt data.
 */
const PG_VERSION_REQUIRED_MAJOR = "17";
const PG_VERSION_DIR = "17.4";

/** Postgres.app download URL. PR 3 will use this for the auto-download. */
export const PG_APP_URL = "https://postgresapp.com/downloads.html";
export const PG_APP_REQUIRED_VERSION = PG_VERSION_DIR;

// ---------------------------------------------------------------------------
// Path resolution
// ---------------------------------------------------------------------------
// All paths are derived from `app.getPath("userData")` so they follow
// Electron's standard macOS layout: `~/Library/Application Support/<appName>`.
// For the packaged Multica app this resolves to
// `~/Library/Application Support/Multica/`. For dev (electron-vite) it
// resolves to a per-build tmp dir.
//
// `app.getPath` is safe to call lazily — Electron is always ready by the
// time the server-manager invokes us — but we wrap it in functions (not
// module-level constants) so test mocks of `electron` can re-target.

function userDataDir(): string {
  return app.getPath("userData");
}

function pgHome(): string {
  return join(userDataDir(), "pg", PG_VERSION_DIR);
}

function pgBin(): string {
  return join(pgHome(), "bin");
}

function postgresBin(): string {
  return join(pgBin(), "postgres");
}

function initdbBin(): string {
  return join(pgBin(), "initdb");
}

function pgCtlBin(): string {
  return join(pgBin(), "pg_ctl");
}

function pgdata(): string {
  return join(userDataDir(), "pgdata");
}

function pgLog(): string {
  return join(pgHome(), "pg.log");
}

function pgVersionFile(): string {
  return join(pgdata(), "PG_VERSION");
}

function postgresqlConf(): string {
  return join(pgdata(), "postgresql.conf");
}

/**
 * Public path-resolver for tests and IPC. The shape is stable so the
 * renderer can use it to surface the install location to the user.
 */
export interface NativePgPaths {
  pgHome: string;
  pgBin: string;
  postgresBin: string;
  initdbBin: string;
  pgCtlBin: string;
  pgdata: string;
  pgLog: string;
}

export function resolveNativePaths(): NativePgPaths {
  return {
    pgHome: pgHome(),
    pgBin: pgBin(),
    postgresBin: postgresBin(),
    initdbBin: initdbBin(),
    pgCtlBin: pgCtlBin(),
    pgdata: pgdata(),
    pgLog: pgLog(),
  };
}

// ---------------------------------------------------------------------------
// Presence check + version guard
// ---------------------------------------------------------------------------

/**
 * Returns true if a Postgres.app tarball has been placed under userData
 * and exposes all three binaries we need. Does NOT verify version —
 * callers that need a version match should call `detectInstalledPgVersion`.
 */
export function checkNativePgInstalled(): boolean {
  return [postgresBin(), initdbBin(), pgCtlBin()].every((p) => existsSync(p));
}

/**
 * Runs `<postgres_bin> --version` and returns the major version string
 * (e.g. `"17"`) or null if the binary is missing/unreadable/wrong
 * version. We deliberately only match the major version: a user with
 * 17.5 should still work, but a user with 16.4 will be told to upgrade.
 *
 * PR 3 will tighten this to a full semver match once we ship the
 * tarball and own the version.
 */
export async function detectInstalledPgVersion(): Promise<string | null> {
  if (!checkNativePgInstalled()) return null;
  try {
    const r = spawnSync(postgresBin(), ["--version"], {
      encoding: "utf-8",
      timeout: 3_000,
    });
    if (r.status !== 0) return null;
    // Postgres.app's version line is "postgres (PostgreSQL) 17.10 (Postgres.app)"
    // — note the closing paren before the version number. We use a
    // regex tolerant of both formats ("PostgreSQL 17.x" and
    // "PostgreSQL) 17.x") so we don't break if upstream Postgres.app
    // tweaks the output wording between releases.
    const m = r.stdout.match(/PostgreSQL[\s)]+(\d+)/i);
    if (!m) return null;
    return m[1];
  } catch {
    return null;
  }
}

// ---------------------------------------------------------------------------
// TCP probe (used internally and re-exported shape matches server-manager)
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Password file for initdb
// ---------------------------------------------------------------------------
// `initdb` requires `--pwfile` to be a regular file (not stdin) with
// a trailing newline. The password is the same as the default we
// configure in docker-compose.yml (`multica`) so the same DATABASE_URL
// works against both backends.

async function writePasswordFile(): Promise<string> {
  const tmpPath = join(
    tmpdir(),
    `multica-pw-${process.pid}-${Date.now()}`,
  );
  writeFileSync(tmpPath, "multica\n", { mode: 0o600 });
  return tmpPath;
}

// ---------------------------------------------------------------------------
// initdb
// ---------------------------------------------------------------------------

/**
 * Idempotent. If `pgdata/PG_VERSION` already exists, returns without
 * touching the data directory. Otherwise runs `initdb` with a 60s
 * timeout and patches `postgresql.conf` to bind explicitly to
 * 127.0.0.1:5432 (defends against hardened-sandbox defaults).
 */
export async function initPgDataDir(): Promise<void> {
  if (existsSync(pgVersionFile())) return;

  const version = await detectInstalledPgVersion();
  if (version !== PG_VERSION_REQUIRED_MAJOR) {
    throw new Error(
      `native PG must be major version ${PG_VERSION_REQUIRED_MAJOR} but found ` +
        `${version ?? "unknown"}. Re-download Postgres.app ${PG_VERSION_DIR} from ` +
        `${PG_APP_URL} and replace ${pgHome()}.`,
    );
  }

  await mkdir(pgdata(), { recursive: true });
  const pwFile = await writePasswordFile();
  try {
    await new Promise<void>((resolve, reject) => {
      execFile(
        initdbBin(),
        [
          "-D", pgdata(),
          "-U", "multica",
          "--auth=md5",
          "--pwfile", pwFile,
          "--encoding=UTF8",
          "--locale=en_US.UTF-8",
        ],
        { timeout: 60_000 },
        (err, stdout, stderr) => {
          if (err) {
            reject(
              new Error(
                `initdb failed: ${err.message}\nstdout: ${stdout}\nstderr: ${stderr}`,
              ),
            );
            return;
          }
          appendFileSync(
            pgLog(),
            `[initdb] ${new Date().toISOString()} ${stdout}\n${stderr}\n`,
          );
          resolve();
        },
      );
    });
  } finally {
    // Always scrub the temp pwfile, even on initdb failure — the file
    // is mode 0600 but the password is still in plaintext, and
    // `multica` is a constant so leaking it is a (small) credential
    // exposure we don't need to take.
    await rm(pwFile, { force: true }).catch(() => undefined);
  }

  // Patch postgresql.conf to bind deterministically. initdb writes
  // defaults that work for most cases, but we want to be explicit so
  // the server always answers on the same port the multica server
  // expects, regardless of whether the data dir was created on macOS,
  // Linux, or inside a hardened sandbox.
  const confPath = postgresqlConf();
  if (existsSync(confPath)) {
    let conf = readFileSync(confPath, "utf-8");
    let patched = conf;
    if (!/^listen_addresses\s*=/m.test(patched)) {
      patched += "\n# Added by Multica desktop\nlisten_addresses = '127.0.0.1'\n";
    }
    if (!/^port\s*=/m.test(patched)) {
      patched += "port = 5432\n";
    }
    if (!/^unix_socket_directories\s*=/m.test(patched)) {
      patched += "unix_socket_directories = '/tmp'\n";
    }
    if (patched !== conf) {
      writeFileSync(confPath, patched, { mode: 0o600 });
    }
  }
  // PR 3 (Stage D-2): CREATE DATABASE + pgcrypto extension is NOT
  // run here — pg_ctl hasn't started yet at this point, so psql would
  // fail to connect. See `ensureMulticaDb` callers in startNativePg.
}

/**
 * Create the `multica` database + pgcrypto extension if they don't
 * already exist. Caller must guarantee PG is listening on 5432 first.
 */
export async function ensureMulticaDb(): Promise<void> {
  const psql = join(pgBin(), "psql");
  let dbExists = false;
  try {
    const probe = execFileSync(
      psql,
      [
        "-U", "multica",
        "-d", "postgres",
        "-h", "127.0.0.1",
        "-p", "5432",
        "-tAc",
        "SELECT 1 FROM pg_database WHERE datname='multica'",
      ],
      { env: { ...process.env, PGPASSWORD: "multica" }, timeout: 10_000, encoding: "utf-8" },
    );
    dbExists = probe.trim() === "1";
  } catch (err) {
    debugLog(`ensureMulticaDb probe failed: ${(err as Error).message}`);
    return;
  }
  if (!dbExists) {
    try {
      execFileSync(
        psql,
        [
          "-U", "multica",
          "-d", "postgres",
          "-h", "127.0.0.1",
          "-p", "5432",
          "-tAc",
          "CREATE DATABASE multica",
        ],
        { env: { ...process.env, PGPASSWORD: "multica" }, timeout: 10_000 },
      );
    } catch (err) {
      debugLog(`ensureMulticaDb CREATE failed: ${(err as Error).message}`);
      return;
    }
  }
  // pgcrypto is mandatory per CLAUDE.md (server uses gen_random_uuid
  // and digest hashing). CREATE EXTENSION IF NOT EXISTS is idempotent.
  try {
    execFileSync(
      psql,
      [
        "-U", "multica",
        "-d", "multica",
        "-h", "127.0.0.1",
        "-p", "5432",
        "-tAc",
        "CREATE EXTENSION IF NOT EXISTS pgcrypto",
      ],
      { env: { ...process.env, PGPASSWORD: "multica" }, timeout: 10_000 },
    );
  } catch (err) {
    debugLog(`pgcrypto extension install failed: ${(err as Error).message}`);
  }
}

// ---------------------------------------------------------------------------
// pg_ctl start / stop
// ---------------------------------------------------------------------------

const PG_START_TIMEOUT_MS = 30_000;
const PG_LISTEN_TIMEOUT_MS = 60_000;
const PG_STOP_TIMEOUT_MS = 3_000;

/**
 * Starts the native PG instance. Idempotent — if 5432 is already
 * answering and pg_ctl status returns 0, returns the existing PID
 * without re-running initdb or pg_ctl start. If initdb has never been
 * run, runs it first.
 *
 * Returns `{ pid, port }` on success. Throws a descriptive Error on
 * any failure path (initdb, pg_ctl start, listen timeout).
 */
export async function startNativePg(): Promise<{ pid: number; port: number }> {
  if (!checkNativePgInstalled()) {
    throw new Error(
      `native PG not installed at ${pgBin()}. ` +
        `Download Postgres.app ${PG_VERSION_DIR} from ${PG_APP_URL} and place ` +
        `its Versions/17/* contents at ${pgHome()}.`,
    );
  }

  // Idempotent short-circuit: if 5432 is already listening and pg_ctl
  // confirms the running instance is ours, don't touch anything.
  if (await probeTcp(5432, 1_500)) {
    const status = spawnSync(pgCtlBin(), ["-D", pgdata(), "status"], {
      encoding: "utf-8",
      timeout: 3_000,
    });
    if (status.status === 0) {
      // We don't know the PID for sure (pg_ctl status prints it but
      // parsing is fragile). The caller (server-manager) only uses it
      // for logging, so returning 0 here is safe.
      return { pid: 0, port: 5432 };
    }
    // P1.2 fix: 5432 is answering but pg_ctl says the instance is NOT
    // ours — this happens when a previous Multica session was force-
    // killed (Cmd+Option+Esc, power loss, crash) and the postmaster
    // survived with a stale `postmaster.pid`. Try a clean stop first;
    // if the PID file references a process that's gone, postmaster
    // recovery will refuse to start. Wipe the stale pid file and the
    // pgdata dir if needed so initdb re-runs cleanly.
    await debugLog("5432 is up but pg_ctl reports 'not running' — recovering from orphan postmaster");
    try {
      spawnSync(pgCtlBin(), ["-D", pgdata(), "stop", "-m", "fast"], {
        encoding: "utf-8",
        timeout: 5_000,
      });
    } catch {
      /* ignore — pg_ctl stop is best-effort recovery */
    }
    // Belt-and-suspenders: kill any postgres process still bound to
    // 5432 by reading the stale pid file and SIGKILL'ing the PID.
    const stalePidFile = join(pgdata(), "postmaster.pid");
    if (existsSync(stalePidFile)) {
      try {
        const firstLine = readFileSync(stalePidFile, "utf-8").split("\n")[0]?.trim();
        if (firstLine && /^\d+$/.test(firstLine)) {
          process.kill(parseInt(firstLine, 10), "SIGKILL");
        }
      } catch {
        /* pid file gone or process already exited */
      }
      try {
        unlinkSync(stalePidFile);
      } catch {
        /* ignore */
      }
    }
    // If 5432 is still answering, we can't recover — surface a clean error.
    if (await probeTcp(5432, 1_500)) {
      throw new Error(
        "5432 is occupied by a process that pg_ctl cannot stop. " +
          "This usually means another PostgreSQL is running on 5432. " +
          "Stop it manually and relaunch Multica.",
      );
    }
  }

  await initPgDataDir();

  // pg_ctl writes its own log via -l. We still capture stderr in case
  // pg_ctl itself fails to exec (Gatekeeper quarantine, missing
  // signature, etc.).
  const child = spawn(
    pgCtlBin(),
    ["-D", pgdata(), "-l", pgLog(), "-o", "-p 5432", "start"],
    { stdio: ["ignore", "pipe", "pipe"] },
  );

  let stderr = "";
  child.stderr.on("data", (chunk) => {
    stderr += chunk.toString();
  });

  await new Promise<void>((resolve, reject) => {
    const timer = setTimeout(() => {
      try {
        child.kill("SIGTERM");
      } catch {
        /* ignore */
      }
      reject(
        new Error(`pg_ctl start did not exit within ${PG_START_TIMEOUT_MS / 1000}s. stderr: ${stderr}`),
      );
    }, PG_START_TIMEOUT_MS);
    child.on("exit", (code) => {
      clearTimeout(timer);
      if (code === 0) {
        resolve();
        return;
      }
      // P1.5 fix: macOS 14+ Gatekeeper may reject our ad-hoc codesign
      // on the spawned child, even after `xattr -dr com.apple.quarantine`
      // and `codesign --force -s -`. The user sees "developer cannot be
      // verified" or similar. Surface a clear remediation path.
      const isGatekeeper = /developer cannot be verified|malware|damaged and can't be opened/i.test(stderr);
      if (isGatekeeper) {
        reject(
          new Error(
            `pg_ctl start failed: macOS Gatekeeper rejected the Postgres.app binary. ` +
              `Open System Settings → Privacy & Security, scroll down, and click "Open Anyway" ` +
              `for the file at ${pgHome()}/bin/. Then relaunch Multica. ` +
              `Original error: ${stderr.trim()}`,
          ),
        );
        return;
      }
      reject(
        new Error(`pg_ctl start exited code=${code}. stderr: ${stderr}`),
      );
    });
  });

  // Wait for 5432 to accept connections. Native cold start can take
  // 20s+ on a fresh laptop (initdb + postmaster boot + buffer cache
  // init), so we allow up to 60s.
  const listenStart = Date.now();
  while (Date.now() - listenStart < PG_LISTEN_TIMEOUT_MS) {
    if (await probeTcp(5432, 1_500)) {
      // PG is up. CREATE DATABASE multica + pgcrypto extension before
      // we hand off to migrate. The default `postgres` db exists from
      // initdb; multica is what the server's DATABASE_URL points at.
      await ensureMulticaDb();
      return { pid: child.pid ?? 0, port: 5432 };
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error(
    `native PG did not accept connections on 5432 within ${PG_LISTEN_TIMEOUT_MS / 1000}s. ` +
      `Check ${pgLog()} for details.`,
  );
}

/**
 * Stops the native PG instance. Safe to call when PG was never
 * started (e.g. another backend won the picker). Uses `pg_ctl stop
 * -m fast` first; if the port is still answering after 3s, falls
 * back to SIGKILL on the postgres PID. Total hard cap ~6s.
 */
export async function stopNativePg(): Promise<void> {
  if (!existsSync(pgVersionFile())) return; // never initialized
  try {
    await new Promise<void>((resolve) => {
      execFile(
        pgCtlBin(),
        ["-D", pgdata(), "stop", "-m", "fast"],
        { timeout: PG_STOP_TIMEOUT_MS },
        () => resolve(),
      );
    });
  } catch {
    /* ignore — we have a SIGKILL fallback below */
  }
  // Belt-and-suspenders: if 5432 is still answering after pg_ctl stop
  // (e.g. it hung waiting for a client), force-kill any postgres PID
  // bound to our data dir.
  if (await probeTcp(5432, 500)) {
    try {
      const r = spawnSync(
        "pgrep",
        ["-f", `postgres.*-D.${pgdata()}`],
        { encoding: "utf-8", timeout: 2_000 },
      );
      for (const line of r.stdout.split("\n")) {
        const pid = Number(line.trim());
        if (pid > 0) {
          try {
            process.kill(pid, "SIGKILL");
          } catch {
            /* already gone */
          }
        }
      }
    } catch {
      /* pgrep missing or failed — port will leak but process exits anyway */
    }
  }
}

/**
 * Diagnostic helper for the renderer banner: if pg_ctl exists but
 * won't run, the most common cause is Gatekeeper quarantine. We check
 * for that and return a hint the banner can show. Returns null when
 * everything is fine or the binary is missing entirely.
 */
export async function diagnoseNativePgFailure(): Promise<
  "quarantine" | "version-mismatch" | "missing" | null
> {
  if (!checkNativePgInstalled()) return "missing";
  const version = await detectInstalledPgVersion();
  if (version !== PG_VERSION_REQUIRED_MAJOR) return "version-mismatch";
  // macOS-only: check for the quarantine xattr. On non-darwin this
  // is a no-op.
  if (process.platform === "darwin") {
    try {
      const r = spawnSync("xattr", ["-l", postgresBin()], {
        encoding: "utf-8",
        timeout: 2_000,
      });
      if (/com\.apple\.quarantine/.test(r.stdout)) return "quarantine";
    } catch {
      /* xattr not available — assume clean */
    }
  }
  return null;
}

// Silence "unused" lints for imports we keep for symmetry with
// cli-bootstrap.ts. These are touched in future PR 3 work.
void homedir;

// ===========================================================================
// PR 3 (Stage D-2): manifest, download, extract, migration
// ===========================================================================
// The functions below replace the "user manually downloads Postgres.app"
// burden from PR 2 with a fully-automated first-launch flow:
//   1. Read the bundled manifest (resources/pg/manifest.json)
//   2. Download the DMG from PostgresApp's GitHub release
//   3. SHA-256 verify against the manifest
//   4. hdiutil attach + cp + detach into ~/Library/AS/Multica/pg/17.4/
//   5. Auto-migrate existing Docker pgdata via pg_dump | pg_restore
//   6. Run initdb + start (delegated to PR 2 helpers above)
// All paths flow through `startNativePg` so the calling code in
// server-manager.ts doesn't need to care whether the binary was
// pre-placed, auto-downloaded, or auto-migrated.

/** Manifest shape as read from resources/pg/manifest.json. */
export interface PgManifest {
  version: string;       // major version (e.g. "17")
  versionFull: string;   // full version (e.g. "17.4")
  source: string;        // HTTPS URL of the .dmg
  sizeBytes: number;     // expected size for early sanity check
  sha256: string;        // lowercase hex of the DMG SHA-256
  license: string;
  attribution: string;
  extractedSubpath: string; // path inside the mounted .dmg where Versions/17 lives
  extractedFiles: string[];
}

/** A single progress event from downloadAndExtractPg. */
export interface PgProgressEvent {
  phase: "fetch" | "verify" | "extract" | "initdb" | "migrate-dump" | "migrate-restore" | "migrate-verify";
  percent: number;       // 0..100 within the phase
  bytesDone?: number;
  bytesTotal?: number;
}

/**
 * Module-level state so the renderer can call `cancelDownload()` to
 * abort an in-flight download. AbortController semantics — the next
 * chunk-write checks `signal.aborted` and rejects the pipeline.
 */
let downloadAbortController: AbortController | null = null;

/**
 * Read the bundled pg manifest. Path is resolved the same way as
 * `resolveResourcePath` in server-manager.ts: packaged → asar.unpacked,
 * dev → apps/desktop/resources.
 */
export async function loadPgManifest(): Promise<PgManifest> {
  // Mirror resolveResourcePath without importing from server-manager.ts
  // (would create a cycle).
  const candidates = [
    join(process.resourcesPath ?? "", "app.asar.unpacked", "resources", "pg", "manifest.json"),
    join(app.getAppPath(), "resources", "pg", "manifest.json"),
  ];
  const manifestPath = candidates.find((p) => existsSync(p));
  if (!manifestPath) {
    throw new Error(
      `pg manifest not found. Looked in:\n${candidates.join("\n")}\n` +
      `This is a packaging bug — bundle-cli.mjs must copy resources/pg/manifest.json into the DMG.`,
    );
  }
  const raw = await import("node:fs/promises").then((m) => m.readFile(manifestPath, "utf-8"));
  const parsed = JSON.parse(raw) as PgManifest;
  // Minimal shape validation. Anything more thorough belongs in Zod
  // (we deliberately keep this file dependency-light).
  for (const field of ["version", "source", "sha256", "extractedSubpath"] as const) {
    if (!parsed[field]) throw new Error(`pg manifest missing field: ${field}`);
  }
  return parsed;
}

/**
 * SHA-256 of a file using streaming so it works on the 120 MB DMG
 * without loading it into memory.
 */
export async function sha256OfFile(filePath: string): Promise<string> {
  const { createHash } = await import("node:crypto");
  const hash = createHash("sha256");
  await pipeline(createReadStream(filePath), hash as unknown as NodeJS.WritableStream);
  return hash.digest("hex");
}

/**
 * Path to the persistent Postgres.app DMG cache under userData.
 * Layout:
 *   ~/Library/Application Support/Multica/pg/17.4/cache/
 *     postgres-app-<versionFull>.dmg       verified DMG
 *     postgres-app-<versionFull>.dmg.sha256 expected SHA-256 (lowercase hex)
 *
 * If both files exist and the on-disk DMG's SHA-256 matches the expected
 * value, we skip the network fetch entirely. This is the 0.3.22 hard
 * requirement from memory/pg-binary-tree-ship-wipe-2026-07-14.md: the
 * fetcher was previously the single point of failure when the binary
 * tree was wiped post-ship. With a verified cache, the next launch can
 * extract from local bytes and never touch the network.
 */
function pgCacheDir(): string {
  return join(pgHome(), "cache");
}

function pgCachedDmgPath(versionFull: string): string {
  return join(pgCacheDir(), `postgres-app-${versionFull}.dmg`);
}

function pgCachedDmgShaPath(versionFull: string): string {
  return join(pgCacheDir(), `postgres-app-${versionFull}.dmg.sha256`);
}

/**
 * Hard ceiling on a single DMG fetch. The Postgres.app DMG is ~120 MB;
 * under a slow link (50 KB/s upstream proxies, captive portals) it can
 * legitimately take 40+ s, so 60 s is the floor. Combined with the
 * caller-provided AbortSignal via AbortSignal.any, this guarantees the
 * fetcher cannot wedge the launcher indefinitely.
 *
 * On timeout the underlying fetch rejects with a DOMException whose
 * name is "TimeoutError"; the surrounding try/catch in
 * `downloadAndExtractPg` falls through to the existing failure path
 * (native pg_ctl failure), preserving all error-handling invariants.
 */
const DMG_FETCH_TIMEOUT_MS = 60_000;

/**
 * Download the Postgres.app DMG with progress callbacks. Three retries
 * with exponential backoff for transient network failures. Respects
 * `cancelDownload()` via the module-level AbortController.
 *
 * Before hitting the network, checks for a verified DMG in the
 * per-version cache under userData. On cache hit we skip both the
 * fetch phase and the SHA-256 verify phase and jump straight to
 * extraction.
 */
export async function downloadAndExtractPg(
  onProgress: (e: PgProgressEvent) => void,
): Promise<void> {
  const manifest = await loadPgManifest();
  downloadAbortController = new AbortController();
  // Declared outside the try so the `finally` cleanup can always reach
  // it; on the cache-hit path it's never created and rm is a no-op.
  const tmpDmg = join(tmpdir(), `multica-pg-${process.pid}-${Date.now()}.dmg`);
  try {
    // Phase 0: cache hit. If a previous launch already downloaded and
    // verified this DMG, reuse it and skip the network entirely.
    const cachedDmg = pgCachedDmgPath(manifest.versionFull);
    const cachedShaPath = pgCachedDmgShaPath(manifest.versionFull);
    if (existsSync(cachedDmg) && existsSync(cachedShaPath)) {
      try {
        const expectedSha = readFileSync(cachedShaPath, "utf-8").trim().toLowerCase();
        if (expectedSha === manifest.sha256.toLowerCase()) {
          const actualSha = await sha256OfFile(cachedDmg);
          if (actualSha.toLowerCase() === expectedSha) {
            debugLog(`DMG cache hit: ${cachedDmg} sha256=${actualSha}`);
            onProgress({ phase: "verify", percent: 100 });
            onProgress({ phase: "extract", percent: 0 });
            await extractDmgAndCopyBins(cachedDmg, manifest.extractedSubpath);
            onProgress({ phase: "extract", percent: 100 });
            return;
          }
          debugLog(`DMG cache SHA-256 mismatch (got ${actualSha}, expected ${expectedSha}); deleting and re-downloading.`);
        } else {
          debugLog(`DMG cache SHA-256 file stale (${expectedSha} vs manifest ${manifest.sha256}); deleting and re-downloading.`);
        }
        // SHA mismatch — wipe cache, fall through to network download.
        await rm(cachedDmg, { force: true }).catch(() => undefined);
        await rm(cachedShaPath, { force: true }).catch(() => undefined);
      } catch (cacheErr) {
        debugLog(`DMG cache probe failed, falling back to network: ${(cacheErr as Error).message}`);
        // On any cache error, nuke the cache and fall through.
        await rm(cachedDmg, { force: true }).catch(() => undefined);
        await rm(cachedShaPath, { force: true }).catch(() => undefined);
      }
    }

    // Phase 1: download
    onProgress({ phase: "fetch", percent: 0 });
    let lastErr: Error | null = null;
    for (let attempt = 0; attempt < 3; attempt++) {
      try {
        await downloadDmgWithProgress(
          manifest.source,
          tmpDmg,
          manifest.sizeBytes,
          (done, total) => {
            onProgress({
              phase: "fetch",
              percent: total ? Math.round((done / total) * 100) : 0,
              bytesDone: done,
              bytesTotal: total,
            });
          },
          downloadAbortController.signal,
        );
        lastErr = null;
        break;
      } catch (err) {
        lastErr = err as Error;
        if (lastErr.name === "AbortError" || lastErr.name === "TimeoutError") {
          // Either user-cancelled (cancelDownload) or hard 60s timeout.
          // Re-throw with a clear message so server-guard / user logs
          // can grep for it.
          throw new Error(
            `postgres-app DMG download aborted (${lastErr.name === "TimeoutError" ? `timeout after ${DMG_FETCH_TIMEOUT_MS}ms` : "cancelled"}): ${lastErr.message}`,
          );
        }
        await new Promise((r) => setTimeout(r, 2_000 * Math.pow(2, attempt)));
      }
    }
    if (lastErr) throw lastErr;

    // Phase 2: SHA-256 verify against manifest
    onProgress({ phase: "verify", percent: 0 });
    const actual = await sha256OfFile(tmpDmg);
    if (actual.toLowerCase() !== manifest.sha256.toLowerCase()) {
      throw new Error(
        `Postgres.app SHA-256 mismatch: expected ${manifest.sha256}, got ${actual}. ` +
          `Aborting before extraction to avoid running unverified code.`,
      );
    }
    onProgress({ phase: "verify", percent: 100 });

    // Persist the verified DMG into the per-version cache so the next
    // launch can skip the network entirely. We write the SHA-256 sidecar
    // after the DMG so a partial write can be detected (sha missing or
    // stale → cache hit path will treat it as a miss and re-download).
    try {
      await mkdir(pgCacheDir(), { recursive: true });
      const finalDmg = pgCachedDmgPath(manifest.versionFull);
      const finalShaPath = pgCachedDmgShaPath(manifest.versionFull);
      const { copyFile } = await import("node:fs/promises");
      await copyFile(tmpDmg, finalDmg);
      writeFileSync(finalShaPath, manifest.sha256.toLowerCase() + "\n");
      debugLog(`DMG cached for next launch: ${finalDmg}`);
    } catch (cacheErr) {
      // Cache write failure is non-fatal — the current install proceeds
      // via tmpDmg; the next launch will simply re-download.
      debugLog(`DMG cache write skipped: ${(cacheErr as Error).message}`);
    }

    // Phase 3: extract
    onProgress({ phase: "extract", percent: 0 });
    await extractDmgAndCopyBins(tmpDmg, manifest.extractedSubpath);
    onProgress({ phase: "extract", percent: 100 });
  } finally {
    downloadAbortController = null;
    await rm(tmpDmg, { force: true }).catch(() => undefined);
  }
}

/**
 * Cancel any in-flight download. Safe to call when no download is
 * running (no-op). The next chunk-write will throw AbortError and the
 * downloader resolves the outer promise with a rejection.
 */
export function cancelDownload(): void {
  downloadAbortController?.abort();
}

async function downloadDmgWithProgress(
  url: string,
  dest: string,
  expectedSize: number,
  onChunk: (done: number, total: number) => void,
  signal: AbortSignal,
): Promise<void> {
  await mkdir(dirname(dest), { recursive: true });
  debugLog(`DMG download starting: url=${url}`);
  // Compose the caller-provided signal (cancelDownload) with a hard
  // 60 s timeout. AbortSignal.any fires whichever aborts first. This
  // is the 0.3.22 fix for the incident where the fetcher wedged
  // indefinitely on captive portals / DNS / proxy stalls and blocked
  // the entire PG startup path.
  const fetchSignal = AbortSignal.any([signal, AbortSignal.timeout(DMG_FETCH_TIMEOUT_MS)]);
  const res = await fetch(url, { redirect: "follow", signal: fetchSignal });
  debugLog(`DMG fetch result: status=${res.status} body=${!!res.body} content-length=${res.headers.get("content-length")}`);
  if (!res.ok || !res.body) {
    throw new Error(`DMG download failed: ${res.status} ${res.statusText}`);
  }
  const total = Number(res.headers.get("content-length") ?? expectedSize);
  let done = 0;
  // Stream the response into a file. We do NOT use stream.pipeline
  // because Readable.fromWeb + transform generator + file sink has
  // subtle backpressure issues in Node 22 — a plain async iteration
  // over Readable.fromWeb with manual writeStream.write() is more
  // reliable for large bodies, and we still get progress updates.
  const nodeStream = Readable.fromWeb(res.body as Parameters<typeof Readable.fromWeb>[0]);
  const writeStream = createWriteStream(dest);
  try {
    for await (const chunk of nodeStream as AsyncIterable<Buffer>) {
      if (signal.aborted) throw new Error("Download cancelled");
      // Respect backpressure — wait for drain when the internal buffer
      // is full so we don't OOM on a 120 MB body.
      if (!writeStream.write(chunk)) {
        await new Promise<void>((resolve) => writeStream.once("drain", resolve));
      }
      done += chunk.length;
      onChunk(done, total);
    }
  } finally {
    await new Promise<void>((resolve, reject) => {
      writeStream.end((err?: Error | null) => (err ? reject(err) : resolve()));
    });
  }
  debugLog(`DMG download complete: bytes=${done} dest=${dest}`);
}

/**
 * Attach the DMG, copy the Postgres.app Versions/17/* tree into our
 * pgHome, strip xattrs + codesign the binary, detach. macOS-only —
 * calling this on Linux/Windows throws.
 */
export async function extractDmgAndCopyBins(
  dmgPath: string,
  subpath: string,
): Promise<void> {
  if (process.platform !== "darwin") {
    throw new Error("DMG extraction is macOS-only. Linux/Windows users must place Postgres.app binaries manually.");
  }
  const paths = resolveNativePaths();
  await mkdir(paths.pgHome, { recursive: true });

  const mountPoint = `/tmp/multica-pg-mount-${process.pid}`;
  await mkdir(mountPoint, { recursive: true });

  try {
    execFileSync("hdiutil", ["attach", "-nobrowse", "-quiet", "-mountpoint", mountPoint, dmgPath], {
      timeout: 60_000,
    });
    const sourceDir = join(mountPoint, subpath);
    if (!existsSync(sourceDir)) {
      throw new Error(
        `Expected Postgres.app contents at ${sourceDir}, not found. ` +
          `The manifest's extractedSubpath may be stale. Found:\n${readFileSyncSyncSafe(join(mountPoint, "Postgres.app/Contents/Versions"))}`,
      );
    }
    execFileSync("cp", ["-R", `${sourceDir}/.`, paths.pgHome], { stdio: "inherit" });
    try { execFileSync("chmod", ["-R", "+x", paths.pgBin], { timeout: 10_000 }); }
    catch { /* non-fatal */ }
    try {
      execFileSync("xattr", ["-dr", "com.apple.quarantine", paths.pgHome], { timeout: 30_000 });
    } catch (err) {
      debugLog(`xattr strip failed (non-fatal): ${(err as Error).message}`);
    }
    try {
      execFileSync("codesign", ["-s", "-", "--force", paths.postgresBin], { timeout: 30_000 });
    } catch (err) {
      debugLog(`codesign failed (non-fatal): ${(err as Error).message}`);
    }
  } finally {
    try {
      execFileSync("hdiutil", ["detach", mountPoint, "-quiet"], { timeout: 30_000 });
    } catch (err) {
      debugLog(`hdiutil detach failed (non-fatal): ${(err as Error).message}`);
    }
    await rm(mountPoint, { recursive: true, force: true }).catch(() => undefined);
  }
}

function readFileSyncSyncSafe(p: string): string {
  try {
    return readFileSync(p, "utf-8");
  } catch {
    return "(unreadable)";
  }
}

function debugLog(msg: string): void {
  appendFileSync(join(homedir(), ".multica", "pg-bootstrap.log"), `[${new Date().toISOString()}] ${msg}\n`);
}

// ---------------------------------------------------------------------------
// Migration from v0.2.x Docker volume to v0.3.0 native pgdata
// ---------------------------------------------------------------------------

const MIGRATION_SENTINEL = join(homedir(), ".multica", ".pg-migrated-v1");
// 0.3.1 P1.8: in-progress sentinel, created with O_EXCL BEFORE pg_restore.
// If we crash/SIGKILL between pg_restore success and the final rename,
// this file is left on disk; the next launch sees it as a "migration
// interrupted, refuse to retry without manual cleanup" signal. Without
// this, a crashed migration + retry would re-run pg_restore on already-
// populated pgdata → "relation already exists" errors → wipe cascade
// → silent data loss.
const SENTINEL_IN_PROGRESS = join(homedir(), ".multica", ".pg-migrating-v1");
const DOCKER_CONTAINER = "multica-postgres-1";
const DOCKER_VOLUME = "multica_pgdata";
const MIGRATION_TABLES = [
  "workspace",
  "issue",
  "comment",
  "agent",
  "agent_task_queue",
  "agent_runtime",
  "member",
  "project",
  "inbox_item",
  "squad",
  "skill",
] as const;

export interface MigrationResult {
  ok: true;
  dumpPath: string;
  dockerRows: number;
  nativeRows: number;
  durationMs: number;
}

export type MigrationOutcome =
  | MigrationResult
  | { skipped: true; reason: "already-migrated" | "no-docker-volume" | "user-cancelled" };

/**
 * Idempotent. Detects an existing Docker pgdata, prompts the user via
 * the renderer (callback), runs `pg_dump | pg_restore`, and verifies
 * row counts before stamping the sentinel file.
 *
 * Returns `{skipped: true, reason: ...}` for any non-fatal skip path;
 * throws on actual errors. The caller surfaces the result to the
 * renderer for the migration toast / progress modal.
 */
export async function runMigrationFlow(opts: {
  onProgress: (phase: PgProgressEvent["phase"], percent: number) => void;
  confirmed: boolean;
}): Promise<MigrationOutcome> {
  // 0. Sentinel guard
  if (existsSync(MIGRATION_SENTINEL)) {
    return { skipped: true, reason: "already-migrated" };
  }
  // 0.3.1 P1.8: refuse to start if a previous run was interrupted
  // mid-migration. The presence of SENTINEL_IN_PROGRESS without the
  // final MIGRATION_SENTINEL means the last pg_restore may have
  // half-completed. Returning "already-migrated" forces the user to
  // resolve it (see migration-dialog.tsx UX) rather than risk wiping
  // their data via the catch-block cascade on retry.
  if (existsSync(SENTINEL_IN_PROGRESS)) {
    return { skipped: true, reason: "already-migrated" };
  }
  // 1. Detect Docker volume
  const hasDocker = await checkDockerPgdata();
  if (!hasDocker) {
    writeFileSync(MIGRATION_SENTINEL, `skipped-no-docker at ${new Date().toISOString()}`);
    return { skipped: true, reason: "no-docker-volume" };
  }
  // 2. User must confirm — renderer is responsible for asking first.
  if (!opts.confirmed) {
    return { skipped: true, reason: "user-cancelled" };
  }
  // 0.3.1 P1.8: stamp the in-progress sentinel with O_EXCL BEFORE any
  // destructive pg_restore. If a parallel run somehow also gets here
  // (it shouldn't — the IPC handler is single-fire), the second open
  // fails with EEXIST and we bail. createdInProgress tracks ownership
  // for the cleanup paths below.
  let createdInProgress = false;
  try {
    const fd = openSync(SENTINEL_IN_PROGRESS, "wx", 0o600);
    writeFileSync(fd, `started-at=${new Date().toISOString()}\n`);
    closeSync(fd);
    createdInProgress = true;
  } catch (err) {
    if ((err as NodeJS.ErrnoException).code === "EEXIST") {
      return { skipped: true, reason: "already-migrated" };
    }
    throw err;
  }
  // 3. Make sure the Docker container is running (it may have been stopped
  //    when the user quit Docker Desktop). We `docker start` rather than
  //    `docker run` so we don't accidentally create a fresh container.
  await ensureDockerContainerRunning();

  const start = Date.now();
  const ts = new Date().toISOString().replace(/[:.]/g, "-");
  const dumpPath = join(homedir(), ".multica", "backups", `multica-pgdata-${ts}.dump`);

  // 4. pg_dump from inside the container to its /tmp
  opts.onProgress("migrate-dump", 0);
  await mkdir(dirname(dumpPath), { recursive: true });
  execFileSync(
    "docker",
    [
      "exec", DOCKER_CONTAINER,
      "pg_dump",
      "-U", "multica",
      "-d", "multica",
      "-Fc",
      "-f", "/tmp/multica-pgdata.dump",
    ],
    { timeout: 120_000 },
  );
  // 5. docker cp out to host. If the dump file is suspiciously small we
  //    refuse to pg_restore — pg_restore of an empty or truncated dump
  //    would silently produce a broken schema.
  execFileSync("docker", [
    "cp", `${DOCKER_CONTAINER}:/tmp/multica-pgdata.dump`, dumpPath,
  ], { timeout: 60_000 });
  const dumpSize = statSync(dumpPath).size;
  if (dumpSize < 1024) {
    throw new Error(
      `pg_dump output is suspiciously small (${dumpSize} bytes). ` +
        `Aborting before pg_restore to avoid corrupting native pgdata. ` +
        `Your Docker data is unchanged.`,
    );
  }

  // 6. Capture row counts BEFORE pg_restore for post-restore diff
  const dockerCounts = await getDockerTableCounts(MIGRATION_TABLES as unknown as string[]);
  const dockerTotal = Object.values(dockerCounts).reduce((a, b) => a + b, 0);

  // 7. pg_restore — single-transaction + exit-on-error means any error
  //    rolls back the whole restore. pg_restore is the only tool that
  //    can read custom-format dumps (`psql` cannot).
  // P1.3 fix: pg_restore's --single-transaction only wraps the post-data
  // phase. CREATE TABLE statements are auto-committed per statement, so
  // if pg_restore fails halfway (timeout, partial-data corruption), the
  // schema is half-loaded. Next launch's `initPgDataDir` would see the
  // partial pgdata and short-circuit, leaving us with a broken schema
  // that `migrate up` cannot fix. On ANY error during pg_restore or the
  // post-restore row-count verification, wipe pgdata so initdb runs
  // fresh on the next launch. Docker data is untouched.
  opts.onProgress("migrate-restore", 0);
  // P1.3 try/catch wraps the entire restore+verify phase. nativeTotal
  // is hoisted to the function scope so the sentinel-write block below
  // can use it after the try completes successfully.
  let nativeTotal = 0;
  try {
    const pgRestore = join(pgBin(), "pg_restore");
    execFileSync(
      pgRestore,
      [
        "-U", "multica",
        "-d", "multica",
        "-h", "127.0.0.1",
        "-p", "5432",
        "--no-owner",
        "--single-transaction",
        "--exit-on-error",
        "--jobs=4",
        dumpPath,
      ],
      {
        timeout: 300_000,
        env: { ...process.env, PGPASSWORD: "multica" },
      },
    );

    // 8. POST-RESTORE VERIFICATION (Bug 2 mitigation). Counts must match.
    opts.onProgress("migrate-verify", 50);
    const nativeCounts = await getNativeTableCounts(MIGRATION_TABLES as unknown as string[]);
    nativeTotal = Object.values(nativeCounts).reduce((a, b) => a + b, 0);
    for (const table of MIGRATION_TABLES) {
      if (dockerCounts[table] !== nativeCounts[table]) {
        throw new Error(
          `Migration row-count mismatch on table ${table}: ` +
            `docker=${dockerCounts[table]} native=${nativeCounts[table]}. ` +
            `Migration aborted, sentinel NOT written. ` +
            `Your Docker data is unchanged. To retry, restart the app.`,
        );
      }
    }
    opts.onProgress("migrate-verify", 100);
  } catch (err) {
    // P1.3 recovery: wipe native pgdata so the next launch re-initdb's
    // instead of inheriting the half-populated schema. Docker volume is
    // untouched — the gold dump is preserved at dumpPath.
    const msg = (err as Error).message;
    await debugLog(`pg_restore / verification failed; wiping native pgdata for clean retry: ${msg}`);
    try {
      // rm -rf the contents but keep pgdata dir itself.
      const entries = await (await import("node:fs/promises")).readdir(pgdata());
      for (const entry of entries) {
        await (await import("node:fs/promises")).rm(join(pgdata(), entry), { recursive: true, force: true });
      }
    } catch (wipeErr) {
      await debugLog(`pgdata wipe failed: ${(wipeErr as Error).message}`);
    }
    // 0.3.1 P1.8: drop the in-progress sentinel so the wipe cascade
    // can re-run. If we leave it, the next launch sees the in-progress
    // file and refuses to retry (correct behavior for an *interrupted*
    // run, but here we just wiped pgdata so retrying is safe).
    if (createdInProgress) {
      try { unlinkSync(SENTINEL_IN_PROGRESS); } catch { /* ignore */ }
    }
    // Also remove the sentinel-equivalent forward signal if any was written.
    throw new Error(
      `Migration aborted: ${msg}. ` +
        `Native pgdata has been wiped — relaunching the app will re-initdb and start fresh. ` +
        `Docker volume is intact. Dump preserved at ${dumpPath}.`,
    );
  }

  // 9. 0.3.1 P1.8: atomic rename of the in-progress sentinel to the
  // final sentinel. If we crash between the previous block's `resolve`
  // and this line, the in-progress file is left on disk and the next
  // launch correctly refuses to retry (caller handles UX). If we
  // crash BEFORE pg_restore ran, the in-progress file is on disk but
  // pgdata is empty — next launch also refuses, and the user can
  // remove the file manually to re-attempt. renameSync is atomic on
  // POSIX, so we either end up with the new name or the old name, never
  // both and never neither.
  renameSync(SENTINEL_IN_PROGRESS, MIGRATION_SENTINEL);
  try {
    writeFileSync(
      MIGRATION_SENTINEL,
      `migrated-at=${new Date().toISOString()} dockerRows=${dockerTotal} nativeRows=${nativeTotal}`,
    );
  } catch {
    /* rename already happened; the metadata write is a nice-to-have */
  }
  return {
    ok: true,
    dumpPath,
    dockerRows: dockerTotal,
    nativeRows: nativeTotal,
    durationMs: Date.now() - start,
  };
}

/**
 * Quick non-destructive check: does a Docker container named
 * `multica-postgres-1` exist (running or stopped) AND does the
 * `multica_pgdata` volume exist with our schema?
 */
export async function checkDockerPgdata(): Promise<boolean> {
  try {
    const r = execFileSync(
      "docker",
      ["ps", "-a", "--filter", `name=^${DOCKER_CONTAINER}$`, "--format", "{{.Names}}"],
      { encoding: "utf-8", timeout: 5_000 },
    );
    if (!r.includes(DOCKER_CONTAINER)) return false;
    const vol = execFileSync(
      "docker",
      ["volume", "inspect", DOCKER_VOLUME, "--format", "{{.Name}}"],
      { encoding: "utf-8", timeout: 5_000 },
    );
    return vol.trim() === DOCKER_VOLUME;
  } catch {
    return false;
  }
}

async function ensureDockerContainerRunning(): Promise<void> {
  const status = execFileSync(
    "docker",
    ["inspect", DOCKER_CONTAINER, "--format", "{{.State.Running}}"],
    { encoding: "utf-8", timeout: 5_000 },
  ).trim();
  if (status !== "true") {
    execFileSync("docker", ["start", DOCKER_CONTAINER], { timeout: 30_000 });
    // Wait for pg_isready inside the container
    for (let i = 0; i < 30; i++) {
      try {
        const ok = execFileSync(
          "docker",
          ["exec", DOCKER_CONTAINER, "pg_isready", "-U", "multica"],
          { encoding: "utf-8", timeout: 5_000 },
        );
        if (ok.includes("accepting connections")) return;
      } catch {
        /* keep polling */
      }
      await new Promise((r) => setTimeout(r, 1_000));
    }
    throw new Error(`Docker container ${DOCKER_CONTAINER} did not become ready within 30s after start`);
  }
}

async function getDockerTableCounts(tables: readonly string[]): Promise<Record<string, number>> {
  const unionSql = tables.map((t) => `SELECT '${t}' AS t, COUNT(*)::bigint FROM ${t}`).join(" UNION ALL ");
  const out = execFileSync(
    "docker",
    ["exec", DOCKER_CONTAINER, "psql", "-U", "multica", "-d", "multica", "-tAc", unionSql],
    { encoding: "utf-8", timeout: 30_000, env: { ...process.env, PGPASSWORD: "multica" } },
  );
  return parseCountOutput(out, tables);
}

async function getNativeTableCounts(tables: readonly string[]): Promise<Record<string, number>> {
  const unionSql = tables.map((t) => `SELECT '${t}' AS t, COUNT(*)::bigint FROM ${t}`).join(" UNION ALL ");
  const out = execFileSync(
    join(pgBin(), "psql"),
    ["-U", "multica", "-d", "multica", "-h", "127.0.0.1", "-p", "5432", "-tAc", unionSql],
    { encoding: "utf-8", timeout: 30_000, env: { ...process.env, PGPASSWORD: "multica" } },
  );
  return parseCountOutput(out, tables);
}

function parseCountOutput(out: string, tables: readonly string[]): Record<string, number> {
  const counts: Record<string, number> = {};
  for (const line of out.split("\n")) {
    const m = line.match(/^\s*(\w+)\s*\|\s*(\d+)\s*$/);
    if (!m) continue;
    const [, table, count] = m;
    if (tables.includes(table)) counts[table] = Number(count);
  }
  return counts;
}

/**
 * Compose the forward-signal fields for desktop.json after a successful
 * migration. The caller (server-manager.ts) merges these into the
 * existing config.
 */
export function migrationForwardSignal(dumpPath: string): {
  pgBackend: "native";
  previousBackend: "docker";
  previousPgdataDump: string;
  previousDockerVolume: string;
} {
  return {
    pgBackend: "native",
    previousBackend: "docker",
    previousPgdataDump: dumpPath,
    previousDockerVolume: DOCKER_VOLUME,
  };
}
