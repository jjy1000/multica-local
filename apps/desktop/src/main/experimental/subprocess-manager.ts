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

// READY_TIMEOUT_MIN/MAX (s) — clamp window for both the manifest value and the
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

const managers = new Map<string, BaseExperimentalManager>();

// resolveGenericSubprocessManager returns a cached BaseExperimentalManager
// for a subprocess flag driven entirely by its manifest.runtime block.
// Returns null when the flag has no usable subprocess manifest (missing
// file, wrong kind, no binary) — the caller then surfaces the same
// "idle" stub as before, so a misconfigured manifest degrades cleanly
// rather than crashing the dispatcher.
//
// The binary path in the manifest is relative to resources/ (e.g.
// "code-canvas/run.sh"); we split it into (resourceSubdir, binName)
// because BaseExperimentalManager joins them via resolveResourcePath.
//
// ready_timeout_ms precedence (0.5.28, P0-1 — synthesizer Round 7):
//   1. READY_TIMEOUT_MS env override (parsed + clamped)
//   2. manifest runtime.ready_timeout_ms
//   3. 30_000 fallback
// All three paths are clamped to [10s, 600s] so a too-small manifest value
// (e.g. 1000) or an unbounded env override (=999999999) cannot wedge the
// singleton manager. The env key is also added to daemon.go's
// isBlockedEnvKey (F-005 belt-and-braces) so a user-custom_env override
// cannot arm the agent subprocess env with an adversarial timeout.
export function resolveGenericSubprocessManager(
  flagKey: string,
): ExperimentalManager | null {
  const existing = managers.get(flagKey);
  if (existing) return existing;

  const spec = readManifestRuntime(flagKey);
  if (spec === null) return null;

  const binary = spec.runtime.binary ?? "";
  const slash = binary.indexOf("/");
  // The manifest binary is "<subdir>/<file>" (e.g. "code-canvas/run.sh").
  // A binary with no slash would resolve against resources/ root, which
  // no lab uses — treat it as misconfigured.
  if (slash <= 0) return null;
  const resourceSubdir = binary.slice(0, slash);
  const binName = binary.slice(slash + 1);
  const loopbackService = spec.loopbackService;

  const envOverrideMs = parseReadyTimeoutFromEnv(process.env.READY_TIMEOUT_MS);
  const manifestMs = spec.runtime.ready_timeout_ms;
  const readyTimeoutMs = clampReadyTimeoutMs(
    envOverrideMs ?? manifestMs ?? 30_000,
  );

  const manager = new BaseExperimentalManager({
    name: flagKey,
    resourceSubdir,
    binName,
    args: spec.runtime.args ?? [],
    healthPath: spec.runtime.health_path ?? "/health",
    readyTimeoutMs,
    stopGraceMs: spec.runtime.stop_grace_ms ?? 5_000,
    onReady: (url) => {
      void registerExperimentalUpstream(loopbackService, url);
    },
    onStop: () => {
      void unregisterExperimentalUpstream(loopbackService);
    },
  });
  managers.set(flagKey, manager);
  return manager;
}
