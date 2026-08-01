---
name: release-notes-0.5.1-fork
created: 2026-08-01
updated: 2026-08-01
status: complete
---

# 0.5.1 fork — upstream UI & animation port batch

Schema/token-first port of the upstream snapshot's (`Downloads/multica-main 2`)
UI and animation work into the localized fork. Five commits, all on
`epic/0.3.58-cherry-pick`:

| commit | content |
|---|---|
| `c065ae1` | Safe batch: WCAG muted-foreground contrast fix, `--faint-foreground` + `--find-match*` tokens (fixes the in-page find highlight that referenced undefined vars), `--chat-launcher-*` geometry, Button `brand`/`brandSubtle` variants, `font-synthesis: style` (CJK fake-bold fix) |
| `404676a` | Surface system completed + bound (`--background/--card/--popover` → page-canvas/surface/surface-raised, near-zero visual shift), 10-step type scale tokens, sidebar collapse easing `linear→ease-out` + `motion-reduce`, NumberFlow animated-number components + `@number-flow/react@^0.6.1` |
| `bb29b46` | NumberFlow wired into 6 display surfaces (inbox unread, sidebar badge, usage KPIs, runtime cost, activity runs, dashboard) |
| `1b0cdb5` | 14 component animation/polish deltas: app-shell bg, breadcrumb faint separator, semantic `<header>`, chat FAB/window surface tokens, batch-toolbar AnimatePresence slide-in, activity-indicator debounced tooltip, comment save/highlight, issues-header delayed spinner, `MainCanvas` spring, Inter italic axis, Geist Mono variable font, `ui/lib/motion` constants |
| `a803f94` | Type-scale migration: 212 files from `text-xs/sm/base/lg/xl/2xl/3xl` to semantic `text-caption/body/title*` (pixel-identical mappings; 93 residual files verified fork-only/upstream-unconverted) |

## Verification

- `pnpm typecheck` 6/6, `electron-vite build` green, docs-sync gate PASS
- Cloud/telemetry/support UI zero re-introduction (5 cloud-exclude items skipped;
  billing/update-notification/cloud-runtime excluded from migration)
- No core-token rebind damage: all changes additive or per-site upstream-mirrored

## Version

`apps/desktop/package.json` 0.5.0 → 0.5.1 (canonical version source).
