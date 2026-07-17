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

  const manager = new BaseExperimentalManager({
    name: flagKey,
    resourceSubdir,
    binName,
    args: spec.runtime.args ?? [],
    healthPath: spec.runtime.health_path ?? "/health",
    readyTimeoutMs: spec.runtime.ready_timeout_ms ?? 30_000,
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
