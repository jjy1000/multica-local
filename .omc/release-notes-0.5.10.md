# Release Notes — 0.5.10 (2026-08-06)

## Summary

Batch A of the upstream 0.4.19-window integration: four cherry-picks focused
on agent context-exhaustion handling, scheduled-autopilot dispatch, and the
subscription control. Server-only Go + one shared-views fix. Zero migrations,
zero schema changes, packaging config untouched.

## Changes (upstream cherry-picks)

### #6366 — classify response-side context-window overflow (MUL-5708)

Claude Code 2.1.x reports an exhausted context window on the RESPONSE
(stop_reason `model_context_window_exceeded` / "API Error: The model has
reached its context window limit.") instead of as a request-side 400. Neither
wording matched the classifier, so the failure landed in `agent_error.unknown`
and the over-full session stayed pinned as the resume pointer — every later
comment on the issue resumed the same transcript and overflowed again.

- Added the two response-side witnesses to `pkg/taskfailure` rule 1.
- **Fork-completion**: upstream already excluded `agent_error.context_overflow`
  from its resume blacklists, so classification alone retired the session
  there; this fork's blacklists still carried only the original four reasons.
  Added `agent_error.context_overflow` to `resumeUnsafeFailureReason` +
  `GetLastTaskSession` / `GetLastChatTaskSession`.
- Upstream's `NormalizeDaemonReason` half was NOT ported — this fork ships
  daemon + server in the same `.app`, so the mixed-version fleet that shim
  upgrades does not exist here.

### #6422 — fail a run whose provider session ran out of context (MUL-5739)

- Parse Claude Code's structured `terminal_reason` on the stream-json result
  frame; `prompt_too_long` wins over `is_error`, and a context-exhausted turn
  clears its output so the provider's notice is never published as the agent's
  answer.
- `ContextExhaustedCompletion` text-side backstop (bounded, composite wordings
  only) for backends/daemons that can't read the structured field.
- Daemon `classifyPoisonedOutput` recognises the notice first.
- `/complete` re-routes a context-exhausted "success" to the failure path via
  a shared `failTask` helper so an older daemon can't keep the dead session
  pinned.

### #6410 — invalidate empty cache for scheduled tasks (MUL-5747)

The fork reproduced the exact bug: `main()` constructed a second
TaskService/AutopilotService for the background workers instead of reusing the
router's, so scheduled Autopilot dispatch sent the daemon wakeup without
bumping the router-wired EmptyClaim cache — an idle runtime kept returning
empty claims until the TTL expired. Added `backgroundServices(h)` and an AST
guard test that fails if the duplicate wiring ever returns.

### #6380 — subscription toggle hardening (MUL-5714)

Fork port of the half that applies here (the fork has no subtree-unsubscribe
concept): `subscriptionKnown` gating so nothing renders as "not subscribed"
before the query resolves; serialized direct toggles (two clicks in one tick
can no longer overlap the optimistic snapshot); failure toast instead of a
silent rollback.

## Migration & schema

**None.** Zero new migrations, zero `ALTER TABLE`, zero `CREATE INDEX`.
Verified: `git diff 62ce9bd..HEAD --stat -- server/migrations/` is empty.

## Packaging impact

- `apps/desktop/package.json` version: **0.5.9 → 0.5.10** (patch bump).
- Everything else (electron-builder config, asar layout, signing) unchanged.

## Verification

- `go build ./...` — OK
- `go test -race` across `pkg/taskfailure/ pkg/agent/ internal/daemon/
  internal/service/ internal/handler/ cmd/server/` — all green
  (`TestQuickCreateIssueParentTrustBoundary` flake confirmed pre-existing and
  not touched by this release).
- `pnpm --filter @multica/views typecheck` — OK
- `npx eslint` on changed views files — clean
- New tests: `context_exhausted_test.go` (+ adapted daemon/handler/service
  tests), `claude_context_exhausted_test.go`, `background_services_guard_test.go`,
  `use-issue-subscribers.test.tsx` (4 tests), `context_overflow_contract_test.go`.

## Commits (since 0.5.9)

```
787910406 cherry(issues): #6380 — subscription toggle hardening (MUL-5714)
ef52722c3 cherry(autopilot): #6410 — invalidate empty cache for scheduled tasks (MUL-5747)
4a8d39635 cherry(agent): #6422 — fail a run whose provider session ran out of context (MUL-5739)
4d79acc0a cherry(server): #6366 — classify response-side context-window overflow (MUL-5708)
70974feed docs: sync CLAUDE.md with 0.5.5-0.5.7 product-level lab promotion
```
