// Main-process mirror of server/internal/experimental/safety.go.
// Reads ~/.multica/experimental-blacklist.json so the desktop can:
//   1. Render a "broken by safety net" badge in the Labs UI without
//      round-tripping the server (server may be down for the very
//      reason the flag was blacklisted).
//   2. Expose a "Restore" IPC handler that clears an entry and asks
//      the user to relaunch (we do not hot-reload — the user's
//      working app should not be disrupted by a flag re-enabling
//      while the server is mid-request).
//
// The wire shape MUST match the server's Blacklist JSON exactly —
// both are the source of truth in their own process. Tests in
// safety-shape.test.ts lock the field names so a divergence in
// either direction breaks the build rather than the user.

import { promises as fs } from "node:fs";
import * as os from "node:os";
import * as path from "node:path";

export type SafetyReason = "panic" | "5xx_burst" | "init_timeout";

export interface BlacklistEntry {
  flag_key: string;
  reason: SafetyReason;
  broken_at: string; // RFC3339
  context?: string;
  stack_hint?: string;
}

export interface Blacklist {
  version: number;
  entries: BlacklistEntry[];
}

const CURRENT_VERSION = 1;

function defaultBlacklistPath(): string {
  const override = process.env.MULTICA_EXPERIMENTAL_BLACKLIST_PATH;
  if (override) return override;
  return path.join(os.homedir(), ".multica", "experimental-blacklist.json");
}

let resolvedPath = defaultBlacklistPath();

export function setBlacklistPath(p: string): void {
  resolvedPath = p;
}

export function getBlacklistPath(): string {
  return resolvedPath;
}

/**
 * Load the blacklist file from disk. A missing file is not an error —
 * returns an empty Blacklist at the current schema version. Schema
 * mismatch returns null so the caller can distinguish "no entries"
 * from "corrupted file" (the UI uses this to show a "blacklist
 * unreadable" banner instead of silently hiding broken flags).
 */
export async function loadBlacklist(): Promise<Blacklist | null> {
  let raw: string;
  try {
    raw = await fs.readFile(resolvedPath, "utf8");
  } catch (err) {
    if ((err as NodeJS.ErrnoException).code === "ENOENT") {
      return { version: CURRENT_VERSION, entries: [] };
    }
    return null;
  }
  try {
    const parsed = JSON.parse(raw) as Blacklist;
    if (parsed.version !== CURRENT_VERSION) {
      return null;
    }
    if (!Array.isArray(parsed.entries)) {
      return null;
    }
    return parsed;
  } catch {
    return null;
  }
}

/**
 * Atomic save: write to a sibling .tmp file with a UUID suffix, then
 * rename. Same contract as the Go side — crash mid-write leaves the
 * prior good file in place. Concurrent writers do not collide
 * because the UUID suffix is unique per call.
 */
export async function saveBlacklist(bl: Blacklist): Promise<void> {
  bl.version = CURRENT_VERSION;
  bl.entries.sort((a, b) => {
    if (a.flag_key !== b.flag_key) return a.flag_key < b.flag_key ? -1 : 1;
    return a.broken_at < b.broken_at ? -1 : 1;
  });
  const dir = path.dirname(resolvedPath);
  await fs.mkdir(dir, { recursive: true });
  const tmp = path.join(
    dir,
    `.experimental-blacklist.${crypto.randomUUID()}.tmp`,
  );
  await fs.writeFile(tmp, JSON.stringify(bl, null, 2), { mode: 0o600 });
  await fs.rename(tmp, resolvedPath);
}

export async function isBroken(flagKey: string): Promise<BlacklistEntry | null> {
  const bl = await loadBlacklist();
  if (!bl) return null;
  return bl.entries.find((e) => e.flag_key === flagKey) ?? null;
}

export async function clearBroken(flagKey: string): Promise<void> {
  const bl = await loadBlacklist();
  if (!bl) return;
  const next = bl.entries.filter((e) => e.flag_key !== flagKey);
  if (next.length === bl.entries.length) return;
  bl.entries = next;
  await saveBlacklist(bl);
}

/**
 * Boot-time check: which flags should be considered broken RIGHT NOW.
 * Used by the desktop to skip wiring IPC handlers / preload surfaces
 * for broken flags, mirroring the server's catalog.DefaultFor check.
 *
 * Reads the file once at startup; subsequent writes during the same
 * session do not change the result (the server still re-reads on its
 * next request, the desktop re-reads on its next launch). This is
 * intentional — the desktop UI is read-mostly.
 */
export async function loadBrokenFlagKeys(): Promise<Set<string>> {
  const bl = await loadBlacklist();
  if (!bl) return new Set();
  return new Set(bl.entries.map((e) => e.flag_key));
}

/**
 * Convenience for the IPC handler: return the full entries (not just
 * the key set) so the renderer can render "broken by 5xx_burst at
 * /api/..." without a second round-trip. The wire shape MUST match
 * server/internal/experimental/safety.go::BlacklistEntry exactly.
 */
export async function loadBrokenFlagEntries(): Promise<BlacklistEntry[]> {
  const bl = await loadBlacklist();
  if (!bl) return [];
  return bl.entries;
}
