// Generic Labs IPC dispatcher (0.3.19 P2).
//
// Background: the 0.3.18 desktop had per-manager `setupXxxIPC` calls
// (setupPythiaIPC, setupClaudeScienceIPC) registered at app boot. Each
// call hard-coded its own ipcMain.handle channel name. Adding a new
// subprocess lab required editing apps/desktop/src/main/index.ts to
// import the new manager and call its setupXxxIPC, which silently
// dropped a renderer-call site when the import was missed.
//
// This file introduces a generic channel namespace
// `experimental:<flagKey>:<verb>` that the renderer can call without
// knowing which manager backs each flag. The catalog entry's
// RuntimeKind decides what the manager does:
//
//   - "subprocess" → spawn the bin from manifest, register loopback
//   - "inline"     → return "ready" (no-op manager, e.g. claude_science)
//   - "headless"   → return "ready" (the agent runtime owns it; no IPC)
//   - "none"       → not registered at all (plain toggle flags)
//
// Out of scope (deferred to PR 4):
//
//   - Dynamic spawning from the manifest's spec.runtime block. P2 only
//     adds the IPC scaffolding; the actual spawn logic still lives in
//     the per-manager files. PR 4 (P2.4) routes through this generic
//     surface by reading manifest.runtime.{binary,args,health_path}.
//   - Sidebar / LabsTab. P3.

import { ipcMain, type BrowserWindow } from "electron";
import { resolveManager, descriptorsForKind, loadFlagDescriptors } from "./manager-factory";
import { loadBrokenFlagKeys } from "../experimental-safety";
import { isValidWorkspaceId } from "./subprocess-manager";

// Re-export so index.ts can call the boot-time catalog loader
// without importing manager-factory directly.
export { loadFlagDescriptors };

// LabsChannelPrefix is the IPC channel namespace every Labs flag uses.
// The renderer calls `experimental:<flagKey>:get-status` etc. The
// concrete handler is selected by setupExperimentalIPC based on the
// flag's RuntimeKind.
const LabsChannelPrefix = "experimental:";

type RuntimeKind = "subprocess" | "inline" | "headless" | "none";

// channelFor builds the IPC channel name for a (flag, verb) pair.
// Renderer-side callers use the same helper via the `experimentalAPI`
// preload bridge (see apps/desktop/src/preload/* in a follow-up).
function channelFor(flagKey: string, verb: string): string {
  return `${LabsChannelPrefix}${flagKey}:${verb}`;
}

// ExperimentalInvokePayload is the optional second-arg shape the
// renderer passes alongside verb. Today only workspaceId is
// consumed (semantica / code_canvas / llm_wiki_bridge per-workspace
// subprocess keys).
type ExperimentalInvokePayload = {
  workspaceId?: string | null;
};

// extractWorkspaceId normalizes the payload's workspaceId to either
// a validated UUID string or null. Returns null when the field is
// absent (pre-workspace login screen, legacy renderer call site)
// or invalid (renderer bug). Per P0-2 we re-validate the UUID
// regex at the IPC boundary even though the renderer side is
// expected to pass a clean UUID — defense-in-depth against a
// renderer-side bug or future XSS / extension injection.
function extractWorkspaceId(payload: unknown): string | null {
  if (!payload || typeof payload !== "object") return null;
  const v = (payload as ExperimentalInvokePayload).workspaceId;
  if (typeof v !== "string") return null;
  return isValidWorkspaceId(v) ? v : null;
}

// setupExperimentalIPC registers the four generic IPC channels for
// every catalog entry with a non-"none" RuntimeKind. The renderer's
// preload bridge surfaces the same set, so the renderer code does
// not depend on which manager class backs a flag.
//
// Idempotent: calling setupExperimentalIPC twice (e.g. on app
// re-init) re-registers the same handlers. We use ipcMain.handle
// which replaces a previous registration with the same name, so
// the latest call wins.
//
// Channels registered (per non-none flag):
//   experimental:<flagKey>:get-status   → "idle" | "starting" | ...
//   experimental:<flagKey>:get-url      → loopback URL or null
//   experimental:<flagKey>:ensure-up    → bring the manager up
//   experimental:<flagKey>:stop         → graceful stop
export async function setupExperimentalIPC(_windowGetter: () => BrowserWindow | null): Promise<void> {
  const broken = await loadBrokenFlagKeys();
  const allFlags: RuntimeKind[] = ["subprocess", "inline", "headless"];
  const flagKeys = allFlags.flatMap((k) => descriptorsForKind(k).map((d) => d.flagKey));
  for (const key of flagKeys) {
    const m = resolveManager(key);
    if (m === null) {
      // Subprocess manager not yet registered (e.g. cold-boot race).
      // We still register the IPC handler so a renderer call returns
      // a clean error rather than throwing "no handler registered".
      ipcMain.handle(channelFor(key, "get-status"), () => "idle");
      ipcMain.handle(channelFor(key, "get-url"), () => null);
      ipcMain.handle(channelFor(key, "ensure-up"), () => {
        throw new Error(`experimental manager "${key}" is not available`);
      });
      ipcMain.handle(channelFor(key, "stop"), () => undefined);
      continue;
    }
    ipcMain.handle(channelFor(key, "get-status"), () => m.getStatus());
    ipcMain.handle(channelFor(key, "get-url"), () => m.getUrl());
    if (broken.has(key)) {
      // 0.5.18 DT-P1-1: a flag blacklisted by the safety net (panic /
      // 5xx burst / init timeout) must not be brought back up through
      // IPC — the server refuses to serve it and re-tripping the
      // watchdog would only re-blacklist it. get-status / get-url /
      // stop stay wired so the Labs UI can still render the badge.
      ipcMain.handle(channelFor(key, "ensure-up"), async () => {
        throw new Error(
          `experimental flag "${key}" is blacklisted by the safety net; restart Multica after clearing it to retry`,
        );
      });
    } else {
      // 0.5.29 P0-2: per-workspace subprocess keys. The renderer
      // passes the active wsId in payload.workspaceId; the
      // dispatcher forwards to resolveManager() so
      // subprocess-manager caches by (flagKey, wsId). pythia (its
      // own dedicated manager) ignores the second arg.
      ipcMain.handle(channelFor(key, "ensure-up"), async (_event, payload) => {
        const wsId = extractWorkspaceId(payload);
        const fresh = resolveManager(key, wsId) ?? m;
        if (fresh.getStatus() === "idle" || fresh.getStatus() === "stopped") {
          await fresh.ensureUp();
        }
        return fresh.ensureUp();
      });
    }
    // stop() also re-resolves so a stale wsId never tears down
    // the current workspace's manager. Pre-0.5.29 the singleton
    // was global and one stop() killed every active workspace.
    ipcMain.handle(channelFor(key, "stop"), async (_event, payload) => {
      const wsId = extractWorkspaceId(payload);
      const fresh = resolveManager(key, wsId) ?? m;
      await fresh.stop();
    });
  }
}

// stopAllExperimentalManagers walks every registered subprocess flag
// and asks its manager to stop. Called from the before-quit chain in
// index.ts. Idempotent: managers that have not been started treat
// stop() as a no-op.
export async function stopAllExperimentalManagers(): Promise<void> {
  for (const d of descriptorsForKind("subprocess")) {
    const m = resolveManager(d.flagKey);
    if (m === null) continue;
    try {
      await m.stop();
    } catch {
      // best-effort; before-quit tolerates manager errors so the
      // bundled Go server can still get its SIGTERM.
    }
  }
}
