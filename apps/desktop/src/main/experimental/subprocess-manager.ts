// Generic manifest-driven subprocess manager (0.3.25).
//
// Before this file, apps/desktop only knew how to spawn one
// subprocess lab: pythia_oracle, via a hand-written pythia-manager.ts.
// manager-factory.ts carried an `if (flagKey === "pythia_oracle")`
// switch and every other subprocess flag (code_canvas) fell through to
// a dead "idle" stub whose ensureUp() threw and whose proxy always
// returned 502.
//
// This module closes that gap for subprocess flags that need no
// bespoke env: it reads the flag's manifest
// (resources/experiments/<flagKey>/manifest.json), pulls the
// spec.runtime block (binary / args / health_path / timeouts), and
// wraps a BaseExperimentalManager — the same base pythia-manager uses.
// A subprocess flag whose binary is a self-contained HTTP server
// (like code_canvas/run.sh) now spawns, health-checks, registers its
// loopback URL, and serves through the reverse proxy with zero
// per-flag TypeScript.
//
// pythia_oracle keeps its dedicated manager because it needs the
// Multica JWT bridge env (MULTICA_AGENT_RUNTIME_URL / _API_TOKEN);
// manager-factory routes pythia to pythia-manager and every other
// subprocess flag here.

import { randomBytes } from "node:crypto";
import { readFileSync } from "node:fs";
import {
  BaseExperimentalManager,
  resolveResourcePath,
  type ExperimentalManager,
} from "./manager-template";
import {
  registerExperimentalUpstream,
  unregisterExperimentalUpstream,
} from "./upstream-registry";

// ManifestRuntimeSpec mirrors the spec.runtime block in
// apps/desktop/resources/experiments/<flagKey>/manifest.json. Only the
// fields the manager needs are typed; unknown fields are ignored.
interface ManifestRuntimeSpec {
  kind?: string;
  binary?: string;
  args?: string[];
  health_path?: string;
  ready_timeout_ms?: number;
  stop_grace_ms?: number;
}

interface ExperimentManifest {
  spec?: {
    runtime?: ManifestRuntimeSpec;
    surface?: { loopback_service?: string };
  };
}

// readManifestRuntime loads and parses the runtime spec for flagKey.
// Returns null when the manifest is missing or malformed so the caller
// can fall back to a clear "idle" surface instead of throwing during
// module init.
function readManifestRuntime(
  flagKey: string,
): { runtime: ManifestRuntimeSpec; loopbackService: string } | null {
  try {
    const manifestPath = resolveResourcePath(
      "experiments",
      flagKey,
      "manifest.json",
    );
    const raw = readFileSync(manifestPath, "utf-8");
    const parsed = JSON.parse(raw) as ExperimentManifest;
    const runtime = parsed.spec?.runtime;
    if (!runtime || runtime.kind !== "subprocess" || !runtime.binary) {
      return null;
    }
    const loopbackService = parsed.spec?.surface?.loopback_service ?? flagKey;
    return { runtime, loopbackService };
  } catch {
    return null;
  }
}

// generateExperimentalApiKey returns a 32-byte hex string (64 chars).
// Subprocesses that opt into SEMANTICA_REQUIRE_AUTH=1 (semantica) use
// this as their X-API-Key shared secret. The desktop main process
// READY_TIMEOUT_MS env override. Below 10s starves first cold start (sentence-
// transformers + torch import ≈30s on warm caches; first model download up to
// 180s on a fresh disk). Above 600s holds the managers.get(flagKey) singleton
// forever — every ensure-up blocks the IPC dispatcher until the spawn
// resolves, so an unbounded upper bound is a per-flag DoS (R4 P0-1 attack).
const READY_TIMEOUT_MIN_MS = 10_000;
const READY_TIMEOUT_MAX_MS = 600_000;

function clampReadyTimeoutMs(value: number | undefined): number {
  if (typeof value !== "number" || !Number.isFinite(value) || value <= 0) {
    return 30_000;
  }
  if (value < READY_TIMEOUT_MIN_MS) return READY_TIMEOUT_MIN_MS;
  if (value > READY_TIMEOUT_MAX_MS) return READY_TIMEOUT_MAX_MS;
  return value;
}

// parseReadyTimeoutFromEnv reads the READY_TIMEOUT_MS env override (millis).
// Returns undefined when unset / not a positive integer. Mirrors the PATH_PY
// pattern in apps/desktop/vendor/semantica/run.sh:85 (the desktop main process
// honors the same env the upstream script honors).
function parseReadyTimeoutFromEnv(raw: string | undefined): number | undefined {
  if (raw === undefined || raw === "") return undefined;
  const n = Number(raw);
  if (!Number.isFinite(n) || n <= 0) return undefined;
  return n;
}

const WS_ID_REGEX = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function isValidWorkspaceId(wsId: string | null | undefined): wsId is string {
  return typeof wsId === "string" && WS_ID_REGEX.test(wsId);
}

// generateExperimentalApiKey returns a 32-byte hex string (64 chars).
// Subprocesses that opt into SEMANTICA_REQUIRE_AUTH=1 (semantica)
// use this as their X-API-Key shared secret. The desktop main
// process owns the key (in-memory transport to the server via
// upstreamRegister IPC) — the per-launch file at
// $GRAPH_PATH.api-key can be deleted; pre-0.5.29 that 0600 file
// was reachable by any user-plugin `python3 -I` child (F-013 class).
function generateExperimentalApiKey(): string {
  return randomBytes(32).toString("hex");
}

const managers = new Map<string, BaseExperimentalManager>();

// resolveGenericSubprocessManager returns a cached
// BaseExperimentalManager for a subprocess flag driven entirely by
// its manifest.runtime block. Returns null when the flag has no
// usable subprocess manifest (missing file, wrong kind, no binary).
//
// 0.5.29 P0-2: per-(flagKey, wsId) manager key. Pre-0.5.29 the
// singleton was keyed by flagKey only — switching the active
// workspace returned the cached manager, so WS-B inherited WS-A's
// process + graph.json + port + loopback URL. The new key is
// `${flagKey}@${wsId}`; wsId is mandatory for any subprocess flag
// that writes per-workspace state to disk. Invalid wsId is refused
// with a console.warn so the bug is visible without wedging IPC.
export function resolveGenericSubprocessManager(
  flagKey: string,
  wsId: string | null,
): ExperimentalManager | null {
  if (!isValidWorkspaceId(wsId)) {
    console.warn(
      `[labs] resolveGenericSubprocessManager(${flagKey}) called with invalid wsId=${JSON.stringify(wsId)}; refusing to register`,
    );
    return null;
  }
  const cacheKey = `${flagKey}@${wsId}`;
  const existing = managers.get(cacheKey);
  if (existing) return existing;

  const spec = readManifestRuntime(flagKey);
  if (spec === null) return null;

  const binary = spec.runtime.binary ?? "";
  const slash = binary.indexOf("/");
  if (slash <= 0) return null;
  const resourceSubdir = binary.slice(0, slash);
  const binName = binary.slice(slash + 1);
  const loopbackService = spec.loopbackService;

  const envOverrideMs = parseReadyTimeoutFromEnv(process.env.READY_TIMEOUT_MS);
  const manifestMs = spec.runtime.ready_timeout_ms;
  const readyTimeoutMs = clampReadyTimeoutMs(
    envOverrideMs ?? manifestMs ?? 30_000,
  );

  // 0.5.29 P1-1: per-spawn API key. Generated here so the desktop
  // main process holds the only in-memory copy; run.sh reads it
  // from SEMANTICA_API_KEY and refuses to fall back to xxd (no
  // file, no per-launch persistence).
  const apiKey = generateExperimentalApiKey();

  // Per-workspace child env. SEMANTICA_WORKSPACE_ID drives the
  // graph.json path inside run.sh; SEMANTICA_API_KEY is the
  // X-API-Key the server-side Director injects (R4 P1-1 fix).
  const childEnv: Record<string, string> = {
    SEMANTICA_WORKSPACE_ID: wsId,
    SEMANTICA_API_KEY: apiKey,
  };

  const manager = new BaseExperimentalManager({
    name: cacheKey,
    resourceSubdir,
    binName,
    args: spec.runtime.args ?? [],
    healthPath: spec.runtime.health_path ?? "/health",
    readyTimeoutMs,
    stopGraceMs: spec.runtime.stop_grace_ms ?? 5_000,
    childEnv,
    onReady: (url) => {
      // 0.5.29 P1-1: key travels in IPC body so the server-side
      // reverse proxy can replace any caller-supplied X-API-Key
      // with this in-memory value (R4 TOP-1 mitigation).
      void registerExperimentalUpstream(loopbackService, url, apiKey);
    },
    onStop: () => {
      void unregisterExperimentalUpstream(loopbackService);
    },
  });
  managers.set(cacheKey, manager);
  return manager;
}

// stopAllSubprocessManagers stops and clears every cached manager.
// Used at workspace switch by the IPC dispatcher when the renderer
// signals "drop the per-workspace singletons for the previous wsId".
export async function stopAllSubprocessManagers(): Promise<void> {
  const all = Array.from(managers.values());
  for (const m of all) {
    try {
      await m.stop();
    } catch (err) {
      console.warn(`[labs] stop() failed for ${m.name}:`, err);
    }
  }
  managers.clear();
}
