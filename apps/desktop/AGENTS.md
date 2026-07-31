<!-- AUTO-SYNCED MIRROR of ./CLAUDE.md (the source of truth for this directory). Edit CLAUDE.md, then regenerate this file; parity is enforced by scripts/check-agents-docs-sync.mjs. -->

# Desktop App Rules (apps/desktop/)

Electron desktop app — the **primary target** of this localized single-user fork.
This file consolidates the desktop-scoped constraints scattered in the root
`CLAUDE.md` so you don't have to read the whole root file to work here. The root
file remains the source of truth for the complete ship chain, Labs platform,
and cross-cutting rules; each section below points back to it for detail.

## Pythia source-of-truth

`apps/desktop/vendor/pythia-src/engine/` is the source-of-record, **NOT**
`apps/desktop/resources/pythia/engine/`. `scripts/bundle-cli.mjs` wipes
`resources/pythia/` and re-copies from `vendor/pythia-src/` on every run — any
edit made directly under `resources/pythia/engine/*.py` is silently overwritten
at bundle time (this bit the 0.3.21 Pythia i18n pass three times). Edit the
vendor copy, then re-run `pnpm --filter @multica/desktop bundle-cli`.

## Experimental tab network calls (`rawRequest`)

Every HTTP/SSE call from a Labs tab must go through `api.rawRequest(path, init)`
(`@multica/core/api`), **never a bare `fetch()`**. `rawRequest` prepends the
configured `baseUrl`, injects Bearer/CSRF/workspace `authHeaders()`, sets
`credentials: "include"`, and returns the raw `Response`. Bare
`fetch("/api/...")` silently fails in the desktop app: the renderer origin is
`localhost:5173` (dev) / `file://` (packaged), not the bundled backend on
`localhost:8090`, and desktop runs in token mode so cookies are never attached.

Exception: `use-pythia-sse.ts` must **NOT** use `rawRequest` — its URL comes
from `window.experimentalAPI.pythia.getURL()` and points at the loopback Python
engine, not the Multica backend. Pythia loopback calls go through
`window.experimentalAPI.pythia.proxy(path, init)` (allowlisted + rate-limited
in `src/main/pythia-manager.ts`), never a bare `fetch()` to the loopback URL.

See root `CLAUDE.md` → "Experimental tab network calls (0.3.30)".

## Packaging & ship chain

Canonical order lives in root `CLAUDE.md` → "Ship chain (canonical order)".
Non-negotiables:

1. **Pre-update snapshot is mandatory** before any packaging:
   `bash ~/.multica/scripts/pre-update-snapshot.sh` (exit 1 blocks packaging).
2. **Run pending migrations before `bundle-cli`**:
   `cd server && go run ./cmd/migrate up` — surface SQL errors at build time,
   not at first user launch.
3. Build steps (from repo root):
   ```bash
   pnpm --filter @multica/desktop bundle-cli   # Go binaries + migrations + PG manifest → resources/
   pnpm --filter @multica/desktop build        # electron-vite build (does NOT run electron-builder!)
   pnpm exec electron-builder --mac --dir      # DMG creation hangs on create-dmg 1.2.3; ship the .app from dist/mac-arm64/
   ```
   `pnpm build` alone does NOT replace the asar — verify with
   `grep -c rawRequest apps/desktop/dist/mac-arm64/Multica.app/Contents/Resources/app.asar`.
4. **Re-sign nested Go binaries** after `cp -R ... /Applications/` — macOS 27
   Gatekeeper SIGKILLs unpacked binaries that `--dir` did not sign. This is
   now a runnable, self-verifying script (was doc-only/manual and got skipped
   on past ships):
   ```bash
   bash scripts/desktop-sign-nested-binaries.sh /Applications/Multica.app
   ```
   It signs the app + the 3 nested binaries and asserts `.../bin/multica
   --help` exits 0 (exit 137 = a binary was missed / unsigned).
5. Verify cold start: `bash ~/.multica/scripts/verify-desktop-cold-start.sh`
   (three-check pass: ports 5432/8090 listening ≤ 6 s, `/health` ok, row parity).
6. If `electron-builder --mac --dir` deadlocks (> 5 min at `unpack-electron`),
   use the manual asar repack fallback — root `CLAUDE.md` → "Ship chain
   fallback: manual asar repack (0.3.63+)". Valid only for patches that don't
   add/remove/rename resource paths.

## Version source

`apps/desktop/package.json` is the **only** version file. `git describe` is
tried first by `bundle-cli.mjs` but this repo's tags are `pre-update-*`
snapshot markers, so it falls back to `package.json`. Bump nowhere else.

## Build config invariants (do NOT regress)

- `electron-builder.yml` files list must keep `- "!dist/**"`, and
  `scripts/package.mjs` must keep its Step 0 pre-build `rmSync(distDir)` —
  dropping either repacks a stale multi-GB `dist/` into the asar and aborts
  with "links out of the package".
- **Never set `node-linker=hoisted` in `.npmrc`** to silence asar symlink
  errors — it breaks electron version detection and the pnpm dependency
  collector (ships an app with missing node modules). `.npmrc` stays isolated
  (`shamefully-hoist=true` only).

## Self-contained backend & data safety

Lifecycle is managed by `src/main/server-manager.ts` (probe PG → migrate →
spawn server → daemon follows) with native PG in `src/main/pg-bootstrap.ts`.
Before touching either, read root `CLAUDE.md` → "Desktop Rules" and memory
`multica-0.3.0-standalone-2026-07-02.md`. Hard contracts:

- **P0**: `runMigrate(profile, env, backend?)` REFUSES `backend === "external"`
  and the `ensureServerUp` call site also skips. Do NOT remove either layer;
  any new caller must pass `backend` explicitly.
- **P1.8**: `runMigrationFlow` writes the `.pg-migrating-v1` sentinel with
  `O_EXCL` BEFORE the destructive restore and renames it only on success. Do
  NOT write the final sentinel before the operation succeeds.
- **Migrations are forward-only** (never drop tables/columns); **config fields
  are append-only** (no deletions/renames in `config.json` / `.env`).
- Packaged path resolution: `child_process` does not resolve asar paths — use
  `resolveResourcePath()` in server-manager.ts.

Any change to `src/main/{server-manager,pg-bootstrap,daemon-manager}.ts` or a
packaged `.app` build requires the desktop smoke test (root `CLAUDE.md` →
"Testing" → three-check pass).

## Routing & window

- New pre-workspace desktop flows register a `WindowOverlay` type in
  `stores/window-overlay-store.ts`; do not add them to `routes.tsx`.
- `apps/desktop/src/renderer/src/platform/` is the only place for
  `react-router-dom` navigation wiring.
- `setCurrentWorkspace(slug, uuid)` from `@multica/core/platform` is the active
  workspace source of truth; code leaving workspace context must call
  `setCurrentWorkspace(null, null)` explicitly, and cross-workspace navigation
  goes through the navigation adapter (`switchWorkspace`).
- Full-window views outside the dashboard shell must mount `<DragStrip />` from
  `@multica/views/platform` as the first flex child; interactive controls in
  the top 48px need `WebkitAppRegion: "no-drag"`.
- `src/main/index.ts::ensureWindowOnscreen()` clamps stale BrowserWindow bounds
  (Electron 39/macOS restores off-screen bounds) — keep it called after
  construction, on `ready-to-show`, and on `move`/`resize`/`display-removed`.

## Worktree dev isolation

`scripts/dev.mjs` auto-detects worktree-isolated renderer ports and app names
via `worktree-dev-env.mjs`. Linked worktrees appear as "Multica Canary" with
offset ports and share one PG container (`make worktree-env` +
`make setup-worktree` + `make start-worktree` for manual setup).
