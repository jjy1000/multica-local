# Release Notes — 0.5.49

**Shipped 2026-08-21** (branch `epic/0.5.13-integration`, 2 commits on top of 0.5.48: 1 retrospective + 1 schema migration). `pnpm typecheck` 6/6 + `go build` clean.

## Summary

**Schema migration sweep round 2 (first migration of round 2).** Adds `agent_task_queue.branch_name TEXT` column. MUL-6310 caller integration still blocked (more sqlc queries + protocol types needed).

## Changes

### 1. `docs(0.5.49)` `5a91a2f62` — retrospective comprehensive doc

`.omc/0.5.49-retrospective.md` (173 lines). Captures 0.5.43-0.5.48 cumulative state (6 ships, 37 commits, +7007 LOC), 0.5.44 SKIP reconciliation (4 → 2 cleared), 0.5.49+ candidates, outstanding user decisions.

### 2. `feat(0.5.49)` `9268697ac` — add `agent_task_queue.branch_name` column + sqlc regen

Fork-port of upstream migration 308 (`agent_task_branch_name`).

**Schema migration** (sequenced 269 to continue from fork's 268):
- `269_agent_task_branch_name.up.sql` — `ALTER TABLE agent_task_queue ADD COLUMN IF NOT EXISTS branch_name TEXT`
- `269_agent_task_branch_name.down.sql` — `ALTER TABLE agent_task_queue DROP COLUMN IF EXISTS branch_name`

**Why this column**: A worktree-mode task hand its result back as a git branch in the user's own repo rather than as edits to the working copy. The branch name is the only pointer to where the work went. Nullable (only worktree tasks populate it).

**Migration 308 picked** because it directly unblocks one missing column for MUL-6310 caller integration. Other migrations in the 269-300+ range (mostly indexes + plugin lifecycle cleanup) are deferred — many reference upstream abstractions fork hasn't ported (plugin_contribution, plugin_installation, etc.).

**sqlc regen cascade** (6 files, 83 LOC added / 42 removed):
- `agent.sql.go` — column added to `AgentTaskQueue` model
- `autopilot.sql.go`, `chat.sql.go`, `chat_input_ownership.sql.go`, `runtime.sql.go` — cascade updates from column addition
- `models.go` — schema constants updated

**Not generated**: `SetAgentTaskBranchName` query method (the one upstream uses in `AckTaskCancelled`). The actual sqlc query lives in a separate `.sql` file that wasn't part of this port. Adding the query without the handler would be dead code; landed all together when MUL-6310 caller integration resumes.

## SKIP reconciliation from 0.5.48

| Skip | 0.5.49 progress | Status |
|---|---|---|
| MUL-6472 dispatch leak | LANDED in 0.5.47 ✅ | cleared |
| MUL-6310 NUL bytes | `branch_name` column added ✅; task.go + protocol still blocked | **PARTIAL** — column prerequisite landing |
| CJK markdown | LANDED in 0.5.48 ✅ | cleared |
| MUL-6417 follow-ups | daemon API drift (ChatChannelType, kindIssue missing) | minimal port requires forking these too |

## Verification

| Gate | Result |
|---|---|
| `go run ./cmd/migrate up` | PASS (1 migration applied, 269 → 269) |
| `sqlc generate` | PASS (6 files cascade) |
| `go build ./...` | PASS |
| `scripts/check-agents-docs-sync.mjs` | PASS (5 guarded categories) |

## Files changed (2 commits)

| Commit | Files | Insertions |
|---|---:|---:|
| `5a91a2f62` retrospective | 1 | 173 |
| `9268697ac` migration + sqlc | 8 | 115 |
| **Total** | **9** | **288** |

## Strategic significance

0.5.49 is the smallest batch since 0.5.48 (1 functional commit + 1 docs). The migration additions are pure schema — no behavior change to the running binary. The actual blocking work for MUL-6310 (sqlc queries, protocol types, AckTaskCancelled handler) remains in 0.5.50+.

The deliberate pace of 0.5.49 reflects the cherry-pick frontier: each remaining SKIP requires multi-component porting (schema + sqlc + service refactor + handler). The schema sweep round 2 has started with 1/30+ migrations; subsequent batches will continue.
