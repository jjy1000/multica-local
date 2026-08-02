# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

> **Current release: 0.5.5 (installed at `/Applications/Multica.app`).** `agent_creation_studio` and `agent_self_optimization` are now **product-level resources**, not opt-in Labs. The `agent_creation_expert` + `智能体优化专家` leader agents are boot-provisioned into every workspace by `boot_provision_product_labs.go` (no Labs toggle, no `multica experimental install` step, no `experimental.Claim` row, no `experimental_resource_visibility` hide). Users reach the leaders through the AssigneePicker directly. The LabPicker entry list drops these two keys entirely (HIDDEN_LAB_KEYS defense-in-depth, with the catalog `AlwaysShowInLabPicker` flipped off so a future catalog drift never re-surfaces them). Catalog flag values stay as `true` (DefaultVal) for legacy `issue.lab_source='agent_creation_studio'` lookups; full catalog removal is deferred to 0.5.6. Self-opt's two cron autopilots (every-3-workdays bulk + daily 00:00 SkillOpt-Multica) and its trust/agent_opt_edit runtime are unchanged — 0.3.45.1 already wires the service at boot regardless of the flag. Previous 0.5.4 fixed the LabPicker click-through so 智能体创建 on a single click dispatches via 0.3.46 P0#4. 0.5.3 generalized self-optimization from agents to four subjects (agent/skill/squad/autopilot, migrations 232-233: polymorphic `agent_opt_edit.target_type`/`target_id`/`subject_scope` + 0.5.2 `updated_at` drift fix). **Main working directory was renamed `multica-main` → `multica-exploration-dev` (2026-08-01).** Shipped via **manual asar repack** because `app-builder-bin@5.0.0-alpha.13` ENOENTs on a fork-0.3.63 stale `multica-main` path (CLAUDE.md "Ship chain fallback: manual asar repack (0.3.63+)"). Still deferred: `dashboard.go` cost wire-up (needs `foldRestrictedAgents` fix — see `.omc/plans/upstream-integration-0.5.0-proposal-rev2.md`), 93 fork-only files still on old text-size utilities, cloud physical-deletion PRs A/B/C/D on kept branches (`fork-hygiene-a/b/c/d`).

> Keep this file short and authoritative: rules here should be hard to infer from code or easy to get wrong.

> This file is the single source of truth for cross-cutting rules; `AGENTS.md` is a derived digest of it. Shared constraints (toolchain versions, package boundaries, verification commands, and the critical-constraint tokens: localized fork prohibitions, state management, backend UUID rules, Pythia source-of-truth, experimental network calls, migration/config immutability, i18n selectors) are enforced by `scripts/check-agents-docs-sync.mjs` (CI `docs-sync` job + `githooks/pre-push`).

## Sub-domain Guides (read the nearby file when working in a sub-domain)

Each large sub-domain has a co-located `CLAUDE.md` with its own boundaries,
commands, and common pitfalls. When your work is scoped to one of these, that
nearby file is sufficient — you do not need to read this whole root file. This
root file is the navigation route plus cross-cutting product/ship/desktop rules.

| Working in | Read first |
| --- | --- |
| `server/` (Go backend, handlers, migrations, experimental catalog) | [`server/CLAUDE.md`](server/CLAUDE.md) |
| `packages/` (`core` / `ui` / `views` shared FE) | [`packages/CLAUDE.md`](packages/CLAUDE.md) |
| `packages/views/` (shared business pages/components) | [`packages/views/CLAUDE.md`](packages/views/CLAUDE.md) |
| `apps/desktop/` (Electron app, packaging, self-contained backend) | [`apps/desktop/CLAUDE.md`](apps/desktop/CLAUDE.md) |
| `apps/mobile/` (Expo / React Native) | [`apps/mobile/CLAUDE.md`](apps/mobile/CLAUDE.md) |
| `apps/web/` (Next.js App Router, platform wiring) | [`apps/web/CLAUDE.md`](apps/web/CLAUDE.md) |

Each guide directory also carries an auto-synced `AGENTS.md` mirror (same
content, discoverable by agent platforms that load `AGENTS.md`). The co-located
`CLAUDE.md` is the source of truth; parity is enforced by
`scripts/check-agents-docs-sync.mjs`.

## Localized Fork

This is a **fully localized, single-user fork** of Multica. The primary target is the macOS desktop app. Key differences from upstream:

- **No telemetry**: `analytics.NewFromEnv()` always returns `NoopClient{}`. All frontend analytics functions are no-ops. The PostHog implementation file (`server/internal/analytics/posthog.go`) has been deleted.
- **No auto-update**: CLI update command is stubbed. Daemon does not start `autoUpdateLoop`. Desktop `updater.ts` is a no-op stub. `electron-builder.yml` has no `publish:` block. `electron-updater` dependency is removed.
- **No Google OAuth / email verification**: `SendCode`, `VerifyCode`, and `GoogleLogin` handlers all return 410 Gone. Only `UsernameLogin` (`POST /auth/login {"name":"..."}`) works. The web callback page redirects to login.
- **No cloud features**: Cloud billing, cloud runtime, CloudFront CDN, contact sales, cloud PAT, invitations, and workspace members management have been deleted.
- **No external support UI**: HelpLauncher, JoinDiscordCard, Discord icon, and FeedbackModal have been deleted.

Do **not** re-add any of the above.

- **Username-only login upserts a new user on every login.** `POST /auth/login` creates a new user row when the supplied name is unseen. Workspace membership is bound to the *creator* user_id, so any username change across restarts yields a fresh user with zero workspaces. Do not "fix" this by auto-binding to existing workspaces (let typo grant ownership). See `.omc/incidents/2026-06-27-username-only-login-loses-workspaces.md`.

- **i18next selector block-body incident (2026-07-14).** i18next's `keysFromSelector` reads `[PATH_KEY]` off whatever the selector returns. A block-body selector like `t(($) => { const v = $.foo; return v; })` returns a plain string instead of the proxy, so `path` becomes `undefined` and the very next line `if (path.length > 1 && nsSeparator)` throws `TypeError`. The error escapes React's render pass, unmounts the surrounding tree, and (because the offending selector was inside `AppSidebar`) blanked the entire desktop window. The fix: selectors must be arrow expressions, e.g. `($) => $.sidebar[item.labelKey]`. Three layers of protection are now in place:
  1. `packages/views/i18n/use-t.ts` — comment block with incident reference.
  2. `packages/views/eslint.config.mjs` — `no-restricted-syntax` rule blocks `t(($) => { ... })` and `useT(($) => { ... })` at build time.
  3. `packages/views/layout/app-sidebar.tsx` — `AppSidebar` is wrapped in `@multica/ui/components/common/error-boundary` with a "Sidebar failed to render / Retry" fallback so any future selector crash stays scoped to the sidebar panel.

- **i18n selector rule (quick reference):** use arrow-expression selectors ONLY. `t(($) => $.foo.bar)` ✓. `t(($) => { return $.foo.bar; })` ✗ — block body returns a plain string, `[PATH_KEY]` becomes undefined, throws `TypeError`. ESLint blocks both forms via `no-restricted-syntax` in `packages/views/eslint.config.mjs`.

## Retired Features (do NOT re-add)

These were intentionally removed in 0.3.x. Future sessions must not re-introduce them even if upstream ships them — the fork's localization contract is deliberate:

- **`constitution_agent` lab** (retired 0.3.57, migration 165). Removed the `宪法智能体` agent, 3 autopilots (CTR/CSIL/TAOL), 4 `experimental_resource_visibility` rows, and the bundled `multica-constitution-agent` skill. If upstream re-adds a constitution lab, do NOT cherry-pick it back — the fork user explicitly rejected it.
- **Username-only login user-creation side effects**: `POST /auth/login` still upserts a new user row on every unseen name (no auto-bind to existing workspaces). Do NOT "fix" by binding workspaces across logins — see incident `.omc/incidents/2026-06-27-username-only-login-loses-workspaces.md`.
- **Inline lab workspace panel on issue detail** (removed 0.3.38). `LabWorkspacePanel`, `pickLabInlineView`, `IssueDetailProps.renderLabInline`, and the `*Inline` view wrappers (`ClaudeLabInline` / `PythiaInline` / `MythosInline` / `LLMWikiBridgeInline`) are all gone. Lab surfaces are reachable ONLY via `/experimental/<suffix>` from the sidebar or `<IssueLabsSection>` "open panel" link.

- **BrowserWindow off-screen guard.** Electron 39 on macOS restores stale bounds from system window-state cache; if the bounds fall outside every connected display's workArea the window is invisible. `apps/desktop/src/main/index.ts` clamps bounds in `ensureWindowOnscreen()` — called synchronously after `new BrowserWindow(...)`, again on `ready-to-show`, and on every `move` / `resize` / `display-removed`.

- **Pythia engine source-of-truth is `apps/desktop/vendor/pythia-src/engine/`, NOT `apps/desktop/resources/pythia/engine/`.** The `bundle-cli` script (`apps/desktop/scripts/bundle-cli.mjs:281-283`) wipes `resources/pythia/` and re-copies from `vendor/pythia-src/` on every run. Any prompt or code change made directly under `resources/pythia/engine/*.py` is silently overwritten at bundle time. Edit the vendor copy, then re-run `pnpm --filter @multica/desktop bundle-cli` so the staged resources get the new content. This bit the 0.3.21 Pythia i18n pass — three rounds of `Edit` to `resources/pythia/engine/{swarm,brief,oracle}.py` all looked successful until a re-bundle reverted every change.

## Conventions

The source of truth for code naming, i18n glossary, and Chinese product voice is:

- `apps/docs/content/docs/developers/conventions.mdx`
- `apps/docs/content/docs/developers/conventions.zh.mdx`

Read it before editing translations in `packages/views/locales/`, naming routes/packages/files/DB columns/types, or writing Chinese UI/docs copy. (Earlier versions pointed at `packages/views/locales/glossary.md` as a redirect stub; that file no longer exists — the glossary lives in `conventions.mdx` above, so do not reference or recreate it.)

## Project Shape

Multica is an AI-native task management platform for small teams, with agents as first-class assignees that can own issues, comment, and change status.

- `server/` — Go backend (Chi router, sqlc, gorilla/websocket). Three entrypoints under `cmd/`: `server` (HTTP + WS), `multica` (CLI), `migrate` (forward/back SQL).
- `apps/web/` — Next.js App Router.
- `apps/desktop/` — Electron desktop app (primary target).
- `apps/mobile/` — Expo / React Native iOS app. Read `apps/mobile/CLAUDE.md` before touching.
- `apps/docs/` — Nextra documentation site (`pnpm dev:docs`).
- `packages/core/` — headless business logic, API client, React Query hooks, Zustand stores.
- `packages/ui/` — atomic UI components only.
- `packages/views/` — shared business pages/components for web and desktop.
- `packages/tsconfig/` — shared TypeScript config.

Shared packages export raw `.ts` / `.tsx` and are compiled by consuming apps. Dependency direction: `views -> core + ui`; `core` and `ui` must stay independent.

### Data Flow (60-second mental model)

A new session that needs to understand "where does an issue go when I assign it to an agent" can read this section instead of grepping across `server/`, `daemon/`, and `apps/`.

```
  ┌──────────────────────────┐
  │  Renderer (desktop/web)  │  TanStack Query + Zustand
  │  packages/views/issues   │
  └────────────┬─────────────┘
               │  HTTP (api.rawRequest) + WS (gorilla)
               ▼
  ┌──────────────────────────┐
  │  server/internal/handler │  Chi router, sqlc, membership-gated
  │  → service/* (issue.go) │
  │  → agent_self_optimization / mythos / claude_science_runtime
  └────────────┬─────────────┘
               │  sqlc queries
               ▼
  ┌──────────────────────────┐
  │  PostgreSQL 17 + pgvector│  multica DB (shared across worktrees;
  │  (Docker or native PG)  │  schema is forward-only additive)
  └────────────┬─────────────┘
               ▲
               │  WS push (daemon heartbeat, task updates)
  ┌────────────┴─────────────┐
  │  Local Daemon            │  Spawns Claude Code / Codex / copilot /
  │  server/cmd/multica      │  openclaw / opencode / hermes / etc.
  │  + apps/desktop/src/main │  against the user's chosen runtime
  │   /daemon-manager.ts     │  (workspace-scoped runtime row)
  └──────────────────────────┘
```

Lifecycle of a single assigned task: **PATCH `issue.assignee_*`** → server `assignDefaultLabAgent` (if lab-bound) → daemon claim on `agent_task_queue` → daemon `LoadAgentSkillsForClaim` injects builtin skills + workspace skill rows → subprocess spawns the agent CLI → progress streams over WS → renderer patches Query cache via `["agent-task-snapshot"]` invalidation.

Labs add a parallel path: **issue.lab_source='agent_creation_studio'** → server `defaultLabLeaderForKey` resolves to `agent_creation_expert` → same `agent_task_queue` claim, but the leader agent's bundled skill (`multica-creating-agents`) authors the resource. The renderer-side `LabPicker` is the only entry point that writes `lab_source`; `IssueLabsSection` is the only read-side surface.

## State Rules

Keep server state and client state separate.

- **TanStack Query** owns server state: issues, users, workspaces, inbox, agents, members, anything fetched from the API.
- **Zustand** owns client state: selected workspace, filters, drafts, modals, tab layout, navigation history.
- Shared Zustand stores live in `packages/core/`, never in `packages/views/` or app directories.
- React Context is for platform plumbing only (`WorkspaceIdProvider`, `NavigationProvider`).
- Only auth/workspace stores may call `api.*` directly. Other server interaction belongs in queries/mutations.
- Workspace-scoped query keys must include `wsId`.
- Mutations are optimistic by default: patch locally, send request, roll back on failure, invalidate on settle.
- WebSocket events invalidate or patch Query cache; they never write directly to Zustand stores.
- Persist durable preferences/drafts/layout. Do not persist server data or ephemeral UI state.
- Zustand selectors must return stable references.
- Hooks that need workspace context should accept `wsId`; do not call `useWorkspaceId()` internally unless guaranteed to run under the provider.

## Package Boundaries

> Full boundaries, state model, and testing rules for `core`/`ui`/`views`: [`packages/CLAUDE.md`](packages/CLAUDE.md).

Hard constraints:

- `packages/core/`: no `react-dom`, `localStorage` (use `StorageAdapter`), `process.env`, or UI libraries.
- `packages/ui/`: no `@multica/core` imports and no business logic.
- `packages/views/`: no `next/*`, no `react-router-dom`, no stores. Use `NavigationAdapter`, `useNavigation()`, `<AppLink>`.
- `apps/web/platform/`: only place for Next.js navigation/platform APIs.
- `apps/desktop/src/renderer/src/platform/`: only place for `react-router-dom` navigation wiring.
- Every workspace under `apps/` and `packages/` must declare directly imported external packages in its own `package.json`.
- Shared dependencies use `catalog:` from `pnpm-workspace.yaml`; `apps/mobile/` pins Expo/React Native directly.

## Sharing Rules

Web and desktop share business logic through `packages/core/`, `packages/ui/`, and `packages/views/`. Extract shared code unless it depends on platform APIs:

1. Next.js, Electron, or router APIs stay in the app/platform layer.
2. Headless logic → `packages/core/`.
3. Shared UI or business views → `packages/views/`.
4. Shared primitives → `packages/ui/`.

Mobile is independent. It may import types and pure functions from `@multica/core` (with `import type`), but owns its UI, state, hooks, providers, i18n, React version, build pipeline, and release cadence.

## Toolchain Baseline

Pinned versions — do not bump casually:

| Tool | Version | Source |
| --- | --- | --- |
| Node | 22.x | CI workflows |
| pnpm | 10.28.2 | `package.json` (`packageManager`) |
| Go | 1.26.1 | CI workflows |
| TypeScript | ^5.9.3 | `pnpm-workspace.yaml` catalog |
| React | 19.2.3 | `pnpm-workspace.yaml` catalog |
| PostgreSQL | 17 with pgvector | `pgvector/pgvector:pg17` (CI service) |

`apps/mobile/` pins Expo / React Native versions directly and is excluded from root turbo pipelines.

## Commands

`make` (or `make help`) prints the full target list — `make` defaults to `help`, not to a destructive target. The one-command bootstrap is `make dev`: auto-detects main vs worktree, creates env file, installs deps, runs migrations, starts backend + frontend.

```bash
make dev              # auto-setup and start the app
make start            # start backend + frontend
make stop             # stop app processes for this checkout
make server           # run Go server only
make daemon           # run local daemon
make test             # Go tests
make sqlc             # regenerate sqlc code after SQL changes
pnpm install
pnpm dev:web
pnpm dev:desktop
pnpm build
pnpm typecheck
pnpm lint
pnpm test             # TS/Vitest tests through Turborepo
pnpm exec playwright test
pnpm ui:add badge     # shadcn/Base UI component into packages/ui

# Single Go test (from server/)
cd server && go test -run TestName -count=1 -timeout 60s ./internal/handler/

# Single Vitest test (from repo root)
pnpm test path/to/file.test.ts

# Desktop packaging (from repo root)
pnpm --filter @multica/desktop bundle-cli   # Go binaries + migrations + PG manifest → resources/
pnpm --filter @multica/desktop build        # electron-vite build
pnpm --filter @multica/desktop package      # electron-builder → dist/multica-desktop-<ver>-mac-arm64.dmg
```

### Before packaging

Run the data-safety snapshot (mandatory):
```bash
bash ~/.multica/scripts/pre-update-snapshot.sh
```
Backs up current `/Applications/Multica.app`, DB tables, config files, KB vaults. Failure (exit 1) blocks packaging. Auto-detects PG port (5432 prod / 5433 dev). Prunes old `.bak` dirs on each run.

**Run pending migrations before bundle-cli** (see 0.3.20 ship lesson):
```bash
cd server && go run ./cmd/migrate up
```
The `.app` cold start auto-runs pending migrations, but surface SQL errors at build time rather than at first user launch.

### Desktop install

```bash
hdiutil attach dist/multica-desktop-*-mac-arm64.dmg -nobrowse -quiet
cp -R /Volumes/Multica*/Multica.app /Applications/
hdiutil detach /Volumes/Multica* -quiet

# Fallback when DMG generation hangs (create-dmg 1.2.3 incompat, 0.3.4/0.3.13/0.3.15+):
cp -R dist/mac-arm64/Multica.app /Applications/

# Verify cold start loaded the new binary:
bash ~/.multica/scripts/verify-desktop-cold-start.sh
```
Do NOT run `chown -R $USER:admin` (broken in zsh sandbox, memory `multica-dmg-replace-chown-pitfall`); `cp -R` doesn't trigger it.

### Worktree dev isolation (0.2.95)

`apps/desktop/scripts/dev.mjs` auto-detects worktree-isolated renderer ports and app names via `worktree-dev-env.mjs`. Linked worktrees appear as "Multica Canary" with offset ports. Worktrees share one PG container; use `make worktree-env` + `make setup-worktree` + `make start-worktree` for manual setup.

### Version source

This checkout **is** a git repository, but its tags are `pre-update-*` snapshot markers, not release tags — so `git describe --tags` returns a marker like `pre-update-...-gfa6fb31`, not a usable release version. `bundle-cli.mjs` runs `git describe` first, then falls back to `apps/desktop/package.json` → `version` whenever the result is empty or a `pre-update-` marker (see `apps/desktop/scripts/bundle-cli.mjs`). `apps/desktop/package.json` is the canonical version source. Bump `apps/desktop/package.json` only.

## Authentication

Username-only. `POST /auth/login` accepts `{"name":"alice"}` — first call creates the user (email = `name + "@local"`), returns a JWT. No email verification, no Google OAuth, no password.

Login pages: `apps/desktop/src/renderer/src/pages/login.tsx`, `apps/web/app/(auth)/login/page.tsx`. Both call `useAuthStore.getState().loginWithUsername(name)`. `loginWithGoogle` removed from both `AuthState` and `ApiClient`. Web callback page is a redirect stub.

## API Compatibility

Frontend code must survive backend response drift, especially in installed desktop builds.

- Parse API JSON with `parseWithFallback` in `packages/core/api/schema.ts` and a zod schema. Do not cast network JSON to `T`.
- Endpoint responses consumed by UI logic must pass through a schema before returning.
- Downstream UI should optional-chain and default fields defensively.
- Prefer explicit boolean checks (`=== true`) over truthy/falsy checks on server fields.
- Server-driven enum switches need a `default` branch.
- When adding or changing an endpoint, add/update the schema and include a malformed-response test.

## Backend UUID Rules

> Full backend boundaries, commands, and pitfalls: [`server/CLAUDE.md`](server/CLAUDE.md).

In `server/internal/handler/`, always know where a UUID came from before using it in write queries.

- Resource path params that may be UUIDs or human-readable IDs must be resolved through loaders (`loadIssueForUser`, `loadSkillForUser`, `loadAgentForUser`, `requireDaemonRuntimeAccess`); subsequent writes use the resolved `entity.ID`.
- Pure UUID inputs from request boundaries use `parseUUIDOrBadRequest(w, s, fieldName)` and return immediately on `ok=false`.
- Trusted UUID round-trips from sqlc results or test fixtures use `parseUUID(s)`, which panics on invalid input.
- Outside handlers, `util.ParseUUID(s) (pgtype.UUID, error)` is the safe variant; always check the error.

## Coding Rules

- TypeScript strict mode is enabled; keep types explicit.
- Go follows standard conventions: `gofmt`, `go vet`, checked errors.
- Code comments must be English.
- Prefer existing patterns/components over new parallel abstractions.
- Avoid broad refactors unless required by the task.
- For internal, non-boundary code, do not add compatibility layers, fallback paths, dual writes, legacy adapters, or temporary shims unless explicitly requested.
- If a flow or API is being replaced and the product is not live, prefer removing the old path instead of preserving both.
- New global pre-workspace routes must be a single word (`/login`, `/inbox`) or `/{noun}/{verb}` (`/workspaces/new`). Do not add hyphenated root routes like `/new-workspace`.
- Reserved slugs: `server/internal/handler/reserved_slugs.json`. Edit it, run `pnpm generate:reserved-slugs`, commit the generated `packages/core/paths/reserved-slugs.ts`.
- When changing CLI commands/flags, API fields, or product behavior documented by built-in skills under `server/internal/service/builtin_skills/*`, update the relevant `SKILL.md` and `references/*-source-map.md` in the same PR.

## Web/Desktop Features

When adding a shared page or feature for web and desktop:

1. Put the page/component in `packages/views/<domain>/`.
2. Add platform wiring in both `apps/web/app/` and the desktop router (unless the desktop flow is a transition overlay).
3. Use `useNavigation().push()` or `<AppLink>` in shared code.
4. Use shared guards/providers such as `DashboardGuard` from `packages/views/layout/`.
5. Keep platform-only UI in the app or inject it through props/slots.
6. Hooks that need workspace context should accept `wsId`.

CSS for web/desktop is shared from `packages/ui/styles/`. Use semantic tokens (`bg-background`, `text-muted-foreground`); avoid hardcoded Tailwind colors and duplicated base styles.

When reviewing or auditing UI code — accessibility, UX, visual design, or "does this look right" — invoke the `web-design-guidelines` skill (`.agents/skills/web-design-guidelines/SKILL.md`). Triggers: "review my UI", "check accessibility", "audit design", "review UX", "check against best practices". It fetches the Web Interface Guidelines and returns terse `file:line` findings with a summary.

## Mobile Rules

Read `apps/mobile/CLAUDE.md` before touching `apps/mobile/`. Mandatory pre-flight, import limits, parity rules, tech stack, UI rules, data helpers, realtime strategy, and mobile release flow live there.

Root-level reminders:
- Mobile shares only `@multica/core` types and pure functions.
- Mobile must match web/desktop product semantics: counts, permissions, enums/transitions, and data identity.
- Mobile may differ in UI/interaction when the phone context requires it.

## UI Rules

- Prefer shadcn/Base UI components over custom implementations. Add with `pnpm ui:add <component>` from repo root.
- Use design tokens and semantic classes; avoid hardcoded colors.
- Do not introduce extra local state unless the design requires it.
- Handle overflow, long text, scrolling, alignment, and spacing deliberately.
- If a component is identical between web and desktop, it belongs in a shared package.

## Desktop Rules

### Self-contained backend (server-manager.ts)

The desktop app bundles its own Go backend and auto-starts everything on launch. No external `make start` required. Lifecycle managed by `apps/desktop/src/main/server-manager.ts`:

1. **Probe PostgreSQL** — TCP connect to `127.0.0.1:5432`. PG is bundled Postgres.app 17.4 under `~/Library/Application Support/Multica/pg/`. If unreachable, `startNativePg` downloads/extracts/initdb/starts it.
2. **Run migrations** — Execute the bundled `migrate up` binary (schema migrations at `resources/server/migrations/`). Retries up to 3 times with 2 s backoff. **P0 guard**: `runMigrate` REFUSES `backend === "external"`.
3. **Spawn server** — Start the bundled `server` binary as a background child process, poll `/health` until it responds.
4. **Daemon follows** — Once the server is live, the existing daemon-manager registers the agent runtime.

On `app.before-quit`, the server receives SIGTERM (5 s grace) then SIGKILL. The quit handler is registered alongside `setupDaemonManager()` in `apps/desktop/src/main/index.ts` — it calls `stopServerManager()` so the server port is always freed on exit.

**Bundled resources** (all Go binaries, migration SQL files, PG manifest, experimental manifests, Pythia source, OpenScience prompts) ship via `asarUnpack: resources/**`. The bundle script is `apps/desktop/scripts/bundle-cli.mjs` — it builds three Go binaries from `server/cmd/*` and copies them + `server/migrations/` + PG manifest + Pythia + OpenScience + claude-science manifests into `apps/desktop/resources/`. Docker compose is no longer bundled (0.3.0+ standalone).

**Path resolution in packaged app**: `child_process` APIs do not resolve asar paths. Use `resolveResourcePath()` (in server-manager.ts) which routes to `process.resourcesPath + app.asar.unpacked/resources/...` when `app.isPackaged`, and `app.getAppPath() + resources/...` in dev.

**Desktop runtime config**: `~/.multica/desktop.json`:
```json
{"apiUrl": "http://localhost:8090", "wsUrl": "ws://localhost:8090/ws", "appUrl": "http://localhost:3000"}
```
Per-profile server env (`.env`) at `~/.multica/profiles/<name>/.env`.

### v0.3.0+ Standalone (Docker removed)

PG is now bundled Postgres.app (17.4) shipped under `resources/pg/`, downloaded on first launch to `~/Library/Application Support/Multica/pg/17.4/`. Read `memory/multica-0.3.0-standalone-2026-07-02.md` before touching the native PG lifecycle.

- `apps/desktop/src/main/pg-bootstrap.ts` — native PG lifecycle: `downloadAndExtractPg` (DMG + SHA-256 verify), `initPgDataDir` (initdb + postgresql.conf patches), `startNativePg` (pg_ctl + force-quit orphan recovery + Gatekeeper fallback), `stopNativePg` (pg_ctl stop -m fast + pgrep SIGKILL fallback), `runMigrationFlow` (Docker→native pg_dump|pg_restore + row-count parity).
- `apps/desktop/src/main/server-manager.ts` — `pickPgBackend` (2-way picker: external if multica PG is on 5432, else native), `probeMulticaPg` (4-field identity check: user=multica, db=multica, pgcrypto, `schema_migrations` table), `runMigrate` (P0 guard), `ensureServerUp` (P1.1 in-flight cache + P1.2 stopping flag), `stopServerManager` (await inflight, then SIGTERM).

**P0 data-safety contract** (2026-07-02 incident destroyed 69 user tables when a Docker pgdata was migrated by the bundled `migrate` binary whose history includes `029_drop_daemon_pairing`, `046_drop_runtime_usage`, `103_drop_legacy_daily_rollups`):

> `runMigrate(profile, env, backend?)` REFUSES `backend === "external"`.
> The call site in `ensureServerUp` ALSO skips for defense in depth.
> **Do NOT remove either layer. Do NOT make `backend` optional without
> re-reading the memory file.** Any new caller that invokes `runMigrate`
> must pass `backend` explicitly.

**P1.8 sentinel atomicity**: `runMigrationFlow` creates `~/.multica/.pg-migrating-v1` with `O_EXCL` BEFORE the destructive `pg_restore`, renames it to `~/.multica/.pg-migrated-v1` on success. A SIGKILL between restore-success and rename leaves the in-progress file; the next launch sees it and refuses auto-retry (returns `{skipped: "already-migrated"}`). The user must remove the file manually after investigation. **Do NOT write the final sentinel before the operation succeeds.**

### Routing

Three categories:

- **Session routes**: workspace-scoped tab destinations such as `/:slug/issues`.
- **Transition flows**: pre-workspace one-shot actions such as create workspace or accept invite. These are `WindowOverlay` state, not routes.
- **Error/stale states**: stale workspace tabs should auto-heal by dropping stale tab groups, not render desktop error pages.

Constraints:
- New pre-workspace desktop flows register a `WindowOverlay` type in `stores/window-overlay-store.ts`; do not add them to `routes.tsx`.
- `setCurrentWorkspace(slug, uuid)` from `@multica/core/platform` is the active workspace source of truth.
- Code that leaves workspace context must call `setCurrentWorkspace(null, null)` explicitly.
- Leave/delete workspace flow order: read cached destination, clear current workspace, navigate, then run the mutation.
- Cross-workspace navigation must go through the navigation adapter so it can call `switchWorkspace(slug, targetPath)`.
- Full-window desktop views outside the dashboard shell must mount `<DragStrip />` from `@multica/views/platform` as the first flex child. Interactive controls in the top 48px need `WebkitAppRegion: "no-drag"`.

## Data Safety & Version Upgrades

The DMG install **only replaces `/Applications/Multica.app`**. All user data lives in independent paths that survive upgrades:

| Data | Path | Upgrade impact |
|------|------|----------------|
| PostgreSQL | Docker volume `multica_pgdata` | Untouched |
| Config / tokens | `~/.multica/profiles/<name>/config.json` | Untouched |
| Server env | `~/.multica/profiles/<name>/.env` | Untouched |
| Workspace files | `~/multica_workspaces_<profile>/` | Untouched |
| KB vaults | `~/Documents/` | Untouched |
| Desktop config | `~/.multica/desktop.json` | Untouched |

Rules:
- **Migrations are forward-only**: never drop a table or column in a migration. Schema changes must be additive.
- **Config fields are append-only**: don't delete or rename existing keys in `config.json` or `.env`. New fields must have defaults.
- **Pre-update snapshot** (see Commands section) must pass before building a DMG.
- **Verify data integrity** after upgrade: `docker exec multica-postgres-1 psql -U multica -d multica -c "SELECT COUNT(*) FROM workspace"` should return the expected count.

## Testing

Tests follow the code:

| What is tested | Location |
| --- | --- |
| Shared business logic, stores, queries, hooks | `packages/core/*.test.ts` |
| Shared UI components, pages, forms, modals | `packages/views/*.test.tsx` |
| Platform wiring (cookies, redirects, search params) | `apps/web/*.test.tsx` or `apps/desktop/` |
| End-to-end flows | `e2e/*.spec.ts` |
| Backend | `server/` Go tests |

Rules:
- Never test shared component behavior in an app test file.
- `packages/views/` tests must not mock `next/*` or `react-router-dom`.
- Mock `@multica/core` stores with the Zustand callable-store shape (`selectorFn` plus `getState`).
- Mock `@multica/core/api` for API calls.
- E2E tests should use `TestApiClient` for setup/teardown.
- Prefer writing the failing test in the correct package before implementation when the change is behavioral.

**Desktop self-contained backend smoke** (any change to `apps/desktop/src/main/{server-manager,pg-bootstrap,daemon-manager}.ts` or a packaged `.app` build): install the new DMG, hard-quit the prior `/Applications/Multica.app`, run `pkill -f "multica daemon"` and `pkill -f "Multica.app/Contents/MacOS/Multica"`, then `open /Applications/Multica.app` and verify the **three-check pass**:

1. `lsof -nP -iTCP:5432 -sTCP:LISTEN` and `lsof -nP -iTCP:8090 -sTCP:LISTEN` both have a listener within 6 s.
2. `curl -s http://localhost:8090/health` returns `{"status":"ok"}`.
3. Row parity via `PGPASSWORD=multica psql -U multica -d multica -h 127.0.0.1 -p 5432 -tAc "SELECT COUNT(*) FROM workspace|issue|comment|agent"` matches `workspace=1 / issue≈84 / comment≈477 / agent=38` (the gold baseline for jyf's local data; small drift is expected from issue creation between releases, but the workspace and agent counts must not move unless the change explicitly modifies them).

## Verification

For code changes, run the narrowest useful checks while iterating, then broader verification when risk justifies it or when asked:

```bash
pnpm typecheck
pnpm test
make check-fast       # affected TS typecheck + unit + lint; no DB/Go/E2E
make test
pnpm exec playwright test
make check
```

Do not claim verification passed unless you ran it. If you skip checks because the change is docs-only or the user asked not to run them, say so.

## Commits and Releases

- Atomic commits with conventional prefixes: `feat(scope)`, `fix(scope)`, `refactor(scope)`, `docs`, `test(scope)`, `chore(scope)`.
- This checkout is a git repository, but releases are **not** tagged as `v0.x.x` (existing tags are `pre-update-*` snapshots; see "Version source" above), so `git describe` never yields a release version. For local releases, bump `apps/desktop/package.json` `version` field and document the change in `.omc/release-notes-<ver>.md` rather than creating a release tag.
- **DMG creation hangs on create-dmg 1.2.3** (`electron-builder --mac` produces no `.dmg` on this fork). Use `pnpm exec electron-builder --mac --dir` to produce `dist/mac-arm64/Multica.app` directly and ship that. Every 0.3.x release ships via the `--dir` path. **Mandatory**: `pnpm build` does NOT run electron-builder; the asar replacement is silent if this step is skipped. Verify with `grep -c rawRequest apps/desktop/dist/mac-arm64/Multica.app/Contents/Resources/app.asar` after each build.
- Bump patch by default unless the user specifies a version.

## Ship chain (canonical order)

> **Prefer the enforced script:** `make ship-mac` (or `bash scripts/ship-mac.sh`)
> runs every step below in order, aborts on the first failure, verifies the
> renderer asar was actually replaced, calls `desktop-sign-nested-binaries.sh`
> (its first real caller), and runs the cold-start check. It prompts before the
> destructive `/Applications` overwrite (`--yes` to auto-confirm, `--build-only`
> to stop before install). The manual steps below are kept for reference and for
> the asar-repack fallback; do not hand-run them when the script will do.

Mandatory steps in order. Skipping any step risks data loss or a broken `.app`:

```bash
# 1. Snapshot — refuses to proceed if data-safety invariants fail
bash ~/.multica/scripts/pre-update-snapshot.sh

# 2. Apply pending migrations BEFORE bundle-cli (so SQL errors surface at build time)
cd server && go run ./cmd/migrate up && cd ..

# 3. Bundle Go binaries + stage PG/Pythia/OpenScience/manifests into resources/
pnpm --filter @multica/desktop bundle-cli

# 4. Build renderer via electron-vite
pnpm --filter @multica/desktop build

# 5. Package — DMG creation is broken in create-dmg 1.2.3 on this version (see
#    stability note below). Use --dir and ship the .app directly:
pnpm exec electron-builder --mac --dir
cp -R dist/mac-arm64/Multica.app /Applications/

# 5a. Re-sign nested Go binaries in app.asar.unpacked/ (macOS 27 Gatekeeper
#     kills fork+exec if these lack self-contained ad-hoc signatures).
#     Without this step, GUI Helper processes come up but `multica --help`
#     returns exit 137 (SIGKILL) and the server never binds :8090.
#     This is now a runnable, self-verifying script (was doc-only/manual and
#     got forgotten on past ships — see Known Stability Surfaces). It signs
#     the app + the 3 nested binaries and asserts `multica --help` exits 0.
bash scripts/desktop-sign-nested-binaries.sh /Applications/Multica.app

# 6. Verify cold start (three-check pass + row parity)
pkill -f "multica daemon" ; pkill -f "Multica.app/Contents/MacOS/Multica"
open /Applications/Multica.app
bash ~/.multica/scripts/verify-desktop-cold-start.sh
```

### Ship chain fallback: manual asar repack (0.3.63+)

`electron-builder --mac --dir` deadlocks intermittently on macOS 27 with
`app-builder-bin@5.0.0-alpha.12` (process stuck in `pthread_cond_wait` at
`unpack-electron` step, 0% CPU indefinitely). When the canonical path hangs
for > 5 min, use the manual asar repack:

```bash
# 1. Extract the existing installed asar
asar extract /Applications/Multica.app/Contents/Resources/app.asar /tmp/multica-extract

# 2. Overwrite renderer output
rm -rf /tmp/multica-extract/out/{main,preload,renderer}
cp -R apps/desktop/out/{main,preload,renderer} /tmp/multica-extract/out/

# 3. Overwrite Go binaries
cp apps/desktop/resources/bin/{multica,server,migrate} /tmp/multica-extract/resources/bin/

# 4. Repack
asar pack /tmp/multica-extract /tmp/multica-new.asar

# 5. Deploy
cp /tmp/multica-new.asar /Applications/Multica.app/Contents/Resources/app.asar
cp apps/desktop/resources/bin/{multica,server,migrate} \
   /Applications/Multica.app/Contents/Resources/app.asar.unpacked/resources/bin/

# 6. Re-sign (same as canonical path step 5a — runnable, self-verifying)
bash scripts/desktop-sign-nested-binaries.sh /Applications/Multica.app
```

Prerequisites: `npm install -g @electron/asar`. The fallback is valid only for
patches that do NOT add/remove/rename resource paths (no new lab, no new
manifest, no new Python dep). For structural changes, resolve the
electron-builder deadlock first (pin `app-builder-bin` or wait for stable 5.x).

Write a release note at `.omc/release-notes-<ver>.md` and a ship log at
`.omc/0.3.<ver>-ship-<date>.md` (one-line header, ship steps, risk vs outcome,
row parity numbers).

## Labs Platform (0.3.31, current model)

Unified experiment-development platform: manifest → catalog → registry → IPC dispatcher → proxy mount → sidebar. Every new flag that needs a sidebar entry, IPC channel, proxy route, or install handler registers through this single chain — no per-flag hardcodes.

### Hard constraints (do NOT violate)

1. **Flag = off must completely bypass experimental code.** No new imports, no module init in the legacy path. View toggle points use `{flagEnabled ? <NewCode /> : null}` so the experimental branch never runs when off.
2. **Users cannot create flags.** Catalog is developer-only at `server/internal/experimental/catalog.go`. Labs UI only renders what the server returns.
3. **Labs tab is the only entry point.** No nav bar, no CLI shortcuts, no `api.*` callers outside `labs-tab.tsx`.
4. **Not a plugin system.** Simple toggle pattern, not dynamic load.
5. **No reserved workspace for new labs.** Pre-0.3.22 `claude_science` / `claude_science_runtime` created a `claude-science` reserved-slug workspace via `upsertClaudeScienceWorkspace`. As of 0.3.25 this is **enforced in code, not aspirational**: `InstallClaudeScience` / `InstallMythos` take the caller's active `workspaceID` (resolved from `X-Workspace-ID` via `resolveLabWorkspace`, falling back to the user's first workspace) and write all resources into it. `upsertClaudeScienceWorkspace` / `upsertMythosWorkspace` are deleted; the workspace itself is **never** `Claim`ed (locking a user's own workspace would let a rollback hide it). Lab resources are isolated purely by `experimental_resource_lock` (`experimental_source` column) + the visibility table. The `claude-science` / `mythos-swarm` / `code-canvas` reserved slugs stay in `reserved_slugs.json` as legacy protection only (a new user cannot claim a name a legacy install may still occupy) — they are not created by any current flag. Do not re-introduce reserved workspaces for new flags.

### Architecture (10 PRs, shipped)

- **Manifest** (`apps/desktop/resources/experiments/<flagKey>/manifest.json`): `apiVersion: multica.dev/experiment/v1`, `kind: Experiment`. Declares workspace, capabilities, entry_points, runtime (kind + binary + health_path), surface (proxy_prefix + loopback_service), resources, safety, graduation.
- **Catalog** (`server/internal/experimental/catalog.go`): `Flag` struct with `ManifestPath`, `RuntimeKind`, `ProxyPrefix`, `LoopbackService`, `Sidebar`. 8 flags registered (current set: `chat_pin_ui`, `claude_science_lab`, `pythia_oracle`, `mythos_swarm`, `llm_wiki_bridge`, `code_canvas`, `agent_self_optimization`, `agent_creation_studio`). `constitution_agent` was retired in 0.3.57 (see Retired Features). `claude_science` and `claude_science_runtime` were removed in 0.3.22 — see "Lab Consolidation" below.
- **Registry** (`server/internal/experimental/registry.go`): singleton replacing scattered registries. `ProxyRoutes()`, `LoopbackURL()`, `RegisterInstallHandler()`, `RunInstall()`, `RunRollback()`, `SidebarEntries()`.
- **RuntimeKind** enum: `none` (UI toggle only), `inline` (claude_science_lab, llm_wiki_bridge, mythos_swarm, agent_self_optimization, agent_creation_studio), `subprocess` (pythia_oracle, code_canvas — separate process with health check), `headless` (agent runtime). `ManagerFactory` (`apps/desktop/src/main/experimental/manager-factory.ts`) dispatches per flag key, with `loadFlagDescriptors()` boot-time sync that pulls from `/api/experimental-flags`.
- **IPC channels**: `experimental:<flagKey>:<verb>` (get-status, get-url, ensure-up, stop). `setupExperimentalIPC` iterates catalog entries.
- **LifecycleMarker** (`server/internal/experimental/lock.go`): SHA-256-derived UUID per flag key (prefix `0xEC`).
- **Safety auto-mount** (`server/internal/handler/experimental_proxy.go`): `injectExperimentalFlagHeader` middleware injects `X-Experimental-Flag` for the safety-net burst breaker.
- **Skill boot loader** (`server/internal/service/builtin_skills.go`): main-product skills stay embed; experiment skills appended at boot from `$MULTICA_RESOURCES_DIR/skills/<flagKey>/<skillName>/SKILL.md` with `multica.experiment` frontmatter.
- **Sidebar** (`packages/views/layout/app-sidebar.tsx`): `useExperimentalNav()` derives nav items from `useExperimentalFlags()` → catalog `Sidebar` entries. No more hardcoded nav rows.
- **CLI** (`server/cmd/multica/main.go`): `multica experimental status|install|rollback|gc`.
- **Visibility gate** (`server/internal/experimental/visibility.go` + `labs_visibility_filter.go`): per-flag hideable resource sets seeded in `experimental_resource_visibility` rows. List handlers in `agent.go`/`autopilot.go`/`skill.go` use `filterLabsHiddenByDefault[T any]` helper (5 inline filter blocks → 1 generics helper).
- **Autopilot skip**: `server/internal/service/autopilot.go::shouldSkipDispatch` short-circuits when flag is off + autopilot is in hidden set.

### Lab Consolidation (0.3.22)

The 0.3.20 pair `claude_science` + `claude_science_runtime` is **deprecated and removed from the catalog**. The 0.3.22 single flag `claude_science_lab` (one sidebar entry, one view `/experimental/claude-lab`, one runtime gate) folds in both surfaces:

- The runtime sandbox HTTP route prefix `/api/experimental/claude-science-runtime/*` is unchanged for wire-compat with the existing Skill adapter. The router gate moves from `DefaultFor("claude_science_runtime")` to `DefaultFor("claude_science_lab")`.
- `experimental_resource_lock.experimental_source` CHECK was widened in migration 154 to include `claude_science_lab` (and `mythos_swarm`); the old `claude_science` value is still valid for legacy rows.
- `server/internal/experimental/lock.go::SourceClaudeScience` is now deprecated; new code uses `SourceClaudeScienceLab`. The `Source*` constants live alongside the catalog so the SQL enum and the Go constant stay in sync.
- `apps/desktop/src/renderer/src/pages/claude-lab-view.tsx` is the single-pane lab view with six tabs (Plan / Chat / Artifact / Forecast / Code / Knowledge). The Forecast tab reuses `<PythiaDashboard />` (forceSample). The Chat tab uses `<ExperimentalChatPane />` (see "Pre-workspace Lab surfaces" below).
- The bundled Skill is `multica-claude-science`; the bundled Skill catalogue is `/api/experimental/claude-science/skills` (public to any signed-in user, filtered by `handler.claude_science_skills.go::Visible*`).
- The vendor data dir `apps/desktop/resources/claude-science/` and `apps/desktop/vendor/claude-science-manifest/` are still bundled by `bundle-cli` (see `apps/desktop/scripts/bundle-cli.mjs:394-418`) because `server/internal/handler/install_claude_science.go` still reads the vendored manifest. Cleanup is a future PR — when it ships, expect to drop the bundle-cli step and `install_claude_science.go` together.

### Pre-workspace Lab surfaces (0.3.23+)

`ChatWindow` (`packages/views/chat/components/chat-window.tsx`) accepts an optional `wsId?: string` prop. When supplied, the component bypasses `useWorkspaceId()` and uses the prop directly. When omitted, behaviour is unchanged (hook path, backward-compatible). The prop is required by pre-workspace Labs surfaces such as the Claude Lab `Chat` tab, where there is no `<WorkspaceSlugProvider />` in scope.

- `packages/views/experimental/components/experimental-chat-pane.tsx` is the thin wrapper that takes a `wsId` prop and binds it to `<ChatWindow wsId={wsId} />`.
- The Claude Lab `Chat` tab uses `getCurrentWsId()` from `@multica/core/platform` (500 ms polling) to follow the user's currently active workspace without unmounting.
- Empty `wsId` renders a "请先选择或创建一个工作区" hint rather than throwing. Callers must hand in a valid UUID; the wrapper does not validate format.

### Forecast SSE endpoint (0.3.24)

`GET /api/experimental/claude-science-lab/forecast/stream` (text/event-stream) emits a `prediction` envelope every 5 s. Wire shape:

```
event: prediction
data: {"id":"p_…","scenario":"…","narrative":"","probability":0.42,
       "confidence":0.71,"horizon":"week","persona":"strategist",
       "createdAt":"2026-07-15T22:00:00Z"}
```

15 s keep-alive comment for proxy survival, 60 s max lifetime per subscriber, deterministic via `?seed=N`. The route is gated by `experimental.DefaultFor("claude_science_lab")` in `cmd/server/router.go`; 0.3.24 ships a synthetic generator, 0.3.25 swaps it for a real model call. Unit test lives at `server/internal/handler/claude_lab_forecast_test.go` (custom `captureWriter` shim — `httptest.NewRecorder` does not implement `http.Flusher`).

### Experimental tab network calls (0.3.30)

Every HTTP/SSE call from a Labs tab must go through `api.rawRequest(path, init)` (`@multica/core/api`), **never a bare `fetch()`**. `rawRequest` prepends the configured `baseUrl`, injects the Bearer/CSRF/workspace `authHeaders()`, sets `credentials: "include"`, and returns the raw `Response` (no throw-on-error, no JSON decode) so callers keep their own 204 / SSE-body / best-effort status handling. See the doc comment at `packages/core/api/client.ts::rawRequest`.

Why bare `fetch("/api/experimental/...")` silently fails in the desktop app:
- **Origin**: a site-relative `/api/...` (or `${window.location.origin}/api/...`) resolves against the renderer document origin (dev `localhost:5173`, packaged `file://`), NOT the bundled backend on `localhost:8090`. There is no vite proxy, no `webRequest` rewrite, and no `<base>` in the renderer, so the request never reaches the server. `baseUrl` prefixing pins it to the API host. On web `baseUrl` is `""` and the Next.js rewrite proxies `/api/*` same-origin, so `rawRequest` is a no-op there.
- **Auth**: desktop runs in token mode (CoreProvider has no `cookieAuth`), so a cross-origin cookie is never attached; the Bearer token from `authHeaders()` is the only working credential. Bare `fetch(..., { credentials: "include" })` fails on both counts.

Converted tabs (0.3.30): `claude-lab-view.tsx`, `forecast-stream-view.tsx`, `mythos-view.tsx`, `llm-wiki-bridge-view.tsx`, `experimental-artifact-view.tsx`, `pythia-report-surface.tsx`.

Exceptions / follow-ups:
- **`use-pythia-sse.ts` must NOT use `rawRequest`.** Its URL comes from `window.experimentalAPI.pythia.getURL()` and points at the loopback Python engine (`http://127.0.0.1:<port>`), not the Multica backend — prefixing `baseUrl` would corrupt it.
- **Native resource loads cannot carry a Bearer header.** Artifact `<img src>` / `<iframe src>` / `<a href download>` still resolve against the renderer origin and will 401/404 on a token-auth desktop build; tracked as a follow-up (needs a blob objectURL fetched via `rawRequest`, or a signed artifact URL). The inline readers in `experimental-artifact-view.tsx` already go through `rawRequest`.

### Pythia engine (0.3.30.3+, full reference port)

`apps/desktop/vendor/pythia-src/engine/` is the source-of-record (mirror of `Pythia-main/engine/`): 18 modules covering the full Pythia surface — `alerts brief config ledger loop mcp models oracle osiris_intake pipeline run runtime server state swarm tickers webhooks world_state`. Wired through `apps/desktop/scripts/bundle-cli.mjs` (deps: `fastapi uvicorn httpx pydantic pydantic-settings mcp python-dotenv`).

**Multica runtime bridge (oracle.py)** — `_complete()` checks `MULTICA_AGENT_RUNTIME_URL` first and POSTs to `<runtime>/api/runtime/llm-call` with `Authorization: Bearer ${MULTICA_API_TOKEN}` + `X-Pythia-Source: pythia-oracle`. Falls back to the original Ollama/OpenAI path when env is unset (standalone dev). `pythia-manager.ts::pythiaRuntimeEnv()` reads the desktop profile's `config.json` and injects both vars into the subprocess env so the engine reaches the user's JWT-authenticated Multica provider chain — Multica is the single source of truth for which model runs.

**Multica world intake (osiris_intake.py)** — `OsirisIntake.fetch()` checks the same env pair and, when set, calls `engine._fetch_multica()` which hits `GET /api/issues` on the Multica runtime and maps each issue row onto a `WorldEvent` via `_to_multica_issue_event`. Lab-tagged issues get a salience bump. Falls back to the original 30+ Osiris feeds (which 404 in desktop mode) when env is unset.

**Multica lifespan (server.py)** — `_is_multica_mode()` short-circuits the Osiris probe loop in `lifespan`: with env set, `hydrate_from_ledger()` + `refresh_world()` + `run_prediction(trigger="boot")` run immediately; with env unset the original "wait for Osiris, then refresh" path runs. The `/health` endpoint already returns OK unconditionally.

**Per-issue forecast endpoint** — the engine exposes `POST /forecast/issue` (`engine/server.py`) that takes `{question, scenario_context, issue_id, horizon, persona, round, seed}` and returns the Go-layer envelope directly: `{scenario, narrative, probability, confidence, horizon, persona, round}`. Reuses `oracle.what_if()` so the LLM prompt + JSON parsing stay identical to the rest of the engine; on LLM failure it falls back to a seeded synthetic envelope so the SSE loop is always coherent. The Multica Go layer's `queryOracleIssue` (in `server/internal/handler/forecast_issue.go`) calls this endpoint; do not change its path.

**Per-issue forecast SSE loop (Go)** — `POST /api/experimental/pythia-oracle/forecast/issue` runs the 10-round deliberation loop, gated by `experimental.DefaultFor("pythia_oracle")` in `router.go`. Rounds clamped by `clampIssueForecastRounds()`: `0 / negative → 10` (default), `> 10 → 10` (cap). Each round blocks on a `/forecast/issue` upstream POST (~3–5 s with a live LLM); 10 × 5 s = 50 s per call. Tested at `server/internal/handler/forecast_issue_test.go::TestClampIssueForecastRounds`.

**Issue creation auto-launch** — `packages/views/modals/create-issue.tsx` checks `labSource === "pythia_oracle"` after the create mutation and POSTs `{rounds: 10}` to the per-issue SSE endpoint. Best-effort (a Pythia failure must not block the create flow). The 10 rounds stream into the issue detail's Pythia panel via the existing IssueLabsSection.

**lab ↔ assignee mutex** — since the 0.3.33 narrowing `pythia_oracle` no longer participates in the mutex (see "Lab ↔ Assignee Mutex" section below): selecting it does NOT clear the assignee, the server auto-rewrites the assignee to the lab leader on a `lab_source` flip (0.3.47, Active Contract #2), and an explicit manual assignee is allowed and wins over the auto-rewrite. Only `mythos_swarm` (sole mode) still reserves the roster.

**Manager proxy allowlist** — `apps/desktop/src/main/pythia-manager.ts::PYTHIA_PROXY_ALLOWLIST` covers 25+ endpoints: write paths (`/whatif /chat /predict /forecast/issue /model /swarm/model /loop /watchlist /alerts /brief/run /brief/config /webhooks`) + read paths (`/agent/view /predictions /world /runs /scorecard /state /links /config /models /swarm/models /personas /watch /drift /alerts/feed /brief`) + parametric prefixes (`/watchlist/{symbol}`, `/alerts/{rule_id}`). Rate limit 30/min/renderer; exceed returns `{ok:false, status:429}`. Renderer calls `window.experimentalAPI.pythia.proxy(path, init)` (returns the parsed body) — never a bare `fetch()` to the loopback URL.

### `FF_*` env override caveat

`server/pkg/featureflag` honors `FF_<FLAG_KEY>` env vars via `NewEnvProvider("FF_")`, but `apps/desktop/src/main/server-manager.ts::serializeEnvFile` only passes `PORT` / `DATABASE_URL` / `JWT_SECRET` / `MULTICA_PUBLIC_URL` to the spawned server. To override a flag at runtime without rebuilding the .app, set the env on the desktop process via `launchctl setenv FF_CLAUDE_SCIENCE_LAB true` and restart `Multica.app` so the new env is inherited by the child server. The `.env` file at `~/.multica/profiles/<name>/.env` is generated but not sourced.

### Priority chain

```
FF_<KEY> env var          (Ops kill switch — highest)
   ↓
experimental_pref row     (per-user override — DB persisted)
   ↓
MULTICA_FEATURE_FLAGS_FILE YAML
   ↓
catalog default           (code constant; experimental flags = false)
```

### Adding a new experiment

1. Add `manifest.json` under `apps/desktop/resources/experiments/<flagKey>/` (use `entry_points.sidebar` for sidebar entries; without this, the flag's UI surface won't show in Labs).
2. Append a `Flag` literal to `Catalog` in `server/internal/experimental/catalog.go` with `ManifestPath`, `RuntimeKind`, `ProxyPrefix`, `LoopbackService`, `Sidebar` (derived from manifest via `SidebarEntries(flagKey)`).
3. Add migration (if needed) and run `make sqlc`. Seed `experimental_resource_visibility` rows for any agents / autopilots / skills / squads the flag should hide by default.
4. If `installable: true`, add `RegisterInstallHandler` in `server/cmd/server/router.go`.
5. If the flag needs a dedicated view, add the route in `apps/desktop/src/renderer/src/routes.tsx` (pre-workspace, prefix `experimental/<slug>`) and the view file under `apps/desktop/src/renderer/src/pages/`. Any network call from that view must use `api.rawRequest` (see "Experimental tab network calls"), never a bare `fetch`.
6. If agents should invoke the flag, add a builtin skill at `server/internal/service/builtin_skills/multica-<name>/SKILL.md`.
7. `bundle-cli` + `electron-vite build` + `electron-builder --dir` → `.app`.

### Mythos Swarm dual-mode (0.3.31)

`mythos_swarm` is the only lab with a **per-issue mode** (`issue.lab_mode` column, migration 157). Two modes:

- **`sole`** (default): mythos 5-agent RDT runner owns the issue end-to-end. Assignee is cleared — the lab owns the roster.
- **`enhancer`**: mythos preludes + supervises; the user picks a target assignee (any agent/squad in the workspace) who executes the plan. After coda synthesis, a **30s supervise goroutine** watches the assignee's progress and writes `mythos_run.supervision_state` / `mythos_members.reflection` rows.

The `LabPicker` renders a second-level TabsList (sole/enhancer) when the selected lab is `mythos_swarm`. The `IssueLabsSection` shows a `MythosEnhancerSupervisePanel` with phase icon, sub-task progress, latest reflection, and a "立即检查" button.

**Supervise infrastructure** (`server/internal/service/mythos/supervise.go`):
- `Service.superviseSet map[pgtype.UUID]context.CancelFunc` — tracks in-flight goroutines per run
- `Service.startSupervise()` — called by `runner.Run()` after coda; flips status to `'supervising'`
- `superviseLoop(ctx, runID, cfg, rootIssueID)` — 30s ticker, max 24h lifetime; writes `supervision_state` JSONB each tick
- `Service.ResumeSupervision(ctx, workspaceID)` — daemon bootstrap recovery: scans for `status='supervising'` rows and re-launches goroutines
- `Service.Stop()` — cancels all in-flight supervises; called from the server shutdown sequence in `cmd/server/main.go` (0.3.68)

**Supervise HTTP surface** (`server/internal/handler/mythos_supervise.go`):
- `GET /api/experimental/mythos-swarm/supervise/{runID}` — read current `supervision_state`
- `POST /api/experimental/mythos-swarm/supervise/{runID}/tick` — trigger an immediate synchronous tick (used by "立即检查" button)
- `GET /api/issues/{id}/mythos-runs` — find recent runs for an issue (used by IssueLabsSection supervise panel)

**Supervise lifecycle**: `PhasePreparing → PhasePlanning → PhaseSupervising → PhaseDone | PhaseAborted | PhaseDegraded`. The backend NEVER touches `runtime.go` — supervise reads issue/comment rows only, writes only mythos_run + mythos_members. Flag-off blocks NEW runs at the HTTP boundary but does NOT cancel in-flight supervise goroutines — those self-terminate on issue terminal status or the 24h cap. `Service.Stop()` is wired to the server shutdown path (`cmd/server/main.go`), not the flag toggle; supervision state persists every tick and `ResumeSupervision` re-adopts `status='supervising'` rows on the next boot, so shutdown cancellation is lossless. The HTTP run path uses the boot-wired `h.MythosService` (0.3.68 fix — previously each request built a throwaway `mythos.NewService` whose superviseSet `Stop()` could never see). **Completion determination (2026-07-28 audit)**: `tickSupervision` flips to `PhaseDone` when the run's `final_issue_id` reaches a terminal issue status (`done`/`closed`/`cancelled`, `isTerminalIssueStatus`), snapping `SubTasksDone` to total. Before the fix nothing ever wrote `SubTasksDone`, so every supervised run polled until the 24h cap.

**Visibility for squads** (0.3.31): `experimental_resource_visibility` CHECK widened from `('agent','autopilot','skill')` to include `'squad'`. The `install_mythos.go` handler seeds 6 visibility rows (5 mythos_* agents + 1 Mythos Swarm squad) so the regular agent/squad pickers never show mythos internals when the flag is off. `squad.go::ListSquads` now calls `filterLabsHiddenByDefault(..., HideSquad, ...)`.

**Database state for dual-mode**: `issue.lab_mode` (`'sole'|'enhancer'|NULL`), `mythos_run.mode` (mirrors issue), `mythos_run.target_assignee` (JSONB `{type,id}`), `mythos_run.supervision_state` (JSONB tick snapshot), `mythos_run.status` CHECK extended with `'supervising'`, `mythos_members.reflection_iter` (INT, written alongside `reflection`).

**Database state for install-time seeding**: `install_mythos.go:upsertMythosVisibility` writes `INSERT OR IGNORE` rows into `experimental_resource_visibility` for the 5 mythos_* agents and the Mythos Swarm squad. The SQL is `ON CONFLICT DO NOTHING` — idempotent across re-installs. Note: agent UUIDs are runtime-derived (the install handler upserts by name), so they are NOT seeded in the migration; the install handler seeds them after the agents exist. The squad UUID is similarly runtime-derived.

### When flag graduation is appropriate

A flag's catalog `DefaultVal` may flip to `true` after the opt-in rate stabilises above a threshold you and the user agree on. Edit the catalog, write a release note explaining the graduation, ship as a regular version bump. Keep the toggle point in place so users who relied on opt-in can still find the override under Labs.

### User Plugin System (0.3.60)

Hard constraint #2 modified: built-in flags remain developer-only; user-created plugins use the `user_*` namespace, stored in the `user_plugin` table (migration 166), merged into the Registry at boot via `RegisterUserPlugins()` + `MergeUserPlugins()`.

**Architecture:** dual-layer catalog — `catalog.go` static `Catalog` slice (8 built-in flags) + dynamic `userPlugins map[string]Flag` guarded by `userPluginMu`. `IsKnownKey()` / `DefaultFor()` / `AllFlagKeys()` check both layers. Built-in flags always win on key collision.

**API endpoints** (all authenticated, no flag gate):
- `GET/POST /api/user-plugins` — list / create (slug: `^[a-z0-9]+(?:-[a-z0-9]+)*$`, 2-64 chars; `flag_key = "user_" + slug`; 409 on duplicate **among live rows** — migration 168 replaced the full-table UNIQUE on slug/flag_key with partial unique indexes `WHERE status != 'deleted'`, so a soft-deleted slug can be re-created)
- `PUT/DELETE /api/user-plugins/{slug}` — partial update / soft-delete (status='deleted' + pref cleanup + Registry removal)
- `GET/POST /api/user-plugins/{slug}/artifacts` — list / upload (multipart or JSON inline)
- `GET /api/user-plugins/{slug}/artifacts/{id}/raw` — serve raw file
- `DELETE /api/user-plugins/{slug}/artifacts/{id}`
- `POST /api/user-plugins/{slug}/run` — execute the plugin runtime (see below)

**Artifact storage:** `~/.multica/plugins/<slug>/artifacts/` — `index.json` (atomic tmp+rename) + files. Seven types: image / chart / table / html / code / text / file.

**Execution runtime** (`user_plugin_runtime.go`, closes the run loop): `POST /run` dispatches on the `runtime_kind` column — `none` → 400, `subprocess` → 501 (reserved upgrade slot), `inline` → runs `python3 -I entry.py` (mirrors `claude_science_runtime.go`; reuses `probePython3`/`kindFromName`/`mimeForKind` + the artifact index helpers, no re-declaration). Missing plugin → 404, non-`active` → 409. Persistent per-plugin env at `~/.multica/plugins/<slug>/env/` (the future container mount point). Code source priority: request body `code` → `manifest.runtime.entry_code` → existing `env/entry.py`. Emitted files are diffed by mtime and ingested as artifacts (mapped png/svg → image / html → html / else → file), copied into the artifact store, and surfaced in the panel's Artifacts tab (client invalidates `["user-plugin-artifacts", slug]`). Run history: `~/.multica/plugins/<slug>/runs.json` (atomic, last 50). Limits reuse the claude constants: 64 KiB code, 30s default / 120s max timeout. `manifest.runtime = {kind, entry_code?, timeout_ms?}` is a pure-additive convention — `normalizeManifest` only validates legal JSON; the `runtime_kind` column stays authoritative. **Hang hardening (2026-07-28 audit):** both `user_plugin_runtime.go` and `claude_science_runtime.go` set `cmd.WaitDelay = 10s` and call `configureRuntimeCmd()` (`runtime_proc_unix.go` / `runtime_proc_windows.go`): on unix the python child runs in its own process group and context-cancel kills the whole group, so a grandchild holding the inherited stdout pipe can neither outlive the run nor keep `cmd.Run()` (and the HTTP handler) blocked forever.

**Container-like environment (on-demand, no Docker):** the run is on-demand (spawned when an agent/user triggers it, never a long-running container) but its `env/` dir persists, giving each lab a private, stateful workspace — Multica ships as a standalone installer, so there is no external Docker dependency. The process env carries the platform contract: `MULTICA_PLUGIN_SLUG`, `MULTICA_PLUGIN_ENV` (= cwd), and `MULTICA_PLUGIN_DB` (= `env/data.db`). The **database is the stdlib `sqlite3` module against that path** — zero install, per-plugin isolated, state accumulates across runs (e.g. a keymap graph a lab stores then renders next run). Ingestion excludes private data via `isIngestableName` (the DB + its `-wal`/`-shm`/`-journal` sidecars, `.sqlite*`, `.pyc`, dotfiles, `entry.py`) so persistent state never leaks into the Artifacts tab. Interactivity is delivered through `html` artifacts rendered in a `sandbox="allow-scripts"` iframe; the same `env/` becomes the mount point when the `subprocess`/container upgrade lands.

**Visibility:** `CreateUserPlugin` seeds `experimental_resource_visibility` rows for agents/squads declared in `manifest.capabilities` — hidden from regular pickers by default.

**Plugin Shell:** `packages/views/experimental/components/plugin-shell-view.tsx` — manifest-driven tabs (chat via `ExperimentalChatPane` / artifacts gallery / table / iframe / code / settings). The `iframe` tab is sandboxed (`sandbox="allow-scripts"` + `referrerPolicy="no-referrer"`, opaque origin — same rule as html artifacts; plugin-authored content must never reach the app's credentials). Desktop route: `/experimental/plugin/:pluginSlug` (`plugin-shell-page.tsx`).

**Built-in skill:** `server/internal/service/builtin_skills/multica-lab-builder/SKILL.md` — teaches agents to first survey the current lab landscape (Step 0: `GET /api/experimental-flags` + `GET /api/user-plugins`, mapped in `references/api-source-map.md`) and then run the full plugin CRUD lifecycle via curl against `http://localhost:8090/api/user-plugins`.

**Issue creation:** `create-issue.tsx` LabPickerRow has a 「创建实验室」button — sets `[实验室创建]` title prefix + the selected agent **or squad** as assignee; the assignee uses the `multica-lab-builder` skill to first survey the current lab landscape (`GET /api/experimental-flags` + `GET /api/user-plugins`) and then create the plugin. Mutually exclusive with LabPicker selection.

**Boot loading:** `router.go` after install-handler registration: `ListActiveUserPlugins` → `UserPluginsToFlags` → `RegisterUserPlugins` + `MergeUserPlugins`. Error is `slog.Warn`, non-fatal.

**Agent-runtime auth bridge (0.3.61, hardened 0.3.63):** the daemon injects `MULTICA_API_TOKEN` (an alias of the task-scoped `mat_` `MULTICA_TOKEN`) into every agent env in `daemon.go`. The `multica experimental …` CLI (`experimentalToken()`/`experimentalAPIURL()` in `cmd_experimental.go`), the `multica lab delegate` CLI (via `resolveToken()` in `cmd_auth.go`, added 0.3.63), and the `multica-lab-builder` skill's raw curl all authenticate via `MULTICA_API_TOKEN` (default base `http://127.0.0.1:8090`), NOT `MULTICA_TOKEN` — without the alias, an agent working a `[实验室创建]` issue would 401 on every `/api/user-plugins` and `/api/experimental-flags` call and the lab-creation loop could never close. Same credential, no privilege expansion. **0.3.63:** `resolveToken()` now honors `MULTICA_API_TOKEN` / `MULTICA_API_TOKEN_FILE` (ahead of the `inAgentExecutionContext()` short-circuit) so `multica lab delegate` works from inside an agent task, and `experimentalAuthToken()` fails loudly when the token is missing inside an agent context instead of firing an unsigned request. All `MULTICA_*` keys — plus `PYTHON*` (0.3.63) — are blocked from user `CustomEnv` override (`isBlockedEnvKey`).

**User-plugin inline runtime sandbox (0.3.60, hardened 0.3.63):** `pluginRuntimeEnv()` in `user_plugin_runtime.go` builds an explicit **minimal** env (`PATH`, `HOME` pinned to the plugin env dir, `LANG`/`LC_ALL`, `MULTICA_PLUGIN_*`) and no longer inherits `os.Environ()`. In desktop co-resident mode the server is the daemon's child and carries `MULTICA_API_TOKEN` + the user's profile-bearing `HOME`; the old `append(os.Environ(), …)` leaked both into every `python3 -I` plugin run (a malicious `entry.py` could read the JWT via `HOME` → `~/.multica/profiles/<name>/config.json` or replay the task token). The minimal env also carries no `PYTHONPATH`/`PYTHONSTARTUP`, closing the module-shadowing / startup-hook vectors that `-I` alone does not.

**Adding a user plugin (agent or API):**
1. `POST /api/user-plugins` with slug, title, description, `trigger_mode` (`"auto"` = self-driven / `"issue_select"` = task-bound), `runtime_kind` (`"none"` / `"inline"` / `"subprocess"`), optional `manifest`.
2. Plugin appears in Labs tab 「用户插件」section and (if `issue_select`) in LabPicker.
3. `trigger_mode: "auto"` sets `HideFromIssueLabPicker: true` — no per-issue selection.
4. Delete = `DELETE /api/user-plugins/{slug}` (soft-delete; visibility rows and pref rows cleaned up).
5. Run an `inline` plugin: `POST /api/user-plugins/{slug}/run` (empty body re-runs persisted `env/entry.py`); artifacts appear in the panel Artifacts tab. See `multica-lab-builder/references/runtime-example.md` for a full walkthrough.

### Two plugin archetypes: tool-lab vs. agent-lab (0.3.63)

The `manifest.capabilities` slots now support two complementary plugin shapes; both are usable by ANY Multica agent, and both are pure user-plugin-layer additions (the 8 built-in labs are untouched).

**Type 1 — tool-lab (no agent, skills auto-bind globally).** A composite of `skills` + optional `autopilots` + optional inline runtime, with empty `agents`/`leader`. Previously `capabilities.skills` was a hollow contract: the docs said "any agent can call them" but a skill only loaded if bound via an `agent_skill` row. Closed in 0.3.63 by **dynamic global injection at task-claim time**:

- `server/internal/service/task.go::LoadAgentSkillsForClaim` replaces the direct `LoadAgentSkills` call on the claim path. It appends, via `appendEnabledPluginSkills` → `enabledPluginSkillNames`, every skill name declared in the `capabilities.skills` of any **enabled** user plugin — so those skills load for **every agent in the workspace** while the plugin flag is on, with no per-agent binding row. Disable the plugin and the injection stops.
- "Enabled" is resolved by the new query `ListEnabledFlagKeys` (`queries/experimental_pref.sql`, `SELECT DISTINCT flag_key ... WHERE enabled = true`). In this single-user fork this is "the user's enabled flags"; the `DISTINCT` keeps it correct if a second user ever exists.
- The skill name must match a real workspace skill row (provision via `multica skill create` first). Missing rows are skipped silently — injection never fails a claim.

**Type 2 — agent-lab (delegation target).** Declares one or more hidden `agents` + a `leader`. Any team/agent can hand it a self-contained sub-task and block for the result via the new CLI verb:

- `multica lab delegate <lab> "<task>"` (`server/cmd/multica/cmd_lab.go`, group `groupExperimental`) — synchronous blocking delegation. `<lab>` is the slug or `user_<slug>` flag key. Flags: `--title`, `--status` (default `todo`; must be non-backlog so the run dispatches), `--timeout` (default 15m), `--poll-interval` (default 3s), `--output` (`json`|`plain`).
- **Zero new server endpoint.** It composes existing primitives: (1) `POST /api/issues` with `lab_source=user_<slug>` — the create path resolves the plugin leader and enqueues via `assignDefaultLabAgent` → `maybeEnqueueOnAssign`; (2) poll `GET /api/issues/{id}/task-runs` to a terminal state; (3) read `result.output` and print it. The delegated run is a normal issue-bound task (full transcript/usage in the UI) — delegation is observable, not a hidden RPC.
- **User-plugin leader resolution** (the backend groundwork that lets a user-plugin lab auto-dispatch like a built-in): `experimental.UserPluginLeader(manifestJSON)` extracts `capabilities.leader`; both `IssueService.resolveLabLeader` (create path) and `handler.(*Handler).resolveLabLeader` (update path) fall through to it for `user_<slug>` keys after the built-in `defaultLabLeaderForKey`/`defaultLeaderAgentForLab` tables miss. `ListAutopilots` filtering was widened to all flag keys so hidden user-plugin resources stay hidden.
- **Prerequisite (fails fast, never hangs):** the target lab must be enabled, declare `capabilities.leader`, and that leader agent must be bound to a running daemon runtime. If no run dispatches within ~30s the command errors with a clear "not dispatched (plugin disabled / no leader / no runtime)" message rather than blocking to `--timeout`.

Full authoring guidance (both archetypes + the delegate verb) is in `multica-lab-builder/SKILL.md`.

## Agent Self-Optimization & Trust (0.5.2)

The `agent_self_optimization` lab (catalog default OFF, per-user opt-in via `experimental_pref`) runs a SkillOpt-style loop: the agent's `agent.instructions` is the trainable state, a separate optimizer LLM proposes bounded add/delete/replace edits, a validator LLM scores them, and only validated edits land back in the instructions. It is the fork's implementation of the user's "智能体自由化循环" (Boris Cherny ablation principle: delete → add back line by line → test).

### Trust-score ledger (migration 228)

- `agent_trust_profile` — per-(workspace, agent) `score` NUMERIC(4,1) init **5.0**, max **10.0**; `review_threshold` default **7.0**; counters (`correction_count`, `review_*_count`). Upserted atomically with SQL-side `+1` on each counter.
- `agent_trust_event` — timeline: `correction` **-0.5** / `review_requested` (0) / `review_pass` **+0.2** / `review_fail` **-0.5** / `review_skipped`. Each row carries `task_id`/`issue_id` anchors, `score_before`/`score_after`, an optional user `note`, and `created_by`.
- `server/internal/service/agent_trust/` — `Service.ApplyCorrection`, `ReviewTask` (LLM verdict via `CLIReviewer` → `RunProviderLLM`), `ProcessTaskCompletion` gate (score < threshold → auto-review). Score arithmetic lives in the service; `pgtype.Numeric` is scanned via string (`fmt.Sprintf("%.1f", v)`) because `Scan(float64)` leaves `Int=nil`.
- HTTP (`server/internal/handler/agent_trust.go`, all membership-gated): `GET /api/experimental/trust/profiles`, `GET /trust/events`, `POST /trust/{agentId}/correct`, `POST /trust/{agentId}/review`.

### Two-stage edit application (migrations 229-231)

`agent_opt_edit` is the SkillOpt edit ledger. `application` ∈ `applied | suggested | rejected | ignored | reverted`; plus `validation_score` NUMERIC, `validation_reason`, `instructions_snapshot` (pre-edit rollback point), `applied_by` (`user`|`auto`, nullable), `corrected_task_id` (traceability anchor).

**Design verdict — destructiveness by construction, not by score:**

- `delete` / `replace` **NEVER auto-apply** — they always land in `suggested` (human confirms any overwrite/removal).
- `add` auto-applies ONLY when ALL hold: `validation_score ≥ 90` + agent enrolled (`【self-opt:enroll】` marker in instructions) + trust ≥ 8 (`MinAutoApplyTrustScore`) + not lab-managed/hard-blocked + correction-backed + rate-capped `MaxAutoAppliesPerAgentPerRun=1`/run + snapshot committed.
- `suggested` (待确认建议 tier): score 60-89, or gate-missed. `ProposeFloor=60`, `AutoApplyGate=90`.
- `rejected`: score < 60. `ignored`: expiry sweep (21d, `SuggestionExpiryWindow`) — **never expire-to-rejected** (a busy user's inaction must not poison the rejection buffer; ignored stays re-proposable).

**Traceability contract (c10, adversarial review):** auto-apply requires a `correction` trust event with a valid `task_id` in the window; that `corrected_task_id` is persisted on the applied edit and the proposal prompt anchors on it (the LLM derives the fix from that specific corrected task). The validator only ever sees **sanitized** issue titles/notes (`sanitizeForPrompt`: strips control chars, truncates to 80 runes) — raw user copy never reaches the optimizer prompt.

**Post-hoc commit gate (d1):** the NEXT run re-scores each applied edit against its `instructions_snapshot` via `Optimizer.RevalidateAppliedEdits`; a regressive edit (score < `RevalidateRetainFloor`=60) is auto-reverted to the snapshot, the ledger row flips to `reverted`, and a `review_fail` trust event (-0.5) is recorded.

**Negative-experience buffer (d6/d7):** `ListNegativeExperienceEdits` returns ONLY `rejected` + `reverted` rows to the optimizer (ignored/applied/suggested stay out). `isRejected` hard-blocks `reverted` pairs too — a user's explicit rollback is never re-proposed.

### Runner + scheduler

- `server/internal/service/agent_self_optimization/runner.go` — one-pass runner. Data-sufficiency deferral (< 5 done issues AND < 3 trust corrections → `status='deferred'` + retry +24h). Parallel per-agent optimization (`sem=3`). **Must** `IncrementIssueCounter` before `CreateIssue` (the self-opt issue carries `lab_source='agent_self_optimization'` and is auto-hidden from the main panel via `exclude_lab`).
- `scheduler.go` — weekly cadence (`MinRunInterval=168h`) with catch-up firing immediately when last run > 7d old. `service.go::maybeFire` holds the advisory lock + `CountActiveAgentSelfOptRuns` in-flight guard.
- HTTP (`server/internal/handler/agent_self_optimization.go` + `self_opt_edits.go`): `GET/POST /self-opt/runs`, `GET /self-opt/runs/{id}`, `POST /self-opt/runs/{id}/cancel`, `GET /self-opt/edits`, `POST /self-opt/edits/{id}/apply|reject|ignore|revert` — all membership-gated; flag off → 404.

### Language contract

The optimizer/validator LLM prompts are **English** (system + rubric + JSON reply). User-facing product copy (report markdown, the 待确认建议 UI, error strings) is **Chinese** with English terms retained.

### Hard-block

The primary product agent (`Multica Helper`) is never enrolled, never auto-applied (`isHardBlockedAgent`); lab-managed agents (hidden by any `experimental_resource_visibility` row) are fail-closed excluded from auto-apply.

## Lab ↔ Assignee Mutex (0.3.31, narrowed 0.3.33; batch + front-end realigned 2026-07-28 audit)

**Current contract (narrowed): the mutex applies to `mythos_swarm` ONLY.**

- `mythos_swarm` + `lab_mode='sole'` (or NULL): manual assignee is rejected — the 5-agent RDT roster owns the issue.
- `mythos_swarm` + `lab_mode='enhancer'`: the mutex is REVERSED — an assignee is REQUIRED (the supervised target). `lab_mode='enhancer'` with any other `lab_source` → 400.
- **Every other lab (built-in or `user_*` plugin): NO mutex.** A manual assignee is a legal combination; when `lab_source` flips and the caller did not pick one, the server auto-rewrites the assignee to the lab's leader (0.3.47, Active Contract #2). An explicit assignee always wins over the auto-rewrite.

Enforced at three layers; all three carry the SAME narrowed gate (pre-audit they had drifted — batch + LabPicker still enforced the old any-lab mutex):

1. **UI (`LabPicker`)** — fires `onClearAssignee` ONLY when picking `mythos_swarm` in sole mode (including switching the mode tab back to sole); every other lab keeps the current assignee and leaves `AssigneePicker` unlocked. `lockedReason` on `AssigneePicker` is set only for mythos sole.
2. **Server (CreateIssue + UpdateIssue)** — mythos sole + assignee → 400 mutex error; mythos enhancer without assignee → 400 "requires an assignee". The gate sits BEFORE `validateAssigneePair` so a non-existent member/agent row never produces a misleading "does not refer to a member" error.
3. **Server (BatchUpdateIssues)** — same narrowed gate, but honouring the batch contract: violations `continue` (per-issue skip), never 400 the whole batch. Batch cannot set `lab_mode`, so enhancer-ness is decided from the persisted `prevIssue.LabMode`; the post-state (lab/assignee) is computed by overlaying the batch fields on the previous row. Tests: `TestBatchUpdateIssuesRespectsLabMutex` (`issue_lab_source_test.go`).

Catalog validation (0.3.26+) is unchanged: `lab_source` must be `experimental.IsKnownKey()` (checks both built-in and user-plugin layers), and it runs before the mutex gate so an unknown lab produces a clean "must match a known experimental flag key" error.

`issue.lab_source` + `issue.lab_mode` schema: `lab_source` nullable TEXT (mig 155), `lab_mode` nullable TEXT CHECK `'sole'|'enhancer'` (mig 157). Both are NULL for non-lab issues. Any new lab that needs per-issue mode semantics must extend the CHECK constraint and the mutex gate — in ALL THREE layers above, plus their pinned tests.

### Per-issue lab workspace (REMOVED in 0.3.38)

**The 0.3.31 inline-rendering layer described below was REMOVED in
0.3.38.** `lab-workspace-panel.tsx`, `LabWorkspacePanel`,
`pickLabInlineView`, `IssueDetailProps.renderLabInline`, and the
desktop `*Inline` view wrappers (`ClaudeLabInline` /
`PythiaInline` / `MythosInline` / `LLMWikiBridgeInline`) all
deleted; `apps/desktop/src/renderer/src/pages/issue-detail-page.tsx`
is now a thin wrapper around `<IssueDetail issueId={id} />`. All
lab surfaces are reachable only via `/experimental/<suffix>`
routes from the sidebar footer or `<IssueLabsSection>`'s
"open panel" link. Pre-0.3.38 dispatch logic
(`/api/experimental/claude-science-labs/dispatch`,
`assignDefaultLabAgentOnUpdate` → `defaultLabLeaderForKey`)
remains live — only the inline UI rendering was retired.

The following architecture description is kept for historical
reference, NOT for implementation guidance:

> The IssueDetail sidebar has a SECOND section under
> `IssueLabsSection` — a `LabWorkspacePanel` that mounts the
> lab's dedicated view (Claude Lab tabs / Pythia report /
> Mythos swarm form / LLM Wiki status) **inline on the issue
> detail page**, pre-scoped to the bound issue.
>
> [`lab-workspace-panel.tsx`] — generic chrome container.
> Takes a `renderInline?: (issueId: string) => ReactNode` prop.
> Flag-off / disabled / missing-renderInline all return `null`
> (silent skip).
> [`issue-detail.tsx`] — accepts a
> `renderLabInline?: (issueId: string) => ReactNode` prop on
> `IssueDetailProps`. Renders `LabWorkspacePanel` below
> `IssueLabsSection` when both `issue.lab_source` and
> `renderLabInline` are set.
> [`issue-detail-page.tsx`] — provides
> `pickLabInlineView(issue.lab_source)` which dispatches to one
> of 4 named inline components.

### 0.3.45 action-type lab (NEW)

A separate entry point **distinct from issue-bound labs**:
`agent_creation_studio`. Lives under
`apps/desktop/resources/experiments/agent_creation_studio/`
(manifest + pre-workspace route `/experimental/agent-creation-studio`).
Mounted from issue detail via `LabPicker.onAction` (footer sub-menu
in the picker popover, NOT in the issue `lab_source` column);
orthogonal to the existing lab/assignee mutex, `IssueLabsSection`,
and `assignDefaultLabAgentOnUpdate`.

- `server/internal/experimental/catalog.go:121-311` — append the
  `agent_creation_studio` Flag literal. `DefaultVal: false`,
  `RuntimeKind: "inline"`, no install handler, no
  `HideableResource`, no `experimental_resource_lock` rows.
- `packages/views/issues/components/pickers/lab-picker.tsx` —
  `onAction?: (actionKey: string) => void` callback wired by the
  caller (issue-detail) to `router.push("/experimental/agent-creation-studio?from_issue=<id>")`.
  Footer is rendered only when `onAction` is supplied; arrow-key
  navigation skips it (built-in `PropertyPicker` footer slot).
- `apps/desktop/src/renderer/src/pages/agent-creation-studio-view.tsx` —
  3-tab orchestrator (agent / skill / squad) calling the existing
  `api.createAgent / createSkill / createSquad` methods directly
  (no new mutation hook, no new sqlc). Pre-workspace route; does
  not write `issue.lab_source`.
- `packages/views/issues/components/issue-detail.tsx:1555` —
  `LabPicker` PropRow hoisted out of the `issue.lab_source &&`
  guard so the action footer is always reachable; the
  `labSourceRouteSuffix` ExternalLink still only renders when
  `issue.lab_source` is set.
- `packages/views/locales/{en,zh-Hans,ja,ko}/experimental.json` —
  new namespace `experimental.agent_creation_studio_view.*`.
  Registered in `packages/views/locales/index.ts` (4 locales)
  and typed in `packages/views/i18n/resources-types.ts`
  (`I18nResources.experimental`).

Constrained to 0.3.45: NO schema migration, NO new sqlc query,
NO new IPC channel, NO new visibility row, NO new agent /
squad / skill. The "与智能体宪法兼容" toggle in each tab is a UI
placeholder only — wiring `system_key` requires an upstream
`agent.system_key` column migration this fork has not yet
shipped (deferred to 0.3.45.1).

## Active Contracts (0.3.45.7+) — Polling fallback, lab leader rewrite, by-issue route order

Three patterns landed in 0.3.45.7 → 0.3.46 that future Claude sessions
must respect when adding or refactoring lab-class surfaces. Each
cost a ship to discover the gap; reproducing the regression is
slower than honouring the contract.

### 1. 5s polling fallback for any lab-class query key (0.3.45.7 → 0.3.45.9)

WS push is the primary freshness signal for `agent_task_queue`,
`autopilot_run`, `mythos_run`, but the push can drop (network blip,
server restart, slow startup) or fire before the renderer mounts.
The 30s `staleTime` is a tab-focus safety net only — it does NOT
catch a WS event that never lands. Apply this pattern to every
lab-class query key that has no WS push:

```ts
refetchInterval: (query) => isLive(query.state.data) ? 5_000 : idleMs
```

Three idle modes depending on WS coverage:

- **WS covers + no idle needed** (autopilot runs): idle = `false`,
  rely on focus + WS only.
- **No WS + tab-cross relevance** (runtime session list): idle =
  `30_000` so externally-created rows still surface.
- **No WS + very low frequency** (self-opt, runs every 4 days):
  idle = `60_000`.

See `agentTaskSnapshotOptions` (`packages/core/agents/queries.ts:36`)
for the canonical implementation and `memory/0.3.45.{7,9}-ship-log-*`
for the lineage.

**0.3.49 documented variant** — `claude-lab-view.tsx:1319` (`claude-lab-runtime-sessions` / Artifact tab) and `:1554` (`claude-lab-code-sessions` / Code tab) use a 15-second idle beat instead of the canonical Mode B 30s. These are deliberate per-issue scoped tabs where the user is actively viewing the Artifact or Code surface and 15s feels right (a fresh `finished` session surfaces within a single reading beat). The Plan tab `:1167` and the workbench context `:361` already follow Mode B canonical 30s. If the per-issue viewing assumption ever changes, switch the two sites to `30_000` and drop this paragraph.

### 2. Lab leader rewrite on `lab_source` flip (0.3.46 P0#4)

`server/internal/handler/issue.go::shouldRewriteAssigneeForLabLeader`
+ `assignDefaultLabAgentOnUpdate` form a 4-case contract that any
new code path flipping `issue.lab_source` MUST honour. The pre-0.3.46
gate `!issue.AssigneeType.Valid` only fired on unassigned issues and
silently broke the common "user picked an agent, then later flipped
to a lab" path.

| Caller intent | Existing assignee | Result |
|---|---|---|
| lab_source untouched | (any) | noop |
| lab_source → no-leader lab (mythos_swarm) | (any) | noop |
| lab_source → leader lab | already leader | noop |
| lab_source → leader lab | missing / non-agent / different agent | **rewrite to leader** |

Leader resolution goes through `resolveLabLeader` — built-in table first, then `experimental.UserPluginLeader` for `user_<slug>` keys (0.3.63), so user-plugin labs auto-dispatch like built-ins.

**BatchUpdateIssues parity (2026-07-28 audit):** the batch path now honours the same contract — when a batch update flips `lab_source` to a leader lab and the request body does NOT explicitly touch `assignee_type`/`assignee_id`, the assignee is rewritten to the leader; an explicit `assignee_*` in the same batch body wins. Pre-audit the batch path skipped the rewrite entirely, leaving lab issues with a stale manual assignee.

Future CreateIssue / workflow-script paths that touch `lab_source` must go through this helper rather than re-derive
the gate. Tests: `TestUpdateIssueLabSource*` in
`server/internal/handler/issue_lab_dispatch_test.go`.

### 3. chi route order — literal slug BEFORE `{param}` (0.3.45.8)

`server/internal/handler/claude_science_runtime.go::RegisterClaudeScienceRuntimeRoutes`
got the `/sessions/by-issue` route registered AFTER `/sessions/{sessionID}`,
and chi fell through to `{sessionID}` capturing the literal
`"by-issue"`. The handler returned 400 "sessionID is not a UUID"
and the Claude Lab 产物 tab rendered "加载 session 失败: by-issue 400".

When adding a new `/sessions/<key>` (or any `/path/<key>`) route
that competes with an existing `{param}` route, the new literal
**must** register FIRST. Comment the order rationale inline so the
next reader doesn't reorder for "alphabetical consistency".

### 4. `lab_managed` DTO marker — hide retired-lab agents/squads from regular pickers (0.3.56)

Hard contract: when an `experimental_resource_visibility` row exists for a flag, ALL agents/squads owned by that flag must be hidden from regular selection surfaces (assignee picker, project lead picker, quick-create issue, squad member picker, issues-header filter, issue-detail subscribers, etc.). Hiding at the catalog level is NOT enough — the row-level filter `filterLabsHiddenByDefault` is one layer; the renderer also gates selection surfaces on `lab_managed`.

Mechanism:

- **Server** — `agent.go::ListAgents` and `squad.go::ListSquads` stamp `lab_managed?: boolean` on each DTO (derived from `experimental_resource_visibility` set-contains, NOT from a column). Schema: `lab_managed: z.boolean().optional().default(false)` in `packages/core/api/schemas.ts`. Types: `packages/core/types/agent.ts` and `squad.ts`.
- **Renderer** — every selection surface (assignee picker, project lead, quick-create, squad member, filter chips, subscribers list) MUST pass `lab_managed: false` to its query hook OR filter the resulting list with `.filter(a => !a.lab_managed)`. Set `disabled` on the trigger + render a Tooltip with `pickers.assignee.lab_managed_tooltip` (4 locales).

Audit methodology when adding a new agent/squad picker: enumerate all `agentListOptions` / `squadListOptions` consumers, classify each as *selection* vs *display*. Selection surfaces without `lab_managed` filtering are bugs. Past audit (0.3.56) caught 6 missed surfaces in one pass — re-audit on any new picker.

**Do NOT remove the `ListAgents` lab_managed stamp even if `useActorName` shares the same query** — display paths need full lists (comment authors, member names), but selection paths need the filter. The cleanest pattern is `useActorName` calling a `with_archived=true, include_lab=true` variant while pickers call the default-filtered variant.

Reference: memory `0.3.56-lab-managed-marker-2026-07-20.md`.

## Known Stability Surfaces

Real failure modes that took non-trivial debugging. NOT obvious from reading the code, so do not skip them when touching the relevant subsystems:

- **Daemon does not auto-start on GUI relaunch when the user is already logged in.** Root cause is a renderer `useEffect(() => window.daemonAPI.autoStart(), [user])` in `apps/desktop/src/renderer/src/App.tsx` — the dependency `[user]` does not "change" on a session where `auth.initialize()` resolves to an existing user, so the IPC is never fired. Symptoms: `agent_task_queue` rows pile up as `queued`, GUI shows "智能体在排队中" / "agents queued". Three source-side fixes are written and typecheck-green (`tryAutoStartFromMain()` + `maybeRecoverDaemon()` in `daemon-manager.ts`, plus the App.tsx useEffect dep change) but **not** in the running 0.3.20 build. System-level mitigation: `~/.multica/scripts/multica-daemon-watchdog.sh` + `~/.multica/scripts/com.multica.daemon-watchdog.plist` (already loaded into `~/Library/LaunchAgents/`, polls every 60s). When the watchdog writes `~/.multica/daemon-needs-spawn.txt`, run `~/.multica/scripts/multica-spawn-daemon.zsh` to bring the daemon back. **Before editing `App.tsx`, `daemon-manager.ts`, or the watchdog scripts, re-read memory `0.2.97-daemon-autostart-regression.md`** — it has the prevention contract.
- **Spawning the multica daemon from a Bash harness kills the daemon when the harness exits.** macOS bash does not support `setsid`; `nohup ... &` is fragile outside of an interactive shell. The only reliable detach on macOS is zsh's `&!` operator (or launchd). Use `~/.multica/scripts/multica-spawn-daemon.zsh` for any manual daemon launch — do not roll your own.
- **code_canvas is auto-mounted via registry (0.3.20+), DO NOT add a manual `setupCodeCanvasManager` in router.go.** `server/internal/handler/experimental_proxy.go::MountExperimentalProxies` walks `h.ExperimentRegistry.ProxyRoutes()` (registry.go:206-227) and auto-mounts every `RuntimeKind=="subprocess"` catalog entry whose `ProxyPrefix` + `LoopbackService` are non-empty. `code_canvas` already fills both in `catalog.go:265-279`, so `router.go:503` `MountExperimentalProxies(r, h)` is sufficient — adding a manual handler would double-register. Before assuming any subprocess flag needs router.go wiring, grep `MountExperimentalProxies` and `ProxyRoutes` first. **0.3.25**: `code_canvas` is `installable: false` in its manifest (it provisions no skills/agents/squads, so there is nothing to install) — do not flip it back to `true` without also registering a `code_canvas_install` handler in router.go, or the install POST returns 404 against a manifest that claims otherwise. Its `run.sh` stub (`resources/code-canvas/run.sh`) is a real 30-line python `/health` server; the only missing piece for a live subprocess is a manifest-driven generic spawner in `manager-factory.ts` (pythia is currently the only subprocess with a dedicated manager).
- **`launchctl bootstrap gui/$UID/...` rejects the multica daemon binary with `OS_REASON_CODESIGNING | embedded signature doesn't match attached signature`** if the binary was hot-patched into `Multica.app/Contents/Resources/app.asar.unpacked/resources/bin/` via `cp`. The codesign manifest in the .app bundle does not match the replaced binary. Fix: repackage the .app via `pnpm --filter @multica/desktop package` (which re-signs) or resign with `codesign --force --deep --sign - Multica.app`.
- **`electron-builder --mac --dir` deadlocks intermittently on macOS 27 (0.3.63, app-builder-bin alpha12).** Process `app-builder-bin` stuck at `unpack-electron` step in `pthread_cond_wait` with 0% CPU. Killing and re-running does not help — deadlocks again. Workaround: manual asar repack (see "Ship chain fallback" section above). Root cause is `app-builder-bin@5.0.0-alpha.12` IPC pipeline; tracked for resolution before 0.3.64. Options: (a) pin to stable 4.x, (b) wait for 5.0.0 stable, (c) keep manual repack as canonical. The manual path was validated end-to-end in 0.3.63 ship.
- **`pnpm --filter @multica/desktop package --dir` does NOT sign nested binaries (0.3.62 ship-blocker).** The `app.asar.unpacked/resources/bin/{multica,server,migrate}` Go binaries ship with ad-hoc signatures from `bundle-cli`, but `electron-builder --dir` only re-signs the top-level `.app` bundle. macOS 27 Gatekeeper treats unpacked binaries as bundle parts and kills any fork+exec with SIGKILL (`exit 137`). Symptom: `Multica.app` cold-starts, the GUI Helper processes come up, but `multica --help` returns 137, daemon.log reports `signal: 'SIGKILL', cmd: '...multica version --output json'`, and the server binary never binds `:8090` because the daemon can't spawn its bundled CLI. Crash reports show `namespace=CODESIGNING indicator=Taskgated Invalid Signature`. Diagnostic trick: `cp <unpacked_binary> /tmp/<bin> && /tmp/<bin> --help` succeeds — the SIGKILL only fires inside `.app/Contents/Resources/app.asar.unpacked/`. **Fix (0.3.66: automated)**: after `cp -R dist/mac-arm64/Multica.app /Applications/`, run `bash scripts/desktop-sign-nested-binaries.sh /Applications/Multica.app` — it signs the app + the 3 nested binaries and self-verifies `multica --help` exits 0 (exits 1 on a 137). It is wired into both the canonical ship chain (step 5a) and the asar-repack fallback (step 6). Do NOT revert to inline `codesign` lines or a doc-only reminder — the manual step was exactly what got skipped. Full prevention contract in memory `multica-0.3.62-codesign-nested-binary-2026-07-23.md`.
- **electron-builder asar `Unable to load ... links out of the package` is a FORK CONFIG regression, NOT a pnpm-linker problem (0.3.63, 2026-07-24).** Symptom: packaging aborts with ~42 out-of-tree symlinks pointing at `../../../node_modules/.pnpm/...`. Root cause is a divergence from upstream: this fork's `apps/desktop/electron-builder.yml` had dropped the `- "!dist/**"` exclude AND `apps/desktop/scripts/package.mjs` had dropped its Step 0 pre-build `rmSync(distDir)`, so a stale multi-GB `dist/` (a prior run's `.app`+DMG+ZIP, whose nested `Electron Framework` symlinks live outside the app root) got repacked into the new `app.asar`. **Fix (official-aligned, applied)**: restore `- "!dist/**"` in `electron-builder.yml` files list + `import { rmSync }` and a Step 0 `rmSync(resolve(desktopRoot,"dist"), {recursive:true,force:true})` in `package.mjs` main(), then delete the stale `dist/`. **ANTI-PATTERN — do NOT set `node-linker=hoisted` in `.npmrc` to make the `.pnpm` symlinks disappear.** It does silence the asar error but breaks two things: (1) electron-builder's electron version detection (electron hoists to root `node_modules/electron`; the detector only looks in `apps/desktop/node_modules/electron` and fails → forces a brittle `-c.electronVersion=<pin>`), and (2) electron-builder's pnpm dependency collector, which then logs `dependency not found on disk` for ~55 runtime deps (commander, execa, marked, @radix-ui/*, …) and ships an app with **missing node modules** that crashes at runtime. Upstream builds fine on the DEFAULT isolated linker (`.npmrc` must stay `shamefully-hoist=true` only, no `node-linker`); electron-builder dereferences the `.pnpm` symlinks into the asar correctly under isolated. Full context in memory `multica-desktop-asar-links-out-hoisted-antipattern-2026-07-24.md`.
- **Bundle-cli "version source"**: `apps/desktop/package.json` is the only version file; `git describe` is tried first and fails silently. Never bump anywhere else.
- **Pre-update snapshot before any DMG rebuild** is mandatory — the script checks `apps/desktop/package.json` version against the running app and refuses to proceed if the data-safety invariants don't hold.
- **Ship chain must run `migrate up` before `bundle-cli`** — the `.app` cold start auto-applies pending migrations, but SQL errors should surface at build time, not first user launch. (Lesson from 0.3.20 ship where migration 153 was missed and had to be applied manually post-install.)
- **PG binary tree wiped on ship + fetcher hangs (2026-07-14, status 0.3.22.1 partial fix).** 0.3.21 ship 后 `~/.multica/pg/17.4/{bin,lib,share,include}` 被清(根因 ship 链具体哪一步仍**未锁死**,候选 ship 链 / launchd watchdog / cleanup 脚本 — ship 链 audit 是 0.3.23 待办),`apps/desktop/src/main/pg-bootstrap.ts:892` 的 `await fetch(url, { signal: AbortSignal.timeout(60_000) })` 已加(0.3.22.1),`cache/<version>.dmg` + `cache/<version>.dmg.sha256` 命中路径已加(0.3.22.1)。`server/internal/handler/auth.go:313` 加 `slog.Warn("auth lookup failed", "error", err, "name", req.Name)` 透 wrapped pgx err 到 slog(0.3.22.1)。`~/.multica/scripts/multica-guard-server.zsh` 加 `SELECT 1` DB 探针,DB-down 与 /health-down 分支分别报(0.3.22.1)。**0.3.22 ship 时落地措施**:brew `postgresql@17` 17.6 symlink 替身 + 写 `VERSION=17.4` 绕过 fetcher → 0.3.22.1 已升级为真二进制(`cp -RL` 固化,备份在 `~/.multica/pg/17.4.bak.<ts>` 30 天后清理)。**Before editing `pg-bootstrap.ts` / ship 链 / `auth.go` / `multica-guard-server.zsh`, re-read `.omc/incidents/2026-07-14-pg-binary-tree-wiped-on-ship.md` + memory `pg-binary-tree-ship-wipe-2026-07-14.md`.** 详见 `0.3.22-ship-2026-07-14.md`(待建) §R1 永久预防 5 项 PR 状态:✅ fetch timeout · ✅ DMG cache · ✅ server-guard DB 探针 · ✅ login 错误透 err · ⏳ ship 链根因锁定。
- **launchd daemon-watchdog silently exits (2026-07-14).** `~/.multica/scripts/multica-daemon-watchdog.sh` 在 `set -u` 下引用未声明的 `$START_TIME` (line 258/259 附近),每次 kickstart 立即 exit 0,launchd 看到 exit 0 停 job → `launchctl print gui/$UID/com.multica.daemon-watchdog` 持续 `state = not running`。修复:line 68 加 `START_TIME="${START_TIME:-$(date +%s)}"`(默认值兜底,0.3.22.1 已修)。诊断技巧:`cat /tmp/multica-daemon-watchdog.launchd.err.log | grep unbound` 是快速定位手段。**Before editing watchdog 脚本,grep `set -u` 后列出所有引用变量,确保每个都有默认值兜底。**
- **Experimental runtime GC never swept (fixed 2026-07-28 audit).** `server/internal/experimental/runtime_gc.go::Run()` called `g.stopOne.Do(func() { close(g.stopped) })` EAGERLY at loop entry instead of `defer`-ring it — the first `select` hit `<-g.stopped` immediately and the GC exited without ever sweeping. Additionally `tarGz` was a placeholder stub, so "archived" runtime dirs were never actually archived. Fixed: the close is deferred, and `tarGz` is a real streaming tar.gz writer (tmp + rename, symlinks skipped). When touching GC-style loops, verify the sweep branch is actually reachable with a test, not by reading the code.
- **Panic flag attribution must survive LIFO defer unwind (fixed 2026-07-28 audit).** `experimental/panic_context.go::WithPanicFlagContext` originally cleared its flag slot in its own `defer` — which runs BEFORE the outer sentinel's `recover` during panic unwind (defers are LIFO), so the blacklist attribution was always empty. Contract: the slot is retained on panic and popped by the sentinel after attribution. Do not "clean up" the slot-clearing back into a defer.

## Memory Index (cross-session)

Before editing any subsystem with a known-regression or regression-suspect surface, read the matching memory file in `~/.claude/projects/-Users-jiangjianyan-jjy-multica-main/memory/` (the session slug keeps the pre-rename `multica-main` name — the project dir was renamed `multica-exploration-dev` on 2026-08-01, but the memory store location is unchanged; do NOT recreate it under a new slug). The full index is in `MEMORY.md` next to the files (one line per memory, descriptive title only).

> **These files live OUTSIDE this repo** (in the Claude project-memory dir above), so a bare name like `multica-0.3.0-standalone-2026-07-02.md` referenced anywhere in this doc is NOT a repo path — `git`/filesystem lookups at the repo root will not find it. Read it via the absolute path above. They are intentionally not committed (per-user, cross-session context).

**For a new session, start here:**
- `0.5.2-self-opt-ship-2026-08-01.md` — **Current release (2026-08-01).** Agent self-optimization loop: trust-score ledger (mig 228), two-stage edit application (migs 229-231, add-only auto-apply + 待确认建议 tier), post-hoc commit gate (`RevalidateAppliedEdits`), and 7 adversarial-review fixes. **Desktop ship complete** — `/Applications/Multica.app` = 0.5.2 (canonical `electron-builder --dir`; the alpha.13 deadlock did NOT trigger this run). P0 gotcha: `applied_by NOT NULL CHECK` broke the entire suggested tier (fixed nullable + `sqlc.narg`). `pgtype.Numeric` must string-scan. Read before any 0.5.2.x touch. Supersedes the 0.5.1 entry below.
- `0.5.1-ui-port-ship-2026-08-01.md` — 0.5.1 (2026-08-01, superseded by 0.5.2). Upstream UI/animation port batch: 5 commits (`c065ae1` `404676a` `bb29b46` `1b0cdb5` `a803f94`) — WCAG contrast + faint/find-match/chat-launcher tokens, Button brand variants, CJK `font-synthesis`, surface system bound, type-scale tokens, NumberFlow + 6 surfaces, 14 animation deltas, Inter italic + Geist Mono variable, 212-file type-scale migration. Desktop ship via manual asar-repack fallback. Worktrees for the 4 fork-hygiene cloud-deletion PRs were **deleted** (branches kept). Main dir renamed `multica-main` → `multica-exploration-dev`.
- `0.5.0-fork-ship-2026-07-31.md` — 0.5.0 release (schema-first wave-1: `client_usage_daily` + `task_usage` cost). Superseded by 0.5.1; kept for the wave-1 schema/rollup history.
- `project-init-doc-2026-07-14.md` — Full fork snapshot for 0.3.20 (still useful for high-level architecture; some flag / manifest details are superseded by 0.3.22+).
- `0.3.24-ship-2026-07-15.md` — Forecast SSE + interactive-chart. Read before any 0.3.25+ version bump or DMG rebuild.
- `0.3.22-ship-2026-07-15.md` — Claude Research Lab consolidation ship log. Read before touching the `claude_science_lab` flag, install handler, or any consolidated lab route.
- `0.3.30.2-ship-2026-07-16.md` — Labs runtime fixes (Pythia `already started` + Claude Lab `Failed to fetch` + Pythia `预测神谕` → `多视角推演` rename). **Critical**: discovered 0.3.30.1 ship had not actually replaced renderer asar (`pnpm build` does NOT run electron-builder) — `pnpm exec electron-builder --mac --dir` is now a mandatory ship step. **Cold-start three-check does NOT catch renderer fetch failures**; verify with `grep -c rawRequest app.asar`.
- `0.3.31-ship-2026-07-16.md` — Mythos swarm dual-mode (sole + enhancer). Adds `issue.lab_mode` column, mythos supervise goroutine, squad visibility gating, 4-language i18n for mythos enhancer UI. **Read before touching**: `service/mythos/runner.go` (Mode/Target/Extension/Reflection/startSupervise), `service/mythos/supervise.go` (superviseLoop/tickSupervision/ResumeSupervision/Stop), `handler/mythos_supervise.go` (GET state/POST tick/GET runs-by-issue), `handler/install_mythos.go` (upsertMythosVisibility), `experimental/visibility.go` (HideSquad), `handler/squad.go` (filterLabsHiddenByDefault), `handler/issue.go` (lab_mode in CreateIssue/UpdateIssue), `views/issue-detail.tsx` (labMode prop), `views/lab-picker.tsx` (sole/enhancer tabs), `views/issue-labs-section.tsx` (MythosEnhancerSupervisePanel), `migrations/157_mythos_dual_mode.*`.
- `0.3.45.8-ship-log-2026-07-19.md` — Claude Lab UX cleanup: by-issue route (chi 路由顺序 lesson) + LabPicker 隐藏基础设施/自驱动 flag + Claude Lab 视图删 LabAgentLockBar + ExecutionLogSection autoOpen 最新 past run transcript. Read before touching `claude_science_runtime.go` route order, `catalog.Flag.HideFromIssueLabPicker`, or any lab-view agent wiring.
- `0.3.45.9-ship-log-2026-07-19.md` — 5s polling fallback 通用模式(autopilot / squad member / runtime session / self-opt run 4 个 sibling query key);3 idle 决策模式(false / 30s / 60s)。Read before adding any lab-class query key without WS coverage.
- `0.3.46-ship-log-2026-07-19.md` — **P0#4** lab leader rewrite:`shouldRewriteAssigneeForLabLeader` + `assignDefaultLabAgentOnUpdate` 4-case 决策表;Mythos enhancer 不动;4 tests 全过。Read before touching any code path that mutates `issue.lab_source`.
- `0.3.56-lab-managed-marker-2026-07-20.md` — `lab_managed` DTO marker contract: server stamps `lab_managed?: boolean` on `Agent`/`Squad` DTOs derived from `experimental_resource_visibility`; every selection surface MUST filter. Audit methodology + 6 missed surfaces catalogued. Read before adding ANY new agent/squad picker.
- `0.3.57-ship-2026-07-22.md` — constitution_agent retirement (migration 165). Read before re-introducing any charter/constitution agent or autopilot.
- `0.3.58-ship-2026-07-22.md` — UI cherry-picks (5 surface-system tokens / sidebar resize cursor / 8 reduced-motion opt-outs / in-page find highlight / horizontal scroll-fade axis). Pure CSS + hook work.
- `0.3.59-ship-2026-07-22.md` — Cleanup (lab-badge icon consistency + delete 2 placeholder manifests).
- `0.3.61-ship-2026-07-23.md` — Squad-as-subscriber / squad-as-recipient schema fix (migration 167): widens `issue_subscriber.user_type` + `inbox_item.recipient_type` CHECK to allow `'squad'`; relaxes `agent_task_queue_accountable_matches_originator` to allow both columns independently nullable. Verified upstream `/Users/jiangjianyan/Downloads/multica-main` has identical bug (zero-byte diff on `subscriber_listeners.go` / `notification_listeners.go`), so this is fork-local patch.
- `0.3.62-ship-2026-07-23.md` — Consolidated 0.3.60 user-plugin runtime closure + 0.3.61 squad-subscriber schema fix into one 0.3.62 ship (3 atomic commits, 47 files). **Critical**: ship-blocker discovered during this session — macOS 27 Gatekeeper kills `app.asar.unpacked/resources/bin/*` with SIGKILL because `electron-builder --dir` only signs the top-level bundle; manual `codesign --force --sign -` on each nested binary is now a mandatory ship step. Plus: `TestMainRouterDoesNotExposePrometheusMetrics` panic from `db.New(nil)` returning non-nil struct with nil inner DBTX — fixed via defer/recover guard around the 0.3.60 boot-time user-plugin loader.

**Per-subsystem memory (read before touching the relevant code):**
- `multica-0.3.0-standalone-2026-07-02.md` — P0 destructive-migration incident; read before touching `pg-bootstrap.ts` / `server-manager.ts` migrate logic.
- `multica-0.3.1-stability-fix-2026-07-03.md` — 4 P1 fixes (in-flight cache, stopping flag, probeMulticaPg 4th field, sentinel O_EXCL).
- `multica-0.3.2-stability-fix-2026-07-06.md` — Agent not-continuous fix; Path D modulePreload; asarUnpack embedded-postgres; row-parity baseline drift.
- `0.3.3-audit-and-fixes-2026-07-12.md` — Audit grading (78/100) + 5 P1 fixes.
- `0.3.4-chat-mul4351-and-ui-2026-07-12.md` — MUL-4351 chat_input_task_id + IM unread count + agent_intro + chat pin. `CUSTOM_DMGBUILD_PATH` bypass pattern established.
- `0.3.5-squad-and-search-fixes-2026-07-12.md` — squad_creator_scope 5-handler gate + search_timeout 5s + 504.
- `0.3.6-experimental-flags-plan-2026-07-12.md` — First Labs framework (kept for history; superseded by 0.3.19 platform).
- `multica-labs-platform-blueprint-2026-07-14.md` — 0.3.19 10-PR Labs platform blueprint; manifest contract, ExperimentRegistry, RuntimeKind dispatch, IPC namespacing, safety auto-mount.
- `0.3.18-labs-safety-net-2026-07-14.md` — panic/5xx_burst/init_timeout → blacklist; `DefaultFor` chokepoint.
- `0.3.19-pythia-dynamic-view-2026-07-14.md` — Pythia engine SSE/state-stream, WhatIf panel, IPC proxy.
- `0.3.19-experimental-platform-dev-2026-07-14.md` — `claude_science_runtime` + `llm_wiki_bridge` flags.
- `labs-flag-enable-breaks-2026-07-14.md` — Root cause of "Labs flag enabled but plugin doesn't work"; 5-step fix pattern.
- `constitution-agent-plugin-2026-07-14.md` — `constitution_agent` flag; 4 sync points (catalog/visibility/handlers/service); archive contract.
- `0.2.97-daemon-autostart-regression.md` — Daemon auto-start prevention contract.
- `multica-ipc-registration-order.md` — Never double-register the same IPC channel.
- `daemon-default-profile-pickup.md` — Empty profile dirs make the daemon pick the wrong URL.
- `multica-fork-vs-upstream-divergence-map.md` — Canonical "what to keep / what to cherry-pick" checklist for upstream updates.
- `multica-dmg-replace-chown-pitfall.md` — Why `chown -R $USER:admin` is broken in zsh sandbox.
- `multica-launch-via-open.md` — App verification via `osascript` (not `open --version`).
- `pre-update-data-safety.md` — `~/.multica/scripts/pre-update-snapshot.sh` mandatory before DMG rebuild.
- `multica-version-upgrade-compat.md` — DB volume / config / workspace upgrade immutability contract.
- `multica-0.3.62-codesign-nested-binary-2026-07-23.md` — `pnpm package --dir` does NOT sign unpacked Go binaries; macOS 27 Gatekeeper kills fork+exec with SIGKILL. Re-sign step must run between `cp -R` and `verify-desktop-cold-start.sh`. Read before any 0.3.62+ ship.
- `multica-desktop-asar-links-out-hoisted-antipattern-2026-07-24.md` — asar "links out of the package" is a fork config regression (dropped `!dist/**` + `package.mjs` dist pre-clean), NOT a pnpm-linker problem. `node-linker=hoisted` is an anti-pattern: it breaks electron version detection + the pnpm dep collector. Keep `.npmrc` isolated (`shamefully-hoist=true` only). Read before touching `.npmrc`, `electron-builder.yml`, or `package.mjs`.
- `0.3.63-ship-2026-07-24.md` — Security hardening (pluginRuntimeEnv minimal env, resolveToken MULTICA_API_TOKEN, isBlockedEnvKey PYTHON*, squad notification filter) + electron-builder deadlock workaround (manual asar repack). Read before any 0.3.64+ ship or security audit of user-plugin sandbox.
- `.omc/release-notes-0.3.64.md` (2026-07-28 labs audit) — 9 high-severity labs/plugin fixes: batch+LabPicker mutex realigned to the 0.3.33 narrowed semantics, batch leader-rewrite parity, experimental runtime GC defer bug (GC never swept) + real tarGz, panic flag attribution (LIFO defer vs sentinel recover), runtime pipe-hang hardening (WaitDelay + process-group kill), migration 168 partial unique slug index, mythos supervise completion, plugin-shell iframe sandbox. **3 tests were pinning the old broken behavior and were fixed alongside** — when a contract changes, grep its tests for pinned assertions. Read before touching the lab mutex gate, batch issue updates, runtime GC, or supervise.

## Domain Reminders

- All queries filter by `workspace_id`; membership gates access; `X-Workspace-ID` selects the workspace.
- Issue assignees are polymorphic: `assignee_type` plus `assignee_id` can reference a member or an agent.
- **Mythos swarm has 5 agents**: `mythos_prelude` (leader) + 3 loop members (`mythos_loop_coder`, `mythos_loop_researcher`, `mythos_loop_analyst`) + `mythos_coda`. The `mythosAgents` array in `install_mythos.go` and the `RosterCard` in `mythos-view.tsx` must stay in sync. 0.3.31: the 5 mythos agents + the Mythos Swarm squad are hidden from regular agent/squad pickers via `experimental_resource_visibility` rows seeded by `upsertMythosVisibility` in `install_mythos.go`. The visibility rows are inserted `ON CONFLICT DO NOTHING` — re-install is idempotent. The squad filter path is `squad.go::ListSquads` → `filterLabsHiddenByDefault(..., HideSquad, ...)` (added 0.3.31, the last Labs list endpoint to get visibility gating).
- **Mythos supervise goroutine lifecycle**: `Service.Run` launches a per-run supervise goroutine for enhancer-mode runs. The goroutine writes `mythos_run.supervision_state` every 30s tick and self-terminates at 24h max lifetime. Daemon bootstrap (`newMythosService` in `router.go`) calls `ResumeSupervision` for every workspace to recover orphaned goroutines after a restart. Flag-off cancels all in-flight supervises via `Service.Stop()`. **Do NOT add any flag gating inside the supervise goroutine itself** — the goroutine reads the issue's current `lab_mode` from the run row; if the user flips the flag off after a run started, the goroutine exits cleanly via the cancel func.
- **Issue `lab_source` column** (nullable TEXT, added migration 155) + **`lab_mode` column** (nullable TEXT, added migration 157, CHECK `'sole'|'enhancer'`). `lab_source` associates an issue with an experimental lab flag key. `lab_mode` is currently meaningful only for `mythos_swarm` — `'sole'` means the lab owns the issue end-to-end; `'enhancer'` means the lab preludes + supervises while the user-picked assignee executes. The `LabPicker` component renders a sub-tab (sole/enhancer) when `labSource === 'mythos_swarm'`. When adding a new lab with per-issue mode semantics, extend the CHECK constraint in a migration and add the mode handling to the lab's runner + frontend picker.
- **Explicit-column-list queries in `queries/issue.sql`**: `ListIssues`, `ListOpenIssues`, `CreateIssue`, and `CreateIssueWithOrigin` enumerate columns manually (they omit heavy fields like `acceptance_criteria`, `context_refs`). When adding a new column to `issue` table, update ALL of these SELECTs/INSERTs + their generated Row structs + Scan/args calls. Other queries use `SELECT *` / `RETURNING *` and are handled automatically by `sqlc generate`.
- **User plugin flag keys** always carry the `user_` prefix (`plugin_scanner.go::IsUserPluginKey()`). `GET /api/experimental-flags` returns user plugins with `is_user_plugin: true` — the Labs tab and LabPicker use this to distinguish them from built-in flags. User plugin `DefaultVal` is always `false` (opt-in). Deleting a user plugin soft-deletes the DB row (`status='deleted'`), removes the flag from the in-memory Registry, and cleans up the caller's `experimental_pref` row. The `user_plugin` table (migration 166) enforced `slug` and `flag_key` UNIQUE at the column level; migration 168 (2026-07-28 audit) converted both to **partial unique indexes scoped to live rows** (`WHERE status != 'deleted'`) so a soft-deleted slug can be re-created — all `user_plugin.sql` queries already filter `status != 'deleted'`, and the create handler's 23505 → 409 mapping is unchanged. Slug is immutable after creation.
- **Server log lives at `~/.multica/profiles/<profile>/server.log`, NOT `~/.multica/server.log`.** `server-manager.ts::serverLogPath()` writes stdout+stderr into the profile dir. The legacy `~/.multica/server.log` (if any) is from a pre-0.3.0 dev run and stays frozen at its last mtime. Always diagnose ship-post behavior from the profile-local log; the daemon/CLI/desktop activity you want to see lives there.
- **Squad-as-subscriber / squad-as-recipient schema — migration 167 (0.3.61)** extended `issue_subscriber.user_type` and `inbox_item.recipient_type` CHECK constraints to allow `'squad'`, and relaxed `agent_task_queue_accountable_matches_originator` so both columns are independently nullable (only equal-required when both set). The fix matches the upstream latent bug present in `/Users/jiangjianyan/Downloads/multica-main` (verified by zero-byte diff on `subscriber_listeners.go` / `notification_listeners.go`). When reviewing or writing squad assignee paths, schema no longer blocks; if you discover a new place that should *not* subscribe/notify a squad (e.g. squad-as-recipient showing up in a personal inbox), filter at the handler layer — do NOT re-tighten the CHECK constraints and re-introduce the upstream regression. **0.3.63:** the handler-layer squad filter has now landed — `subscriber_listeners.go` skips `*issue.AssigneeType == "squad"` in both the `issue:created` and `issue:updated` assignee-subscription paths, and `notification_listeners.go::notifyDirect` early-returns on `recipientType == "squad"`. This prevents squad-routed subscriber/inbox rows from surfacing in a human member's `ListInbox`. Squads still receive task dispatch via the queue path (unaffected); only the personal-inbox subscription/notification fan-out is short-circuited.
- **`AgentCreationStudioView` / `plugin-shell-view.tsx` are distinct surfaces.** The studio (route `/experimental/agent-creation-studio`) is for ad-hoc creation of agent/skill/squad rows through the existing REST APIs — no new mutation hook, no new sqlc, no schema column. The plugin shell (route `/experimental/plugin/:pluginSlug`) is for `user_*` lab plugins with manifest-driven tabs. Don't conflate them when routing new lab work; the studio edits *built-in* schema, the shell renders *user* artifacts.