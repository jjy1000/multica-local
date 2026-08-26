---
name: release-notes-0.5.75
created: 2026-08-27T08:00:00Z
updated: 2026-08-27T08:00:00Z
---

# 0.5.75 — Upstream Integration Batch (Cherry-Picks B2/B3)

**Released:** 2026-08-27
**Branch:** `epic/0.5.72-followups`
**Builds on:** 0.5.74 (lab integration polish)

## TL;DR

4 cherry-picks from upstream triage `Batches 2-3` surgically ported on top of 0.5.74. The prior session's isolated-worktree batch (`wf_eb8dcaa6-935`) had landed 5 commits on 3 orphan branches (`worktree-wf_eb8dcaa6-935-{4,6,9}`); this session recovered them, split the triple-merge, and reverted the incomplete cherry-pick (`#7018` chat live output) whose upstream test file referenced fork-absent props.

## What changed

### Batch 2 — CLI/skill surgical

**#7557 `fix(cli): remove obsolete autopilot priority flag`** — `server/cmd/multica/cmd_autopilot.go` drops the `--priority` flag from the autopilot subcommand, migrates `cmd_autopilot_test.go` table to use `tag` for priority-bearing autopilot references.

**#7418 `feat(server): MULTICA_TASK_QUEUED_TTL`** (manual port of upstream `5a80f802d`) — env-driven TTL for queued agent tasks (`defaultTaskQueuedTTL = 2h` default, env override). `server/cmd/server/runtime_sweeper.go` renames `queuedTTLSeconds` → `defaultTaskQueuedTTL` (2h); threads `queuedTTL time.Duration` through `runRuntimeSweeper` → `sweepExpiredQueuedTasks`. `server/cmd/server/main.go` reads env via `envDuration("MULTICA_TASK_QUEUED_TTL", defaultTaskQueuedTTL)`. New test `sweeper_task_timeouts_test.go::TestQueuedTTLFromEnv` (4 subtests: unset/positive/unparseable/non-positive) + `TestDefaultTaskQueuedTTL` (pin 2h default). **Fork adaptation**: `apps/desktop/src/main/server-manager.ts::serializeEnvFile` adds `MULTICA_TASK_QUEUED_TTL=5m` — single-user desktop overrides upstream's 2h self-hosted default; stale queued tasks fail fast.

NOT PORTED (out of scope, fork intentionally diverges from upstream self-hosted deploy story): `.env.example` (fork doesn't ship self-hosted env templates), `apps/docs/content/docs/{tasks,troubleshooting,environment-variables}` + 4 locales (fork-local docs story deferred), `deploy/helm/multica/*` + `docker-compose.selfhost.yml`.

### Batch 3 — UX/cosmetic

**#6634 `MUL-5928: bound board description previews`** — `packages/views/issues/components/description-preview.tsx` clamps description preview rendering to bounded chars/lines. New test `description-preview.test.ts` (3 cases). Board performance: prevents giant issue descriptions from blowing up the issue board view.

**#7385 `fix(github): support single-character issue prefixes`** — `server/internal/handler/github.go` allows single-char prefixes in GH integration. `github_test.go` adds regression pin.

## Skipped (cherry-pick incompleteness)

**#7018 `fix(chat): keep following rapid live output`** — REVERTED in this batch. Source diff itself is only 5 lines (scroll behavior in `chat-message-list.tsx::followOutput`), but the upstream test file (576 LOC new) references fork-absent props:

- `transformContent` (ChatMessageListProps)
- `onQuickAction` (ChatMessageListProps)
- `quickActionsPendingMessageId` (ChatMessageListProps)
- `quickActionsDisabled` (ChatMessageListProps)
- `message_kind: 'onboarding_opening'` / `'onboarding_kickoff'` (ChatMessage enum)
- `quick_actions` (ChatMessage field)

These are upstream chat surface features the fork never ported. Per CLAUDE.md §Known Stability Surfaces "Cherry-pick completeness check MUST verify both code paths AND test imports (0.5.15 ship blocker, `da1cc2003`)" — applying the test would have broken `pnpm typecheck`. The 5-line source fix is recoverable later with a custom test file aligned to fork's chat surface.

## Pre-existing failure locked (not this batch)

`TestDashboardPerAgentRollupsUseExactWindow` (`server/internal/handler/dashboard_test.go`) fails on the eb9d23704 pre-batch baseline (verified via `/tmp/wt-baseline` worktree + bisect). Same class as `TestQuickCreateIssueParentTrustBoundary` documented in CLAUDE.md §Known Stability Surfaces. Not introduced by this batch — defer to a future batch's pre-existing-failure cleanup cycle.

## Files changed (cumulative)

```
M  CLAUDE.md                                                                  (release header)
M  apps/desktop/package.json                                                  (version bump)
M  server/cmd/multica/cmd_autopilot.go                                        (B2 #7557)
M  server/cmd/multica/cmd_autopilot_test.go                                    (B2 #7557 test update)
A  server/cmd/server/sweeper_task_timeouts_test.go                             (B2 #7418 new test)
M  server/cmd/server/runtime_sweeper.go                                       (B2 #7418)
M  server/cmd/server/main.go                                                  (B2 #7418)
M  apps/desktop/src/main/server-manager.ts                                    (B2 #7418 fork env)
M  packages/views/issues/components/description-preview.tsx                   (B3 #6634)
M  packages/views/issues/components/description-preview.test.ts                (B3 #6634 new test)
M  server/internal/handler/github.go                                          (B3 #7385)
M  server/internal/handler/github_test.go                                     (B3 #7385 test update)
```

LOC delta: +6 commits, ~+92 / -12 (estimate; ~+50 LOC in 5 src files + ~+90 LOC in 4 test files + 1 new test file 43 LOC).

## End-to-end verification (latest)

- `pnpm typecheck` (full turbo): 6/6 OK (~53 s)
- `go test -count=1 -timeout 600s ./internal/... ./pkg/agent/...`: all packages green except 2 pre-existing failures (locked): `TestQuickCreateIssueParentTrustBoundary` (race) + `TestDashboardPerAgentRollupsUseExactWindow` (new lock this batch).
- New `TestQueuedTTLFromEnv` (4 subtests) + `TestDefaultTaskQueuedTTL`: PASS.
- `pnpm test` (vitest, key files): `description-preview.test.ts` PASS.

## Lessons (for future integration batches)

1. **Cherry-pick completeness check MUST verify test imports too** (CLAUDE.md §Known Stability Surfaces). #7018's 576-LOC test file referenced 6 fork-absent props; `pnpm typecheck` caught it but only AFTER the merge. Future batches: read the upstream test file BEFORE merging, verify each prop/field/union-arm exists in fork.

2. **Triple-merge via cherry-pick individually**: the prior session's merge of `worktree-wf_eb8dcaa6-935-9` (3 commits in one merge) made it impossible to revert just the bad commit. Cherry-pick individually (or via `git revert -m 1` + cherry-pick) so each commit is independently revertable.

3. **Worktree isolation strategy mismatch**: `isolation: 'worktree'` in Workflow agent gave agents `.claude/worktrees/wf_*/` directories, not the named `wt-batch*/` dirs the prompts pointed at. Documented in `0.5.75-upstream-integration-paused-2026-08-26.md`. Future batches: either pre-create worktrees + prompt agents with `cwd:` paths, or accept the `.claude/worktrees/wf_*/` location and merge from there.

4. **Pre-existing failure bisect protocol**: when a test fails on HEAD, `git worktree add /tmp/wt-baseline <base-commit>` + `ln -s node_modules` + targeted test run is faster than reading the diff. Same pattern as `TestQuickCreateIssueParentTrustBoundary` baseline-verify (CLAUDE.md §Known Stability Surfaces).

## Deferred (all non-blocking, future-cycle candidates)

- **#7018 chat live output** — re-attempt with a custom test file aligned to fork's chat-message-list surface (no `onQuickAction`/`quick_actions` props).
- **~25 remaining triage commits** from `.omc/upstream-integration-triage-2026-08-26.md` (P0 security CVE bumps + UX/cosmetic + 41 docs-only changelog entries).
- **Batch 5 REVIEW-tier commits** (8 large / fork-heavy): `f8ec870f` per-agent starter prompts, `8c563b49` source context for sub-issues, `54027ba76` skill listings + stall timeouts, `46b5d9e6` process tree ownership, `b2b4699f` inbox status/priority filters, etc. — user decision required per triage §Batch 5.
- **`TestDashboardPerAgentRollupsUseExactWindow` fix** — 900s delta leak, distinct from the `TestQuickCreateIssueParentTrustBoundary` race. Likely a date-window aggregation bug in `pkg/dashboard` rollup query.
