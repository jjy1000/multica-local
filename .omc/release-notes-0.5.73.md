# Release Notes 0.5.73 — 2026-08-26

Hotfix for a UX regression introduced by 0.5.72's experimental-route
architecture. 1 surgical fix, no schema changes, no API changes.

## The bug

User feedback: clicking an experimental plugin in the Labs sidebar
(e.g. "Claude Research Lab") defaults to a full-window view that
**hides the function bar** (AppSidebar + WindowToolbar). The lab should
keep the sidebar mounted so the user can navigate back / switch to
another section without first hitting the experimental shell's "Back"
button.

## Root cause

`extractWorkspaceSlug("/experimental/claude-lab")` returned
`"experimental"` (the first path segment), which was **not** in the
reserved-slug list. The desktop navigation adapter
(`tryRouteToOtherWorkspace`) treated this as a cross-workspace push →
`switchWorkspace("experimental", path)` → created a brand-new tab group
keyed by `"experimental"`. That wiped the active workspace singleton,
and `DesktopShell`'s `{slug && <AppSidebar />}` gate then hid the
sidebar — the lab view rendered full-window and collapsed the function
bar.

## The fix

Add `"experimental"` + `"plugin"` (the pre-workspace plugin-shell
namespace, `/experimental/plugin/:slug`) to the reserved slugs list.
`extractWorkspaceSlug()` now returns null for both → the navigation
adapter falls through to in-tab `router.navigate()` → `WorkspaceRouteLayout`
does NOT unmount → `setCurrentWorkspace` stays at the active workspace's
slug → `AppSidebar` keeps rendering with its workspace context.

The change is **purely additive** to a data table:

| File | Change |
|---|---|
| `server/internal/handler/reserved_slugs.json` | new "Pre-workspace lab routes (0.5.72+)" group with `experimental` + `plugin` entries — Go source of truth |
| `packages/core/paths/reserved-slugs.ts` | regenerated via `pnpm generate:reserved-slugs` (CI enforces JSON ↔ TS parity) |
| `packages/core/paths/reserved-slugs.test.ts` | regression pin `reserves the pre-workspace lab route namespaces` so a future removal fails the test |
| `apps/desktop/src/renderer/src/components/desktop-layout.tsx` | comment block on the slug singleton explaining the contract — no code change beyond the comment |
| `apps/desktop/package.json` | version bump 0.5.72 → 0.5.73 |

## Verification

- `pnpm typecheck`: 6/6 packages green
- `go test -count=1 ./internal/... ./pkg/agent/...`: all green
- `pnpm --filter @multica/desktop test`: 357 passed
- `pnpm --filter @multica/core test`: 798 tests, 3 pre-existing failures
  (`auth/store.test.ts` + `api/schemas.test.ts`) unrelated to this
  change — verified via `git stash` baseline run
- Reserved-slug regression pin: PASSES
- Cold-start three-check: PASS (4 s, server PID 39003)

## Numbers

- **Files touched**: 5 (1 JSON + 1 generated TS + 1 test + 1 comment-only
  + 1 version bump)
- **LOC delta**: +41 / -1
- **Migrations**: zero
- **Schema/API changes**: zero — this is a UX fix in the renderer-only
  navigation adapter

## Why a hotfix instead of rolling into 0.5.74

The bug breaks every Labs-tab click on desktop. It's a one-line
addition to a JSON config file plus a generated TS file plus a test
pin. Holding the change for the next batch would leave every desktop
user with the broken "fullscreen lab" experience in the meantime.
Same-day hotfix is the right call.