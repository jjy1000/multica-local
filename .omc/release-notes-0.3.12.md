# Multica 0.3.12 — 2026-07-13

## Summary

Makes Claude Science feel like an **integrated part of Multica**
instead of a foreign site. Three changes:

1. **Same-origin reverse proxy** for Labs-gated subprocesses
   (server/internal/handler/experimental_proxy.go) — the iframe
   inside `claude-science-view.tsx` now points at
   `/experimental/claude-science/*` on Multica's own port 8090
   instead of the loopback URL. Same origin lets the renderer reach
   into the iframe to drive the theme store and run an i18n
   MutationObserver.
2. **Theme follow** — when the user toggles Multica between
   light/dark/system, the new theme value is pushed into the
   OpenScience iframe via `localStorage("openscience-color-scheme")`
   so the workspace switches themes in lockstep.
3. **i18n shim** — a closed-table translator (~120 entries) walks
   the iframe's text nodes and replaces common OpenScience chrome
   strings (nav, buttons, status badges) with their Chinese
   equivalents. Skill bodies and user data are NEVER touched —
   translating research inputs silently would be a data-integrity
   hazard.

The Multica chrome bar above the iframe reuses the design system
tokens (`bg-background`, `text-foreground`, `border-border`) so the
toolbar reads as part of Multica even though the iframe below is a
separate web app.

## Architecture

```
┌────────────────────────────────────────────┐
│ Multica renderer (Electron)                │
│ ┌────────────────────────────────────────┐ │
│ │ Multica chrome bar (h-9, bg-background) │ │  ← Multica design tokens
│ │ 试验性功能 / Claude 科学工作台 · status   │ │
│ └────────────────────────────────────────┘ │
│ ┌────────────────────────────────────────┐ │
│ │ <iframe src="/experimental/claude-science/*"> │ │
│ │   same-origin (Multica :8090)          │ │
│ │   sandbox="allow-same-origin ..."      │ │
│ │   ┌──────────────────────────────────┐ │ │
│ │   │ OpenScience SolidJS UI            │ │ │
│ │   │   localStorage("openscience-...") │ │ │  ← theme-injection target
│ │   │   (translated text nodes)          │ │ │  ← i18n shim target
│ │   └──────────────────────────────────┘ │ │
│ └────────────────────────────────────────┘ │
└────────────────────────────────────────────┘
           │ proxy /experimental/claude-science/*
           ▼
┌────────────────────────────────────────────┐
│ Multica server :8090                         │
│   /__experimental/upstream ← POST desktop   │  ← desktop registers URL
│   /experimental/claude-science/* → reverse  │
│      proxy to loopback URL                  │
└────────────────────────────────────────────┘
           │ httputil.ReverseProxy
           ▼
┌────────────────────────────────────────────┐
│ OpenScience Bun subprocess :random-port      │
│   (or Pythia FastAPI for pythia_oracle)     │
└────────────────────────────────────────────┘
```

The desktop main process's `BaseExperimentalManager` got two new
hooks (`onReady` / `onStop`) so a manager can register/unregister
its bound URL with the server's in-process registry as part of
its lifecycle. The registry is in-memory because the loopback URL
is ephemeral and consumed only by the same host.

## Files touched

| File | Δ |
|---|---|
| `server/internal/handler/experimental_proxy.go` | new, ~150 LOC (same-origin proxies + upstream registry + chi routes) |
| `server/cmd/server/router.go` | +6 (call `handler.MountExperimentalProxies` on public router) |
| `apps/desktop/src/main/experimental/upstream-registry.ts` | new, ~110 LOC (POST/DELETE helpers) |
| `apps/desktop/src/main/experimental/manager-template.ts` | +18 (`onReady` / `onStop` hooks) |
| `apps/desktop/src/main/claude-science-manager.ts` | +6 (wire `onReady`/`onStop` to registry) |
| `apps/desktop/src/main/pythia-manager.ts` | +6 (mirror for Pythia) |
| `apps/desktop/src/renderer/src/pages/claude-science-view.tsx` | rewrite, ~480 LOC (chrome + theme bridge + i18n table + translateText) |
| `apps/desktop/package.json` | bump 0.3.11 → 0.3.12 |

## Verification

- `pnpm typecheck` — **6/6 typecheck tasks green, 0 errors**.
- `cd server && go build ./...` — clean.
- `cd server && go test ./internal/handler/ ./internal/experimental/`
  — both green.
- `pnpm --filter @multica/desktop test` — **274/274 pass** (no
  regression on the 0.3.10 spawn-safety fix or the 0.3.11
  vendor-binary regression guard).
- Cold-start 3-check on 0.3.12 DMG:
  - 5432 LISTEN (postgres) within 8s.
  - 8090 LISTEN (server) within 8s.
  - GUI window appears.
- Row parity preserved: `workspace=1 / issue=162 / agent=80 /
  schema_migrations=184`. No SQL migrations touched.
- `/experimental/claude-science` route serves 502 with a structured
  error message until the desktop registers an upstream. After the
  user enables the flag and clicks the sidebar entry, the manager
  spawns and registers the loopback URL within `readyTimeoutMs`
  (60s for Claude Science, 30s for Pythia).

## Known caveats

- **i18n table is closed and English-only**. The 120 entries cover
  the OpenScience chrome surface (nav, buttons, status badges,
  modal titles). New entries ship as upstream changes land; the
  table is in `claude-science-view.tsx::I18N_TABLE` and adding
  strings is a one-line patch.
- **Skill bodies are NOT translated.** 292 skills + 5 agents' UI
  fields render verbatim. The translator explicitly rejects text
  nodes > 80 chars and inside `<code>/<pre>/<textarea>/contenteditable`
  so research inputs are never corrupted.
- **Theme sync is best-effort** — localStorage writes succeed
  silently (same-origin policy is satisfied by the reverse proxy),
  but if OpenScience's theme-preload script ignores the storage
  event (e.g. caches the theme at load time), the user must reload
  the iframe. We listen for `iframe.onload` to re-push.
- **Pythia proxy is wired but unused** — `pythia_oracle` flag is
  still off by default and Pythia is not yet vendored (no
  `setup-pythia.sh`). The proxy code path is exercised on the next
  Pythia patch.
- DMG hand-built via `create-dmg` (memory 0.3.4 workaround).
- DMG size unchanged at **357 MB** (no new vendored payloads).

## User-visible change

Before 0.3.12: clicking the Claude Science sidebar entry loaded
OpenScience in a foreign-origin iframe. The iframe rendered
OpenScience's own chrome (English nav, hardcoded dark theme that
did not follow Multica's light/dark toggle). User felt they had
left Multica.

After 0.3.12:
- The iframe points at `/experimental/claude-science/*` on
  Multica's own origin.
- A 36 px toolbar above the iframe shows the path
  `试验性功能 / Claude 科学工作台` plus a live status badge
  (`starting` / `ready` / `error`), styled with Multica's design
  tokens.
- Toggling Multica's theme (Settings → Preferences → Theme)
  switches the OpenScience workspace between light and dark
  without a reload.
- Common chrome strings (Settings, Models, New chat, Cancel,
  Save, etc.) render in Chinese inside the iframe.
- Skill bodies, code blocks, and editable fields stay verbatim
  in English so research inputs are never silently corrupted.

## Rollback

`git revert` the 8 file changes above + restore `package.json`
version 0.3.11. The proxy code path is additive — removing it
falls back to the old cross-origin iframe behaviour. No data
concerns.

## By the numbers

- 8 files touched, 2 new files.
- ~700 LOC added, ~30 LOC removed.
- 0 new SQL migrations.
- 0 breaking changes to existing wire format (new internal
  `/__experimental/upstream` endpoint is internal-only).