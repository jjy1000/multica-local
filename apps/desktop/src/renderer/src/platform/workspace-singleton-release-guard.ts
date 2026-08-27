/**
 * One-shot handshake between the navigation adapters (navigation.tsx,
 * desktop-layout.tsx's internal-link handler) and WorkspaceRouteLayout's
 * unmount cleanup (workspace-route-layout.tsx).
 *
 * Why this exists — clicking any Labs surface (/experimental/*) navigates
 * WITHIN the active tab's router. That unmounts WorkspaceRouteLayout, whose
 * cleanup releases the platform workspace singleton
 * (setCurrentWorkspace(null, null)). With the singleton empty, DesktopShell's
 * `{slug && <AppSidebar />}` gate drops the entire left rail and the lab view
 * reads as a fullscreen takeover. The labs intentionally read the ACTIVE
 * workspace implicitly while mounted on a pre-workspace URL, so the singleton
 * must survive that transition — the 0.5.73 reserved-slug fix stopped the
 * *cross-tab-group* damage (switchWorkspace("experimental")) but not this
 * in-tab teardown.
 *
 * Flow: an adapter that is about to router.navigate() to /experimental/*
 * calls suppressNextWorkspaceRelease(); the subsequent WorkspaceRouteLayout
 * cleanup consumes the token and skips the clear (still releasing ownership
 * so a successor can claim normally). Every other teardown path — tab closed,
 * app quit, workspace evicted, another tab claiming a different workspace —
 * never sees a token and releases exactly as before.
 *
 * A counter rather than a boolean so a push immediately followed by a
 * replace (double dispatch) cannot strand half the handshake.
 */

let pendingSuppressions = 0;

/** Arm one release-suppression. Call immediately before navigating to /experimental/*. */
export function suppressNextWorkspaceRelease(): void {
  pendingSuppressions += 1;
}

/**
 * Consume one armed suppression. Returns true when the caller should SKIP
 * clearing the workspace singleton.
 */
export function consumeWorkspaceReleaseSuppression(): boolean {
  if (pendingSuppressions <= 0) return false;
  pendingSuppressions -= 1;
  return true;
}

/** Test-only escape hatch so suites cannot leak tokens across cases. */
export function resetWorkspaceReleaseSuppression(): void {
  pendingSuppressions = 0;
}
