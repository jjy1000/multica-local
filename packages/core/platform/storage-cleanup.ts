import type { StorageAdapter } from "../types/storage";

/**
 * Keys that are namespaced per workspace (stored as `${key}:${slug}`).
 *
 * IMPORTANT: When adding a new workspace-scoped persist store or storage key,
 * add its key here so that workspace deletion and logout properly clean it up.
 * Also ensure the store uses `createWorkspaceAwareStorage` for its persist config.
 */
const WORKSPACE_SCOPED_KEYS = [
  "multica_issue_draft",
  "multica_issues_view",
  "multica_issues_scope",
  "multica_my_issues_view",
  "multica:chat:selectedAgentId",
  "multica:chat:activeSessionId",
  "multica:chat:drafts",
  "multica:chat:expanded",
  "multica_navigation",
];

/** Remove all workspace-scoped storage entries for the given workspace slug. */
export function clearWorkspaceStorage(
  adapter: StorageAdapter,
  slug: string,
) {
  for (const key of WORKSPACE_SCOPED_KEYS) {
    adapter.removeItem(`${key}:${slug}`);
  }
}

/**
 * Remove workspace-scoped storage for EVERY workspace on this device, without
 * being told which ones exist.
 *
 * `clearWorkspaceStorage` needs a slug, and the slugs come from the workspace
 * list — which a session rejected at the identity probe never loaded. A cold
 * start with a stale token therefore had no way to clean up after the previous
 * session, leaving `multica_issue_draft:<slug>` and friends for whoever
 * signed in next. Enumerating the keys removes that dependency entirely, and
 * also catches workspaces the last session had left before it ended.
 *
 * Returns false when the adapter cannot list its keys, so the caller can say
 * what it is falling back to rather than silently doing less.
 */
export function clearAllWorkspaceStorage(adapter: StorageAdapter): boolean {
  const storedKeys = adapter.keys?.();
  if (!storedKeys) return false;

  const prefixes = WORKSPACE_SCOPED_KEYS.map((base) => `${base}:`);
  for (const key of storedKeys) {
    if (prefixes.some((prefix) => key.startsWith(prefix))) {
      adapter.removeItem(key);
    }
  }
  return true;
}
