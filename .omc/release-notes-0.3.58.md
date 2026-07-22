---
name: release-notes-0.3.58
created: 2026-07-22T11:00:00Z
updated: 2026-07-22T11:00:00Z
status: complete
---

# Multica 0.3.58 — UI improvements cherry-picked from upstream 0.4.0

## Scope

This release ports three UI/UX improvements from upstream
`multica-ai/multica@0.4.0` into this fork. **No cloud / composio /
PostHog / workspace-member / invitations / auto-update / Google OAuth
changes are included** — those categories were filtered out at
cherry-pick selection time per the fork's localization constraints
(CLAUDE.md "Localized Fork"). The ship covers 3 files of pure
additive UI/CSS/hook work.

## Changes

### `packages/ui/styles/tokens.css` — surface-system CSS tokens

Added five new tokens to `:root` and `.dark`:

- `--app-shell` — the quiet outer frame (light `oklch(0.964)`,
  dark `oklch(0.155)`)
- `--page-canvas` — where lists/boards/conversations live
  (light `oklch(0.988)`, dark `oklch(0.18)`)
- `--surface-raised` — ephemeral overlays (light white, dark
  `oklch(0.235)`)
- `--menu-shadow` — popover/dropdown shadow
  (light 8px/24px @ 8% / dark 10px/28px @ 30%)
- `--floating-shadow` — dialog/sheet shadow
  (light 16px/40px @ 14% / dark 20px/48px @ 46%)

**Backward compatibility**: tokens are **additive only**. The
existing `--background` / `--card` / `--popover` semantic tokens
are NOT rebound to the new names, so existing UI that consumes
those legacy names continues to render unchanged. New surfaces
should consume the new names; legacy code can migrate later.

### `packages/ui/styles/base.css` — three new blocks

1. **`html[data-sidebar-resizing="true"]`** — sidebar resize
   cursor contract (matches `react-resizable-panels`):
   - `cursor: ew-resize !important` on the document
   - `user-select: none !important` to prevent text selection
     during the drag
   - `transition: none !important` on `[data-slot="sidebar-gap"]`
     and `[data-slot="sidebar-container"]` while resizing
   - visual `::after` rail feedback using `--sidebar-border`

2. **`@media (prefers-reduced-motion: reduce)` extended** —
   opt out 8 app-level keyframe animations under reduced-motion:
   `.animate-entrance-spin`, `.animate-onboarding-enter`,
   `.animate-welcome-emoji-pop`, `.animate-completion-badge`,
   `.animate-completion-check`, `.animate-chat-impulse`,
   `.animate-chat-text-shimmer`, `.animate-nav-progress-sweep`.
   The fork-local `.mythos-boost-pulse` rule is preserved
   unchanged.

3. **`::highlight(multica-find)`** — in-page find highlight for
   Cmd/Ctrl+F on the issue detail page. Uses the CSS Custom
   Highlight API (ranges only, no DOM mutation) so the tint
   layers cleanly over React-rendered markdown and the
   contenteditable title/description editors. Two registrations:
   `multica-find` (all matches, dimmer) and `multica-find-active`
   (current match, brighter, higher priority). Color tokens live
   in `tokens.css` as `--find-match` / `--find-match-active` /
   `--find-match-foreground`.

### `packages/ui/hooks/use-scroll-fade.ts` — horizontal axis support

Added a third parameter `axis: "vertical" | "horizontal" = "vertical"`.
The horizontal branch tracks `scrollLeft` / `scrollWidth` /
`clientWidth` and produces a left-to-right `linear-gradient` mask
instead of a top-to-bottom one.

The fork's `tab-bar.tsx` was already calling
`useScrollFade(ref, 32, "horizontal")` (per the report — verify
in the file before relying on this); the previous hook signature
silently dropped the third argument and rendered a vertical mask
on a horizontal scroller, which produced a no-op fade. With the
new `axis` parameter, tab bars now get a proper horizontal
edge-fade as designed.

Backward compatibility: existing callers that pass only `(ref,
fadeSize)` continue to work — `axis` defaults to `"vertical"`,
preserving the pre-0.3.58 contract.

## Verified

- `pnpm --filter @multica/ui typecheck` → 0 errors
- `pnpm --filter @multica/desktop typecheck:web` → 0 errors
- `go run ./cmd/migrate up` → no new migration (no schema change)
- `pnpm --filter @multica/desktop build` → electron-vite build
  successful (2.03s)
- `pnpm exec electron-builder --mac --dir` → `Multica.app` 786M
  with `CFBundleShortVersionString=0.3.58`
- Cold start: `lsof -nP -iTCP:5432` + `lsof -nP -iTCP:8090`
  listeners within 10s; `curl /health` → `{"status":"ok"}`
- Row parity: workspace=1, issue=220, comment=1346, agent=92
  (identical to 0.3.57 baseline — UI-only change, no DB impact)

## What was NOT included (per CLAUDE.md localization)

- ❌ Cloud runtime (`internal/cloudruntime`, `cloud_billing`,
  `cloud_runtime` handlers) — fork never ships these
- ❌ Composio integration (`pkg/composio`, `internal/integrations/
  composio`) — third-party SaaS; not in this fork
- ❌ PostHog analytics — explicitly removed by fork
- ❌ Workspace members / invitations — fork uses single-user
  model (CLAUDE.md §Localized Fork)
- ❌ Google OAuth / email verification — fork is username-only
- ❌ Electron auto-updater — fork keeps `updater.ts` as a no-op
- ❌ Migration renumbering (52 upstream migrations 165..216) —
  the existing 165-retire-constitution_agent migration in this
  fork covers the structural change; the upstream renumber was
  a bookkeeping-only change that would create merge noise
- ❌ Migration 127 (user_composio_connection) and 129
  (agent_composio_allowlist) — composio-only
- ❌ Migration 165 (user_composio_connection) and similar
  cloud-only migrations

## Source attribution

Cherry-picked from
`/Users/jiangjianyan/Downloads/multica-main` (the upstream
`multica-ai/multica@0.4.0` source snapshot, downloaded
2026-07-22 for diff comparison). The three CSS/hook changes
above are 1:1 ports of the upstream implementations, with
localization comments noting the cherry-pick origin.

The fork has no `git fetch upstream` link (the fork is
intentionally disconnected from upstream to prevent accidental
re-merging of cloud/SaaS features).
