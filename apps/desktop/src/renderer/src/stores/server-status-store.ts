import { create } from "zustand";

/**
 * PR 1 (Stage C): a small, isolated store that mirrors the server-manager
 * `ServerStatus` pushed from main via `serverAPI.onStatusChanged`.
 *
 * The renderer subscribes once on mount, the banner reads it.
 * We deliberately do NOT poll — main pushes only on actual transitions
 * (see `publishInitialServerStatus` in main/index.ts).
 *
 * PR 3 (Stage D-2): we add a `downloading` state for the first-launch
 * progress modal. The `installation` shape replaces the old install-brew
 * hint with native-only (docker is gone).
 */
export type ServerStatus =
  | { state: "stopped" }
  | { state: "starting" }
  | {
      state: "downloading";
      phase: "fetch" | "verify" | "extract" | "initdb" | "migrate-dump" | "migrate-restore" | "migrate-verify";
      percent: number;
      bytesDone?: number;
      bytesTotal?: number;
    }
  | { state: "running"; port: number; pid: number; backend?: "native" | "external" }
  | {
      state: "failed";
      error: string;
      recoverable?: boolean;
      hint?: "install-brew" | "wait-download" | "check-port" | "install-native" | "network";
      backend?: string;
    };

interface ServerStatusStore {
  status: ServerStatus | null;
  // PR 2 → PR 3: tracks whether a native PG binary is on disk.
  nativePgInstalled: boolean;
  // PR 3: whether the user has opted in to the docker → native migration.
  // Used to drive the migration dialog.
  migrationPending: boolean;
  setStatus: (status: ServerStatus | null) => void;
  setNativePgInstalled: (installed: boolean) => void;
  setMigrationPending: (pending: boolean) => void;
  dismiss: () => void;
  dismissed: boolean;
}

export const useServerStatusStore = create<ServerStatusStore>((set) => ({
  status: null,
  nativePgInstalled: false,
  migrationPending: false,
  dismissed: false,
  setStatus: (status) => set({ status }),
  setNativePgInstalled: (nativePgInstalled) => set({ nativePgInstalled }),
  setMigrationPending: (migrationPending) => set({ migrationPending }),
  dismiss: () => set({ dismissed: true }),
}));
