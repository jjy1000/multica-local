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
export function setupExperimentalIPC(_windowGetter: () => BrowserWindow | null): void {
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
    ipcMain.handle(channelFor(key, "ensure-up"), async () => m.ensureUp());
    ipcMain.handle(channelFor(key, "stop"), async () => {
      await m.stop();
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
