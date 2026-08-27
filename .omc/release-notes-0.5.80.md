---
name: release-notes-0.5.80
created: 2026-08-27T15:05:00+08:00
updated: 2026-08-27T15:05:00+08:00
---

# 0.5.80 — Labs keep the shell chrome (fullscreen takeover fix)

**Branch:** `epic/0.5.72-followups`
**Trigger:** user report — "点击实验室功能插件会变为全屏，隐藏了功能栏，非常不方便"

## TL;DR

Clicking any Labs surface (`/experimental/*`, including user-plugin shells at `/experimental/plugin/:slug`) navigated within the active tab and tore down `WorkspaceRouteLayout` WITHOUT mounting a successor — its unmount cleanup released the platform workspace singleton (`setCurrentWorkspace(null, null)`), and `DesktopShell`'s `{slug && <AppSidebar />}` / `{slug && <WindowToolbar />}` gates dropped the entire left rail + top toolbar. The lab read as a fullscreen takeover. **This is the residual half of the 0.5.73 fix**: 0.5.73's reserved slugs (`experimental`, `plugin`) stopped `switchWorkspace("experimental")` from creating a rival tab group, but did nothing about the in-tab teardown path.

## Root cause chain

1. Sidebar labs entry → `AppLink → push("/experimental/...")` on the ACTIVE tab's router.
2. Route swap: workspace-scoped route unmounts, pre-workspace `/experimental/*` mounts.
3. `WorkspaceRouteLayout` unmount cleanup fires with NO successor layout to re-claim the singleton → clears it.
4. `DesktopShell` reads the singleton via `useSyncExternalStore(getCurrentSlug)` → slug flips to null → `{slug && ...}` gates unmount AppSidebar, WindowToolbar, ModalRegistry, SearchCommand.
5. Lab view renders window-wide; only egress was the fixed "Back" pill that `ExperimentalViewShell` had bolted on for exactly this bug (audit: "lab plugin panel covers the left task panel — no way back").

## Fix: one-shot release-suppression handshake

New `apps/desktop/src/renderer/src/platform/workspace-singleton-release-guard.ts` — a module-scope counter (`suppressNextWorkspaceRelease()` / `consumeWorkspaceReleaseSuppression()` / test-only `resetWorkspaceReleaseSuppression()`). Counter rather than boolean so a push+replace double dispatch can't strand half a handshake.

- **Arm sites** (all navigation paths into `/experimental/*`):
  - Root adapter `DesktopNavigationProvider`: `push`, `replace`, `openInNewTab`.
  - Per-tab adapter `TabNavigationProvider`: same three.
  - Shared `tryRouteToPinnedNewTab` helper (fires BEFORE the arm points in push/openInNewTab).
  - `desktop-layout.tsx` `multica:navigate` internal-link handler (`openTab` semantics).
  - Guard predicate `isLabsRoute(path)`: exactly `/experimental` or `/experimental/*`. Deliberately narrow — other reserved prefixes are overlay-handled earlier or must keep releasing normally.
- **Consume site**: `workspace-route-layout.tsx` unmount cleanup — after the ownership + slug-equality checks pass, consume first; token hit → skip `setCurrentWorkspace(null, null)` (still releases `singletonOwner` so a successor claims normally). All other teardowns (tab closed, logout, workspace eviction, re-run for a new slug) never see a token and release exactly as before.
- **Correctness note**: labs intentionally read the ACTIVE workspace implicitly while mounted on a pre-workspace URL (X-Workspace-Slug header via singleton, `getCurrentWsId` polling), so keeping the singleton across this transition is right even beyond the chrome fix.
- **`routes.tsx`**: deleted the fixed top-left "Back" affordance from `ExperimentalViewShell` — it was the workaround for THIS bug; with sidebar+WindowToolbar permanently visible it only collided with lab content (DragStrip space). Also removed now-unused imports (`useNavigate`, `CSSProperties`, `ArrowLeft`, `Button`, `useT`).
- Labs shell keeps `<Outlet />` grouping so future per-lab shell chrome has a mount point.

## Known deliberate gaps (recorded)

- History POP through `use-tab-history.goBack/goForward` and adapters' `back()` does not arm the token. Landing back on a `/experimental/*` entry from a workspace page means the singleton releases once more — WindowToolbar Back stays disabled-looking but Sidebar's labs entries still work (push arms again). Not exercised by the reported repro; revisit if history-crossing labs navigation ever reports the takeover again.
- Deliberate non-goal unchanged from 0.5.72 design notes: labs are full-canvas views INSIDE the tab content pane; sidebar remains the section switcher. This fix restores that contract rather than shrinking labs to a sub-pane.

## Tests

- New `platform/workspace-singleton-release-guard.test.ts` (4 cases): unconsumed baseline, one-shot consumption, stacked armings, reset clearing.
- `workspace-route-layout.test.tsx`: new regression case — armed suppression survives an unmount WITHOUT successor (`currentSlug` stays "acme"); following unrelated teardown releases normally (token proven one-shot). Existing MUL-6303 tab-swap trio untouched and green.
- Verification: `pnpm typecheck` 6/6 green; desktop vitest **45 files / 362 tests all passed** (includes prior 357 + new suite growth).
