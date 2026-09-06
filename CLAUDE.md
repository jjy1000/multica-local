# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

> **TL;DR**: **localized single-user fork** of Multica (no telemetry, no OAuth, no cloud, username-only login — see **Localized Fork** below). Memory: `~/.claude/projects/-Users-jiangjianyan-jjy-multica-exploration-dev/memory/`. Backup: `.omc/backups/<date>/<release>-ship/` (auto per `ship-mac`). Single-command ship: `bash scripts/ship-mac.sh --yes`. Sub-domain guides table below; cross-cutting product/ship/desktop rules in this file.
>
> **Current release: 0.5.97** (2026-09-04, shipped `909402eb3`; `/Applications/Multica.app` = 0.5.97, cold-start ~6s; **lab result rendering overhaul** — TS-only, no migrations / no Go changes; shared `LabTaskResultView` + `interactive-chart-envelope` + `lab-attachment-sanitize`; 9 new i18n keys; gates typecheck/lint/views vitest green). Notes: [`.omc/release-notes-0.5.97.md`](.omc/release-notes-0.5.97.md), ship log: [`.omc/0.5.97-ship-2026-09-04.md`](.omc/0.5.97-ship-2026-09-04.md). Prior: 0.5.96 (upstream sync + audit-fix batch; **CI runs on the fork for the first time**), 0.5.95 (upstream value-port batch), 0.5.94 (tech-debt audit fix batch incl. MUL-6749). Full per-release history: `.omc/release-notes-<ver>.md` + `.omc/<ver>-ship-<date>.md`.
>
> **Gate-integrity reset (2026-09-01, no version bump).** `pnpm typecheck`, `pnpm lint`, and `pnpm test` are GREEN at HEAD, so **any red test from here is a regression**, not background noise. The "N failures = pre-existing baseline" convention is RETIRED. If a suite must be parked, skip it explicitly with a comment naming the gate — never by leaving it red. Current parked: 33 (`inbox-page.test.tsx` + 2 marker tests in `description-preview.test.ts`; MUL-6632 decision gate still open).

## Sub-domain Guides (read the nearby file when working in a sub-domain)

Each large sub-domain has a co-located `CLAUDE.md`. When your work is scoped to one, that nearby file is sufficient. This root file is navigation + cross-cutting rules.

| Working in | Read first |
| --- | --- |
| `server/` (Go backend, handlers, migrations, experimental catalog) | [`server/CLAUDE.md`](server/CLAUDE.md) |
| `packages/` (`core` / `ui` / `views` shared FE) | [`packages/CLAUDE.md`](packages/CLAUDE.md) |
| `packages/views/` (shared business pages/components) | [`packages/views/CLAUDE.md`](packages/views/CLAUDE.md) |
| `apps/desktop/` (Electron app, packaging, self-contained backend) | [`apps/desktop/CLAUDE.md`](apps/desktop/CLAUDE.md) |
| `apps/mobile/` (Expo / React Native) | [`apps/mobile/CLAUDE.md`](apps/mobile/CLAUDE.md) |
| `apps/web/` (Next.js App Router, platform wiring) | [`apps/web/CLAUDE.md`](apps/web/CLAUDE.md) |

Each guide directory also carries an auto-synced `AGENTS.md` mirror. Co-located `CLAUDE.md` is source of truth; parity enforced by `scripts/check-agents-docs-sync.mjs`.

## Commands

> **Single-command release**: `bash scripts/ship-mac.sh --yes` runs snapshot → bundle-cli → build → package → nested-binary signing → cold-start verify. `--build-only` stops before `/Applications` overwrite. Full ship chain is in the script; this file is not a duplicate.

```bash
# Single Go test (from server/)
cd server && go test -run TestName -count1-1 -timeout 60s ./internal/handler/

# Single Vitest test (from repo root)
pnpm test path/to/file.test.ts

# Docs-sync check (run after any root CLAUDE.md edit, before commit)
node scripts/check-agents-docs-sync.mjs
```

### Before packaging (fork-specific, NOT in CONTRIBUTING.md)

```bash
bash ~/.multica/scripts/pre-update-snapshot.sh   # 1. mandatory; exit 1 blocks packaging
cd server && go run ./cmd/migrate up             # 2. apply pending migrations BEFORE bundle-cli
```

### Version source (fork-specific)

`git describe --tags` returns `pre-update-...-g<sha>` (existing tags are snapshot markers, not release tags). `bundle-cli.mjs` falls back to `apps/desktop/package.json` → `version`. **`apps/desktop/package.json` is the canonical version source. Bump only that file.**

## Localized Fork

This is a **fully localized, single-user fork** of Multica. Primary target: macOS desktop app.

- **No telemetry**: `analytics.NewFromEnv()` always returns `NoopClient{}`. Frontend analytics functions are no-ops. `server/internal/analytics/posthog.go` deleted.
- **No auto-update**: CLI update command stubbed. Daemon does not start `autoUpdateLoop`. Desktop `updater.ts` is no-op. `electron-builder.yml` has no `publish:` block. `electron-updater` dependency removed.
- **No Google OAuth / email verification**: `SendCode`, `VerifyCode`, `GoogleLogin` all 410 Gone. Only `UsernameLogin` (`POST /auth/login {"name":"..."}`) works.
- **No cloud features**: billing, cloud runtime, CloudFront, contact sales, cloud PAT, invitations, workspace members — all deleted.
- **No external support UI**: HelpLauncher, JoinDiscordCard, Discord icon, FeedbackModal — all deleted.

Do **not** re-add any of the above.

- **Username-only login upserts a new user on every login.** `POST /auth/login` creates a new user row when the name is unseen. Workspace membership is bound to the creator user_id; any username change across restarts yields a fresh user with zero workspaces. Do NOT "fix" by auto-binding (let typo grant ownership). See `.omc/incidents/2026-06-27-username-only-login-loses-workspaces.md`.

- **i18next selector block-body incident (2026-07-14).** Block-body selectors `t(($) => { const v = $.foo; return v; })` return a plain string instead of the proxy; i18next's `keysFromSelector` reads `[PATH_KEY]` off the return, `path` becomes `undefined`, and `if (path.length > 1 && nsSeparator)` throws `TypeError`. The error escapes React's render pass, unmounts the surrounding tree, and (because the offending selector was inside `AppSidebar`) blanked the entire desktop window. Fix: selectors must be arrow expressions, e.g. `t(($) => $.sidebar[item.labelKey])`. Three layers of protection: (1) comment block in `packages/views/i18n/use-t.ts`; (2) `no-restricted-syntax` rule in `packages/views/eslint.config.mjs`; (3) `AppSidebar` wrapped in `error-boundary` with "Sidebar failed to render / Retry" fallback.

## Retired Features (do NOT re-add)

- **`constitution_agent` lab** (retired 0.3.57, migration 165). Removed the `宪法智能体` agent, 3 autopilots, 4 visibility rows, bundled skill. If upstream re-adds, do NOT cherry-pick back.
- **`agent_self_optimization` + `agent_creation_studio` experiment flags** (promoted 0.5.5/0.5.5.1; catalog entries deleted 0.5.6). Runtimes live as product-level resources — self-opt via `service/agent_self_optimization/*` controlled by the weekly `[自进化]` autopilot row's `status`; the studio as an issue-bound lab (`lab_source='agent_creation_studio'`, leader `agent_creation_expert`) entered via LabPicker. Do NOT re-add catalog entries, Labs-tab toggles, or `flagEnabled(...)` gates for these keys — a re-added gate on a removed key resolves `false` forever and silently kills the feature. Rationale: `.omc/0.5.6-ship-2026-08-02.md`.
- **Username-only login user-creation side effects** (see Localized Fork above).
- **Inline lab workspace panel on issue detail** (removed 0.3.38). `LabWorkspacePanel`, `pickLabInlineView`, `IssueDetailProps.renderLabInline`, `*Inline` view wrappers (`ClaudeLabInline`/`PythiaInline`/`MythosInline`/`LLMWikiBridgeInline`) all gone. Lab surfaces reachable ONLY via `/experimental/<suffix>` from sidebar or `<IssueLabsSection>` "open panel" link.

## Conventions

The source of truth for code naming, i18n glossary, and Chinese product voice is:

- `apps/docs/content/docs/developers/conventions.mdx`
- `apps/docs/content/docs/developers/conventions.zh.mdx`

Read it before editing translations in `packages/views/locales/`, naming routes/packages/files/DB columns/types, or writing Chinese UI/docs copy.

## Project Shape

Multica is an AI-native task management platform for small teams, with agents as first-class assignees that can own issues, comment, and change status.

- `server/` — Go backend (Chi router, sqlc, gorilla/websocket). Three entrypoints: `cmd/server` (HTTP + WS), `cmd/multica` (CLI), `cmd/migrate` (forward/back SQL).
- `apps/web/` — Next.js App Router. `apps/desktop/` — Electron desktop (primary). `apps/mobile/` — Expo React Native. `apps/docs/` — Nextra docs.
- `packages/core/` — headless business logic, API client, React Query hooks, Zustand stores.
- `packages/ui/` — atomic UI components only. `packages/views/` — shared business pages/components for web+desktop.
- Shared packages export raw `.ts`/`.tsx`, compiled by consumers. Dependency direction: `views -> core + ui`; `core` and `ui` must stay independent.

### Data Flow (60-second mental model)

```
  Renderer (desktop/web)  ─HTTP+WS─▶  server/internal/handler → service/* → sqlc → Postgres+pgvector  ─WS push─▶  Renderer
                                                                                                            ▲
  Local Daemon (server/cmd/multica + apps/desktop daemon-manager.ts) ─spawns─▶  Claude Code / Codex / copilot / openclaw / ...
```

Lifecycle of an assigned task: **PATCH `issue.assignee_*`** → server `assignDefaultLabAgent` (if lab-bound) → daemon claim on `agent_task_queue` → daemon `LoadAgentSkillsForClaim` injects builtin + workspace skills → subprocess spawns agent CLI → progress over WS → renderer patches Query cache via `["agent-task-snapshot"]` invalidation. Labs add a parallel path via `issue.lab_source`.

## State Rules

Server state and client state stay separate.

- **TanStack Query** owns server state: issues, users, workspaces, inbox, agents, members, anything fetched from API.
- **Zustand** owns client state: selected workspace, filters, drafts, modals, tab layout, navigation history.
- Shared Zustand stores live in `packages/core/`, never in `packages/views/` or apps.
- React Context is for platform plumbing only (`WorkspaceIdProvider`, `NavigationProvider`).
- Only auth/workspace stores may call `api.*` directly. Other server interaction belongs in queries/mutations.
- Workspace-scoped query keys must include `wsId`.
- Mutations are optimistic by default: patch locally, send request, roll back on failure, invalidate on settle.
- WebSocket events invalidate or patch Query cache; never write directly to Zustand stores.
- Persist durable preferences/drafts/layout. Do NOT persist server data or ephemeral UI state.
- Zustand selectors must return stable references.
- Hooks that need workspace context should accept `wsId`; do not call `useWorkspaceId()` internally unless guaranteed under the provider.

## Package Boundaries

- `packages/core/`: no `react-dom`, `localStorage` (use `StorageAdapter`), `process.env`, or UI libraries.
- `packages/ui/`: no `@multica/core` imports and no business logic.
- `packages/views/`: no `next/*`, `react-router-dom`, no stores. Use `NavigationAdapter`, `useNavigation()`, `<AppLink>`.
- `apps/web/platform/`: only place for Next.js navigation/platform APIs.
- `apps/desktop/src/renderer/src/platform/`: only place for `react-router-dom` wiring.
- Every workspace under `apps/` and `packages/` declares directly imported external packages in its own `package.json`.
- Shared deps use `catalog:` from `pnpm-workspace.yaml`; `apps/mobile/` pins Expo/React Native directly.

Full state model + testing rules: [`packages/CLAUDE.md`](packages/CLAUDE.md).

## Sharing Rules

1. Next.js, Electron, router APIs stay in the app/platform layer.
2. Headless logic → `packages/core/`.
3. Shared UI/business views → `packages/views/`.
4. Shared primitives → `packages/ui/`.

Mobile is independent: imports only types + pure functions from `@multica/core` (`import type`), owns its UI/state/hooks/providers/i18n/React/build/release.

## Toolchain Baseline (do NOT bump casually)

| Tool | Version | Source |
| --- | --- | --- |
| Node | 22.x | CI workflows |
| pnpm | 10.28.2 | `package.json` (`packageManager`) |
| Go | 1.26.1 | CI workflows |
| TypeScript | ^5.9.3 | `pnpm-workspace.yaml` catalog |
| React | 19.2.3 | `pnpm-workspace.yaml` catalog |
| PostgreSQL | 17 with pgvector | `pgvector/pgvector:pg17` (CI service) |

`apps/mobile/` pins Expo/React Native directly; excluded from root turbo pipelines.

## Authentication

Username-only. `POST /auth/login` accepts `{"name":"alice"}` — first call creates the user (email = `name + "@local"`), returns a JWT. No email verification, no Google OAuth, no password. Login pages: `apps/desktop/src/renderer/src/pages/login.tsx` + `apps/web/app/(auth)/login/page.tsx`. Both call `useAuthStore.getState().loginWithUsername(name)`.

## API Compatibility

Frontend code must survive backend response drift, especially in installed desktop builds. zod schemas + `parseWithFallback` for every endpoint consumed by UI logic, explicit `=== true` boolean checks, `default` branch on server-driven enums. Full contract: [`packages/CLAUDE.md`](packages/CLAUDE.md) §API compatibility.

## Backend UUID Rules

In `server/internal/handler/`, know where a UUID came from before using it in write queries: path params via loaders (`loadIssueForUser`, `loadSkillForUser`, `loadAgentForUser`, `requireDaemonRuntimeAccess`), pure UUID inputs via `parseUUIDOrBadRequest`, trusted round-trips via `parseUUID`, outside handlers via `util.ParseUUID`. Full table: `server/CLAUDE.md` §UUID rules.

## Coding Rules

- TypeScript strict mode on; keep types explicit.
- Go follows standard conventions: `gofmt`, `go vet`, checked errors.
- Code comments must be English.
- Prefer existing patterns/components over new parallel abstractions.
- Avoid broad refactors unless required by the task.
- For internal, non-boundary code: no compatibility layers, fallback paths, dual writes, legacy adapters, temporary shims unless explicitly requested.
- If a flow or API is being replaced and the product is not live, prefer removing the old path instead of preserving both.
- New global pre-workspace routes: single word (`/login`, `/inbox`) or `/{noun}/{verb}` (`/workspaces/new`). No hyphenated root routes.
- Reserved slugs: `server/internal/handler/reserved_slugs.json`. Edit it, run `pnpm generate:reserved-slugs`, commit the generated `packages/core/paths/reserved-slugs.ts`.
- When changing CLI commands/flags, API fields, or product behavior documented by built-in skills under `server/internal/service/builtin_skills/*`, update the relevant `SKILL.md` and `references/*-source-map.md` in the same PR.

## Web/Desktop Features

When adding a shared page/feature for web + desktop:

1. Page/component in `packages/views/<domain>/`.
2. Platform wiring in both `apps/web/app/` and desktop router (unless desktop flow is a transition overlay).
3. Use `useNavigation().push()` or `<AppLink>` in shared code.
4. Use shared guards/providers (`DashboardGuard` from `packages/views/layout/`).
5. Platform-only UI in app or via props/slots.
6. Hooks needing workspace context accept `wsId`.

CSS shared from `packages/ui/styles/` — use semantic tokens (`bg-background`, `text-muted-foreground`), never hardcoded Tailwind colors.

When reviewing/auditing UI code (a11y, UX, visual design), invoke `web-design-guidelines` skill.

## Mobile Rules

Read `apps/mobile/CLAUDE.md` before touching. Mandatory pre-flight, import limits, parity rules, tech stack, UI rules, data helpers, realtime, release flow.

- Mobile shares only `@multica/core` types and pure functions.
- Match web/desktop semantics: counts, permissions, enums/transitions, identity.
- May differ in UI/interaction when phone context requires.

## UI Rules

- Prefer shadcn/Base UI components. Add with `pnpm ui:add <component>` from repo root.
- Design tokens + semantic classes; no hardcoded colors.
- No extra local state unless design requires.
- Handle overflow, long text, scrolling, alignment, spacing deliberately.
- If a component is identical between web and desktop, it belongs in a shared package.

## Desktop Rules

> Full desktop lifecycle, routing, packaging, data-safety: [`apps/desktop/CLAUDE.md`](apps/desktop/CLAUDE.md).

**P0 data-safety contract** (2026-07-02 incident destroyed 69 user tables when a Docker pgdata was migrated by the bundled `migrate` binary; do NOT remove either layer, do NOT make `backend` optional without re-reading `memory/multica-0.3.0-standalone-2026-07-02.md`):

> `runMigrate(profile, env, backend?)` REFUSES `backend === "external"`.
> The call site in `ensureServerUp` ALSO skips for defense in depth.
> Any new caller that invokes `runMigrate` must pass `backend` explicitly.

**P1.8 sentinel atomicity**: `runMigrationFlow` creates `~/.multica/.pg-migrating-v1` with `O_EXCL` BEFORE destructive `pg_restore`, renames to `~/.multica/.pg-migrated-v1` on success. SIGKILL between restore-success and rename leaves the in-progress file; next launch refuses auto-retry. **Do NOT write the final sentinel before the operation succeeds.**

**Desktop runtime config**: `~/.multica/desktop.json`:
```json
{"apiUrl": "http://localhost:8090", "wsUrl": "ws://localhost:8090/ws", "appUrl": "http://localhost:3000"}
```
Per-profile server env (`.env`) at `~/.multica/profiles/<name>/.env`.

**BrowserWindow off-screen guard.** Electron 39 on macOS restores stale bounds from system window-state cache; if bounds fall outside every connected display's workArea the window is invisible. `apps/desktop/src/main/index.ts` clamps bounds in `ensureWindowOnscreen()` — called synchronously after `new BrowserWindow(...)`, on `ready-to-show`, and on every `move`/`resize`/`display-removed`.

**Pythia engine source-of-truth is `apps/desktop/vendor/pythia-src/engine/`, NOT `apps/desktop/resources/pythia/engine/`.** `bundle-cli` (`apps/desktop/scripts/bundle-cli.mjs:281-283`) wipes `resources/pythia/` and re-copies from `vendor/pythia-src/` on every run. Edits directly under `resources/pythia/engine/*.py` are silently overwritten at bundle time. Edit the vendor copy, then `pnpm --filter @multica/desktop bundle-cli`. (Bit 0.3.21 Pythia i18n pass — three rounds of Edit to `resources/pythia/engine/*.py` all looked successful until re-bundle reverted every change.)

## Data Safety & Version Upgrades

DMG install **only replaces `/Applications/Multica.app`**. All user data lives in independent paths:

| Data | Path |
|------|------|
| PostgreSQL | Docker volume `multica_pgdata` |
| Config / tokens | `~/.multica/profiles/<name>/config.json` |
| Server env | `~/.multica/profiles/<name>/.env` |
| Workspace files | `~/multica_workspaces_<profile>/` |
| KB vaults | `~/Documents/` |
| Desktop config | `~/.multica/desktop.json` |

Rules:

- **Migrations are forward-only**: never drop a table or column. Schema changes must be additive.
- **Config fields are append-only**: don't delete/rename existing keys in `config.json` or `.env`. New fields have defaults.
- **Pre-update snapshot** mandatory before DMG rebuild (script `~/.multica/scripts/pre-update-snapshot.sh`).
- **Verify data integrity** after upgrade: `docker exec multica-postgres-1 psql -U multica -d multica -c "SELECT COUNT(*) FROM workspace"` should return expected count.

## Testing

AAA pattern. Tests follow the code:

| What | Location |
| --- | --- |
| Shared business logic, stores, queries, hooks | `packages/core/*.test.ts` |
| Shared UI components, pages, forms, modals | `packages/views/*.test.tsx` |
| Platform wiring (cookies, redirects, search params) | `apps/web/*.test.tsx` or `apps/desktop/` |
| E2E flows | `e2e/*.spec.ts` |
| Backend | `server/` Go tests |

Rules:

- Never test shared component behavior in an app test file.
- `packages/views/` tests must not mock `next/*` or `react-router-dom`.
- Mock `@multica/core` stores with Zustand callable-store shape (`selectorFn` + `getState`).
- Mock `@multica/core/api` for API calls.
- E2E uses `TestApiClient` for setup/teardown.
- Prefer writing the failing test in the correct package before implementation when change is behavioral.

## Verification

```bash
pnpm typecheck                  # full turbo pipeline
pnpm lint
pnpm test
make check-fast                 # affected TS typecheck + unit + lint; no DB/Go/E2E
make test
cd server && go test -count=1 -timeout 600s ./internal/... ./pkg/agent/...   # mandatory after any cherry-pick or Go source edit
pnpm exec playwright test
make check
```

Do NOT claim verification passed unless you ran it. If you skip (docs-only or asked not to), say so.

**Ship gate (mandatory)** — before any release commit, BOTH must be green:

- `pnpm typecheck` (full turbo pipeline) — catches TS breakage
- `cd server && go test -count=1 ./internal/... ./pkg/agent/...` — catches Go breakage

0.5.15 lesson: only `pnpm typecheck` was run; broken `#6199` cherry-pick broke `TestBuildMetaSkillContentIssueBodyFormatting` 4/4 subtests in the legacy verbose brief path (default in production) — caught only by post-ship code-reviewer, not the gate. Future batches must run both checks.

**Silent-skip trap (0.5.79 lesson)**: with `DATABASE_URL` unset, DB-backed tests SKIP silently — suite "passes" in ~15s having run nothing. Export first: `export $(grep -E '^DATABASE_URL=' .env | xargs)` and confirm the runner printed its DB-set marker before trusting a suspiciously fast green run. `scripts/check.sh` hard-fails on that precondition.

## Commits and Releases

- Atomic commits with conventional prefixes: `feat(scope)`, `fix(scope)`, `refactor(scope)`, `docs`, `test(scope)`, `chore(scope)`.
- Tags `pre-update-*` are snapshot markers, not release tags; `git describe` never yields a release version. For local releases, bump `apps/desktop/package.json` `version` and document in `.omc/release-notes-<ver>.md`.
- **DMG creation hangs on create-dmg 1.2.3** (`electron-builder --mac` produces no `.dmg` on this fork). Use `pnpm exec electron-builder --mac --dir` to produce `dist/mac-arm64/Multica.app` directly and ship that. Every 0.3.x release ships via `--dir`.
- **`pnpm build` does NOT run electron-builder** — asar replacement is silent if skipped. Verify with `grep -c rawRequest apps/desktop/dist/mac-arm64/Multica.app/Contents/Resources/app.asar` after each build.
- Bump patch by default unless user specifies a version.

### Cherry-pick verification (0.5.36 lesson)

After ANY agent-assisted cherry-pick, compare each resolved file's diff size against upstream's per-file stat:

```bash
git show <upstream-commit> --stat | head -60
git diff --stat HEAD
```

Files whose diff is 5-50x larger than upstream's = wholesale adoption (`git checkout --theirs` replaced fork's file with upstream's ENTIRE current file). Fix: revert that file to the pre-cherry-pick commit (`git checkout <base> -- <file>` — NOT `HEAD`), re-apply only semantic change.

Pre-existing test bisect: prove a failing test predates a port with `git worktree add /tmp/wt-check <base-commit>` + symlinked `node_modules`.

## Labs Platform

Full architecture spec lives in `server/CLAUDE.md` and `.omc/labs-runtime-lifecycle-map.md` (refreshed 2026-08-21 for 0.5.46 canonical state). Core invariants:

- **Flag = off MUST completely bypass experimental code.** No new imports, no module init in legacy path. View toggles use `{flagEnabled ? <NewCode /> : null}`.
- **Users cannot create flags.** Catalog is developer-only at `server/internal/experimental/catalog.go`. Labs UI only renders what server returns.
- **Labs tab is the only entry point.** No nav bar, no CLI shortcuts, no `api.*` callers outside `labs-tab.tsx`.
- **Not a plugin system.** Simple toggle pattern, not dynamic load.
- **No reserved workspace for new labs.** Lab resources isolated by `experimental_resource_lock` + visibility table.
- **Manifest → catalog → registry → IPC dispatcher → proxy mount → sidebar.** Single chain — no per-flag hardcodes.
- **RuntimeKind enum**: `none` / `inline` / `subprocess` / `headless`. `ManagerFactory` dispatches per flag key.
- **User plugin layer** (`user_*` keys, 0.3.60): merged at boot via `RegisterUserPlugins()` + `MergeUserPlugins()`. Built-in flags always win on key collision.

When adding a new experiment: 1) manifest in `apps/desktop/resources/experiments/<flagKey>/manifest.json`; 2) append to `Catalog` in `server/internal/experimental/catalog.go`; 3) migration if needed + visibility seeds; 4) install handler if `installable: true`; 5) route + view if dedicated surface (network calls via `api.rawRequest`, never bare `fetch`); 6) builtin skill if agents invoke; 7) `bundle-cli` + `electron-vite build` + `electron-builder --dir`.

**Lab ↔ Assignee Mutex (narrowed 2026-07-28 audit):** applies to `mythos_swarm` (sole mode) ONLY. Every other lab (built-in or `user_*`): manual assignee is legal; on `lab_source` flip without explicit assignee, server auto-rewrites to lab leader (Active Contract #2).

**Network calls from Labs tabs MUST go through `api.rawRequest`** (`packages/core/api/client.ts`). Never `fetch("/api/experimental/...")` — fails in desktop (renderer origin `file://`, not bundled backend `:8090`; no Bearer = 401). Converted: claude-lab, forecast-stream, mythos, llm-wiki-bridge, experimental-artifact, pythia-report. Exception: `use-pythia-sse.ts` (loopback engine URL, no baseUrl prefix).

**Lab auto-dispatch opt-out** (per-catalog): `experimental.Flag.AutoDispatch *bool` — nil/true = unchanged; false = leader-rewrite still applies (so IssueLabsSection + workbench header show the right agent) but enqueue gate short-circuits; user must explicitly trigger via lab workbench "Run research" button. Today: `pythia_oracle` + `timesfm` (records-only) and `causal_graph` (auxiliary) are opt-out. `multica lab delegate` fails fast naming AutoDispatch=false + frozen labs.

## Active Contracts

Load-bearing cross-cutting patterns. Honour these when adding/refactoring lab-class surfaces.

1. **5s polling fallback for lab-class query keys without WS push.** Pattern: `refetchInterval: (query) => isLive(query.state.data) ? 5_000 : idleMs`. Three idle modes: WS covers + no idle (autopilot), no WS + tab-cross (30_000), no WS + very low frequency (60_000). Canonical: `agentTaskSnapshotOptions` (`packages/core/agents/queries.ts:36`).

2. **Lab leader rewrite on `lab_source` flip (4-case contract).** `server/internal/handler/issue.go::shouldRewriteAssigneeForLabLeader` + `assignDefaultLabAgentOnUpdate`:
   - lab_source untouched → noop
   - lab_source → no-leader lab (mythos_swarm) → noop
   - lab_source → leader lab, already leader → noop
   - lab_source → leader lab, missing/non-agent/different agent → **rewrite to leader**

   `BatchUpdateIssues` honours same contract (post-state compute = previous row + batch fields). Tests: `TestUpdateIssueLabSource*` in `server/internal/handler/issue_lab_dispatch_test.go`.

3. **chi route order — literal slug BEFORE `{param}`.** When adding `/sessions/<key>` that competes with existing `{param}` route, register literal first. Symptom if violated: handler returns 400 "sessionID is not a UUID" for the literal key. Comment the order rationale inline.

4. **`lab_managed` DTO marker.** When `experimental_resource_visibility` row exists for a flag, ALL agents/squads owned by that flag are hidden from regular selection surfaces (assignee picker, project lead, quick-create, squad member, filter chips, subscribers). Server stamps `lab_managed?: boolean` on `Agent`/`Squad` DTOs; renderer gates on `lab_managed: false`. NOT a column — derived from visibility rows.

5. **Swarm Topology contracts (0.5.21).** When `issue.lab_source='swarm_topology'`: orchestrator authors N≤6 role-agents + M skills + 1 coordinator squad on bootstrap; 5-phase machine (`research`/`design`/`implement`/`review`/`done`); phase advance gated by `count(completed) == total`; any role failed fails run fast; teardown via 6h `swarm_gc` tick. Lock to coordinator (no manual override, `lab_mode='enhancer'` rejected); human interrupt via comment `@<swarm_coordinator>` (zero new IPC).

6. **Lab auto-dispatch opt-out (per-catalog).** See Labs Platform above.

7. **Workflow file-overlap graph (batch parallelization).** When 2+ PRs in a batch touch same file (typical: 4 locale files), naive parallel agents race-edit. Phase 1 (parallel) = PRs with pairwise disjoint files; Phase 2 (parallel, depends on 1) = disjoint from each other and from Phase 1; Phase 3 (sequential, depends on 1) = PRs sharing files with Phase 1; Phase 4 (verifier) = `pnpm typecheck` + `go test` + 4-locale parity + per-PR regression pin count. Each executor prompt MUST include "VERIFY FIRST: ..." before edit.

8. **Causal-graph trust ladder + proposer laws.** Tier A (native task hooks) > B (Semantica decision mirrors) > C (Pythia closure) > D (LLM proposals). Tier D ALWAYS lands `status='suggested'` at confidence ≤ 0.5, invisible to subgraph/path until human confirms. **Reject is a tombstone, never delete** (mig 280): proposers re-derive, so hard-deleted rejection re-proposes nightly. Probe `FindCausalEdgeBetween` (ANY status) BEFORE INSERT. New `Source` constant ⇒ add to `experimental.AllSources` same commit (Claim validates the slice, missing entry fails every install).

9. **Causal-graph P0 audit standing laws + callsite contracts (0.5.84).**
   - **`FLAG_ROUTE_SUFFIX` row required for every new lab flag key** in `packages/views/issues/components/issue-labs-section.tsx:30-55`. Missing row silently no-ops. Pinned by full-mapping assertion in `issue-labs-section.test.tsx`.
   - **Every proposer endpoint (REST + internal service) needs the server-side never-nag probe** before INSERT.
   - **TouchCausalNode hot-path refresh.** Every code path mutating issue/comment/task MUST `Recorder.RefreshForIssue(ctx, issueID)` post-success. Legitimate hook points: `UpdateIssue` (L3228), `CreateComment` (L1351), `enqueueIssueTask`/`enqueueMentionTask` (after `RecordTaskAction`), `CompleteTask` (after `RecordTaskOutcome`). Wrapper is fail-soft + flag-gated.

## Known Stability Surfaces

Real failure modes that took non-trivial debugging. NOT obvious from reading the code.

- **Server vs daemon write to DIFFERENT profile dirs.** Desktop main process runs two profiles for one app: daemon = `desktop-<host>` (`desktop-localhost-8090`), server = `<host>` (`localhost-8090`). Both under `~/.multica/profiles/`. **Server log is at `~/.multica/profiles/<profile>/server.log`, NOT `~/.multica/server.log`.** When diagnosing ship-post behavior, check mtime of every `profiles/*/server.log` and read the newest.

- **Workspace singleton lifecycle vs pre-workspace routes (0.5.80).** Navigating within a tab from workspace route into `/experimental/*` unmounts `WorkspaceRouteLayout` without a successor — its cleanup releases the workspace singleton, unmounting AppSidebar + WindowToolbar + ModalRegistry + SearchCommand (fullscreen takeover). Guard: every navigation path into `/experimental/*` MUST call `suppressNextWorkspaceRelease()` BEFORE dispatching. Arm sites in both navigation adapters, `tryRouteToPinnedNewTab`, and `multica:navigate` handler. **If you add a new way to navigate into `/experimental/*`, arm there too.**

- **Bundled Postgres does not auto-start for headless ship runs.** `ship-mac.sh` step 2/7 (`go run ./cmd/migrate up`) dials `.env` DATABASE_URL directly. ECONNREFUSED on 5432 → start PG manually: `pg_ctl -D ~/Library/Application\ Support/Multica/pgdata -l ~/Library/Application\ Support/Multica/pg/17.4/pg.log start`.

- **Daemon does not auto-start on GUI relaunch when already logged in.** Renderer `useEffect` `[user]`-dep does not "change" on existing-user session → IPC never fires → `agent_task_queue` rows pile up as `queued`. 0.3.33 fix: `tryAutoStartFromMain()` at tail of `bootstrapCli()` + `maybeRecoverDaemon()` in `daemon-manager.ts:1114/1035` + App.tsx `[user]`-dep guard + `setTargetApiUrl` ordering. Defense-in-depth: `~/.multica/scripts/multica-daemon-watchdog.sh` (60s poll). Re-read memory `0.2.97-daemon-autostart-regression.md` before editing App.tsx/daemon-manager.ts/watchdog.

- **Spawning multica daemon from Bash harness kills daemon on exit.** macOS bash doesn't support `setsid`; `nohup ... &` is fragile outside interactive shell. Only reliable detach on macOS is zsh's `&!` (or launchd). Use `~/.multica/scripts/multica-spawn-daemon.zsh` for any manual daemon launch.

- **Codesign nested binaries after electron-builder --dir (0.3.62/0.5.18, mandatory).** `electron-builder --dir` only signs the top-level `.app` bundle; macOS 27 Gatekeeper kills `app.asar.unpacked/resources/bin/{multica,server,migrate}` with SIGKILL (`exit 137`). Symptom: GUI Helper processes up but `multica --help` → 137, daemon.log `signal: 'SIGKILL'`, server never binds `:8090`. Fix: `bash scripts/desktop-sign-nested-binaries.sh /Applications/Multica.app` (signs app + 3 nested binaries, asserts `multica --help` exits 0). Wired into ship chain step 5a. Skip → 137 on next launch.

- **`pnpm exec electron-builder --mac --dir` from repo root walks `.claude/worktrees/`.** Sweeps stale exploration worktrees with absolute symlinks (renamed dir, ENOENT). From `apps/desktop/` it's correctly scoped. Also blocks packaging cwd — `cd apps/desktop || exit 1` is mandatory.

- **DMG creation hangs on create-dmg 1.2.3.** `electron-builder --mac` produces no `.dmg` on this fork. Use `--dir` path (see Commits and Releases). Manual asar repack fallback documented in ship chain if `--dir` deadlocks (`app-builder-bin@5.0.0-alpha.12`).

- **Pre-existing flaky test `TestQuickCreateIssueParentTrustBoundary`.** Reads `testRuntimeID` row status='online' to pass gate; other tests flip same shared row to 'offline' with `t.Cleanup` restore — race fires when restore lands AFTER this test reads. Diagnostic: targeted run PASS; full `internal/handler/...` run FAIL. Always baseline-verify before attributing failure to a new batch (`git diff --stat HEAD~N..HEAD -- <test-file>`).

- **`pnpm typecheck` does NOT run Go tests.** Ship gate MUST include `go test -count=1`. 0.5.15 broke `TestBuildMetaSkillContentIssueBodyFormatting` 4/4 subtests in default verbose brief path; only post-ship code-reviewer caught it. Run both checks before declaring ready-to-ship.

- **No red-baseline rule (2026-09-01).** `pnpm typecheck`, `pnpm lint`, `pnpm test` are GREEN at HEAD. Any red test from here is a regression, not background noise. Do NOT reintroduce "N failures = pre-existing baseline" note. If a suite must be parked, skip with comment naming the gate and what still passed — never by leaving it red. Current parked: 33 (MUL-6632 inbox family).

- **`vendor/openscience-bin/openscience` native binary missing — DECLARED inline-only.** `apps/desktop/vendor/openscience-bin/` does not exist; `bundle-cli.mjs` references a binary that has to be drop-shipped externally. Impact limited because `claude_science_lab` is `inline` RuntimeKind → routes through Multica runtime bridge. Standalone subprocess path officially out of scope (bundle-cli logs informational note instead of warning).

## Memory Index (cross-session)

Memory lives in `~/.claude/projects/-Users-jiangjianyan-jjy-multica-exploration-dev/memory/` (re-pointed 2026-09-02 from `multica-main` slug). Full index in `MEMORY.md` (one line per memory, descriptive title). **Read before editing any subsystem with a known-regression surface.** These files are outside this repo (per-user, cross-session context) — `git` lookups at repo root will not find them.

## Domain Reminders

- All queries filter by `workspace_id`; membership gates access; `X-Workspace-ID` selects workspace.
- Issue assignees are polymorphic: `assignee_type` + `assignee_id` reference member or agent.
- **Mythos swarm has 5 agents**: `mythos_prelude` (leader) + `mythos_loop_coder`/`mythos_loop_researcher`/`mythos_loop_analyst` + `mythos_coda`. `mythosAgents` array in `install_mythos.go` and `RosterCard` in `mythos-view.tsx` must stay in sync. Hidden from regular agent/squad pickers via `experimental_resource_visibility` rows seeded by `upsertMythosVisibility` in `install_mythos.go` (idempotent `ON CONFLICT DO NOTHING`).
- **Mythos supervise goroutine lifecycle**: `Service.Run` launches per-run supervise goroutine for enhancer-mode runs. Writes `mythos_run.supervision_state` every 30s tick, self-terminates at 24h max. Daemon bootstrap calls `ResumeSupervision` for every workspace to recover orphaned goroutines. Flag-off cancels all in-flight supervises via `Service.Stop()`.
- **Issue `lab_source`** (nullable TEXT, mig 155) + **`lab_mode`** (nullable TEXT, mig 157, CHECK `'sole'|'enhancer'`). NULL for non-lab issues. `lab_mode` meaningful only for `mythos_swarm`. New lab with per-issue mode semantics must extend CHECK + mutex gate in all three layers (UI/Server/Batch) + tests.
- **Explicit-column-list queries in `queries/issue.sql`**: `ListIssues`, `ListOpenIssues`, `CreateIssue`, `CreateIssueWithOrigin` enumerate columns manually. When adding a new column to `issue`, update ALL of these + Row structs + Scan/args calls. Other queries use `SELECT *` / `RETURNING *`.
- **User plugin flag keys** carry `user_` prefix. `GET /api/experimental-flags` returns user plugins with `is_user_plugin: true`. `user_plugin.slug` / `flag_key` UNIQUE at column level (mig 166) → partial unique indexes `WHERE status != 'deleted'` (mig 168) so soft-deleted slug can be re-created. All `user_plugin.sql` queries already filter `status != 'deleted'`. Slug immutable after creation.
- **Squad-as-subscriber / squad-as-recipient schema (mig 167, 0.3.61)** extended `issue_subscriber.user_type` + `inbox_item.recipient_type` CHECK to allow `'squad'`, relaxed `agent_task_queue_accountable_matches_originator` (only equal-required when both set). Handler layer: `subscriber_listeners.go` skips `*issue.AssigneeType == "squad"` in `issue:created`/`issue:updated` assignee-subscription; `notification_listeners.go::notifyDirect` early-returns on `recipientType == "squad"`. Squads still receive task dispatch via queue path.
- **agent_creation_studio vs plugin-shell-view.tsx — distinct surfaces.** Studio = product-level issue-bound lab (`issue.lab_source='agent_creation_studio'`, leader `agent_creation_expert` authors rows via `multica-creating-agents`); dedicated `/experimental/agent-creation-studio` route + `AgentCreationStudioView` deleted 0.5.4. Plugin shell = route `/experimental/plugin/:pluginSlug` for `user_*` lab plugins with manifest-driven tabs.