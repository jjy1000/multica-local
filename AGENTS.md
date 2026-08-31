# AGENTS.md

This file provides guidance to Qoder (qoder.com) when working with code in this repository.

> **Single source of truth**: the root [`CLAUDE.md`](CLAUDE.md) is the authoritative rules file; this file is a synced digest of it. When the two disagree, `CLAUDE.md` wins — fix the drift here. **Guarded sections** — toolchain versions, package boundaries, verification commands, and the Critical Constraints tokens (localized fork prohibitions, state management, backend UUID rules, Pythia source-of-truth, experimental network calls, migration/config immutability, i18n selectors) — are enforced by `scripts/check-agents-docs-sync.mjs`, which runs in CI (`docs-sync` job) and in the `githooks/pre-push` hook. **Unguarded sections** (Quick Reference, Architecture, Sub-domain Guides, Testing Strategy, Dependency Management prose) are not machine-checked — verify them against `CLAUDE.md` before relying on them.

> **Current release: 0.5.91** (2026-08-31, shipped; `/Applications/Multica.app` = 0.5.91, cold-start PASS ~4s; ship log [`.omc/0.5.91-ship-2026-08-31.md`](.omc/0.5.91-ship-2026-08-31.md), release notes [`.omc/release-notes-0.5.91.md`](.omc/release-notes-0.5.91.md); ship chain 7/7 — snapshot `.bak` + pg_dump 32M retained, 277 migrations all-skip, repo backup `.omc/backups/2026-08-31-0250/0.5.91-ship/`, asar fix-presence check `IssueContextMenuProvider` ×3). **0.5.91 issue context-menu singleton refactor (commits `4fc93130b` + bump `d43b1881d`)**: root-causes the user-reported app-wide freeze ("右键菜单点状态 → 点击无反应 → 卡死") — per-row Base UI ContextMenu roots are modal by default; the menu's own status change re-buckets the issue and unmounts the anchored row, tearing down the open modal menu whose leaked backdrop then swallows every click in the window. Ported the upstream singleton structure (upstream `ba108978a`, commented there as "the single largest slice of the tab-switch freeze"; fork and upstream have NO merge-base — manual cherry-picks only): `IssueContextMenuProvider` = ONE menu root per surface (ListView/BoardView/SwimLaneView/GanttView) with a cursor virtual anchor; `IssueActionsContextMenu` degrades to a cloneElement reporting `(issue, contextmenu event)` and throws without a provider; `ui/context-menu.tsx` forwards `anchor` to the Positioner; row `data-popup-open` highlight kept via imperative management. Verified: root typecheck 6/6; issue-actions-menu 8/8 incl. row-unmount regression + no-provider throw pins; swimlane 43/43; views = exact pre-existing baseline (42); Go gates N/A (zero server changes). ⚠️ shipped from a working tree also carrying another session's uncommitted changes (core `networkMode`/WS-reconnect predicate, status-picker/app-sidebar/experimental-locales WIP) — commit-align ASAP. **Unported upstream ledger**: MUL-6749 (non-ASCII custom status key derivation — the fork's MUL-6243 port hits this with Chinese names), MUL-6835 (status/priority value i18n), `109b67790` (status-filter menu GroupLabel crash), MUL-6632 inbox redesign family (SKIP-DIVERGENCE decision gate unchanged). Residual: route-switch with menu open still unmounts the singleton (upstream-accepted); origin remote 404s. Previous chain: **0.5.90** (2026-08-30) — OpenMythos enhancer-only outer loop (sole retired, coda-strategy delivery: persist + system comment @mention wake + claim briefing injection, `IssueOpenMythosIcon`, LabPicker enhancer-only, full rebrand); **0.5.89** — labs conversational plugin management (five `multica lab` verbs + briefing), mig 284 teardown ledger + reclaim, skills_visibility lab_scoped + daemonless-install stubs (rebind roster 3→11), `.trash/` 30-day GC; **0.5.88** — labs delegation loop `multica lab delegate --parent`; **0.5.87** — mythos async-engine unification; **0.5.86** — interaction-model law, assignee-lock gate, lab report writeback (mig 282), swarm consolidation (mig 283); **0.5.85** — causal graph P1 read-side (`claim_brief.go` BFS ≤2 at claim). **Next cycle (0.5.92+)**: OpenMythos deepening (embedding-based convergence via pgvector + adaptive early-exit, sub-issue `hidden_at` mig 285 + archive, self-opt deep wiring, fixed-roster + skill-adapter reuse) + WS4 delegation UX + WS5 causal memory; remaining ledger: CompleteTask dispatched no-op, TestInstallTimesfm workspace scoping, mythos reaper boot race, DB-backed ClaimTaskByRuntime integration tests. **Gate-integrity**: final gates must be uncached, sequential `go test ./...` with `DATABASE_URL` exported, **and run with runtime-verification servers STOPPED** (their tickers share the DB); runtime verification servers take `PORT=8091` (OrbStack squats :3000/:8080, packaged server :8090, pprof :6060). Packaged-app ops facts: the app bundles Postgres.app (`~/Library/Application Support/Multica/pg`+`pgdata`, owns :5432); the cold-start verifier reads expected version from the PRIMARY checkout (use `EXPECTED_VER=` pre-merge); OrbStack `pocketbase` restart policy `no`; pre-update snapshot keeps ONE `.app` rollback copy (full bundle + `pg_dump`) and `ship-mac` step 6b writes the repo-level `.omc/backups/<TS>/<ver>-ship/`. **0.5.80's nav law stands**: every navigation path into `/experimental/*` MUST arm the workspace-singleton release-suppression token. Open decision gate unchanged: upstream inbox architecture (MUL-6632) — until decided, inbox-family upstream commits stay SKIP-DIVERGENCE. Historical release notes archived at `.omc/_legacy/release-notes-archive.md` (0.5.12→0.5.41); 0.5.43–0.5.91 notes at `.omc/release-notes-0.5.{43..91}.md`; load-bearing contracts live in root [`CLAUDE.md`](CLAUDE.md) Known Stability Surfaces / Active Contracts / Fork-Applicable HIGH Vuln Contracts.

## Quick Reference

```bash
make dev              # One-command bootstrap: auto-setup env, deps, DB, start everything
make start            # Start backend + frontend (requires prior setup)
make check            # Full verification: typecheck → unit → Go tests → E2E
make check-fast       # Fast affected TS checks only (typecheck + unit + lint), no DB/Go/E2E
make test             # Go tests only (server/)
make server           # Run Go backend only
make stop             # Stop app processes
make clean            # Remove build caches

pnpm typecheck        # TypeScript typecheck (Turborepo)
pnpm test             # Vitest unit tests (Turborepo)
pnpm lint             # ESLint (Turborepo)
pnpm dev:web          # Next.js dev server
pnpm dev:desktop      # Electron dev

# Single Go test
cd server && go test -run TestName -count=1 -timeout 60s ./internal/handler/

# Single Vitest test
pnpm test path/to/file.test.ts

# After SQL changes
cd server && sqlc generate

# Verified ship (7 steps: build→migrate→bundle→sign→backup→install→cold-start)
bash scripts/ship-mac.sh --yes

# Packaged-app check (dev .env PORT=8080; the packaged app's server listens on :8090)
bash ~/.multica/scripts/verify-desktop-cold-start.sh
```

## Architecture

Multica is an AI-native task management platform. Agents are first-class assignees that own issues, comment, and change status.

```
server/                  Go backend (Chi router, sqlc, gorilla/websocket)
├── cmd/server/          HTTP + WebSocket server
├── cmd/multica/         CLI binary
├── cmd/migrate/         Forward/back SQL migrations
├── internal/handler/    HTTP handlers
├── internal/service/    Business logic
├── internal/daemon/     Agent daemon runtime
├── internal/experimental/ Labs flag platform
└── migrations/          SQL migration files

apps/web/               Next.js App Router (frontend)
apps/desktop/           Electron desktop app (primary target)
apps/mobile/            Expo / React Native iOS app
apps/docs/              Nextra documentation site

packages/core/          Headless business logic, API client, React Query hooks, Zustand stores
packages/ui/            Atomic UI components (shadcn/Base UI)
packages/views/         Shared business pages/components for web + desktop
packages/tsconfig/      Shared TypeScript config
```

**Dependency direction:** `views → core + ui`; `core` and `ui` are independent. Shared packages export raw `.ts`/`.tsx` compiled by consuming apps.

## Toolchain

| Tool | Version | Pin location |
|------|---------|--------------|
| Node | 22.x | `.nvmrc`, CI |
| pnpm | 10.28.2 | `package.json` `packageManager` |
| Go | 1.26.1 | `server/go.mod`, CI |
| TypeScript | ^5.9.3 | `pnpm-workspace.yaml` catalog |
| React | 19.2.3 | `pnpm-workspace.yaml` catalog |
| PostgreSQL | 17 with pgvector | Docker / bundled Postgres.app |

## Sub-domain Guides

When working in a sub-domain, read its co-located guide first:

| Directory | Guide |
|-----------|-------|
| `server/` | `server/CLAUDE.md` |
| `packages/` | `packages/CLAUDE.md` |
| `packages/views/` | `packages/views/CLAUDE.md` |
| `apps/desktop/` | `apps/desktop/CLAUDE.md` |
| `apps/mobile/` | `apps/mobile/CLAUDE.md` |
| `apps/web/` | `apps/web/CLAUDE.md` |

Each guide directory also carries an auto-synced `AGENTS.md` mirror (same content, discoverable by agent platforms that load `AGENTS.md`). The co-located `CLAUDE.md` is the source of truth; parity is enforced by `scripts/check-agents-docs-sync.mjs`.

Root `CLAUDE.md` has complete rules for desktop packaging, labs platform, ship chain, and cross-cutting constraints.

## Critical Constraints

### This is a localized single-user fork

Do NOT re-add: telemetry, auto-update, Google OAuth/email verification, cloud features, external support UI (Discord/HelpLauncher/Feedback). Only `POST /auth/login {"name":"..."}` (username-only) works.

### Package boundaries

- `packages/core/`: No `react-dom`, no `localStorage` (use `StorageAdapter`), no `process.env`, no UI libs
- `packages/ui/`: No `@multica/core` imports, no business logic
- `packages/views/`: No `next/*`, no `react-router-dom`, no stores — use `NavigationAdapter`/`useNavigation()`/`<AppLink>`
- Every workspace under `apps/` and `packages/` must declare external deps in its own `package.json`
- Shared deps use `catalog:` from `pnpm-workspace.yaml`

### State management

- **TanStack Query** = server state (issues, users, workspaces, agents)
- **Zustand** = client state (filters, drafts, modals, layout)
- WebSocket events invalidate Query cache; never write to Zustand stores directly
- Mutations are optimistic by default

### Backend UUID rules

In `server/internal/handler/`: resource path params → resolve through loaders; pure UUID inputs → `parseUUIDOrBadRequest`; trusted round-trips → `parseUUID` (panics on invalid).

### Desktop primary considerations

- Pythia source-of-truth: `apps/desktop/vendor/pythia-src/engine/` (NOT `resources/pythia/engine/`)
- All experimental tab network calls must use `api.rawRequest()`, never bare `fetch()`
- Migrations are forward-only (never drop tables/columns)
- Config fields are append-only (no deletions/renames)

### i18n

Selectors MUST be arrow expressions: `t(($) => $.foo.bar)` ✓ — block body `t(($) => { return $.foo; })` ✗ (crashes the app). ESLint enforces this.

## Testing Strategy

| Layer | Location | Runner |
|-------|----------|--------|
| Shared logic/stores/hooks | `packages/core/*.test.ts` | Vitest |
| Shared UI/pages | `packages/views/*.test.tsx` | Vitest |
| Platform wiring | `apps/web/*.test.tsx`, `apps/desktop/` | Vitest |
| End-to-end flows | `e2e/*.spec.ts` | Playwright |
| Backend | `server/**/*_test.go` | `go test` |

Go tests run with `-p 1` (serialized packages) because DB-backed packages share one `DATABASE_URL`.

**Gotcha**: DB-backed Go tests **silently skip** when `DATABASE_URL` is unset — a suspiciously fast green run means nothing ran. Export it first: `export $(grep -E '^DATABASE_URL=' .env | xargs)` (then confirm the runner prints your DB-set marker before trusting results).

## Verification Sequence

Iterate with the narrowest useful check, then broaden:

1. `pnpm typecheck` — fast type check
2. `pnpm test` — TS unit tests
3. `make test` — Go tests (requires DB)
4. `make check` — full pipeline including E2E

`make check-fast` runs affected TS checks only (no DB/servers/Go/E2E) — ideal for iterating on frontend changes.

## Dependency Management

- Package manager: pnpm with `node-linker=isolated` (`.npmrc` — do NOT switch to hoisted; breaks electron-builder)
- Monorepo orchestration: Turborepo (`turbo.json`)
- Workspace layout: `pnpm-workspace.yaml` (apps/*, packages/*)
- Shared version catalog in `pnpm-workspace.yaml` — bump versions there, not per-package
