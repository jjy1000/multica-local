# Backend Rules (server/)

Go backend for Multica: Chi router, sqlc, gorilla/websocket. Read this before
touching `server/`. Cross-cutting product rules (state model, package boundaries,
ship chain, desktop lifecycle) live in the root `CLAUDE.md`; this file is the
nearby guide for backend boundaries, commands, and pitfalls so you don't have to
read the whole root file to work here.

## Layout

- `cmd/` — entrypoints. `server` (HTTP + WS), `multica` (CLI), `migrate`
  (forward/back SQL), plus `backfill_*` one-shots.
- `internal/handler/` — HTTP/WS handlers (request boundary; UUID rules below).
- `internal/service/` — business services (`mythos/`, autopilot, builtin skills…).
- `internal/experimental/` — Labs catalog/registry/visibility/locks (see below).
- `internal/{auth,daemon,daemonws,events,realtime,scheduler,skill,storage,util,middleware}/`.
- `pkg/` — reusable across binaries: `agent`, `db` (sqlc output), `featureflag`,
  `protocol`, `redact`, `skillbundle`, `taskfailure`.
- `migrations/` — forward-only SQL. `sqlc.yaml` drives codegen into `pkg/db`.

## Localized fork contract (do NOT re-add)

- **No telemetry.** `analytics.NewFromEnv()` always returns `NoopClient{}`. The
  PostHog file (`internal/analytics/posthog.go`) is deleted — do not restore it.
- **No Google OAuth / email verification.** `SendCode`, `VerifyCode`, and
  `GoogleLogin` handlers return `410 Gone`. Only `UsernameLogin`
  (`POST /auth/login {"name":"..."}`) works.
- **Username-only login upserts a new user on every unseen name.** Workspace
  membership binds to the *creator* `user_id`. Do NOT "fix" this by auto-binding
  to existing workspaces — see `.omc/incidents/2026-06-27-username-only-login-loses-workspaces.md`.

## Commands

```bash
make server          # run the Go server only
make test            # Go tests
make sqlc            # regenerate sqlc code after editing migrations/*.sql or queries/*.sql
make check           # broader verification

# Single Go test (run from server/)
cd server && go test -run TestName -count=1 -timeout 60s ./internal/handler/

# Apply pending migrations (run from server/)
cd server && go run ./cmd/migrate up

# After editing internal/handler/reserved_slugs.json (run from repo root)
pnpm generate:reserved-slugs   # regenerates packages/core/paths/reserved-slugs.ts — commit both
```

Go style: `gofmt`, `go vet`, checked errors, English comments. For internal,
non-boundary code do not add compatibility layers, fallback paths, dual writes,
or shims unless explicitly requested; prefer removing a replaced path.

## UUID rules (internal/handler/)

Always know where a UUID came from before using it in a write query:

- Path params that may be UUIDs *or* human-readable IDs → resolve via loaders
  (`loadIssueForUser`, `loadSkillForUser`, `loadAgentForUser`,
  `requireDaemonRuntimeAccess`); writes use the resolved `entity.ID`.
- Pure UUID inputs from a request → `parseUUIDOrBadRequest(w, s, fieldName)`,
  return immediately on `ok=false`.
- Trusted round-trips (sqlc results, fixtures) → `parseUUID(s)` (panics on bad input).
- Outside handlers → `util.ParseUUID(s) (pgtype.UUID, error)`; always check the error.

## Schema / query pitfalls

- **Migrations are forward-only and additive.** Never drop a table or column in a
  migration; new columns need defaults. `.app` cold start auto-applies pending
  migrations, so run `migrate up` before packaging to surface SQL errors early.
- **Explicit-column-list queries in `queries/issue.sql`** (`ListIssues`,
  `ListOpenIssues`, `CreateIssue`, `CreateIssueWithOrigin`) enumerate columns
  manually and omit heavy fields. When adding an `issue` column, update ALL of
  these SELECTs/INSERTs + their Row structs + Scan/args. `SELECT *` /
  `RETURNING *` queries are handled by `sqlc generate` automatically.
- Every query filters by `workspace_id`; membership gates access; `X-Workspace-ID`
  selects the workspace. Issue assignees are polymorphic (`assignee_type` +
  `assignee_id` → member or agent).

## Labs / experimental (internal/experimental/ + internal/handler/)

Full cross-layer architecture (manifest → catalog → registry → IPC → proxy →
sidebar) is in the root `CLAUDE.md` "Labs Platform" section. Backend rules:

- **Catalog is developer-only** (`experimental/catalog.go`). Users cannot create
  built-in flags; user plugins use the `user_*` namespace (`user_plugin` table).
- Flag gating goes through `experimental.DefaultFor("<key>")` in
  `cmd/server/router.go` and `experimental.IsKnownKey()`. Both the static
  `Catalog` and dynamic `userPlugins` are checked; built-ins win on key collision.
- **chi route order: literal slug BEFORE `{param}`.** A `/sessions/by-issue`
  registered after `/sessions/{sessionID}` makes chi capture `"by-issue"` as the
  param (400 "not a UUID"). Register the literal first and comment the rationale.
- **Lab ↔ assignee mutex (server layer).** `CreateIssue` / `UpdateIssue` /
  `BatchUpdateIssues` reject `lab_source` + manual assignee with
  `400 "lab_source and assignee are mutually exclusive"`, gated BEFORE
  `validateAssigneePair`. Exception: `mythos_swarm` `lab_mode='enhancer'` REVERSES
  it (assignee required). `lab_source` (mig 155) and `lab_mode` (mig 157, CHECK
  `'sole'|'enhancer'`) are nullable TEXT.
- **Lab leader rewrite.** Code paths that flip `issue.lab_source` must go through
  `handler/issue.go::shouldRewriteAssigneeForLabLeader` + `assignDefaultLabAgentOnUpdate`
  (4-case contract; tests in `issue_lab_dispatch_test.go`), not a re-derived gate.
- **`lab_managed` DTO stamp.** `agent.go::ListAgents` / `squad.go::ListSquads`
  derive `lab_managed?: boolean` from `experimental_resource_visibility` (not a
  column). Row-level `filterLabsHiddenByDefault` is one layer; selection surfaces
  also gate on `lab_managed`. Do NOT remove the stamp even when `useActorName`
  shares the query — display paths need full lists, selection paths need the filter.
- **`FF_<KEY>` env override** is honored by `pkg/featureflag` via
  `NewEnvProvider("FF_")` but the desktop server child only inherits it through
  `launchctl setenv FF_<KEY> true` (the profile `.env` is generated, not sourced).
- **User-plugin skills auto-bind globally (0.3.63).** The claim path calls
  `service/task.go::LoadAgentSkillsForClaim` (NOT `LoadAgentSkills` directly): it
  injects every `capabilities.skills` name from each *enabled* user plugin into
  every agent's bundle, via `enabledPluginSkillNames` + `ListEnabledFlagKeys`
  (`queries/experimental_pref.sql`). No `agent_skill` row is written; disabling
  the plugin stops the injection. Missing skill rows are skipped, never erroring
  the claim. This makes a no-agent "tool-lab" a shared capability pack.
- **User-plugin leader resolution + `lab delegate` (0.3.63).** `user_<slug>` labs
  auto-dispatch like built-ins: `experimental.UserPluginLeader(manifestJSON)`
  reads `capabilities.leader`, and both `IssueService.resolveLabLeader` (create)
  and `handler.(*Handler).resolveLabLeader` (update) fall through to it after the
  static `defaultLabLeaderForKey`/`defaultLeaderAgentForLab` tables miss. The
  `multica lab delegate <lab> "<task>"` CLI (`cmd/multica/cmd_lab.go`) is
  pure-CLI (create lab issue → poll `task-runs` → read `result.output`) — add NO
  new server endpoint for it.

## Retired (do NOT re-add)

- `constitution_agent` lab (retired 0.3.57, migration 165). If upstream re-adds a
  constitution lab, do not cherry-pick it.
- `claude_science` / `claude_science_runtime` flags (removed 0.3.22; folded into
  `claude_science_lab`). Reserved workspaces for labs are removed — lab resources
  are isolated by `experimental_resource_lock` + visibility rows only.

## Keeping skills in sync

When you change CLI commands/flags, API fields, or product behavior documented by
built-in skills under `internal/service/builtin_skills/*`, update the relevant
`SKILL.md` and `references/*-source-map.md` in the same PR.

## Before touching a regression-suspect surface

Read the matching memory file in
`~/.claude/projects/-Users-jiangjianyan-jjy-multica-main/memory/` (index in
`MEMORY.md`). Backend-relevant examples: `0.3.31-ship-*` (mythos supervise),
`0.3.46-ship-log-*` (lab leader rewrite), `0.3.45.8-ship-log-*` (chi route order),
`0.3.56-lab-managed-marker-*`, `0.3.61-ship-*` (squad subscriber schema, mig 167).
