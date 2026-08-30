# AGENTS.md

This file provides guidance to Qoder (qoder.com) when working with code in this repository.

> **Single source of truth**: the root [`CLAUDE.md`](CLAUDE.md) is the authoritative rules file; this file is a synced digest of it. When the two disagree, `CLAUDE.md` wins — fix the drift here. **Guarded sections** — toolchain versions, package boundaries, verification commands, and the Critical Constraints tokens (localized fork prohibitions, state management, backend UUID rules, Pythia source-of-truth, experimental network calls, migration/config immutability, i18n selectors) — are enforced by `scripts/check-agents-docs-sync.mjs`, which runs in CI (`docs-sync` job) and in the `githooks/pre-push` hook. **Unguarded sections** (Quick Reference, Architecture, Sub-domain Guides, Testing Strategy, Dependency Management prose) are not machine-checked — verify them against `CLAUDE.md` before relying on them.

> **Current release: 0.5.90** (2026-08-30, shipped; `/Applications/Multica.app` = 0.5.90, cold-start PASS; ship log [`.omc/0.5.90-ship-2026-08-30.md`](.omc/0.5.90-ship-2026-08-30.md), release notes [`.omc/release-notes-0.5.90.md`](.omc/release-notes-0.5.90.md); ship chain 7/7 — snapshot `.bak` + pg_dump 32M retained, 280 migrations all-skip, repo backup `.omc/backups/2026-08-30-1232/0.5.90-ship/`, asar brand check 80 OpenMythos hits). **0.5.90 OpenMythos enhancer-only outer loop (commits `30d58b851` + bump `9705557b5`)**: the 蜂群 lab is rebranded **OpenMythos** (display only — flag key `mythos_swarm` stays, VERBATIM law); **sole mode retired for new bindings** (400 on create, on lab_mode-touching PATCHes, and on the run API; legacy sole rows keep resolving — forward-only); **delivery chain closed** — coda strategy persists to `mythos_run.coda_conclusions` (previously only the sole recovery watch wrote it), posts to the root issue as a system comment @mentioning the target (child-done wake path, `HasPendingTaskForIssueAndAgent` dedupe closes the bind-vs-coda race), and injects `## OpenMythos Strategy (outer loop, read-only)` into the target's claim briefing (`service/mythos/brief.go::BuildEnhancerBrief`, 4 096-byte cap, silent fallback, gated on a hoisted single `claimIssueLabSource` read); run sub-issues parent to the root (`parent_issue_id`, zero migration); the run POST runs on `context.WithoutCancel`; `IssueOpenMythosIcon` sits beside the causal icon (ICP-5 passive) with run status + one-click start; LabPicker always binds enhancer (legacy sole issues get a one-click switch); self-opt keeps its human gate. Verification: Go 38 pkgs 0 FAIL (uncached `-p 1`, verification server stopped), typecheck 6/6, desktop 370/370, views = exact pre-existing baseline, live loop on :8091 15 pass / 0 fail (bind → run → strategy comment → wake → claim briefing); dev record [`.omc/0.5.90-dev-2026-08-30.md`](.omc/0.5.90-dev-2026-08-30.md), notes draft [`.omc/release-notes-0.5.90.md`](.omc/release-notes-0.5.90.md). **0.5.89 ships three layers**: **① labs conversational plugin management** — every issue-bound non-lab claim carries the static `## Lab Plugin Management` briefing and agents create/manage user plugins mid-conversation via five `multica lab` verbs (create/list/inspect/enable/disable/delete; two-step delete protocol; built-in toggles refused in agent context; never-disagree law extended to five verbs via cobra-tree contract test); provenance `created_by_issue/created_by_task` stamped from daemon-injected `MULTICA_ISSUE_ID`/`MULTICA_TASK_ID`. **② mig 284 teardown ledger `user_plugin_resource`** — manifest `agents_inline`/`skills_inline` are server-provisioned hidden resources (visibility row + lab_managed + ledger), declared (pre-existing) resources ledger `origin='declared'` and are never reclaimed; delete = 409-guard on non-terminal bound issues → per-resource reclaim (archive/pause/hard-delete/env-dir to `.trash/`) → 200 report (was 204); `GET /reclaim-plan` + `POST /reclaim` retry endpoints; `capabilities.skills_visibility: global|lab_scoped` scopes skill injection to the plugin's own issues. **③ pre-ship tech-debt batch** — daemonless lab installs (causal_graph/pythia_oracle/timesfm/code_canvas/semantica) synthesize a stable offline runtime stub instead of hitting the `agent.runtime_id` NOT NULL FK (`resolveOrSynthesizeLabRuntime`; rebind roster 3→11), `.trash/` 30-day GC wired into `RuntimeGC.sweep`, invisible LabPicker trigger fixed, "Unknown Agent" assignee chip fixed via agent-by-id fetch-on-miss, reclaim endpoints tested, `title_en` closed as non-existent. Recent chain: **0.5.88** (shipped 2026-08-29) — labs delegation loop `multica lab delegate --parent` (child issue + result comment + child-done wake + causal `depends_on` edge), delegation briefing with the **law: briefing and delegate CLI must never disagree**, catalog `Frozen`/`SuccessorKey`, user plugins join the interaction-model taxonomy, UX/animation polish; **0.5.87** (shipped in 0.5.88) — mythos async-engine unification (stalled-run reaper, per-tick timeout, t=0 heartbeat; 10-lab audit clean); **0.5.86** — labs interaction-model law (assignee vs auxiliary), assignee-lock hard gate, lab report writeback (mig 282), swarm consolidation (ONE 蜂群 lab, `swarm_topology` frozen, mig 283), causal-graph readability; **0.5.85** — causal graph P1 read-side (`claim_brief.go` BFS ≤2 injected at claim; read side 0%→~70%); **0.5.84** — P0 fix batch, mig **281**, Active Contracts #9 (FLAG_ROUTE_SUFFIX + never-nag + TouchCausalNode callsite); **0.5.83** — WL3 issue causal graph + hidden agent team, migs **276-280**, flag key `causal_graph` VERBATIM, trust ladder A>B>C>D; **0.5.82** — WL2 TimesFM 2.5 lab, flag key `timesfm` VERBATIM, route split view vs REST proxy, ICP-1 records-only; **0.5.81** — WL1 task-issue-first UX, ICP-3 deep-link round trip (wall-clock 2s poll budget). **Next cycle (0.5.91+)**: OpenMythos deepening (embedding-based convergence via pgvector + adaptive early-exit, sub-issue `hidden_at` mig 285 + archive, self-opt deep wiring reflection→suggested-edits, fixed-roster + skill-adapter reuse) + WS4 delegation UX (DelegationCard, started comment, terminate-from-both-sides, LabProgressCard user_* branch) + WS5 causal memory (causal recall/trace CLI, LLM extraction as Tier D, LLM Wiki mirror, science-lab archive UI); remaining ledger: CompleteTask dispatched no-op (mechanism pinned, fix with WS4), TestInstallTimesfm workspace scoping, mythos reaper boot race, DB-backed ClaimTaskByRuntime integration tests. **Gate-integrity**: final gates must be uncached, sequential `go test ./...` with `DATABASE_URL` exported, **and run with runtime-verification servers STOPPED** (their tickers share the DB); runtime verification servers take `PORT=8091` (OrbStack squats :3000/:8080, packaged server :8090, pprof :6060). Packaged-app ops facts: the app bundles Postgres.app (`~/Library/Application Support/Multica/pg`+`pgdata`, owns :5432); the cold-start verifier reads expected version from the PRIMARY checkout (use `EXPECTED_VER=` pre-merge); OrbStack `pocketbase` restart policy `no`; pre-update snapshot keeps ONE `.app` rollback copy (full bundle + `pg_dump`) and `ship-mac` step 6b writes the repo-level `.omc/backups/<TS>/<ver>-ship/`. **0.5.80's nav law stands**: every navigation path into `/experimental/*` MUST arm the workspace-singleton release-suppression token. Open decision gate unchanged: upstream inbox architecture (MUL-6632) — until decided, inbox-family upstream commits stay SKIP-DIVERGENCE. Historical release notes archived at `.omc/_legacy/release-notes-archive.md` (0.5.12→0.5.41); 0.5.43–0.5.90 notes at `.omc/release-notes-0.5.{43..90}.md`; load-bearing contracts live in root [`CLAUDE.md`](CLAUDE.md) Known Stability Surfaces / Active Contracts / Fork-Applicable HIGH Vuln Contracts.

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
