// Localized build: self-update is disabled. The setupAutoUpdater entry
// point is preserved so callers in main/index.ts compile and run, and the
// renderer-facing IPC channels are registered as no-ops that return a
// "disabled" result — the UI components can still call them and surface
// a clear message instead of an uncaught promise rejection.
//
// No `electron-updater` import, no GitHub release checks, no downloads,
// no installs, no startup or periodic timers.

import { type BrowserWindow, ipcMain } from "electron";

export type ManualUpdateCheckResult =
  | {
      ok: true;
      currentVersion: string;
      latestVersion: string;
      available: boolean;
    }
  | { ok: false; error: string };

const DISABLED_MESSAGE = "Auto-update is disabled in this build";

export function setupAutoUpdater(_getMainWindow: () => BrowserWindow | null): void {
  ipcMain.handle("updater:check", async (): Promise<ManualUpdateCheckResult> => ({
    ok: false,
    error: DISABLED_MESSAGE,
  }));
  ipcMain.handle("updater:download", () => {
    throw new Error(DISABLED_MESSAGE);
  });
  ipcMain.handle("updater:install", () => {
    throw new Error(DISABLED_MESSAGE);
  });
}
