// LLMWikiBridgeManager (0.3.27+ PR-5 / C2-mini).
//
// Owns the lifecycle of the LLM Wiki Bridge subprocess — a stdio
// JSON-RPC 2.0 MCP server that bridges Multica agents to a local
// /Applications/LLM Wiki.app install.
//
// Why a bespoke manager (and not resolveGenericSubprocessManager):
//
//   - The generic subprocess-manager (apps/desktop/src/main/experimental/
//     subprocess-manager.ts) is HTTP-centric: it spawns a binary, picks
//     a free loopback port, appends it as the last argv, and probes
//     GET <health_path>. The LLM Wiki Bridge stub speaks newline-
//     delimited JSON-RPC 2.0 over stdio with NO HTTP listener, so the
//     generic manager would time out the very first health probe and
//     return "SERVICE_NEEDS_SETUP" instead of "ready".
//   - 0.3.27 B8 already shipped a stdio stub at resources/experiments/
//     llm_wiki_bridge/run.sh; this manager is the IPC glue that
//     finally wires that stub (or the .app's real MCP server when
//     present) into the Labs platform's generic experimental:<flag>:*
//     channel namespace.
//
// Scope (PR-5 / C2-mini, real verbs 0.5.17):
//
//   - IPC通路打通. Spawn the subprocess, perform the MCP handshake,
//     mark ready, expose status / URL / stop to the generic IPC
//     dispatcher.
//   - vault_read / vault_write forward to the multica backend over
//     HTTP (/api/experimental/llm-wiki/*) — the same surface the Go
//     Skill adapter uses — so the stdio verbs are real, not the
//     0.3.27 deterministic "[stub] would read: …" payloads. The
//     `llm_wiki_bridge` flag gate stays server-side (403/404 when
//     off); the manager injects MULTICA_API_URL / MULTICA_API_TOKEN
//     (desktop profile PAT) into the child env at spawn so the stub
//     can call the backend.
//
// Failure policy:
//
//   - /Applications/LLM Wiki.app absent → spawn the bundled stub.
//   - Binary missing on disk (post-bundle regression) → BINARY_NOT_BUNDLED
//     error matching the pattern in BaseExperimentalManager.
//   - Subprocess crashes mid-life → status flips to "stopped" via the
//     'exit' handler; next ensureUp() respawns.
//
// The manager deliberately does NOT register an upstream URL — stdio
// transports have no loopback HTTP endpoint. The Go server-side
// multica-llm-wiki Skill calls /api/experimental/llm-wiki/* over the
// existing chi router path (server/internal/handler/llm_wiki_bridge.go),
// which is unaffected by the desktop manager's lifecycle. The manager
// here exists purely so the Labs platform has a non-idle surface for
// the flag — without it, the IPC dispatcher returned "idle" forever
// because the flag's static descriptor was kind: "inline".

import { spawn, type ChildProcess } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { app } from "electron";
import { resolveResourcePath } from "./manager-template";
import type {
  ExperimentalManager,
  ExperimentalStatus,
} from "./manager-template";

// LLMWikiAppBundleID is the macOS bundle identifier of the local
// desktop app we prefer when present. Since the app's v0.6.x
// releases, `Contents/Resources/mcp-server` is a DIRECTORY (Node
// package: dist/src/index.js + vendored node_modules) — the real
// stdio MCP entry must be spawned through a Node runtime with
// LLM_WIKI_API_TOKEN in the env (exactly the shape the app's own
// "copy MCP config" sheet ships). 0.5.92 regression: this manager
// used to treat the directory itself as the spawn binary, which
// EACCES'd every start once the user had the v0.6.x app installed.
//
// We spawn Electron's own binary with ELECTRON_RUN_AS_NODE=1 rather
// than relying on a system `node` on PATH. When the entry is
// missing (older app layouts or no app at all) we fall back to the
// bundled Python stub so the Labs flag stays operational.
//
// Why not probe via `mdfind` / `osascript`: spawning `mdfind` from
// the Electron main process is slow (>200ms cold) and unreliable
// across sandboxed builds. fs.accessSync is sub-millisecond and
// matches the convention other managers use for bundled-binary
// preflight checks.
const LLM_WIKI_APP_MCP_ENTRY =
  "/Applications/LLM Wiki.app/Contents/Resources/mcp-server/dist/src/index.js";

// readLLMWikiToken resolves the API token to inject into the real
// MCP server's env (LLM_WIKI_API_TOKEN — the same variable the
// app's own "copy MCP config" sheet ships). Priority mirrors the
// Go-side DiscoverToken: Multica's own paste store first, then the
// app's Tauri app-state (apiConfig.token). Silent "" when neither
// exists — the app's allow-unauthenticated mode still works.
function readLLMWikiToken(): string {
  try {
    const store = JSON.parse(
      readFileSync(join(homedir(), ".multica", "llm-wiki.json"), "utf-8"),
    ) as { token?: unknown };
    if (typeof store.token === "string" && store.token !== "") return store.token;
  } catch {
    // fall through
  }
  try {
    const state = JSON.parse(
      readFileSync(
        join(
          homedir(),
          "Library",
          "Application Support",
          "com.llmwiki.app",
          "app-state.json",
        ),
        "utf-8",
      ),
    ) as { apiConfig?: { token?: unknown } };
    const tok = state.apiConfig?.token;
    if (typeof tok === "string" && tok !== "") return tok;
  } catch {
    // fall through
  }
  return "";
}

// Resource subdir under apps/desktop/resources/. bundle-cli.mjs
// stages the vendor copy (apps/desktop/vendor/llm-wiki-bridge/run.sh)
// into this exact path. The stub implements MCP over stdio and is
// the fallback used when the .app is not present.
const LLMWIKI_RESOURCE_SUBDIR = "experiments/llm_wiki_bridge";
const LLMWIKI_STUB_BIN = "run.sh";

// READY_TIMEOUT_MS bounds how long we wait for the MCP `initialize`
// reply before flipping status to "error". 10s matches the manifest's
// ready_timeout_ms and is generous enough for the Python stub (cold
// start ~200ms on a healthy machine).
const READY_TIMEOUT_MS = 10_000;

// JsonRpcResponse is one line we read off stdout. The stub returns a
// result for `initialize` / `tools/list` / `tools/call` / `health`;
// notifications/initialized carries no reply per MCP spec.
interface JsonRpcResponse {
  jsonrpc: "2.0";
  id: number;
  result?: unknown;
  error?: { code: number; message: string };
}

// PendingCall ties a JSON-RPC request id to its resolver so the
// stdout line-buffering loop can dispatch replies to the right
// waiter. We don't expose callTool() publicly in C2-mini — this
// machinery is wired but unused so C2-full can flip the surface on
// without re-architecting.
interface PendingCall {
  resolve(value: JsonRpcResponse): void;
  reject(err: Error): void;
  timer: NodeJS.Timeout;
}

class LLMWikiBridgeManager implements ExperimentalManager {
  readonly name = "llm_wiki_bridge";

  private child: ChildProcess | null = null;
  private statusValue: ExperimentalStatus = "idle";
  private nextRpcId = 1;
  private pending = new Map<number, PendingCall>();
  private stdoutBuf = "";

  status(): ExperimentalStatus {
    return this.statusValue;
  }

  // url() returns null because stdio JSON-RPC has no loopback HTTP
  // endpoint to surface. The renderer's LLMWikiBridgeView fetches
  // /api/experimental/llm-wiki/status directly from the Go server,
  // which has its own HTTP route gated by the same flag. Keeping
  // this null matches the contract of the experimental:<flag>:get-url
  // IPC channel (manager exists, no HTTP URL to share).
  url(): string | null {
    return null;
  }

  async start(): Promise<void> {
    if (this.child !== null) {
      throw new Error(`experimental manager ${this.name}: already started`);
    }
    this.statusValue = "starting";

    const spawnPlan = this.resolveSpawnPlan();
    if (!existsSync(spawnPlan.bin)) {
      this.statusValue = "error";
      const err = new Error(
        `experimental service "${this.name}" is not bundled: ${spawnPlan.bin} does not exist. ` +
          "Drop the vendor copy into apps/desktop/vendor/llm-wiki-bridge/ then re-run " +
          "`pnpm --filter @multica/desktop bundle-cli`.",
      ) as Error & { code?: string };
      err.code = "BINARY_NOT_BUNDLED";
      throw err;
    }

    // stdio MCP servers do not bind a port — pipe stdin/stdout so we
    // can both write JSON-RPC requests and read replies. stderr is
    // captured so a Python traceback (e.g. syntax error after a
    // stub edit) shows up in the main-process log rather than being
    // swallowed.
    //
    // Spawn with an explicit cwd — MCP servers resolve relative
    // paths against cwd, and the bridge works on the user's LLM Wiki
    // vault under ~/Documents/. A packaged app launched from Finder
    // inherits cwd="/", which is never the right directory. Fall
    // back to ~/Documents when the vault subdir does not yet exist
    // so first-launch spawns still succeed.
    //
    // The child env carries MULTICA_API_URL + MULTICA_API_TOKEN so
    // the stub can call /api/experimental/llm-wiki/* on the backend;
    // the flag gate stays server-side. bridgeEnv() resolves the
    // credential pair from the same source daemon-manager uses
    // (~/.multica/profiles/desktop-<host>/config.json).
    const documents = app.getPath("documents");
    const vaultCwd = join(documents, "llm wiki");
    const child = spawn(spawnPlan.bin, spawnPlan.args, {
      cwd: existsSync(vaultCwd) ? vaultCwd : documents,
      env: this.bridgeEnv(spawnPlan.appMode),
      stdio: ["pipe", "pipe", "pipe"],
      detached: false,
    });
    this.child = child;
    this.nextRpcId = 1;

    // Async spawn errors (ENOENT / EACCES) must not escape to Node's
    // default unhandled-rejection path. Mirror the BaseExperimentalManager
    // pattern: tag the child with a 'spawnFailed' flag we check after
    // the handshake so a missing interpreter fails fast instead of
    // hanging READY_TIMEOUT_MS.
    let spawnFailed = false;
    child.once("error", (err) => {
      spawnFailed = true;
      this.statusValue = "error";
      this.rejectAllPending(err);
      process.stderr.write(`[${this.name}] spawn error: ${err.message}\n`);
    });

    child.stdout?.on("data", (chunk: Buffer) => {
      this.handleStdout(chunk.toString("utf8"));
    });
    child.stderr?.on("data", (chunk: Buffer) => {
      process.stderr.write(`[${this.name}] ${chunk.toString("utf8")}`);
    });
    child.once("exit", (code, signal) => {
      const wasStopping = this.statusValue === "stopping";
      this.child = null;
      this.statusValue = "stopped";
      this.rejectAllPending(
        new Error(
          `${this.name} exited code=${code ?? "null"} signal=${signal ?? "null"}`,
        ),
      );
      if (!wasStopping && code !== 0 && code !== null) {
        process.stderr.write(
          `[${this.name}] exited unexpectedly code=${code} signal=${signal}\n`,
        );
      }
    });

    // MCP handshake. We send `initialize` with a tagged id=1, await
    // the reply under READY_TIMEOUT_MS, then send the
    // notifications/initialized notification (no reply expected).
    try {
      await this.rpcRequest("initialize", {
        protocolVersion: "2024-11-05",
        clientInfo: { name: "multica-desktop", version: app.getVersion() },
        capabilities: {},
      });
      this.writeRpc({ jsonrpc: "2.0", method: "notifications/initialized" });

      // Note: a tools/list probe would let us validate incoming
      // callTool names in C2-full (0.3.29). C2-mini keeps the call
      // surface minimal — the stub already echoes the requested name
      // back, so the wire shape is stable enough to lock the manager
      // down without an extra round-trip.

      if (spawnFailed) {
        throw new Error(`${this.name} spawn failed; see main-process log`);
      }
      this.statusValue = "ready";
    } catch (err) {
      this.statusValue = "error";
      await this.stop();
      throw err;
    }
  }

  async stop(): Promise<void> {
    const child = this.child;
    if (child === null) {
      this.statusValue = "stopped";
      return;
    }
    this.statusValue = "stopping";

    const exited = new Promise<void>((resolve) => {
      child.once("exit", () => resolve());
    });

    try {
      child.kill("SIGTERM");
    } catch {
      // already dead
    }

    const grace = new Promise<void>((resolve) =>
      setTimeout(resolve, 3_000),
    );
    await Promise.race([exited, grace]);

    if (this.child !== null) {
      try {
        child.kill("SIGKILL");
      } catch {
        // already gone
      }
      await Promise.race([
        exited,
        new Promise<void>((resolve) => setTimeout(resolve, 2_000)),
      ]);
    }

    this.child = null;
    this.statusValue = "stopped";
    this.rejectAllPending(new Error(`${this.name} manager stopped`));
  }

  // callTool exposes a minimal tool-call surface so the renderer can
  // drive the bridge through the experimental IPC layer. It round-trips
  // a JSON-RPC `tools/call` to the subprocess and returns its reply.
  //
  // Not wired into a public IPC channel in C2-mini — the renderer
  // reaches the bridge via /api/experimental/llm-wiki/* HTTP routes
  // (handled by the Go server). This method is here so future work
  // can add an experimental:llm_wiki_bridge:call-tool channel without
  // re-touching the manager.
  async callTool(
    name: string,
    args: Record<string, unknown>,
  ): Promise<unknown> {
    if (this.child === null || this.statusValue !== "ready") {
      throw new Error(`${this.name} is not ready (status=${this.statusValue})`);
    }
    const reply = await this.rpcRequest("tools/call", {
      name,
      arguments: args,
    });
    return reply;
  }

  // SpawnPlan describes what start() actually launches: either the
  // app's real stdio MCP server (appMode — Electron binary run as
  // Node, entry script as the sole argv) or the bundled Python stub.
  private resolveSpawnPlan(): { bin: string; args: string[]; appMode: boolean } {
    if (existsSync(LLM_WIKI_APP_MCP_ENTRY)) {
      return {
        bin: process.execPath,
        args: [LLM_WIKI_APP_MCP_ENTRY],
        appMode: true,
      };
    }
    return {
      bin: resolveResourcePath(LLMWIKI_RESOURCE_SUBDIR, LLMWIKI_STUB_BIN),
      args: [],
      appMode: false,
    };
  }

  // bridgeEnv builds the child env. Two credential pairs depending
  // on the spawn mode:
  //
  //   - Stub mode (appMode=false): vault_read / vault_write forward
  //     to the multica backend (/api/experimental/llm-wiki/*), so
  //     the child needs MULTICA_API_URL + MULTICA_API_TOKEN. The
  //     main process does not carry the token in its own env — read
  //     it from the desktop profile config.json, the same file
  //     daemon-manager writes the PAT into (daemon-manager.ts::
  //     syncToken).
  //   - App mode (appMode=true): the real MCP server talks to the
  //     LLM Wiki desktop API itself and wants LLM_WIKI_API_TOKEN —
  //     the same env var the app's own MCP config ships, resolved
  //     with the same priority as the Go-side DiscoverToken
  //     (Multica's paste store first, then the app's app-state).
  //
  // Both honour a MULTICA_API_URL already present in process.env so
  // an ops override (e.g. launchctl setenv) wins over the
  // desktop.json default, mirroring cmd_experimental.go::
  // experimentalAPIURL.
  private bridgeEnv(appMode: boolean): NodeJS.ProcessEnv {
    const env = { ...process.env };
    if (!env.MULTICA_API_URL) {
      env.MULTICA_API_URL = this.resolveApiUrl();
    }
    if (!env.MULTICA_API_TOKEN) {
      const token = this.resolveProfileToken(env.MULTICA_API_URL);
      if (token !== "") env.MULTICA_API_TOKEN = token;
    }
    if (appMode) {
      // Run the Electron binary as a plain Node runtime for the
      // app's JS MCP entry — no dependency on a system `node`.
      env.ELECTRON_RUN_AS_NODE = "1";
      const llmToken = readLLMWikiToken();
      if (llmToken !== "") env.LLM_WIKI_API_TOKEN = llmToken;
    }
    return env;
  }

  // resolveApiUrl mirrors server-manager's readDesktopConfig: the
  // bundled backend base comes from ~/.multica/desktop.json, with the
  // CLI default as fallback. ENOENT (first launch) → default; no
  // panic so a half-configured desktop still spawns the bridge.
  private resolveApiUrl(): string {
    try {
      const raw = readFileSync(
        join(homedir(), ".multica", "desktop.json"),
        "utf-8",
      );
      const cfg = JSON.parse(raw) as { apiUrl?: unknown };
      if (typeof cfg.apiUrl === "string" && cfg.apiUrl !== "") {
        return cfg.apiUrl;
      }
    } catch {
      // fall through to default
    }
    return "http://127.0.0.1:8090";
  }

  // resolveProfileToken reads the token from the desktop profile
  // config.json. Profile naming mirrors daemon-manager.ts: the
  // `desktop-<host>` profile whose host is derived from the API URL
  // (the same <host> derivation deriveProfileName uses). A missing or
  // unparseable file is non-fatal — the stub will get an unsigned
  // request and the server will surface 401 via the tool result.
  private resolveProfileToken(apiUrl: string): string {
    try {
      const host = new URL(apiUrl).host.replace(/:/g, "-").toLowerCase();
      const cfgPath = join(
        homedir(),
        ".multica",
        "profiles",
        `desktop-${host}`,
        "config.json",
      );
      const cfg = JSON.parse(readFileSync(cfgPath, "utf-8")) as {
        token?: unknown;
      };
      return typeof cfg.token === "string" ? cfg.token : "";
    } catch {
      return "";
    }
  }

  // writeRpc writes one newline-delimited JSON-RPC message to the
  // subprocess's stdin. The MCP wire spec REQUIRES \n between
  // messages — the Python stub splits on it (run.sh:111).
  private writeRpc(msg: { jsonrpc: "2.0"; id?: number; method: string; params?: unknown }): void {
    const stdin = this.child?.stdin;
    if (stdin === null || stdin === undefined) {
      throw new Error(`${this.name}: stdin is not writable`);
    }
    stdin.write(JSON.stringify(msg) + "\n");
  }

  // rpcRequest sends a JSON-RPC request and returns the matching
  // reply. Bounded by READY_TIMEOUT_MS so a wedged subprocess cannot
  // hang the renderer-facing IPC handler.
  private rpcRequest(method: string, params: unknown): Promise<JsonRpcResponse> {
    return new Promise<JsonRpcResponse>((resolve, reject) => {
      if (this.child === null) {
        reject(new Error(`${this.name}: not running`));
        return;
      }
      const id = this.nextRpcId++;
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(
          new Error(
            `${this.name}: ${method} timed out after ${READY_TIMEOUT_MS}ms`,
          ),
        );
      }, READY_TIMEOUT_MS);
      this.pending.set(id, { resolve, reject, timer });
      try {
        this.writeRpc({ jsonrpc: "2.0", id, method, params });
      } catch (err) {
        clearTimeout(timer);
        this.pending.delete(id);
        reject(err instanceof Error ? err : new Error(String(err)));
      }
    });
  }

  // handleStdout buffers partial lines (a JSON-RPC reply may arrive
  // across two read() callbacks) and dispatches complete lines to
  // the pending-call resolver. Lines that don't parse as JSON are
  // logged and dropped — the stub never writes non-JSON to stdout
  // except the heartbeat notification, which we tolerate silently.
  private handleStdout(data: string): void {
    this.stdoutBuf += data;
    let nl = this.stdoutBuf.indexOf("\n");
    while (nl >= 0) {
      const line = this.stdoutBuf.slice(0, nl).trim();
      this.stdoutBuf = this.stdoutBuf.slice(nl + 1);
      if (line.length > 0) {
        this.dispatchLine(line);
      }
      nl = this.stdoutBuf.indexOf("\n");
    }
  }

  private dispatchLine(line: string): void {
    let parsed: JsonRpcResponse | { jsonrpc: "2.0"; method: string; params?: unknown };
    try {
      parsed = JSON.parse(line);
    } catch {
      // The stub occasionally emits heartbeat notifications
      // (server/heartbeat) that don't match the response shape —
      // log and drop without raising.
      return;
    }
    // Server-initiated notification (e.g. server/heartbeat): no id.
    if (
      typeof parsed === "object" &&
      parsed !== null &&
      "method" in parsed &&
      !("id" in parsed)
    ) {
      return;
    }
    const reply = parsed as JsonRpcResponse;
    if (typeof reply.id !== "number") return;
    const pending = this.pending.get(reply.id);
    if (pending === undefined) return;
    this.pending.delete(reply.id);
    clearTimeout(pending.timer);
    if (reply.error) {
      pending.reject(
        new Error(
          `${this.name}: RPC ${reply.id} error ${reply.error.code}: ${reply.error.message}`,
        ),
      );
    } else {
      pending.resolve(reply);
    }
  }

  private rejectAllPending(err: Error): void {
    for (const [, p] of this.pending) {
      clearTimeout(p.timer);
      p.reject(err);
    }
    this.pending.clear();
  }
}

// Module-level singleton. Mirrors the setupPythiaManager pattern so
// manager-factory.ts can lazily import getLLMWikiBridgeManager()
// without spawning at module-init time. Flag-off users never pay the
// handshake cost.
let sharedManager: LLMWikiBridgeManager | null = null;

export function setupLLMWikiBridgeManager(): LLMWikiBridgeManager {
  if (sharedManager !== null) return sharedManager;
  sharedManager = new LLMWikiBridgeManager();
  return sharedManager;
}

export function getLLMWikiBridgeManager(): LLMWikiBridgeManager | null {
  return sharedManager;
}

export async function stopLLMWikiBridgeManager(): Promise<void> {
  if (sharedManager === null) return;
  await sharedManager.stop();
  sharedManager = null;
}