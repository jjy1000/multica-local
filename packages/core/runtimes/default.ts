import type { Workspace } from "../types/workspace";

/**
 * workspace.settings key holding the workspace's default agent runtime
 * (the CLI an agent binds at creation unless the user picks another).
 * Append-only: readers treat anything but a non-empty string as "unset",
 * so older clients that never write the key and unknown future shapes
 * degrade to the pre-0.5.127 behavior (first usable runtime in the list).
 */
export const DEFAULT_RUNTIME_SETTINGS_KEY = "default_runtime_id";

/**
 * Reads the default runtime id out of a workspace's free-form settings
 * blob. Returns null when unset, empty, or not a string — never throws,
 * because settings is `Record<string, unknown>` end-to-end (server stores
 * it as JSONB) and drift must not break the agent form or the Runtimes
 * page.
 */
export function defaultRuntimeIdFromSettings(
  settings: Record<string, unknown> | null | undefined,
): string | null {
  const value = settings?.[DEFAULT_RUNTIME_SETTINGS_KEY];
  return typeof value === "string" && value !== "" ? value : null;
}

/**
 * Returns the next settings object with the default runtime set (id) or
 * cleared (null removes the key rather than storing an empty string, so
 * the blob keeps carrying only meaningful keys). The input is never
 * mutated; unknown keys are preserved so concurrent settings writers
 * don't clobber each other's fields.
 */
export function withDefaultRuntimeId(
  settings: Record<string, unknown> | null | undefined,
  runtimeId: string | null,
): Record<string, unknown> {
  const next: Record<string, unknown> = { ...(settings ?? {}) };
  if (runtimeId === null) {
    delete next[DEFAULT_RUNTIME_SETTINGS_KEY];
  } else {
    next[DEFAULT_RUNTIME_SETTINGS_KEY] = runtimeId;
  }
  return next;
}

/**
 * Convenience for call sites that hold the Workspace object.
 */
export function workspaceDefaultRuntimeId(
  workspace: Pick<Workspace, "settings"> | null | undefined,
): string | null {
  return defaultRuntimeIdFromSettings(workspace?.settings);
}
