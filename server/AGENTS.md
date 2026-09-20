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
- `internal/{auth,attribution,daemon,daemonws,entitlement,entitlementtest,events,featureflags,realtime,runtimeapps,scheduler,skill,storage,util,middleware}/`.
- `pkg/` — reusable across binaries: `agent`, `db` (sqlc output), `featureflag`,
  `plugincontract`, `protocol`, `redact`, `skillbundle`, `taskfailure`.
- `migrations/` — forward-only SQL. `sqlc.yaml` drives codegen into `pkg/db`.

## 0.5.45-0.5.46 audit-batch abstractions (ported from upstream, mostly additive)

These landed in the 0.5.45 foundation batch + 0.5.46 infra batch. They are the
upstream packages the fork was missing when 6 of the 0.5.44 cherry-picks SKIP'd.
Most are additive — new code can import them; existing fork code is untouched.

- `internal/attribution/` — accountable-human resolution contract (MUL-4302):
  `Source`, `EvidenceKind`, `TriggerKind` + `Classify*` pure functions. "On
  behalf of", never blame/authz. Fork already has `originator_user_id` +
  `accountable_user_id` columns (migration 240); this package labels provenance.
- `internal/entitlement/` (+ `entitlementtest/`) — cloud entitlement cache/client/
  types/stub. Dormant in fork (no billing subsystem); CLAUDE.md previously
  SKIP-DEAD-CASE'd it, decision reversed to port standalone. Activating it needs
  the upstream autopilot_quota schema (migrations 261-374 + sqlc regen).
- `internal/runtimeapps/` — plugin host connected-app registration.
- `internal/featureflags/keys.go` — upstream flag-key vocabulary (Billing,
  Composio, PluginsV1, CustomIssueStatuses). Parallel to `featureflagdispatch`
  (fork-local evaluator). Constants only — no consumers in fork yet.
- `pkg/plugincontract/` — plugin manifest schema (key, name, scopes, contributes).
  `examples_test.go` dropped — fork has no `examples/plugins/` dir.
- `internal/util/text.go` — `SanitizeTextForPostgres` + `SanitizeJSONForPostgres`
  (NUL/UTF-8 persistence guards; `strings.ToValidUTF8` alone does NOT strip NUL).
  All 11 tests pass. Callers NOT yet wired (upstream handler migration blocked on
  sqlc queries the fork lacks: `LockAutopilotForUpdate`,
  `SetAutopilotTriggerPublishersByAutopilot`, `ListDaemonCustomNames`, etc.).
- `internal/service/autopilot_quota.go` — `AutopilotQuotaExceededError` sentinel
  ONLY (32 LOC). Full quota subsystem NOT ported — needs upstream migrations
  261-374 + sqlc regen + service refactor.
- `packages/views/rich-content/cjk-emphasis.ts` — CJK-adjacent strong-emphasis
  repairer. Standalone; NOT yet integrated into fork's `readonly-content.tsx`
  pipeline (deferred).

## 0.5.51 MUL-6471 — opencode/pi custom-provider qualification (landed)

`pkg/agent` gained two fork-local helpers + the daemon uses them to let
opencode reach custom gateway providers (MUL-6471, GH #7300):

- `ModelSelectorMustBeProviderQualified(providerType string) bool` —
  opencode-only in this fork (no deveco/omp/`ProtocolFamily` registry). True
  where the CLI refuses a bare model id (opencode's `provider/model` contract);
  deliberately false for pi, whose resolver accepts every id shape.
- `QualifyModelID(models []Model, model string) (string, bool)` — promotes a
  persisted model id to the catalog's canonical selector ONLY when exactly one
  provider claims it; every uncertain case passes the input through untouched.
  Operates on `[]Model` (fork's `ListModels` return), not the upstream
  `Catalog`/`Fallback` wrapper.
- `daemon.go` model flow: after two-tier resolution (agent.model → env-tier)
  and BEFORE thinking-level validation (which matches on the catalog's
  canonical id), opencode pinned models are qualified against
  `agent.ListModels(ctx, provider, entry.Path)`. The `starting agent` log shows
  the resolved model, not `entry.Model`.
- **pi fix**: `buildPiArgs` passes the model selector whole to `--model` and
  never synthesizes `--provider` (a slash-shaped id like `claude/claude-opus-5`
  used to become `--provider claude` → pi hard-errors `Unknown provider`).
  `splitPiModel` is gone.

Not ported: the upstream single-read loader refactor
(`ValidateThinkingLevelWith`/`ValidateServiceTierWith`) — the fork's
healthy-runtime catalog reads are memoized by `cachedDiscovery`, so a second
read is cheap. If a future port needs the at-most-once contract, port that
refactor alongside.

## 0.5.109 — daemon terminal-report reliability + streaming (landed)

Upstream port batch (12 ports / 10 skips; ledger
`.omc/upstream-sync-2026-09-19.md`). Surfaces a future edit must know about:

- **Terminal-report outbox** (`internal/daemon/terminal_report_queue.go`,
  MUL-7471): daemon persists every complete/fail report to
  `<WorkspacesRoot>/.pending-terminal-reports/` BEFORE sending
  (temp+rename+file fsync; dir fsync no-op on Windows via the
  `_sync_windows.go` build tag). A replay loop (boot + backoff ≤5min, woken
  by the `terminal-report-replay` background loop) retries pending records;
  a nil-store guard skips configs without `WorkspacesRoot`. Corrupt records
  are kept for forensics and logged, never deleted or crash-looped.
  Resource GC will NOT collect the dot-dir (no `.task_owner`/`.gc_meta.json`).
  The persisted record carries the fork client field set only — upstream's
  `sessionRolloutMissing`/`retiredSessionID`/`durableWorkDir` were NOT
  back-filled (would ripple through every backend's result path).
- **`transitioned` gate** (MUL-7471): `service.CompleteTask`/`FailTask`
  return a second bool (true only when THIS call performed the terminal
  transition). Handler paths gate transaction-external side effects
  (`emitIssueExecutedOnFirstCompletion`, token revocation, comment
  reconcile) on it — an already-finalized CompleteTask used to return 200
  and REPLAY those effects. Fork's `CompleteTask` has 4 exits (incl. the H3
  `classifyFinalizeNoRows` branch); all error exits return false.
- **Codex delta streaming** (MUL-7465): `pkg/agent/codex.go` aggregates
  `item/agentMessage/delta` through `codexAgentMessageStream` (shared
  scanner in `stream_scanner.go`, `agentStreamMaxLineBytes` = 10 MiB, fork's
  old inline value — upstream's 32 MiB came from unported MUL-5722) and
  flushes whole pending chunks on the leading edge; daemon's drain loop has
  5 `flushFirstVisible()` points so the first visible chunk reaches the
  server without waiting for the ticker. Transcripts merge by `seq` on both
  write paths, so cross-POST arrival order cannot corrupt render order
  (pinned by test after a real race was observed).
- **hermes bounded shutdown** (MUL-5241): `WaitDelay` = 10s + `reapProcess`
  (sync.Once, per-cmd). An escaped descendant holding the pipes can no
  longer wedge the reader joins forever. Do not "simplify" the
  reap-before-join ordering — the deferred cleanup joining readers BEFORE
  closing `msgCh` is what closes the send-after-close panic window.
- **pi turn-error guard** (MUL-7467): `piTurnErrorGuard` records
  turn_end `stopReason=error`, clears on real recovery events, and fails a
  silently-hung errored turn after the grace timer; cancelled turns keep
  the provider error. stderr is self-managed via `StderrPipe` + `io.Copy`
  into the same logWriter — `cmd.Stderr = newLogWriter` alone lets os/exec
  own an internal pipe whose drain stalls finalization when an escaped
  descendant inherits stderr (measured 3.29s vs 345ms fixed).
- **taskfailure cursor classification** (afedc6f76):
  `isCursorProviderNetworkError` prefix-matches cursor connect-timeout
  wrappers into `ReasonAgentProviderNetwork` instead of the process-failure
  bucket. Fork has no `shouldRetryWithFreshSession`/`Result.ResumeRejected`
  (upstream's resume-preservation consumer) — do not cite them.

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
- **Lab ↔ assignee mutex (server layer) — scoped by `interaction_model` since 0.5.86.**
  The lock belongs to every lab classified `InteractionModelAssignee`:
  `claude_science_lab`, `pythia_oracle`, `semantica`, `timesfm`,
  `mythos_swarm` (sole mode — `lab_mode='enhancer'` REVERSES it, an assignee is
  REQUIRED), plus any `user_*` plugin whose manifest declares
  `interaction_model: assignee`. `llm_wiki_bridge` / `causal_graph`
  (auxiliary) and unclassified legacy flags never lock. Authority is
  `experimental.IsAssigneeModelLab` + `handler/issue.go::assigneeLabLockError`
  (its three messages all begin `lab_source=<key> ... the lab to own the
  assignee` / `locks the assignee to the lab agent` — grep those, not the
  obsolete `"mutually exclusive"` string). CreateIssue and UpdateIssue 400;
  BatchUpdateIssues `continue`s per-issue, never 400s the whole batch.
  **0.5.107 closed a batch gap:** the batch switch only ever hardcoded
  `mythos_swarm` + `swarm_topology`, so a batch PATCH explicitly carrying
  `assignee_*` onto any widened lab persisted silently while the single-issue
  PATCH 400'd — the third recurrence of this class (0.5.21→0.5.60 swarm,
  0.5.86→0.5.107 widened set). If you widen `interaction_model` again, do NOT
  add a key to the batch switch; both paths already call
  `assigneeLabLockError`. Pinned by
  `TestBatchUpdateIssuesLabAssigneeLockParity` (`issue_lab_source_test.go`),
  which derives its key list from the catalog instead of restating it.
  `lab_source` (mig 155) and `lab_mode` (mig 157, CHECK `'sole'|'enhancer'`)
  are nullable TEXT.
- **Lab leader rewrite.** Code paths that flip `issue.lab_source` must go through
  `handler/issue.go::shouldRewriteAssigneeForLabLeader` + `assignDefaultLabAgentOnUpdate`
  (4-case contract; tests in `issue_lab_dispatch_test.go`), not a re-derived gate.
- **Lab auto-dispatch opt-out (0.5.22).** `experimental.Flag.AutoDispatch *bool`
  flags a catalog entry to skip the service-layer auto-dispatch path
  (`IssueService.maybeEnqueueOnAssign` in service/issue.go + `WillEnqueueRun`
  in service/issue_trigger.go — the single chokepoint for UpdateIssue +
  BatchUpdateIssues). Nil/true = unchanged 0.3.46 behaviour; **false**
  (currently `pythia_oracle` + `timesfm` + `causal_graph`) means the
  assignee is still written (leader-rewrite still applies) but no
  `agent_task_queue` row is created until the user explicitly triggers a
  run from the lab workbench (claude_science_lab was the original opt-out;
  its manual trigger via `POST /api/experimental/claude-science/issues/{id}/run`
  in handler/claude_science_run.go calls `TaskService.EnqueueTaskForIssue`
  directly — bypassing both service gates because the endpoint IS the
  manual opt-in). Reading code goes through
  `experimental.AutoDispatch(key)` (true when pointer is nil/unknown,
  returns the dereferenced value otherwise). Adding another opt-out lab
  is a 3-line catalog edit — no service-layer or router changes needed.
  Tests: `TestAutoDispatchFlagBehavior` in
  `internal/experimental/registry_test.go`. (0.5.105 audit M5: this
  clause previously claimed claude_science_lab was the only opt-out.)
- **Experimental runtime GC for `experimental_claude_runtime_session` (0.5.25).**
  `experimental.RuntimeGC` is the 30/90/120-day retention ladder for claude
  science research sessions (migration 151). Three contracts: (1) `Run()`
  never closes `g.stopped` itself — `Stop()` owns the close-once contract
  via `stopOne`. The pre-0.5.25 code called `g.stopOne.Do(close(g.stopped))`
  *eagerly* at the top of `Run()`, so the first `select` evaluated
  `<-g.stopped` immediately and the GC exited without ever ticking.
  (2) `Start()` is wired at boot in `cmd/server/router.go` alongside
  `resource_gc.Start()` (the 0.5.105 rehoming of the former swarm_gc
  tick). Pre-0.5.25 it was orphaned — the comment "parallel
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
  **Execution is flag-gated (0.5.107); management is not.** `user_plugin.status`
  and the Labs toggle are separate state — the row stays `active` after the user
  flips the lab off — so `RunUserPlugin` additionally checks
  `experimentalFlagEnabled` and refuses with **409** naming the remedy. 409 rather
  than `RequireExperimentalFlag`'s 404 because this route is deliberately
  enumerable (the CRUD group above it stays open by design, `router.go`), so the
  caller already knows the plugin exists. Do not extend the ungated exception to
  any new execution endpoint. Pinned by `TestRunUserPluginRejectedWhenLabDisabled`.
- **Every subprocess a handler spawns MUST set `cmd.Env` explicitly.** A nil
  `cmd.Env` silently inherits `os.Environ()`, and in the desktop co-resident
  deployment that is the whole server: the daemon-injected `MULTICA_API_TOKEN`,
  the JWT secret, `DATABASE_URL`, `ANTHROPIC_*` and the profile-bearing real
  `HOME` (→ `~/.multica/profiles/<name>/config.json`). `python3 -I` isolates
  site-packages, NOT the environment — it is not a substitute. Build an allowlist
  (`pluginRuntimeEnv` for plugin runs, `runtimeSessionEnv` for the
  claude_science_lab sandbox: `PATH` + `HOME` pinned to the run's own directory +
  a fixed UTF-8 locale) and keep `TMPDIR` pointing at the shared temp dir, since
  a session-directory `TMPDIR` gets re-ingested as phantom artifacts.
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
