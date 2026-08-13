# AGENTS.md

This file provides guidance to Qoder (qoder.com) when working with code in this repository.

> **Single source of truth**: the root [`CLAUDE.md`](CLAUDE.md) is the authoritative rules file; this file is a synced digest of it. When the two disagree, `CLAUDE.md` wins — fix the drift here. **Guarded sections** — toolchain versions, package boundaries, verification commands, and the Critical Constraints tokens (localized fork prohibitions, state management, backend UUID rules, Pythia source-of-truth, experimental network calls, migration/config immutability, i18n selectors) — are enforced by `scripts/check-agents-docs-sync.mjs`, which runs in CI (`docs-sync` job) and in the `githooks/pre-push` hook. **Unguarded sections** (Quick Reference, Architecture, Sub-domain Guides, Testing Strategy, Dependency Management prose) are not machine-checked — verify them against `CLAUDE.md` before relying on them.

> **Current release: 0.5.18 (dev, committed 2026-08-13 — not yet packaged).**
> Lab environment batch (plan `.omc/plans/lab-environment-0.5.18-plan.md` 阶段 0–3, 13 atomic commits). 阶段 0 reliability/security P1: Pythia proxy allowlist +`/agent/events`+`/scorecard/resolve`, IPC `ensure-up` gated on safety-net blacklist, `user_` flag-key prefix deduped via `experimental.UserPluginPrefix`, `lab_managed` stamped on single-fetch `GetAgent`/`GetSquad`. 阶段 1 (G1 core): user-plugin `runtime_kind: subprocess` now executes `manifest.runtime.command`+`args` (argv, no shell) — was 501. 阶段 2 (G1 verify): `multica lab delegate` tests + `multica-lab-builder` SKILL updates + `scripts/lab-plugin-smoke.sh`. 阶段 3 (G2): `code_canvas` real stdlib-only `/render` service + view + i18n. Zero migrations. Next: 阶段 4 (LabOutputPanel + iframe auth proxy spec) + 阶段 5 (6 HIGH vulns).
>
> **0.5.17 (shipped 2026-08-12).**
> Lab usability batch (continuation of 0.5.16 Phase 1 lab P0 fixes). 5 atomic commits land per-flag install button on Labs tab (B1a — pythia + any installable flag now installable from GUI), real stdio verbs + spawn cwd fix + env injection for llm_wiki_bridge subprocess (B1b — `~/Documents/llm wiki` cwd + MULTICA_API_URL/TOKEN 注入 + vault_read/vault_write 真转发 backend), visibility seed regression tests for all 4 install handlers (B1d — claude_science 新加 + 2 new files for mythos/pythia + code_canvas 已有覆盖), web `/experimental/*` 6 stub routes + app-sidebar gate removal (B2c — web 端能发现实验室 + 引导到 desktop download), version bump (`bda25997c`). `pnpm typecheck --force`: 6/6 ok. `go test -count=1 ./internal/... ./pkg/agent/...`: 32 packages ok, 0 fail. Cold-start verified: 2 s, server PID 61258, Info.plist = 0.5.17. Zero migrations. **Lessons**: (1) audit 验证不能跳过 — B1c (zh-Hant) + B2a (Chat/Knowledge) + B2b (DEFAULT_TABS) 都是 audit 误报,先 verify 再动手;每个 agent prompt 要求"先确认 audit 是否成立";(2) packaging 必须从 `apps/desktop/` 跑 — 0.5.16 noted lesson,本 ship 又踩一次(从 repo root 跑撞到 `multica-main` 旧项目名 stale symlink ENOENT,切 `cd apps/desktop` 通过);未来 ship-mac.sh 加 pre-flight cwd 检查;(3) LabOutputPanel / iframe auth proxy 真缺但需 spec,scope discipline 拒绝造组件 — 0.5.18+ 跟 design doc 再做。Next ship (0.5.18 候选): LabOutputPanel / iframe auth proxy / code_canvas Phase 3 OR sec-first 8 项 fork-applicable HIGH vuln。Full ship log at `.omc/0.5.17-ship-2026-08-12.md`.

> **0.5.16 (shipped 2026-08-12).**
> Phase 1 lab P0 fixes ship — 7 P0 blockers closed across 5 lab surfaces (`llm_wiki_bridge` flag-gate + DTO drift, `chat_pin_ui` SQL list-sort, `claude_science_lab` dead-skill + forecast PRNG→LLM, `code_canvas` skill binding, daemon auto-start docs). 11 atomic fix commits + 1 version bump (`ab3a71cd2`) + 1 ship log (`905fd6df9`) = 13 commits landed on top of 0.5.15 baseline. `pnpm typecheck` (full turbo): 6/6 tasks, 0 errors. `go test -count=1 ./internal/... ./pkg/agent/...`: 32 packages ok, 0 fail. Cold-start verified: 2 s launch, server PID up, `multica --help` exit 0 (signed nested binaries), row parity skipped (`psql` client not in PATH, non-blocker). Zero migrations. Phase 2 (P1 UX closure) + Phase 3 (stub replacement: `code_canvas` Monaco + `claude_science_lab` Knowledge tab) scheduled for 0.5.17 / 0.5.18. Full ship log at `.omc/0.5.16-ship-2026-08-12.md`.

> **0.5.15 (shipped 2026-08-10, physically deployed 2026-08-11).**
> Surgical upstream cherry-pick batch — 13 `fix(*)` PRs ported as plain
> `git cherry-pick` drops. Filter pipeline: 1,594 → 829 → 632 → 394 → 270
> → 150 attempts → 14 succeeded, 144 conflicts auto-aborted, 1 follow-up
> revert (`#5980` referenced `itemArgs` helper from `#4790` which the
> fork has not back-ported), 1 follow-up fixup (`da1cc2003` wired
> `writeIssueBodyFormatting` into the fork's legacy verbose brief path
> — cherry-pick of `#6199` only touched the slim path).
> `pnpm typecheck` (full turbo): 6/6 tasks, 0 errors.
> `go test -count=1 ./internal/...` `./pkg/...`: all green after
> `da1cc2003`. Process gap closed: ship gate must include `go test` in
> addition to `pnpm typecheck`. Physical ship verified: cold launch 6s,
> `multica --help` exit 0 (signed nested binaries), row parity confirmed
> (`workspace=1` baseline-stable; `issue=310` / `comment=2138` /
> `agent=105` reflect user activity since the 0.5.13 baseline, not
> schema drift). Zero migrations. Zero product-level behaviour change.
> 0.5.14 (shipped 2026-08-10): Fork-local cleanup batch —
> no upstream cherry-picks this cycle. Repo hygiene only: `.threat-model-state/`,
> `.triage-state/`, `.vuln-scan-state/` added to `.gitignore`; three pre-existing
> `.omc/` planning docs committed as historical reference for 0.5.15+. A systematic
> survey of `v0.4.13..upstream/main` (258 distinct PRs) confirmed zero small,
> zero-conflict Class A candidates remain — every surgical fix is already
> integrated through 0.5.8 → 0.5.13; the remaining missing PRs are full-feature
> blocks (saved views, channel framework, runtime catalog expansion, font
> overhaul, ACP backends) that exceed fork-local cleanup scope. Zero migrations.
> 0.5.13 (shipped 2026-08-09): Upstream integration
> ship: daemon fail-fast re-introduced (#5674, fork-local fusion),
> search cancelled-demotion (#6515), CLI `--compact` (#6546), audit
> closes (custom_args writer + subscriber filter), backup slim via
> `pg_dump -Fc` (24 MB vs ~1.5 GB). Baseline `epic/0.5.12-cherry-pick`
> carries #6194 rollup, #5406 ErrNoRows, #6095 WCAG, #5355 self-heal,
> #6124 open-tab. For the full release history read the root
> `CLAUDE.md` header.

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
