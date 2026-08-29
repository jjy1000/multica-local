# AGENTS.md

This file provides guidance to Qoder (qoder.com) when working with code in this repository.

> **Single source of truth**: the root [`CLAUDE.md`](CLAUDE.md) is the authoritative rules file; this file is a synced digest of it. When the two disagree, `CLAUDE.md` wins — fix the drift here. **Guarded sections** — toolchain versions, package boundaries, verification commands, and the Critical Constraints tokens (localized fork prohibitions, state management, backend UUID rules, Pythia source-of-truth, experimental network calls, migration/config immutability, i18n selectors) — are enforced by `scripts/check-agents-docs-sync.mjs`, which runs in CI (`docs-sync` job) and in the `githooks/pre-push` hook. **Unguarded sections** (Quick Reference, Architecture, Sub-domain Guides, Testing Strategy, Dependency Management prose) are not machine-checked — verify them against `CLAUDE.md` before relying on them.

> **Current release: 0.5.86** (2026-08-28, shipped from `epic/0.5.72-followups`; `/Applications/Multica.app` = 0.5.86 — labs interaction-model law, assignee lock, lab report writeback, swarm consolidation, causal-graph readability; ship log [`.omc/0.5.86-ship-2026-08-28.md`](.omc/0.5.86-ship-2026-08-28.md), release notes [`.omc/release-notes-0.5.86.md`](.omc/release-notes-0.5.86.md)). **Two dev cycles committed on the branch, NOT packaged**: **0.5.87** — mythos async-engine unification (swarm orchestrator's stalled-run reaper `ListStalledMythosRunsForGC` + `service/mythos/reaper.go` [24h running / 1h-stale supervising heartbeat, boot-sweep-first, no-context], per-tick 2×30s timeout, t=0 heartbeat; full 10-lab built-in-plugin audit clean; web stubs + locale gaps fixed; commits `19d4c506f`+`8d0cf35f6`; dev record [`.omc/0.5.87-dev-2026-08-29.md`](.omc/0.5.87-dev-2026-08-29.md)). **0.5.88** — labs delegation loop closed end-to-end: `multica lab delegate --parent [--stage]` creates the lab child under the calling issue, posts the result comment back on the parent, the parent agent wakes via the existing child-done mention channel, and a causal parent→child `depends_on` edge (provenance `issue_delegate`) records the linkage; daemon claim briefing gains `## Available Labs (delegation)` listing ONLY enabled assignee-model labs with resolvable leaders — frozen labs AND `AutoDispatch=false` labs (pythia_oracle/timesfm) are skipped, and the CLI fails fast on the latter (**law: briefing and delegate CLI must never disagree**); catalog `Frozen`/`SuccessorKey` advisory fields (swarm_topology→mythos_swarm; toggle unchanged); user plugins join the interaction-model taxonomy (manifest `interaction_model` [absent→auxiliary] + `leader_agent` [required iff assignee], both leader tables resolve user plugins from the DB row, lock semantics identical to built-ins); UX polish: LabProgressCard skeletons + status fade-through, section expand animation, settings interaction-model badges + frozen banner, causal-graph loading overlay + eased zoom + optimistic suggestions, targeted assignee-lock toast, dead `LeaderRewriteConfirmDialog` removed. **Full verification passed** (commits `f2467873e`/`98fd95138`/`eb48f6a24`; dev record + acceptance chapter [`.omc/0.5.88-dev-2026-08-29.md`](.omc/0.5.88-dev-2026-08-29.md)): static gates green in a quiet env (one full-suite flake — parallel t.Cleanup resetting shared runtime metadata — determinized via test-only `Handler.QuickCreateVersionGateOverride`; TS failures all proven pre-existing by stash/checkout discrimination); live API full loop (daemon-role register→claim→start→complete, briefing asserted on real claims, 4 delegated creates → 4 idempotent edges, child-done mention, user-plugin lock live); Electron UI audit via Playwright `_electron.launch` (all 4 checkpoints PASS, measured animation timings, 28 screenshots). **Next cycle (0.5.89+)**: ship-gate + packaging on the user's go; 0.5.88 known-remaining ledger (daemonless causal_graph/pythia install → NULL runtime_id install_error — fix = synthetic runtime stub; CompleteTask on dispatched tasks silently no-ops; invisible LabPicker chip on unbound issues; "Unknown Agent" assignee chip for offline leaders; plugin title_en; TestInstallTimesfm workspace scoping); DB-backed handler integration tests for `ClaimTaskByRuntime` with the subgraph path active; Tier C Pythia closure, historian/verifier automation, evolver window bound. **Gate-integrity**: final gates must be uncached, sequential `go test ./...` with `DATABASE_URL` exported, **and run with runtime-verification servers STOPPED** (their tickers share the DB); runtime verification servers take `PORT=8091` (OrbStack squats :3000/:8080, packaged server :8090, pprof :6060). Recent chain: **0.5.85** — causal graph P1 read-side (`claim_brief.go` BFS ≤2 injected at claim; read side 0%→~70%); **0.5.84** — P0 fix batch, mig **281**, Active Contracts #9 (FLAG_ROUTE_SUFFIX + never-nag + TouchCausalNode callsite); **0.5.83** — WL3 issue causal graph + hidden agent team, migs **276-280**, flag key `causal_graph` VERBATIM, trust ladder A>B>C>D; **0.5.82** — WL2 TimesFM 2.5 lab, flag key `timesfm` VERBATIM, route split view vs REST proxy, ICP-1 records-only; **0.5.81** — WL1 task-issue-first UX, ICP-3 deep-link round trip (wall-clock 2s poll budget). Packaged-app ops facts: the app bundles Postgres.app (`~/Library/Application Support/Multica/pg`+`pgdata`, owns :5432); the cold-start verifier reads expected version from the PRIMARY checkout (use `EXPECTED_VER=` pre-merge); OrbStack `pocketbase` restart policy `no`. **0.5.80's nav law stands**: every navigation path into `/experimental/*` MUST arm the workspace-singleton release-suppression token. Open decision gate unchanged: upstream inbox architecture (MUL-6632) — until decided, inbox-family upstream commits stay SKIP-DIVERGENCE. Historical release notes archived at `.omc/_legacy/release-notes-archive.md` (0.5.12→0.5.41); 0.5.43–0.5.86 notes at `.omc/release-notes-0.5.{43..86}.md`; load-bearing contracts live in root [`CLAUDE.md`](CLAUDE.md) Known Stability Surfaces / Active Contracts / Fork-Applicable HIGH Vuln Contracts.

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
