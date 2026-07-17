import { spawn, type ChildProcess } from "node:child_process";
import { existsSync } from "node:fs";
import { app } from "electron";
import { dirname, join } from "node:path";
import { pickFreePort } from "../util/free-port";

// ExperimentalManager is the lifecycle surface every bundled subprocess
// manager in desktop/main must expose. Concrete subclasses (pythia-manager,
// claude-science-manager) live alongside this file and pin their own
// resolveBinary / healthPath / parsePort. Keeping a strict interface
// forces all managers into the same shutdown contract so the
// before-quit hook in index.ts can call stop() on every manager
// without each one re-registering its own listener (memory:
//
// daemon-auto-start-prevention contract warns against per-manager
// lifecycle hooks that race the central stopServerManager call).
export interface ExperimentalManager {
  readonly name: string;
  start(): Promise<void>;
  stop(): Promise<void>;
  status(): ExperimentalStatus;
  url(): string | null;
}

export type ExperimentalStatus = "idle" | "starting" | "ready" | "stopping" | "stopped" | "error";

// ExperimentalManagerConfig captures the per-manager customization.
// All fields are optional except `name`. Concrete managers fill them
// in their constructors; this base never inspects them beyond the
// four hooks below.
export interface ExperimentalManagerConfig {
  name: string;
  // Subdirectory under resources/ that holds the bundled binary.
  // Resolved via resolveResourcePath; falls back to app.getAppPath()
  // in dev per the existing pattern in server-manager.ts:280-288.
  resourceSubdir: string;
  // Name of the executable inside resourceSubdir (e.g. "openscience"
  // for the Claude Science Bun binary).
  binName: string;
  // Extra args appended after the chosen port flag. Should include
  // whatever subcommand + flag the binary expects (e.g. ["web",
  // "--port"]) so the manager can append the free port as the last
  // argument. Empty array means args passed verbatim without a port.
  args: string[];
  // Default env additions for the spawned child. The manager does
  // NOT inherit Multica's provider env — that prevents accidental
  // model mixing (see plan file, Phase D risk #4).
  childEnv?: Record<string, string>;
  // HTTP path to GET for the health probe. Should return <500 within
  // 2s for the manager to consider the service ready. Default "/health".
  healthPath?: string;
  // Time to wait between health probes, in milliseconds. Default 500.
  probeIntervalMs?: number;
  // Total time we are willing to wait for the child to become ready,
  // in milliseconds. Default 30_000.
  readyTimeoutMs?: number;
  // Time to give SIGTERM before escalating to SIGKILL, in ms. Default
  // 5_000 to match server-manager.ts:511-525 (5s grace).
  stopGraceMs?: number;
  // Called once when the manager transitions to "ready". Receives
  // the loopback URL so the host application can register it with
  // the in-process state used by the same-origin reverse proxy
  // (server/internal/handler/experimental_proxy.go). Default is a
  // no-op — concrete managers that want same-origin embed support
  // wire this to handler.SetExperimentalLoopbackURL.
  onReady?: (loopbackURL: string) => void;
  // Called once when the manager transitions to a terminal state
  // other than "ready" (stopped, error, exit). Used to clear the
  // upstream registration so the proxy stops returning 502 instead
  // of stale data. Default no-op.
  onStop?: () => void;
}

// resolveResourcePath mirrors the private helper in server-manager.ts
// so we don't have to export it. child_process APIs cannot resolve
// paths under app.asar, hence the unpacked-redirect in packaged mode.
//
// Exported (not just internal) so the renderer can ask the main process
// for the absolute preload path that the <webview> tag will load. The
// preload lives under resources/main/experimental/ and needs to be
// reachable in both dev (app.getAppPath() + resources/) and packaged
// (process.resourcesPath + app.asar.unpacked + resources/) modes.
export function resolveResourcePath(...segments: string[]): string {
  if (app.isPackaged) {
    return join(process.resourcesPath, "app.asar.unpacked", "resources", ...segments);
  }
  return join(app.getAppPath(), "resources", ...segments);
}

// waitForHealth polls a loopback URL until the child responds with
// anything under 5xx, or the timeout expires. Matches the policy in
// server-manager.ts:496-509.
async function waitForHealth(url: string, timeoutMs: number, intervalMs: number): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const res = await fetch(url, { signal: AbortSignal.timeout(2_000) });
      if (res.status < 500) return;
    } catch {
      // Not up yet. The probe interval paces the loop.
    }
    await new Promise((r) => setTimeout(r, intervalMs));
  }
  throw new Error(`experimental service did not become healthy at ${url} within ${timeoutMs}ms`);
}

// BaseManager implements ExperimentalManager using only the contract
// the concrete subclass plugs into. Subclasses MAY override parsePort
// to read the bound port from the child's stdout if the binary does
// not accept --port; the default just returns the picked port.
export class BaseExperimentalManager implements ExperimentalManager {
  readonly name: string;
  private readonly cfg: ExperimentalManagerConfig;
  private process: ChildProcess | null = null;
  private port: number | null = null;
  private currentStatus: ExperimentalStatus = "idle";

  constructor(cfg: ExperimentalManagerConfig) {
    this.name = cfg.name;
    this.cfg = cfg;
  }

  status(): ExperimentalStatus {
    return this.currentStatus;
  }

  url(): string | null {
    if (this.port === null) return null;
    return `http://127.0.0.1:${this.port}`;
  }

  async start(): Promise<void> {
    if (this.process !== null) {
      throw new Error(`experimental manager ${this.name}: already started`);
    }
    this.currentStatus = "starting";

    const bin = resolveResourcePath(this.cfg.resourceSubdir, this.cfg.binName);
    // Pre-flight existence check. `spawn()` itself does not throw on
    // ENOENT — it emits an asynchronous `'error'` event on the child
    // and (depending on Node version) silently leaks a child_process
    // object. Without this check the renderer would see an opaque
    // "spawn ENOENT" message and the main process would still bubble
    // the error up. With the check we surface a structured error
    // before any IPC round-trip cost is paid, and the renderer can
    // render the documented "service not bundled" copy.
    if (!existsSync(bin)) {
      this.currentStatus = "error";
      const err = new Error(
        `experimental service "${this.name}" is not bundled: ${bin} does not exist. ` +
          "Drop the binary into the expected vendor path and re-run `pnpm --filter @multica/desktop bundle-cli`.",
      ) as Error & { code?: string };
      err.code = "BINARY_NOT_BUNDLED";
      throw err;
    }

    const port = await pickFreePort();
    this.port = port;

    const args = [...this.cfg.args, String(port)];

    const child = spawn(bin, args, {
      env: {
        ...process.env,
        // 0.3.29.2: hand the bundled binary its real location so any
        // self-resolve (HEREDOC engine lookups, relative `engine/`
        // PYTHONPATH, globs in scripts that `cd` to "$(dirname $0)")
        // can find its companions. Without this the spawn inherits
        // Electron main's cwd, which on macOS is "/" — making the
        // relative `engine/` import in pythia/run.sh fail with
        // "ModuleNotFoundError: No module named 'engine'".
        PYTHIA_SCRIPT_PATH: bin,
        ...this.cfg.childEnv,
      },
      cwd: dirname(bin),
      stdio: ["ignore", "pipe", "pipe"],
      detached: false,
    });
    this.process = child;

    // Capture async spawn errors (ENOENT, EACCES, EPERM, …) so they
    // don't escape to Node's default unhandled-rejection path which,
    // in Electron's main process, surfaces as a modal NSAlert. The
    // waitForHealth loop below would otherwise hang on a child that
    // never emits stdout because spawn failed. We reject a shared
    // deferred so health-probe timeout can return immediately.
    let spawnError: Error | null = null;
    const spawnFailed = new Promise<void>((_, reject) => {
      child.once("error", (err) => {
        spawnError = err;
        this.currentStatus = "error";
        reject(err);
      });
    });

    // stdout / stderr tagged with the manager name so logs are easy
    // to correlate. Subclasses MAY attach their own listeners via
    // the parsePort hook if needed.
    child.stdout.on("data", (chunk: Buffer) => {
      process.stdout.write(`[${this.name}] ${chunk}`);
    });
    child.stderr.on("data", (chunk: Buffer) => {
      process.stderr.write(`[${this.name}] ${chunk}`);
    });

    child.once("exit", (code, signal) => {
      // The process we spawned ended. Mark us stopped unless we
      // expected it (during a graceful stop the currentStatus is
      // already "stopping"). Be defensive: a SIGKILL during stop
      // still has to flip the status.
      this.process = null;
      const wasStopping = this.currentStatus === "stopping";
      this.currentStatus = "stopped";
      if (!wasStopping) {
        process.stderr.write(
          `[${this.name}] exited unexpectedly code=${code} signal=${signal}\n`,
        );
      }
    });

    const healthPath = this.cfg.healthPath ?? "/health";
    const healthUrl = `http://127.0.0.1:${port}${healthPath}`;
    const probeMs = this.cfg.probeIntervalMs ?? 500;
    const readyMs = this.cfg.readyTimeoutMs ?? 30_000;

    try {
      // Race health probe against the spawn-error deferred so a
      // failed spawn returns immediately instead of waiting the full
      // readyTimeoutMs. Either branch settles currentStatus to a
      // terminal state before returning.
      await Promise.race([
        waitForHealth(healthUrl, readyMs, probeMs),
        spawnFailed,
      ]);
      if (spawnError) {
        throw spawnError;
      }
      this.currentStatus = "ready";
      // Notify the host that the upstream is bound so it can
      // register the URL with the same-origin reverse proxy. This
      // is what lets the iframe inside claude-science-view.tsx be
      // same-origin with Multica (and therefore share localStorage
      // for theme + i18n injections).
      this.cfg.onReady?.(`http://127.0.0.1:${port}`);
    } catch (err) {
      this.currentStatus = "error";
      // Tear down so we leave no orphan.
      await this.stop();
      this.cfg.onStop?.();
      // If the spawn itself failed, the error already carries the
      // right context (ENOENT → BINARY_NOT_BUNDLED via the pre-check,
      // EACCES/EPERM → raw spawn error). Otherwise the binary
      // launched but did not become healthy in time — that usually
      // means the underlying service needs external setup (first-run
      // onboarding, missing API keys, network blocked, etc.). Surface
      // a structured code so the renderer can point the user at the
      // right setup path.
      if (!(err as Error & { code?: string })?.code) {
        const wrapped = new Error(
          `${this.name} started but did not become healthy at ${healthUrl} within ${readyMs}ms. ` +
            "If this is a first-run on a fresh machine, the service may need external setup " +
            "(login, API keys, or onboarding) before it can bind a usable HTTP endpoint.",
        ) as Error & { code?: string };
        wrapped.code = "SERVICE_NEEDS_SETUP";
        throw wrapped;
      }
      throw err;
    }
  }

  async stop(): Promise<void> {
    if (this.process === null) {
      this.currentStatus = "stopped";
      this.cfg.onStop?.();
      return;
    }
    const child = this.process;
    this.currentStatus = "stopping";

    const exited = new Promise<void>((resolve) => {
      child.once("exit", () => resolve());
    });

    child.kill("SIGTERM");
    const graceMs = this.cfg.stopGraceMs ?? 5_000;
    const grace = new Promise<void>((resolve) => setTimeout(resolve, graceMs));
    const winner = await Promise.race([exited, grace]);

    if (winner === undefined) {
      // Grace period elapsed without an exit. Force kill.
      try {
        child.kill("SIGKILL");
      } catch {
        // Already gone — fine.
      }
      // Wait once more for the exit listener to fire so the status
      // settles. Bounded to a couple of seconds to avoid hanging.
      await Promise.race([
        exited,
        new Promise<void>((resolve) => setTimeout(resolve, 2_000)),
      ]);
    }

    this.process = null;
    this.currentStatus = "stopped";
    this.cfg.onStop?.();
  }
}
