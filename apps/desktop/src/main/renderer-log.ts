import { appendFile } from "node:fs/promises";

/**
 * Production renderer-console capture. Dev already routes console.* through
 * stderr (see the in-file listener in index.ts); production has no terminal,
 * so renderer-side errors used to disappear silently when the window crashed.
 *
 * Path is set by setupDaemonManager's first `ensureActiveProfile()` call,
 * once we know which profile dir is active. Messages arriving before that
 * resolution (typically none — the BrowserWindow loads after the profile
 * resolves in practice) are dropped.
 *
 * Best-effort writes: a renderer-log write failure must never propagate to
 * the main process or block the renderer's next paint.
 */
let path: string | null = null;

export function setRendererLogPath(p: string): void {
  path = p;
}

export function writeRendererConsoleLine(line: string): void {
  if (!path) return;
  void appendFile(path, line).catch(() => {});
}