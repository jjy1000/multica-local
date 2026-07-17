import { app } from "electron";

// upstream-registry bridges the desktop main-process managers to
// the in-process server state used by the same-origin reverse proxy
// at server/internal/handler/experimental_proxy.go.
//
// Why a HTTP POST and not an in-process function call: the Go server
// runs as a separate process (spawned by server-manager.ts) from the
// Electron main process. The two already speak HTTP for everything
// else (renderer fetches API, WebSocket, etc.), so we extend that
// pattern instead of reaching for IPC or shared memory.
//
// Endpoint: POST /__experimental/upstream (NOT /api/...) because the
// upstream registry is internal-only — the desktop main process
// authenticates by virtue of being on localhost, but the path does
// not leak into the public API surface.
//
// Wire shape (matches server/internal/handler/experimental_proxy.go):
//   POST /__experimental/upstream
//   Content-Type: application/json
//   Body: {"service": "pythia_oracle", "url": "http://127.0.0.1:<port>"}
//   → 204 No Content
//
//   DELETE /__experimental/upstream/{service}
//   → 204 No Content
//
// 0.3.22 lab consolidation: `claude_science` is no longer registered
// here — Claude Lab (`claude_science_lab`) is an inline flag with no
// subprocess manager and never publishes an upstream URL. The
// `"claude_science"` literal is kept as a no-op sentinel for legacy
// callers (0.3.20 install handlers still on disk) and to match the
// server-side switch in experimental_proxy.go.

// 0.3.25: `service` is a plain string, not a fixed union. The server
// validates it against the catalog's subprocess ProxyRoutes allowlist
// (experimental_proxy.go::isAllowedUpstreamService), so any new
// subprocess flag (e.g. code_canvas) can register without editing a
// TS union here. The desktop side only forwards; the server is the
// authority on which services may register.
interface UpstreamRegistryEntry {
  service: string;
  url: string;
}

// apiBaseURL resolves the desktop's notion of the local server URL.
// server-manager.ts writes ~/.multica/desktop.json with apiUrl on
// launch. In dev / packaged the port is stable (8090) but reading
// from the file matches the renderer's `apiBaseURL()` helper so we
// never disagree about which Multica server we are talking to.
let cachedBaseURL: string | null = null;
async function apiBaseURL(): Promise<string> {
  if (cachedBaseURL !== null) return cachedBaseURL;
  try {
    const fs = await import("node:fs/promises");
    const os = await import("node:os");
    const path = await import("node:path");
    const cfgPath = path.join(os.homedir(), ".multica", "desktop.json");
    const raw = await fs.readFile(cfgPath, "utf-8");
    const cfg = JSON.parse(raw) as { apiUrl?: string };
    if (typeof cfg.apiUrl === "string" && cfg.apiUrl.length > 0) {
      cachedBaseURL = cfg.apiUrl;
      return cfg.apiUrl;
    }
  } catch {
    // Fall through to env-based guess.
  }
  const fromEnv = process.env["MULTICA_API_URL"];
  cachedBaseURL = fromEnv ?? "http://localhost:8090";
  return cachedBaseURL;
}

// registerExperimentalUpstream POSTs the manager's loopback URL to
// the server's in-process registry so /experimental/{service}/*
// reverse-proxy requests have an upstream to forward to.
//
// Safe to call multiple times — the server overwrites the entry on
// every POST. Errors are logged but never thrown: registration is
// best-effort; if the server is down at the moment of spawn the
// next manager.start() (or the next renderer poll) will surface the
// real cause.
export async function registerExperimentalUpstream(
  service: UpstreamRegistryEntry["service"],
  url: string,
): Promise<void> {
  const base = await apiBaseURL();
  try {
    const res = await fetch(`${base}/__experimental/upstream`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ service, url } satisfies UpstreamRegistryEntry),
    });
    if (!res.ok) {
      process.stderr.write(
        `[upstream-registry] POST /__experimental/upstream failed: ${res.status}\n`,
      );
    }
  } catch (err) {
    process.stderr.write(
      `[upstream-registry] POST /__experimental/upstream errored: ${
        err instanceof Error ? err.message : String(err)
      }\n`,
    );
  }
}

export async function unregisterExperimentalUpstream(
  service: UpstreamRegistryEntry["service"],
): Promise<void> {
  const base = await apiBaseURL();
  try {
    await fetch(`${base}/__experimental/upstream/${service}`, {
      method: "DELETE",
    });
  } catch (err) {
    process.stderr.write(
      `[upstream-registry] DELETE /__experimental/upstream/${service} errored: ${
        err instanceof Error ? err.message : String(err)
      }\n`,
    );
  }
}

// __unusedAppIsPackaged is referenced so the linter does not strip
// the electron `app` import — we may want to read app.isPackaged
// here later to skip the network call in dev mode where the server
// may be off. For now keep it as a forward declaration.
void app;