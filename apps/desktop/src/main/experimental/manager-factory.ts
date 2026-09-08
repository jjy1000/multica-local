// Manager factory: resolves a flag's runtime kind into a live
// ExperimentalManager surface for the generic IPC dispatcher.
//
// Background: 0.3.18 had per-manager files (pythia-manager.ts,
// claude-science-manager.ts) each registering their own
// ipcMain.handle channels. Adding a new subprocess lab required
// editing each new manager's setupXxxIPC and the index.ts call site.
// The IPC dispatcher (experimental/ipc-dispatcher.ts) routes through a
// generic experimental:<flagKey>:<verb> channel namespace; this file
// is the single place that maps a flagKey → its manager surface.
//
// Current state (0.3.25):
//
//   - staticFlagDescriptors mirrors all 8 catalog flags so the IPC
//     layer is complete on a cold boot, before loadFlagDescriptors()
//     resolves the server catalog fetch.
//   - subprocess spawn: only pythia_oracle has a dedicated manager
//     (pythia-manager.ts) today; other subprocess flags (code_canvas)
//     get an "idle" surface until a manifest-driven generic spawner
//     lands. See resolveManager below.
//
// Out of scope: the preload bridge for `experimental:<flagKey>:<verb>`
// IPC channels. Today the renderer calls `pythia:*` / `claude-science:*`
// directly; the generic surface is used by the desktop itself.

import { getPythiaManager, setupPythiaManager } from "../pythia-manager";
import {
  getLLMWikiBridgeManager,
  setupLLMWikiBridgeManager,
} from "./llm-wiki-bridge-manager";
import { resolveGenericSubprocessManager } from "./subprocess-manager";

export type ManagerFactoryKind = "subprocess" | "inline" | "headless" | "none";

export interface ManagerFactoryDescriptor {
  flagKey: string;
  kind: ManagerFactoryKind;
  // The label the sidebar / Labs UI uses for the entry. i18n key, not
  // a translated string — the renderer looks the label up in its own
  // locale table.
  label: string;
  // Optional message returned to the renderer when the manager is
  // asked for its URL but no subprocess is registered. Default is a
  // generic "manager is not running" copy.
  notRunningMessage?: string;
}

// flagDescriptors is the source of truth for which flags the desktop
// spawns / tracks. The list mirrors the server's
// server/internal/experimental/catalog.go runtime_kind field, but
// the desktop does NOT call back to the server at boot to read
// catalog — the static list is the same data shape, in the same
// iteration order, so the two stay in sync via code review.
//
// Adding a new flag here is the desktop-side equivalent of the
// 0.3.18 "add a manager file" task. Future plan: read the catalog
// from a /api/experimental-flags response at boot and merge it
// with the desktop-side binary path config.
//
// 0.3.25: the static list now mirrors ALL catalog flags (9), not
// just the 3 that ship a desktop-side manager. Previously only
// claude_science_lab / pythia_oracle / mythos_swarm were listed, so
// on a cold boot — before loadFlagDescriptors() resolves the
// /api/experimental-flags fetch — the renderer opening an experimental
// page for any of the other 6 flags hit "no handler registered". A
// flag with runtime_kind "none" (chat_pin_ui) has no manager, but it
// still needs a descriptor so the IPC dispatcher can answer
// get-status with a stable "ready" surface instead of throwing.
// loadFlagDescriptors() still merges any server-only additions on top.
const staticFlagDescriptors: ReadonlyArray<ManagerFactoryDescriptor> = [
  { flagKey: "chat_pin_ui", kind: "none", label: "experimental_chat_pin_ui" },
  { flagKey: "claude_science_lab", kind: "inline", label: "experimental_claude_science_lab" },
  { flagKey: "pythia_oracle", kind: "subprocess", label: "experimental_pythia" },
  { flagKey: "mythos_swarm", kind: "headless", label: "experimental_mythos" },
  // 0.5.105 (audit H3): the swarm_topology descriptor was removed with
  // the runtime retirement.
  { flagKey: "llm_wiki_bridge", kind: "subprocess", label: "experimental_llm_wiki_bridge" },
  { flagKey: "code_canvas", kind: "subprocess", label: "experimental_code_canvas" },
  // 0.5.22 Phase 2: semantica is a catalog-only subprocess flag driven by
  // the generic manifest path (resolveGenericSubprocessManager). The static
  // descriptor is the cold-boot safety net — without it, on a boot where
  // loadFlagDescriptors() has not resolved the server catalog yet, the IPC
  // dispatcher cannot answer experimental:semantica:get-status.
  { flagKey: "semantica", kind: "subprocess", label: "experimental_semantica" },
  // 0.5.82 WL2: timesfm mirrors semantica — generic subprocess manager
  // spawning resources/timesfm/run.sh (vendored TimesFM 2.5 torch stack).
  // Static cold-boot safety net for experimental:timesfm:get-status.
  // Flag-key literal "timesfm" is a VERBATIM copy per the duplication law
  // (server catalog + install_timesfm.go + migration 275 CHECK pin it).
  { flagKey: "timesfm", kind: "subprocess", label: "experimental_timesfm" },
  // 0.5.83 WL3: causal_graph is server-NATIVE — the graph tables and
  // gated REST surface live in the Go server, so the desktop owns no
  // subprocess for it (kind "none" = get-status answers "not running"
  // with the honest message instead of an unknown-flag error). The
  // sidebar entry comes from the manifest's entry_points.sidebar.
  // Flag-key literal "causal_graph" is a VERBATIM copy per the
  // duplication law (server catalog + router gate + lock.go +
  // migration 279 CHECK pin it).
  { flagKey: "causal_graph", kind: "none", label: "experimental_causal_graph" },
  // 0.3.57: constitution_agent retired alongside migration 165.
];

let flagDescriptors: ReadonlyArray<ManagerFactoryDescriptor> = staticFlagDescriptors;
let descriptorsLoaded = false;

export interface CatalogFlagDescriptor {
  key: string;
  runtime_kind?: string;
  label?: string;
  proxy_prefix?: string;
  loopback_service?: string;
}

// loadFlagDescriptors is called once during boot. It fetches the
// server catalog and merges any new subprocess / inline / headless
// entries onto the static list. Idempotent: re-running replaces the
// snapshot with the latest server reply.
//
// The fetch is best-effort: a network error leaves the static list
// untouched and the boot continues. The renderer can still call
// `experimental:<flagKey>:get-status` against any static flag.
export async function loadFlagDescriptors(
  fetcher: () => Promise<CatalogFlagDescriptor[]>,
): Promise<void> {
  try {
    const remote = await fetcher();
    const merged: ManagerFactoryDescriptor[] = [
      ...staticFlagDescriptors.map((d) => ({ ...d })),
    ];
    const seen = new Set(merged.map((d) => d.flagKey));
    for (const f of remote) {
      const kind = f.runtime_kind ?? "none";
      if (kind !== "subprocess" && kind !== "inline" && kind !== "headless") continue;
      if (seen.has(f.key)) continue;
      seen.add(f.key);
      merged.push({
        flagKey: f.key,
        kind,
        label: f.label ?? `experimental_${f.key}`,
      });
    }
    flagDescriptors = merged;
    descriptorsLoaded = true;
  } catch (err) {
    // Keep the static list. The IPC layer still works for any of
    // the 3 known flags; remote-only flags won't get handlers.
    console.warn("[labs] loadFlagDescriptors failed; using static list:", err);
  }
}

export function isDescriptorsLoaded(): boolean {
  return descriptorsLoaded;
}

export function descriptorsForKind(kind: ManagerFactoryKind): ManagerFactoryDescriptor[] {
  return flagDescriptors.filter((d) => d.kind === kind);
}

export function descriptorFor(flagKey: string): ManagerFactoryDescriptor | undefined {
  return flagDescriptors.find((d) => d.flagKey === flagKey);
}

export interface ResolvedManager {
  flagKey: string;
  getStatus: () => string;
  getUrl: () => string | null;
  ensureUp: () => Promise<string>;
  stop: () => Promise<void>;
}

// resolveManager returns the live manager surface for flagKey. The
// kind drives the dispatch:
//
//   - subprocess → call setupPythiaManager() (idempotent) and wrap
//     the singleton's API in the ResolvedManager shape. Subprocess
//     flags other than pythia_oracle would route through their own
//     setupXxxManager() helper; P4 keeps the 0.3.18 wire so existing
//     behaviour is preserved.
//   - inline / headless → return the no-op "ready" surface. The
//     flag is opt-in (Skills, registry install/rollback, etc.) but
//     the desktop does not spawn anything for it.
//   - none / unknown → return null so the dispatcher can throw a
//     clean error rather than a partial handler.
//
// 0.5.29, P0-2 — synthesizer Round 7: wsId is mandatory for the generic
// subprocess path (semantica / code_canvas / llm_wiki_bridge). The IPC
// dispatcher passes the current workspace UUID from the renderer; pythia
// keeps its global singleton (workspace-agnostic). Passing null returns
// null so the caller's wsId plumbing is observable in tests.
export function resolveManager(flagKey: string, wsId: string | null = null): ResolvedManager | null {
  const desc = descriptorFor(flagKey);
  if (!desc || desc.kind === "none") return null;
  if (desc.kind === "subprocess") {
    if (flagKey === "pythia_oracle") {
      setupPythiaManager();
      const m = getPythiaManager();
      if (!m) return null;
      return {
        flagKey,
        getStatus: () => m.status(),
        getUrl: () => m.url(),
        ensureUp: async () => {
          // Idempotent. sharedManager outlives the IPC call (Electron
          // session-wide), so a prior start() (proxy warm-up, another
          // tab visit, a concurrent ensure-up) leaves status in
          // "ready" — blind `await m.start()` then throws
          // "already started" at manager-template.ts:134 and the
          // renderer surfaces a bogus pythia_unavailable error.
          const cur = m.status();
          if (cur === "ready") return cur;
          if (cur === "starting") {
            // Poll briefly until status leaves starting. Generous cap
            // (10s) because pythia-manager cold boot pulls a venv +
            // uvicorn import. Exceeding it falls through to start().
            const deadline = Date.now() + 10_000;
            while (m.status() === "starting" && Date.now() < deadline) {
              await new Promise((r) => setTimeout(r, 100));
            }
            return m.status();
          }
          await m.start();
          return m.status();
        },
        stop: async () => {
          await m.stop();
        },
      };
    }
    // llm_wiki_bridge speaks stdio JSON-RPC rather than HTTP, so the
    // generic manifest-driven HTTP probe (resolveGenericSubprocessManager)
    // would time out on the very first health check. 0.3.27 B8 shipped
    // the stub; PR-5 / C2-mini wires it through its own bespoke manager.
    if (flagKey === "llm_wiki_bridge") {
      setupLLMWikiBridgeManager();
      const m = getLLMWikiBridgeManager();
      if (!m) return null;
      return {
        flagKey,
        getStatus: () => m.status(),
        getUrl: () => m.url(),
        ensureUp: async () => {
          // Idempotent — same pattern as pythia above. Without this
          // guard, the same "already started" throw fires on the
          // second llm-wiki-bridge tab visit within a session.
          const cur = m.status();
          if (cur === "ready") return cur;
          if (cur === "starting") {
            const deadline = Date.now() + 10_000;
            while (m.status() === "starting" && Date.now() < deadline) {
              await new Promise((r) => setTimeout(r, 100));
            }
            return m.status();
          }
          await m.start();
          return m.status();
        },
        stop: async () => {
          await m.stop();
        },
      };
    }
    // Non-pythia subprocess flags (e.g. code_canvas) are driven
    // entirely by their manifest.runtime block via the generic
    // manifest-driven manager. This spawns the bundled binary,
    // health-checks it, and registers the loopback URL so the reverse
    // proxy serves it — no per-flag TypeScript needed.
    //
    // 0.5.29 P0-2: wsId is mandatory; resolveGenericSubprocessManager
    // returns null when the IPC dispatcher forgot to validate.
    const generic = resolveGenericSubprocessManager(flagKey, wsId);
    if (generic !== null) {
      return {
        flagKey,
        getStatus: () => generic.status(),
        getUrl: () => generic.url(),
        ensureUp: async () => {
          if (generic.status() === "idle" || generic.status() === "stopped") {
            await generic.start();
          }
          return generic.status();
        },
        stop: async () => {
          await generic.stop();
        },
      };
    }
    // Manifest missing or malformed: fall back to a stable "idle"
    // surface. The renderer can poll get-status / get-url; ensureUp
    // returns a clear error instead of a thrown "no handler" IPC.
    return {
      flagKey,
      getStatus: () => "idle",
      getUrl: () => null,
      ensureUp: async () => {
        throw new Error(
          `experimental manager "${flagKey}" has no usable subprocess manifest (missing binary or manifest.json)`,
        );
      },
      stop: async () => undefined,
    };
  }
  // inline / headless: stable "ready" surface.
  return {
    flagKey,
    getStatus: () => "ready",
    getUrl: () => null,
    ensureUp: async () => "ready",
    stop: async () => undefined,
  };
}
