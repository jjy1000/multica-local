import { ipcMain, type BrowserWindow } from "electron";
import { BaseExperimentalManager, type ExperimentalManager } from "./experimental/manager-template";
import { registerExperimentalUpstream, unregisterExperimentalUpstream } from "./experimental/upstream-registry";

// PythiaManager spawns the bundled Pythia Python service on a free
// loopback port. Pythia is a FastAPI app exposing /health, /predict,
// /brief, /whatif — no UI rendering. Multica agents reach it via
// the multica-pythia Skill (server/internal/service/builtin_skills/
// multica-pythia/SKILL.md) which shells out to the `multica pythia`
// CLI. The CLI then proxies HTTP through the manager's loopback URL.
//
// The manager reuses BaseExperimentalManager entirely. The only
// Pythia-specific config is the resource subdirectory under
// resources/ + the child env so the bundled python interpreter
// finds the staged source.
//
// Env policy: this manager does NOT inherit Multica's provider
// chain. Pythia runs entirely on the user's local LLM via MiroFish +
// Ollama, so we keep `childEnv` empty and rely on system PATH for
// `python3` / `uvicorn`. If the user is opted into OpenScience
// separately, that manager's own childEnv isolates its keys — there
// is no shared env state to leak.

interface PythiaManagerConfig {
  // Path under resources/ where the staged Pythia source lives.
  // Bundle script (bundle-cli.mjs) copies
  // apps/desktop/vendor/pythia-src/engine/*.py into
  // apps/desktop/resources/pythia/engine/ at build time.
  resourceSubdir: string;
}

let sharedManager: PythiaManager | null = null;

// pythiaRuntimeEnv returns the env vars PYTHIA needs to route LLM
// calls through Multica. We read the active profile's config.json so
// the bridge picks up the same JWT the renderer would send to the
// server, and the same apiUrl the user bound the desktop to.
//
// Failures are NOT silent anymore (0.3.32: desktop is Multica-only —
// see the MULTICA_REQUIRED contract in `.omc/decisions/pythia-multica-only.md`):
// if the profile dir isn't there yet, we still inject
// `MULTICA_REQUIRED=1` so the engine fails fast with a clear
// missing-runtime error rather than silently contacting Ollama. The
// engine stays spawnable in dev / standalone testing as long as the
// developer does NOT set MULTICA_REQUIRED=1 themselves.
//
// Re-exported from syncToken's readProfileConfig path so we don't
// duplicate the disk-format guesswork — same parsing rules, same
// edge-case handling.
function pythiaRuntimeEnv(): Record<string, string> {
  try {
    const os = require("os") as typeof import("os");
    const fs = require("fs") as typeof import("fs");
    const path = require("path") as typeof import("path");
    const homedir = os.homedir();
    const profilesRoot = path.join(homedir, ".multica", "profiles");
    if (!fs.existsSync(profilesRoot)) return { MULTICA_REQUIRED: "1" };
    const entries = fs
      .readdirSync(profilesRoot, { withFileTypes: true })
      .filter((d: { isDirectory: () => boolean }) => d.isDirectory())
      .map((d: { name: string }) => d.name);
    // Prefer the desktop- profile (mirrors daemon-manager.resolveActiveProfile
    // heuristic); fall back to the first profile that has a config.json.
    const desktopProfile = entries.find((n: string) => n.startsWith("desktop-"));
    const candidates = desktopProfile ? [desktopProfile] : entries;
    for (const profile of candidates) {
      const cfgPath = path.join(profilesRoot, profile, "config.json");
      if (!fs.existsSync(cfgPath)) continue;
      const raw = fs.readFileSync(cfgPath, "utf-8");
      let cfg: Record<string, unknown>;
      try {
        cfg = JSON.parse(raw);
      } catch {
        continue;
      }
      const apiUrl = typeof cfg.server_url === "string" ? cfg.server_url : "";
      const token =
        typeof cfg.token === "string"
          ? cfg.token
          : typeof cfg.access_token === "string"
          ? cfg.access_token
          : typeof cfg.jwt === "string"
          ? cfg.jwt
          : "";
      if (!apiUrl || !token) continue;
      return {
        MULTICA_AGENT_RUNTIME_URL: apiUrl.replace(/\/+$/, ""),
        MULTICA_API_TOKEN: token,
        MULTICA_REQUIRED: "1",
      };
    }
  } catch {
    // best-effort; on read error we still mark MULTICA_REQUIRED=1 so
    // the engine fails fast instead of silently contacting Ollama.
  }
  return { MULTICA_REQUIRED: "1" };
}
function pythiaEntryPointPath(resourceSubdir: string, ...segments: string[]): string {
  // We hand the path resolution off to BaseExperimentalManager via
  // resolveResourcePath, but we also need it for the -m flag
  // argument here. Importing the helper is awkward across files, so
  // inline a minimal copy.
  // The pattern matches server-manager.ts:280-288 exactly.
  const { app } = require("electron") as typeof import("electron");
  const { join } = require("node:path") as typeof import("node:path");
  const path = app.isPackaged
    ? join(process.resourcesPath, "app.asar.unpacked", "resources", resourceSubdir, ...segments)
    : join(app.getAppPath(), "resources", resourceSubdir, ...segments);
  return path;
}

class PythiaManager implements ExperimentalManager {
  readonly name = "pythia";
  private readonly inner: BaseExperimentalManager;
  private readonly resourceSubdir: string;

  constructor(cfg: PythiaManagerConfig) {
    this.resourceSubdir = cfg.resourceSubdir;
    // We hand the staged engine dir PYTHONPATH so the embedded
    // uvicorn target engine.server:app resolves cleanly. The actual
    // port flag is appended by BaseExperimentalManager as the last
    // arg.
    //
    // 0.3.16+: PYTHIA no longer spawns Ollama locally — every LLM call
    // is routed through Multica's runtime at /api/runtime/llm-call.
    // We inject MULTICA_AGENT_RUNTIME_URL + MULTICA_API_TOKEN into
    // the subprocess env so engine/oracle.py's `_complete()` can
    // reach the bridge without any extra configuration on the user's
    // side. The token is the currently logged-in user's JWT, read
    // off the desktop profile's auth.json; the loopback endpoint is
    // 127.0.0.1:8090 (the server-manager's bound port).
    const runtimeEnv = pythiaRuntimeEnv();
    this.inner = new BaseExperimentalManager({
      name: this.name,
      resourceSubdir: cfg.resourceSubdir,
      // Bash-style shim. We deliberately shell out rather than spawn
      // python3 directly so the wrapper can resolve the right
      // interpreter on every platform. The wrapper lives at
      // resources/pythia/run.sh — bundle-cli.mjs emits it.
      binName: "run.sh",
      // Args are passed verbatim to the wrapper. The wrapper is
      // responsible for invoking `python3 -m uvicorn engine.server:app
      // --host 127.0.0.1 --port $1`. BaseExperimentalManager appends
      // the picked port, matching the shell's positional $1.
      args: [],
      healthPath: "/health",
      childEnv: {
        // Force unbuffered stdout so any startup banner can be
        // captured by BaseExperimentalManager's pipe listeners.
        PYTHONUNBUFFERED: "1",
        // 0.3.16: route every LLM call through Multica's runtime
        // instead of spawning Ollama/MiroFish. See engine/oracle.py.
        ...runtimeEnv,
      },
      readyTimeoutMs: 30_000,
      // Same-origin registration: when Pythia is up, the renderer can
      // fetch /experimental/pythia/{predict,brief,whatif} through
      // the Multica origin (see experimental_proxy.go). Without this
      // hook the renderer would call the loopback URL directly and
      // lose same-origin localStorage for any future theme/i18n work.
      onReady: (url) => registerExperimentalUpstream("pythia_oracle", url),
      onStop: () => unregisterExperimentalUpstream("pythia_oracle"),
    });
  }

  async start(): Promise<void> {
    return this.inner.start();
  }

  async stop(): Promise<void> {
    return this.inner.stop();
  }

  status() {
    return this.inner.status();
  }

  url() {
    return this.inner.url();
  }

  // entryPointDir exposes the staged engine directory for the
  // multica-pythia CLI to log when surfacing the URL.
  entryPointDir(): string {
    return pythiaEntryPointPath(this.resourceSubdir, "engine");
  }
}

// setupPythiaManager installs the manager as a module-level singleton
// and returns it. Mirrors the pattern in server-manager.ts where
// setupServerManager is a separate phase invoked from index.ts at
// app.whenReady. Callers MUST go through this entry point — never
// instantiate the manager directly outside of tests.
export function setupPythiaManager(): PythiaManager {
  if (sharedManager !== null) return sharedManager;
  sharedManager = new PythiaManager({ resourceSubdir: "pythia" });
  return sharedManager;
}

export function getPythiaManager(): PythiaManager | null {
  return sharedManager;
}

export async function stopPythiaManager(): Promise<void> {
  if (sharedManager === null) return;
  await sharedManager.stop();
  sharedManager = null;
}

// The CLI subcommands (server/cmd/multica/cmd_pythia.go) need a way
// to ask the running manager for its loopback URL. That goes over
// the same IPC channel used by other managers — see
// index.ts:setupPythiaIPC for the actual binding. This helper is
// only used by the renderer-side `experimentalAPI.getPythiaURL()`.
export function pythiaLoopbackURL(): string | null {
  return sharedManager?.url() ?? null;
}

// setupPythiaIPC registers the IPC channels the renderer needs to
// talk to the Pythia subprocess. Mirrors the shape of
// setupServerManager() (server-manager.ts:738-759) and
// setupDaemonManager() — keep the pattern uniform so future managers
// can copy-paste the wiring. Called once from index.ts at
// app.whenReady, AFTER setupDaemonManager so the existing server/daemon
// IPC handlers are already registered.
//
// Channels exposed (all main-process handlers; nothing the renderer
// can mutate directly):
//   pythia:get-status   — current ExperimentalStatus string
//   pythia:get-url      — loopback URL, or null when not ready
//   pythia:ensure-up    — bring the service up; rejects on ready timeout
//   pythia:stop         — graceful stop (SIGTERM → 5s grace → SIGKILL)
//   pythia:status       — push channel for status changes
//
// Note: this function does NOT spawn the subprocess itself. Bring-up
// is opt-in via pythia:ensure-up so the manager remains off when
// the pythia_oracle Labs flag is disabled. The lifetime is bounded
// by the `before-quit` hook in index.ts which calls stopPythiaManager.
export function setupPythiaIPC(windowGetter: () => BrowserWindow | null): void {
  void windowGetter;
  ipcMain.handle("pythia:get-status", () => {
    return sharedManager?.status() ?? "idle";
  });
  ipcMain.handle("pythia:get-url", () => {
    return sharedManager?.url() ?? null;
  });
  ipcMain.handle("pythia:ensure-up", async () => {
    if (sharedManager === null) {
      // Construct on demand so a renderer request can bring the
      // service up the first time the user opts into the flag.
      sharedManager = new PythiaManager({ resourceSubdir: "pythia" });
    }
    // Idempotent. sharedManager outlives the IPC call (Electron
    // session-wide). A previous start() (proxy warm-up, earlier tab
    // visit, a concurrent caller) leaves process !== null and a blind
    // start() throws "already started" at manager-template.ts:134,
    // which the renderer then renders as pythia.pythia_unavailable.
    const cur = sharedManager.status();
    if (cur === "ready") return cur;
    if (cur === "starting") {
      // Wait briefly for the in-flight cold boot. Capped at 10s so a
      // hung start fails loud instead of hanging the IPC handler.
      const deadline = Date.now() + 10_000;
      while (sharedManager.status() === "starting" && Date.now() < deadline) {
        await new Promise((r) => setTimeout(r, 100));
      }
      return sharedManager.status();
    }
    await sharedManager.start();
    return sharedManager.status();
  });
  ipcMain.handle("pythia:stop", async () => {
    await stopPythiaManager();
  });
}

// PythiaProxyRequest is the renderer → main wire shape for
// pythia:proxy. We deliberately keep the surface tiny: a path, a
// method, an optional JSON body, and an optional AbortSignal-style
// timeout. The main process validates the path + method against an
// allowlist so the renderer cannot smuggle arbitrary HTTP at the
// loopback Pythia endpoint.
export interface PythiaProxyRequest {
  path: string;
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  body?: unknown;
  // Hard timeout in ms; defaults to 30_000 (Pythia /whatif can take
  // ~10–20s when the swarm deliberates).
  timeoutMs?: number;
  // Optional renderer identity hint. When the caller supplies it,
  // proxyRateLimit uses it as the bucket key so per-webContents
  // quotas are independent. When omitted, the IPC handler falls
  // back to `_event.sender.id` (which is the webContents id) for the
  // same effect.
  identity?: string;
}

export interface PythiaProxyResponse {
  ok: boolean;
  status: number;
  body: unknown;
}

// Pythia proxy endpoint allowlist. Phase 3 only needs /whatif and
// /chat (the two endpoints the renderer actually drives). /predict,
// /state, /world, /scorecard stay server-side so the Skill + CLI
// remain the canonical invocation path.
//
// 0.3.30.3: the full Pythia engine is now vendored (engine/{alerts,
// brief, ledger, swarm, tickers, …}.py). The dashboard needs to pull
// every public FastAPI route — /agent/view, /predictions, /world,
// /scorecard, /state/stream, /personas, /links, /drift, etc. — so the
// allowlist grows from 3 endpoints to ~20. Each one stays a passthrough
// to the loopback URL; the proxy still rate-limits 30 req / min per
// renderer identity so a runaway use-pythia-sse can't melt the manager.
// Read-only / SSE endpoints deliberately come BEFORE the write paths
// below — it's just a code-organization choice, the set membership is
// what gates.
const PYTHIA_PROXY_ALLOWLIST: ReadonlySet<string> = new Set([
  // write paths (renderer → oracle)
  "/whatif",
  "/chat",
  "/predict",
  "/forecast/issue",
  "/model",
  "/swarm/model",
  "/loop",
  "/watchlist",
  "/alerts",
  "/brief/run",
  "/brief/config",
  "/webhooks",
  "/scorecard/resolve",
  // read paths (renderer → oracle)
  "/agent/view",
  "/agent/events",
  "/predictions",
  "/world",
  "/runs",
  "/scorecard",
  "/state",
  "/links",
  "/config",
  "/models",
  "/swarm/models",
  "/personas",
  "/watch",
  "/drift",
  "/alerts/feed",
  "/brief",
]);

// PythiaProxy rate limit: 30 req / min per source. The renderer's
// WhatIfPanel calls /whatif roughly once per user click; 30/min is
// well above the human-driven ceiling and stays far below the loopback
// LLM call budget (runtime_llm_call.go caps at 60/min total across all
// sources — a single chatty user should not starve Skill callers).
const PYTHIA_PROXY_WINDOW_MS = 60_000;
const PYTHIA_PROXY_MAX_REQUESTS = 30;
const proxyBuckets = new Map<string, { count: number; resetAt: number }>();

function proxyRateLimit(identity: string): boolean {
  const now = Date.now();
  const bucket = proxyBuckets.get(identity);
  if (!bucket || bucket.resetAt <= now) {
    proxyBuckets.set(identity, { count: 1, resetAt: now + PYTHIA_PROXY_WINDOW_MS });
    return true;
  }
  if (bucket.count >= PYTHIA_PROXY_MAX_REQUESTS) return false;
  bucket.count += 1;
  return true;
}

// setupPythiaProxyIPC registers the pythia:proxy channel so the
// renderer can drive POST /whatif, /chat, /predict without having
// direct loopback access (Electron contextIsolation blocks renderer
// fetch to 127.0.0.1 from the page origin unless we route it through
// the main process).
//
// Hard rules:
//   1. Path must start with "/" and be in PYTHIA_PROXY_ALLOWLIST.
//   2. Method defaults to POST when a body is provided, else GET.
//   3. Body is JSON-encoded on the wire; we forward verbatim.
//   4. Rate-limited per source (30/min) — see PYTHIA_PROXY_*.
//   5. Manager must be up. We auto-construct on demand so the
//      renderer request is the single source of truth for "is Pythia
//      live?". On manager failure we return ok:false with a 503
//      status so the renderer can fall back to a friendly error.
// managerBootHint maps a bring-up failure into a renderer-friendly
// string. The shape of the error comes from PythiaManager.start:
//   - code = "BINARY_NOT_BUNDLED"  → resources/pythia/engine/ missing
//   - ENOENT from child_process.spawn → python3 not on PATH
//   - EADDRINUSE from uvicorn bind → port collision
//   - anything else → manager failed to start; fall back to message.
export function managerBootHint(err: unknown): string {
  if (err && typeof err === "object") {
    const e = err as { code?: string; message?: string };
    if (e.code === "BINARY_NOT_BUNDLED") {
      return "Pythia 的 Python 引擎还没有被打包进桌面 app。请把 Pythia 源码放到 apps/desktop/vendor/pythia-src/engine/,然后跑 `pnpm --filter @multica/desktop bundle-cli && pnpm --filter @multica/desktop package` 重新打包。";
    }
    if (e.code === "ENOENT") {
      return "找不到 python3。请确认已安装 Python 3.11+,或通过 brew install python@3.11 安装。";
    }
    if (e.code === "EADDRINUSE") {
      return "Pythia 端口被占用。请关闭占用端口的进程,或在 Settings → Labs → Pythia 里切换端口。";
    }
    return e.message ?? "Pythia manager failed to start";
  }
  return "Pythia manager failed to start";
}

export function setupPythiaProxyIPC(): void {
  ipcMain.handle(
    "pythia:proxy",
    async (_event, req: PythiaProxyRequest): Promise<PythiaProxyResponse> => {
      // Bucket key per renderer: prefer the caller-supplied identity,
      // fall back to the webContents id so even the legacy preload
      // (which doesn't pass identity) gets per-tab rate isolation.
      const identity = req.identity ?? String(_event.sender.id);
      if (!proxyRateLimit(identity)) {
        return {
          ok: false,
          status: 429,
          body: { error: "pythia:proxy rate limit exceeded (30/min)" },
        };
      }

      if (!req || typeof req.path !== "string" || !req.path.startsWith("/")) {
        return {
          ok: false,
          status: 400,
          body: { error: "pythia:proxy requires { path: '/…' }" },
        };
      }
      // 0.3.30.3: a few engine routes carry path params
      // (/watchlist/{symbol}, /alerts/{id}, /swarm/model, /webhooks).
      // We accept the exact allowlist entries OR any path whose first
      // segment matches one of the parametric prefixes. The
      // /watchlist /alerts prefix check rejects accidental paths like
      // /watchlist/../etc/passwd because the prefix is matched against
      // the full set member, not against the request path.
      const trimmed = req.path;
      const watchlistSymbol = trimmed.startsWith("/watchlist/");
      const alertsDelete = /^\/alerts\/[^/]+$/.test(trimmed);
      const inAllow = PYTHIA_PROXY_ALLOWLIST.has(trimmed);
      const inParametric = watchlistSymbol || alertsDelete;
      if (!inAllow && !inParametric) {
        return {
          ok: false,
          status: 403,
          body: { error: `pythia:proxy path not allowed: ${trimmed}` },
        };
      }

      const method = req.method ?? (req.body !== undefined ? "POST" : "GET");
      if (method === "GET" && req.body !== undefined) {
        return {
          ok: false,
          status: 400,
          body: { error: "GET requests cannot carry a body" },
        };
      }

      const manager = sharedManager ?? (() => {
        sharedManager = new PythiaManager({ resourceSubdir: "pythia" });
        return sharedManager;
      })();

      const baseUrl = manager.url();
      if (!baseUrl) {
        // Make a best-effort bring-up; if that fails we surface a 503
        // with a structured error so the renderer can map it to a
        // friendly hint (Python missing / resources not bundled /
        // port busy / spawn ENOENT).
        try {
          await manager.start();
        } catch (err) {
          const code =
            (err as Error & { code?: string })?.code ?? "MANAGER_BOOT_FAILED";
          return {
            ok: false,
            status: 503,
            body: {
              error: managerBootHint(err),
              code,
            },
          };
        }
      }
      const url = `${manager.url()}${req.path}`;

      const controller = new AbortController();
      const timeoutMs = req.timeoutMs ?? 30_000;
      const timer = setTimeout(() => controller.abort(), timeoutMs);

      try {
        const init: RequestInit = {
          method,
          signal: controller.signal,
          headers: { "Content-Type": "application/json" },
        };
        if (method !== "GET" && method !== "DELETE" && req.body !== undefined) {
          init.body = JSON.stringify(req.body);
        }
        const res = await fetch(url, init);
        const text = await res.text();
        let parsed: unknown = text;
        if (text) {
          try {
            parsed = JSON.parse(text);
          } catch {
            // Leave as raw text when Pythia returns non-JSON (e.g.
            // an error page). The renderer can render a string body.
          }
        }
        return { ok: res.ok, status: res.status, body: parsed };
      } catch (err) {
        const aborted =
          err instanceof DOMException && err.name === "AbortError";
        return {
          ok: false,
          status: aborted ? 504 : 502,
          body: {
            error: aborted
              ? `pythia:proxy timeout after ${timeoutMs}ms`
              : `pythia:proxy fetch failed: ${(err as Error).message ?? String(err)}`,
          },
        };
      } finally {
        clearTimeout(timer);
      }
    },
  );
}
