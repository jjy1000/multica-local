# Release Notes — 0.5.50

**Shipped 2026-08-21** (branch `epic/0.5.13-integration`, 3 commits on top of 0.5.49: 1 sqlc query + 3 index migrations). `pnpm typecheck` 6/6 + `go build` clean.

## Summary

**Schema sweep round 2 (partial) + MUL-6310 prerequisite sqlc queries.** Three performance indexes added for workspace teardown (MUL-5999) + two sqlc queries added for `AckTaskCancelled` handler (MUL-6310). All schema-only — no behavior change at runtime, no binary delta.

## Changes

### 1. `feat(0.5.50)` `a94b731ca` — add `SetAgentTaskBranchName` + `SetAgentTaskErrorIfEmpty` sqlc queries

Fork extension to `pkg/db/queries/agent.sql` — adds the two queries from upstream MUL-6310's `AckTaskCancelled` handler that the fork DAO doesn't yet have:

- `SetAgentTaskBranchName :exec` — records the delivered branch on a CANCELLED task. The daemon finalizes its worktree (committing whatever the agent produced) BEFORE it learns the task was cancelled, so the branch exists but the cancel path has no result payload to carry it.
- `SetAgentTaskErrorIfEmpty :exec` — companion to `SetAgentTaskBranchName`. A cancelled worktree task whose Finalize ABORTED has no branch to deliver — the error text carrying the preserved-worktree path is the only pointer to the agent's work.

Both queries use the `status='cancelled'` CAS so the cancel-ack path never overwrites a reason recorded by a complete/fail callback or the claim gate.

sqlc regen: both methods added to `*db.Queries`. `agent.sql.go` gains the `SetAgentTaskBranchName` / `SetAgentTaskErrorIfEmpty` methods + their `SetAgentTaskBranchNameParams` / `SetAgentTaskErrorIfEmptyParams` struct types.

### 2. `feat(0.5.50)` `2c67c2207` — add `agent_task_queue` keyset indexes (MUL-5999 workspace teardown)

Three single-statement `CREATE INDEX CONCURRENTLY` migrations, fork-sequenced to 270-272:

- `270_agent_task_queue_runtime_id_index` — covers `DELETE FROM agent_runtime`'s `ON DELETE CASCADE` probe (PostgreSQL doesn't index the referencing side of an FK, so without this index every runtime delete full-scans the largest table). Also serves the `WHERE runtime_id = $1` workspace teardown paging path.
- `271_agent_task_queue_agent_id_keyset_index` — `(agent_id, id)` btree for the busy-agent task paging pattern (`agent_id = $1 AND id > $cursor ORDER BY id LIMIT n`). The existing `(agent_id, status)` index cannot produce id order, so every page would read the agent's entire task set and sort before applying LIMIT.
- `272_agent_task_queue_issue_id_keyset_index` — `(issue_id, id)` btree for the issue-keyset paging path. The `(issue_id)` single-column index from migration 035 is left in place (deliberate; dropping it needs its own direction-aware rollback migration).

All three migrations run `CREATE INDEX CONCURRENTLY IF NOT EXISTS` (idempotent), so migration can be re-run safely.

## SKIP reconciliation from 0.5.49

| Skip | 0.5.50 progress | Status |
|---|---|---|
| MUL-6472 dispatch leak | LANDED in 0.5.47 ✅ | cleared |
| MUL-6310 NUL bytes | 2/3 prerequisites landed (branch_name column 0.5.49, sqlc queries + indexes 0.5.50) | **PARTIAL** — task.go + protocol + TaskService methods still pending |
| CJK markdown | LANDED in 0.5.48 ✅ | cleared |
| MUL-6417 follow-ups | No progress (daemon API drift: `ChatChannelType`, `kindIssue`) | minimal port requires forking these too |

## Verification

| Gate | Result |
|---|---|
| `go run ./cmd/migrate up` | PASS (3 indexes applied, 269 → 272) |
| `sqlc generate` | PASS (2 new query methods) |
| `go build ./...` | PASS |
| `scripts/check-agents-docs-sync.mjs` | PASS (5 guarded categories) |

## Files changed (2 commits)

| Commit | Files | Insertions |
|---|---:|---:|
| `a94b731ca` sqlc queries | 2 | 85 |
| `2c67c2207` index migrations | 6 | 50 |
| **Total** | **8** | **135** |

## Strategic significance

0.5.50 is schema-only (no behavior change). The cumulative 0.5.43-0.5.50 work:
- 41 commits landable
- **+7342 LOC**
- 8 new migrations applied (autopilot quota + branch_name column + 3 keyset indexes)
- 2 user-facing cherry-picks landed (MUL-6472 + CJK)
- 2 SKIPs partially cleared (MUL-6472 + CJK)
- 2 SKIPs still blocked (MUL-6310 + MUL-6417)

The MUL-6310 caller integration is now closer to landing (3 of 5+ prerequisites done). The remaining blockers are:
- `TaskService.RebroadcastCancelledTask` + `FinalizeDeferredCancelledChat` methods (code refactor)
- `AckTaskCancelled` handler + route registration (UI integration)
- `protocol.ChatCancelFinalizedPayload` type (definition unknown upstream)
- `taskfailure.ReasonSkillBundleUnavailable` (fork has `taskfailure` package but not this constant)

These are the focus of 0.5.51+.

## 0.5.50 install

**0.5.50 is schema-only** — no binary behavior change. Per the 0.5.46 / 0.5.49 pattern, **ship is skipped** (destructive no-op). The 0.5.50 migrations are applied to the DB; the binary stays at 0.5.48 (last real ship).

If you want to deploy 0.5.50 to the running app, run `bash scripts/ship-mac.sh --yes` explicitly.
