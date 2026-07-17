# 0.3.23 — PR-A (ChatWindow wsId 改造)

**Ship date**: 2026-07-15 (autonomous activation #1)
**Predecessor**: 0.3.22
**Scope**: PR-A only (PR-B / PR-C deferred to 0.3.24)
**Status**: 0.3.23 NOT shipped to .app; PR-A code merged into source.

## Highlights

### ChatWindow `wsId` prop (forward-compatible)
- `packages/views/chat/components/chat-window.tsx` now accepts an
  optional `wsId` prop. When provided, the component bypasses
  `useWorkspaceId()` and uses the supplied id directly. When omitted,
  behaviour is unchanged (reads from context as before).
- Empty / null wsId renders a single empty-state hint instead of
  throwing, matching the "no workspace selected" contract that
  pre-workspace surfaces need.
- No call site updated; existing 5 chat call sites (main app,
  workspace shell, etc.) continue to use the hook path.

### ExperimentalChatPane (new)
- `packages/views/experimental/components/experimental-chat-pane.tsx`
  is a thin wrapper around `<ChatWindow wsId={...} />` for
  pre-workspace Labs surfaces. Empty-state hint asks the user to
  pick or create a workspace.
- New package exports: `@multica/views/experimental` and
  `@multica/views/experimental/components`.

### ClaudeLabView Chat tab live
- `apps/desktop/src/renderer/src/pages/claude-lab-view.tsx` now
  binds the `Chat` tab to the user's currently active workspace
  via `getCurrentWsId()` polling (500 ms tick). Switching the
  active workspace while the lab is open re-binds the chat
  without unmounting.

## Verification

- `pnpm typecheck` (desktop): 0 errors
- `pnpm --filter @multica/desktop test`: 38 test files / 312 tests
  passed (no regressions)
- module resolution fix: pnpm symlink cache needed explicit
  reinstall after adding a new subpath to
  `packages/views/package.json` exports.

## Deferred to 0.3.24

- **PR-B — install handler consolidation**: drop
  `upsertClaudeScienceWorkspace`, write `labId` to a new
  `experimental_pref.value` column (needs migration 155 to widen
  the existing `experimental_pref` table), update
  `Registry.InstallHandler` signature to accept `wsId`. Blocked
  on migration design (the existing `experimental_pref` schema
  only stores `enabled`, no key/value column).
- **PR-C — vendor cleanup**: drop
  `apps/desktop/resources/claude-science/` (data dir) and
  `apps/desktop/vendor/claude-science-manifest/`. Saves ~150 MB
  from the bundled DMG. Currently blocked on PR-B because the
  install handler still reads the vendored manifest.

## Files changed (5)

- `packages/views/chat/components/chat-window.tsx`
- `packages/views/experimental/components/experimental-chat-pane.tsx` (new)
- `packages/views/experimental/components/index.ts` (new)
- `packages/views/experimental/index.ts` (new)
- `packages/views/package.json` (added 2 export entries)
- `apps/desktop/src/renderer/src/pages/claude-lab-view.tsx`
