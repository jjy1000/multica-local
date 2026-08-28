# AGENTS.md

This file provides guidance to Qoder (qoder.com) when working with code in this repository.

> **Single source of truth**: the root [`CLAUDE.md`](CLAUDE.md) is the authoritative rules file; this file is a synced digest of it. When the two disagree, `CLAUDE.md` wins — fix the drift here. **Guarded sections** — toolchain versions, package boundaries, verification commands, and the Critical Constraints tokens (localized fork prohibitions, state management, backend UUID rules, Pythia source-of-truth, experimental network calls, migration/config immutability, i18n selectors) — are enforced by `scripts/check-agents-docs-sync.mjs`, which runs in CI (`docs-sync` job) and in the `githooks/pre-push` hook. **Unguarded sections** (Quick Reference, Architecture, Sub-domain Guides, Testing Strategy, Dependency Management prose) are not machine-checked — verify them against `CLAUDE.md` before relying on them.

> **Current release: 0.5.85** (2026-08-28, shipped from `epic/0.5.72-followups`; `/Applications/Multica.app` = 0.5.85, cold-start verify pending ship-mac). **0.5.85 — Causal graph P1 read-side (agent reads the graph).** The headline missing piece from the 0.5.83 audit is closed: `handler/daemon.go::ClaimTaskByRuntime` injects a compact causal subgraph context into the agent briefing at claim time (read-once, BFS depth ≤ 2, top-20 nodes/edges, skip Tier D + status≠active, filter `blocks`/`contradicts` edge types + `assumption`/`evidence` node types, confidence ≥ 0.6, hard cap 16 000 bytes / ~4 000 tokens with `…(truncated, N more edges)` suffix, 200 ms `context.WithTimeout`, silent fallback on every error path — broken causal read must NEVER block claim). Files: `server/internal/service/causal_graph/claim_brief.go` (new, 435 LOC, BFS + filter + render) + `claim_brief_test.go` (new, 456 LOC, 15 DB-less unit tests, all PASS) + `handler/daemon.go` (+31 lines, wiring at line 1961 after WorkspaceContext injection, before token-mint — mirrors the existing `squad_briefing.go` pattern). No new flag, no new migration — pure code on existing schema. Flag-gated upstream via `experimental.DefaultFor("causal_graph")` so off-flag installs pay zero overhead. Cooperation projection: **read side 0% → ~70%**, **overall ≈ 40-45% → ≈ 70-75%** — the loop is finally closed both ways. The 0.5.84 contracts (#8 trust ladder + #9 FLAG_ROUTE_SUFFIX + never-nag + TouchCausalNode callsite) all still hold. **0.5.86 (dev, unshipped on this branch — labs interaction model + swarm consolidation + causal-graph readability)**: every lab flag now carries `interaction_model` — `assignee` (独立工作型: claude_science_lab/pythia_oracle/mythos_swarm/swarm_topology/semantica/timesfm) vs `auxiliary` (辅助协作型: causal_graph/llm_wiki_bridge); assignee-model labs lock the issue's assignee slot to the lab leader (`handler/issue.go::assigneeLabLockError`, create+update parity; empty assignee always allowed, mythos keeps the 0.3.33 strict mutex; lab-only flip requires the leader AGENT ROW to exist in the workspace — `timesfm` gained leader `timesfm_oracle` in both leader tables); Pythia + TimesFM issue runs write a text report comment back on the issue (new `handler/lab_report_writeback.go` + mig 282 `report_comment_id`, exactly-once via COALESCE); swarm consolidated to ONE lab — `mythos_swarm` is the entry, `swarm_topology` frozen (picker-hidden + empty sidebar + consolidation banner), orphan `swarm\_%\_%` agents archived (mig 283 + swarm-GC stalled-run reap); causal graph readability: depth-banded rings + node drag + zoom/pan + `pollPaused` while dragging + reduced-motion animations + depth-1 popup with "+N 节点 · M 边" summary; issue lab progress card with per-lab run state + click-through. Migs 282-283, bundled copies tracked. Full handler package PASS uncached with `DATABASE_URL`; NOT packaged per user directive. Dev record: [`.omc/0.5.86-dev-2026-08-28.md`](.omc/0.5.86-dev-2026-08-28.md). **Next cycle**: DB-backed handler integration tests for `ClaimTaskByRuntime` (handler pin); Tier C Pythia hypothesis→evidence closure; historian/verifier automation; evolver window bound. **0.5.85 ship gate detour**: `node_modules/turbo` was deleted between the 0.5.84 ship (12:30) and the 0.5.85 gate (15:49) — recovery is `CI=true pnpm install --frozen-lockfile` (28 s, no TTY prompt). Ship log: [`.omc/0.5.85-ship-2026-08-28.md`](.omc/0.5.85-ship-2026-08-28.md); release notes [`.omc/release-notes-0.5.85.md`](.omc/release-notes-0.5.85.md). Recent chain: **0.5.84** — Causal Graph P0 fix batch: single mig **281** `causal_graph_maintenance_state`; closes all 6 P0 bugs (`FLAG_ROUTE_SUFFIX` row for `causal_graph`, `createCausalSuggestion` probes `FindCausalEdgeBetween` any-status, `TouchCausalNode` callers on every issue/comment/task hot path via `Recorder.RefreshForIssue` + bulk `RefreshCausalNodesForIssue` CTE, maintenance ticker DB-anchored, edge rendering split by `status` via `resolveEdgeTone(type, status)`, ring layout pre-computes `ringLengths`); two new standing laws (Active Contracts #9: FLAG_ROUTE_SUFFIX + never-nag + TouchCausalNode callsite). **0.5.83** — WL3 issue causal graph + hidden agent team: migs **276-280**; flag key `causal_graph` VERBATIM; trust ladder A>B>C>D; `TestAllSourcesContainsCausalGraph` pin; `multica-causal-graph-curator` skill; nightly evolver + maintenance ticker; client `IssueCausalGraphIcon` → `/experimental/causal-graph`. **0.5.82** — WL2 TimesFM 2.5 forecasting lab: flag key `timesfm` VERBATIM; route split `/experimental/timesfm-lab` view vs bare `/experimental/timesfm` REST proxy; engine-down honesty (503, no synthetic envelope; `provenance` never stripped); ICP-1 records-only; mig 275 taught the lock-CHECK widen. **0.5.81** — WL1 labs task-issue-first UX: `<IssueBreadcrumb/>`; ICP-3 deep-link round trip (wall-clock 2s poll budget). **Gate-integrity**: final gates must be uncached, sequential `go test ./...` with `DATABASE_URL` exported (0.5.83: concurrent CPU load flakes wall-clock timing tests — serial rerun 47 packages, 0 FAIL; 0.5.84 ship gate 5 packages 0 FAIL; 0.5.85 ship gate 2 packages 0 FAIL after one `CI=true pnpm install --frozen-lockfile` recovery). Packaged-app ops facts: the app bundles Postgres.app (`~/Library/Application Support/Multica/pg`+`pgdata`, owns :5432 with its process tree); the cold-start verifier reads expected version from the PRIMARY checkout (correct post-merge-back, use `EXPECTED_VER=` pre-merge); OrbStack's `pocketbase` container maps host :8090 (restart policy set to `no` 2026-08-27). **0.5.80's nav law stands**: every navigation path into `/experimental/*` MUST arm the workspace-singleton release-suppression token. **Next cycle (0.5.87+)**: DB-backed handler integration tests for `ClaimTaskByRuntime` with the subgraph path active; P2 Tier C Pythia closure, historian/verifier automation, mention-flow doc notes, evolver window bound; mythos async-engine unification (swarm orchestrator port) as fast-follow. Open decision gate unchanged: upstream inbox architecture (MUL-6632) — until decided, inbox-family upstream commits stay SKIP-DIVERGENCE. Historical release notes archived at `.omc/_legacy/release-notes-archive.md` (0.5.12→0.5.41); 0.5.43–0.5.85 notes at `.omc/release-notes-0.5.{43..85}.md`; load-bearing contracts live in root [`CLAUDE.md`](CLAUDE.md) Known Stability Surfaces / Active Contracts / Fork-Applicable HIGH Vuln Contracts.

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
