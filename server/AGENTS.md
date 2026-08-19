<!-- AUTO-SYNCED MIRROR of ./CLAUDE.md (the source of truth for this directory). Edit CLAUDE.md, then regenerate this file; parity is enforced by scripts/check-agents-docs-sync.mjs. -->

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
- **Lab ↔ assignee mutex (server layer) — `mythos_swarm` ONLY (narrowed 0.3.33).**
  `CreateIssue` / `UpdateIssue` / `BatchUpdateIssues` reject `mythos_swarm` sole
  mode + manual assignee with `400 "lab_source and assignee are mutually
  exclusive"`, gated BEFORE `validateAssigneePair`; `lab_mode='enhancer'`
  REVERSES it (assignee required). Every other lab (built-in or `user_*`) has NO
  mutex — assignee + lab is legal, and the leader auto-rewrite fills the gap.
  Batch violations `continue` per-issue, never 400 the whole batch. `lab_source`
  (mig 155) and `lab_mode` (mig 157, CHECK `'sole'|'enhancer'`) are nullable TEXT.
- **Lab leader rewrite.** Code paths that flip `issue.lab_source` must go through
  `handler/issue.go::shouldRewriteAssigneeForLabLeader` + `assignDefaultLabAgentOnUpdate`
  (4-case contract; tests in `issue_lab_dispatch_test.go`), not a re-derived gate.
- **Lab auto-dispatch opt-out (0.5.22).** `experimental.Flag.AutoDispatch *bool`
  flags a catalog entry to skip the service-layer auto-dispatch path
  (`IssueService.maybeEnqueueOnAssign` in service/issue.go + `WillEnqueueRun`
  in service/issue_trigger.go — the single chokepoint for UpdateIssue +
  BatchUpdateIssues). Nil/true = unchanged 0.3.46 behaviour; **false**
  (currently only `claude_science_lab`) means the assignee is still
  written (leader-rewrite still applies) but no `agent_task_queue` row
  is created until the user explicitly clicks "Run research" on the lab
  workbench's IssueContextBar (`apps/desktop/.../claude-lab-view.tsx`).
  That trigger fires via
  `POST /api/experimental/claude-science/issues/{id}/run`
  (handler/claude_science_run.go), which calls
  `TaskService.EnqueueTaskForIssue` directly — bypassing both service
  gates because the endpoint IS the manual opt-in. The route is mounted
  inside the existing `RequireExperimentalFlag("claude_science_lab")`
  chi group in cmd/server/router.go. Reading code goes through
  `experimental.AutoDispatch(key)` (true when pointer is nil/unknown,
  returns the dereferenced value otherwise). Adding a second opt-out lab
  is a 3-line catalog edit — no service-layer or router changes needed.
  Tests: `TestAutoDispatchFlagBehavior` in
  `internal/experimental/registry_test.go`.
- **Experimental runtime GC for `experimental_claude_runtime_session` (0.5.25).**
  `experimental.RuntimeGC` is the 30/90/120-day retention ladder for claude
  science research sessions (migration 151). Three contracts: (1) `Run()`
  never closes `g.stopped` itself — `Stop()` owns the close-once contract
  via `stopOne`. The pre-0.5.25 code called `g.stopOne.Do(close(g.stopped))`
  *eagerly* at the top of `Run()`, so the first `select` evaluated
  `<-g.stopped` immediately and the GC exited without ever ticking.
  (2) `Start()` is wired at boot in `cmd/server/router.go` alongside
  `swarm_gc.Start()`. Pre-0.5.25 it was orphaned — the comment "parallel
  to runtime_gc.Start pattern" had never been realized on the runtime_gc
  side. Store the GC on `Handler.RuntimeGC` so `cmd/server/main.go`'s
  shutdown can call `Stop()` before SIGKILL. (3) The loop body is
  regression-pinned by `TestRuntimeGC_RunSweepsBeforeExit` (asserts
  `sweepCount >= 2` in 60ms at 20ms Interval; FAILS on pre-fix code
  with "got 0 sweep invocations", PASSES with fix). `sweepCount` is an
  `atomic.Uint64` field on `RuntimeGC` that `sweep()` increments —
  production ignores it, tests read it. **Before touching this GC:**
  read memory `0.5.25-runtimegc-fix-2026-08-17.md` and add a sweep-
  execution assertion to the test (do not assume the loop body runs
  just because the code looks right).
- **`lab_managed` DTO stamp.** `agent.go::ListAgents` / `squad.go::ListSquads` AND the single-fetch `GetAgent` / `GetSquad` (0.5.18 SEC-P1-7 closed the single-fetch gap)
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
- **User-plugin subprocess runtime (0.5.18).** `user_plugin_runtime.go::RunUserPlugin`
  dispatches `runtime_kind=subprocess` to execute `manifest.runtime.command`+`args`
  (argv, no shell; shell metacharacters rejected) in the same sandbox env as inline
  (`pluginRuntimeEnv` — minimal env, HOME pinned, timeout). One-shot on-demand child,
  NOT a long-lived loopback service (that stays desktop-manager-owned for built-in
  subprocess labs like pythia/code_canvas). Do not re-add the old 501 "reserved
  upgrade slot".
- **Trust-gated agent auto-approval (0.5.18 F-002).** The claude backend's
  `--permission-mode bypassPermissions` is no longer hardcoded: it is appended
  only when `agent.ExecOptions.BypassPermissions` is true. The server computes
  the gate at task-claim time (`handler/daemon.go::ClaimTaskByRuntime` via
  `agent_trust.ShouldGrantBypassPermissions`, threshold
  `BypassPermissionsThreshold` = 8.0) and carries `bypass_permissions` on the
  claim wire → daemon `Task.BypassPermissions` → exec opts. **Softer-gate
  contract (user-chosen)**: an agent WITHOUT a trust profile keeps the
  historical auto-approval; only a REVIEWED agent scored below 8.0 loses it
  (scores only move via review pass / correction). The daemon has no DB — do
  not move the gate there.
- **`isBlockedEnvKey` (0.5.18 F-005).** `daemon.go` blocks custom_env overrides
  for `MULTICA_*`, `PYTHON*`, `HOME/PATH/USER/SHELL/TERM/CODEX_HOME/...`, and
  (0.5.18) `BASH_ENV/ENV/LD_PRELOAD/DYLD_INSERT_LIBRARIES/NODE_OPTIONS/NODE_EXTRA_CA_CERTS`.
- **User-plugin artifact upload hardening (0.5.18 F-006).**
  `user_plugin_artifacts.go` sanitizes multipart filenames (basename + control-char
  strip + ext whitelist `\.[A-Za-z0-9]{1,12}`), whitelists mime types (everything
  else degrades to `application/octet-stream`), and serves every stored artifact
  with `Content-Disposition: attachment` — uploaded HTML/JS can never render
  inline in the renderer origin.
- **Plugin visibility seeding is installer-workspace-scoped (0.5.18 F-013).**
  `user_plugins.go::seedPluginVisibility` takes the installer's workspace
  (resolved via `resolveLabWorkspace`) and scopes the agent/squad/autopilot
  lookups with `workspace_id` — a plugin can never hide resources in another
  workspace.

### Agent self-optimization + trust (0.5.2)

Two services power the `agent_self_optimization` lab's learning loop. Read
`internal/service/agent_self_optimization/` and
`internal/service/agent_trust/` before touching either.

- **Trust-score ledger (mig 228).** `agent_trust_profile` (score init 5.0 /
  max 10.0, `review_threshold` 7.0) + `agent_trust_event`. Score arithmetic +
  atomic upserts live in `agent_trust/service.go`; the handlers in
  `internal/handler/agent_trust.go` are thin membership-gated JSON adapters
  (`/api/experimental/trust/{profiles,events}` + `/{agentId}/correct` +
  `/{agentId}/review`). **`pgtype.Numeric` must be scanned via string**
  (`fmt.Sprintf("%.1f", v)`) — `Scan(float64)` leaves `Int=nil` and the
  numericToFloat rejects it.
- **Two-stage edit application (migs 229-231).** `agent_opt_edit` ledger with
  `application` `applied|suggested|rejected|ignored|reverted`,
  `instructions_snapshot` (rollback point), `applied_by` (`user|auto`,
  **nullable**), `corrected_task_id` (correction-traceability anchor).
  **Design verdict: delete/replace NEVER auto-apply** (by construction); add
  auto-applies only on score ≥ 90 + enrolled + trust ≥ 8 + not lab-managed +
  correction-backed + rate-capped 1/run. `RevalidateAppliedEdits` (the
  post-hoc commit gate) re-scores applied edits next run and auto-reverts a
  regression to its snapshot + records a `review_fail` trust event.
- **HTTP** (`internal/handler/agent_self_optimization.go` +
  `self_opt_edits.go`): runs list/get/trigger/cancel + edits list/apply/reject/
  ignore/revert — ALL membership-gated; flag off → 404.
- **Runner gotcha:** `runner.go` MUST `IncrementIssueCounter` before
  `CreateIssue` (the self-opt issue is `lab_source='agent_self_optimization'`
  and collides on `number=0` otherwise).

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
