---
name: MUL-3724 squad leader briefing — test strategy
created: 2026-07-01T00:00:00Z
updated: 2026-07-01T00:00:00Z
status: in-progress
target: server/internal/handler/{daemon.go, squad_briefing_claim_test.go} + server/migrations/12X_task_squad_id.up.sql
---

# MUL-3724 Squad Leader Briefing — Test Strategy

## 1. Background

MUL-3724 changes the gate the daemon uses to decide whether to inject the squad-leader briefing (Operating Protocol + Roster + squad Instructions) onto a claiming leader agent's `Instructions` field.

| | **Pre-MUL-3724 (today, local 0.2.96)** | **Post-MUL-3724 (upstream)** |
|---|---|---|
| Schema | `agent_task_queue` has `is_leader_task BOOLEAN` (migration 090); no `squad_id` column | + new migration `12X_task_squad_id`: `squad_id UUID NULL` + partial index `WHERE squad_id IS NOT NULL`. No FK to `squad(id)`. |
| Gate (daemon.go:1391) | `issue.AssigneeType == "squad" && issue.AssigneeID.Valid` | `task.IsLeaderTask && task.SquadID.Valid` |
| Briefing source | `GetSquadInWorkspace(assignee_id, workspace_id)` (issue's squad row) | `GetSquadInWorkspace(task.SquadID, workspace_id)` (task's squad row) |
| Caller signature | `TaskService.EnqueueTaskForSquadLeader(ctx, issue, leaderID, triggerCommentID)` | + `squad.ID` arg (5th, after `handoffNote`); `EnqueueTaskForSquadLeaderWithHandoff` likewise |

The user-visible defect MUL-3724 closes:

1. Member posts a plain comment (no `@mention`) on an issue whose `assignee_type='agent'` (MUL-2244 / MUL-2218 / MUL-2788 territory).
2. `computeAssignedSquadLeaderCommentTrigger` returns `true`, so `comment.go` enqueues a leader task via `EnqueueTaskForSquadLeader` — but the issue itself is **not** squad-assigned, so today the briefing is silently skipped.
3. The leader agent boots with no squad context and degrades into doing the work itself (MUL-2429 Path B contamination).
4. MUL-3724 moves the gate from "is the issue squad-assigned?" to "is this task a leader task carrying a squad id?", so the briefing follows the task.

Secondary contract: a leader task with a **dangling** `squad_id` (squad hard-deleted) — claim must still succeed (200) and just skip injection. With no FK on the column, the daemon's `GetSquadInWorkspace` returns no row, the `err != nil` branch activates, and we never emit a stale/empty briefing. This is an explicit non-FK design property (see migration comment).

## 2. Inventory of Existing Test Coverage in the Local Fork

### Local test files that mention the leader-task path (preconditions locked in today)

Found by `find ... | xargs grep -l 'EnqueueTaskForSquadLeader\|squad_id\|SquadID\|is_leader_task\|IsLeaderTask'`:

| File | Lines | What it locks in today |
|---|---|---|
| `/Users/jiangjianyan/jjy/multica-main/server/internal/handler/squad_briefing_test.go` | 426 | `buildSquadLeaderBriefing` unit tests (5): `FullSquad`, `MemberSkillsInRoster`, `OnlyLeader`, `SkipsArchivedAgent`, `MentionsRoundTrip`. **Plus** two **claim-path** tests at L346 / L384: `TestClaimTask_LeaderGetsBriefing` and `TestClaimTask_NonLeaderGetsNoBriefing`. Both gate on `issue.assignee_type='squad'` via helper `queueSquadIssueTaskFor` (which inserts an issue with `assignee_type='squad', assignee_id=$squadID`). **Both stay green under MUL-3724** (the new gate is strictly more permissive; a squad-assigned issue still passes both conditions). They are **not** regressions and must not be modified. |
| `/Users/jiangjianyan/jjy/multica-main/server/internal/handler/issue_involves_test.go` | 467 | `involvesFixture` (467 lines) seeds squads across two workspaces for the `involves_user_id` 4-branch filter; doesn't touch the briefing claim gate. |
| `/Users/jiangjianyan/jjy/multica-main/server/internal/handler/squad_comment_trigger_test.go` | 1000+ | Computes `shouldEnqueueSquadLeaderOnComment` matrix; covers the **enqueue decision** side (which comments wake the leader) — but **does NOT** check what the leader sees when it claims. This is the exact seam MUL-3724 lives in. |
| `/Users/jiangjianyan/jjy/multica-main/server/internal/handler/issue_child_done_test.go` | 522 | Parent/child issue-done notification path. Unrelated to briefing injection. |
| `/Users/jiangjianyan/jjy/multica-main/server/internal/daemon/prompt_test.go` | 542 | Pure unit on the quick-create prompt builder. Unrelated. |

### Existing helpers already available in the local fork (no need to re-introduce)

- `testHandler *Handler` — shared package-level, set in `handler_test.go` (referenced by all `*_test.go` files in the package).
- `testPool *pgxpool.Pool` — same.
- `testWorkspaceID`, `testUserID` — seeded constants.
- `claimAndDecodeAgent(t, runtimeID) *TaskAgentData` — at `squad_briefing_test.go:292`.
- `newDaemonTokenRequest(method, path, body, ws, tag) *http.Request` — used at L295 and across the package.
- `withURLParam(req, key, val) *http.Request` — used at L298.
- `createHandlerTestAgent(t, name, skillsJSON) string` — seeds an agent + runtime, returns agent ID.
- `seedSquadForBriefing(t, leaderID, name, instructions) db.Squad` — at `squad_briefing_test.go:39`.
- `addAgentMember(t, squadID, agentID, role)` — at L66.
- `util.MustParseUUID(s)` — package-level util.
- `util.UUIDToString(pgtype.UUID) string` — same.
- `db.Queries.GetSquadInWorkspace(...)` — sqlc-generated, already imported.

**Conclusion**: zero new helpers required. New test file is purely assertions + minimal seed.

## 3. What Upstream's Reference Tests Already Cover

Reference: `/Users/jiangjianyan/Downloads/multica-main/server/internal/handler/squad_briefing_claim_test.go` (216 lines).

| Test | Asserts | Matches my task-section # |
|---|---|---|
| `TestClaim_LeaderTaskFromCommentMention_InjectsBriefing` | Issue `assignee_type='agent'` + leader task `(is_leader_task=true, squad_id=<id>)` → claim returns 200 + `agent.Instructions` contains `## Squad Operating Protocol` and `## Squad Roster`. **This is MUL-3724's repro.** | #2 below |
| `TestClaim_NonLeaderTask_NoBriefing` | `is_leader_task=false, squad_id=<id>` on same fixture → briefing NOT injected. Prevents over-permissioning. | (extra) |
| `TestClaim_LeaderTaskWithDanglingSquadID_NoBriefing` | Hard-delete `squad(id)` AFTER task enqueued (no FK by design — confirms row still carries `squad_id`) → claim 200, briefing NOT injected. | #3 below |
| `TestClaim_LeaderTaskWithoutSquadID_NoBriefing` | `is_leader_task=true, squad_id=NULL` (legacy / pre-migration-12X row) → no briefing. Guards against panic/guess. | #3 below |

Helpers in upstream file:
- `squadBriefingClaimFixture{RuntimeID, AgentID, SquadID, IssueID}` — `IssueID` always has `assignee_type='agent'` (the MUL-3724-defining setup).
- `newSquadBriefingClaimFixture(t, ctx, name)` — creates a runtime + agent with **empty instructions** + issue marked agent-assigned + a squad with that agent as leader. (Forcing empty agent instructions lets the test assert the briefing as the sole content.)
- `enqueueClaimTask(t, ctx, fx, isLeader bool, withSquadID bool) string` — raw SQL `INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority, is_leader_task, squad_id) VALUES (...)` returning the task ID with a `t.Cleanup(DELETE)`. **This helper hides the schema column name in one place** — so when we port to the local fork, our copy of `enqueueClaimTask` will use the same parameter shape, and a future column rename touches one site.
- `claimAgentInstructionsForTest(t, runtimeID) (string, string, string)` — wraps `claimAndDecodeAgent` and also returns raw body for diagnostics.

## 4. Proposed New Tests (with concrete behavior to assert)

### Test file: `/Users/jiangjianyan/jjy/multica-main/server/internal/handler/squad_briefing_claim_test.go`

This is a **new** file (does not exist locally today; the existing `squad_briefing_test.go` keeps its current content untouched).

#### Helpers (~70 lines)

```go
// squadBriefingClaimFixture wires a runtime + leader agent + squad. The agent
// holds the runtime, has empty instructions, and is the squad's leader. The
// issue is assigned to a plain AGENT (not the squad) — that mismatch is the
// exact precondition MUL-3724 fixes: pre-fix, the briefing gate keyed off
// issue.assignee_type='squad' and silently skipped injection. Post-fix the
// gate keys off task.is_leader_task + task.squad_id, so the briefing flows.
type squadBriefingClaimFixture struct {
    RuntimeID string
    AgentID   string       // squad leader, holds runtime, instructions=''
    SquadID   string
    IssueID   string       // assignee_type='agent' (NOT squad)
}

// enqueueClaimTask inserts a leader task directly into agent_task_queue so
// the test bypasses the comment / autopilot enqueue paths and locks in the
// daemon-side gate in isolation. The withSquadID flag drives migration 12X
// compatibility: withSquadID=false simulates a pre-migration or back-fill
// row that lands with squad_id=NULL.
func enqueueClaimTask(t *testing.T, ctx context.Context,
    fx squadBriefingClaimFixture, isLeader, withSquadID bool) string
```

`newSquadBriefingClaimFixture(t, ctx, name)` mirrors the upstream: creates a runtime via `createHandlerTestAgent` (already in the local package), then `UPDATE agent SET instructions = ''` on that leader, then `INSERT issue (..., assignee_type='agent', assignee_id=$agentID)`, then `INSERT squad (..., leader_id=$agentID)`. All wraped in `t.Cleanup` DELETEs.

`claimAgentInstructionsForTest(t, runtimeID) (taskID, instructions, rawBody string)` — wraps `claimAndDecodeAgent` (already at `squad_briefing_test.go:292`) and also returns the raw body for diagnostics.

#### Test 1 (MUL-3724 reproduction, task #2): `TestClaim_LeaderTaskOnAgentIssue_InjectsBriefing`

Pre-fix pass condition: regression test that fails on the **pre-MUL-3724 gate** (issue.assignee_type='squad') and passes once the task-based gate lands.

- **Given** an issue with `assignee_type='agent', assignee_id=fx.AgentID` (NOT squad).
- **When** we enqueue a leader task via raw SQL: `is_leader_task=true, squad_id=fx.SquadID`.
- **And** the leader agent (the squad leader) calls `ClaimTaskByRuntime`.
- **Then** the response is 200.
- **And** `resp.task.agent.instructions` contains **both** `## Squad Operating Protocol` and `## Squad Roster`.
- **And** (strength guard) the briefing mentions the squad name — pinned to a string fixture, not a regex.

Pre-fix, this test fails at the briefing assertion: the gate `issue.AssigneeType == "squad"` short-circuits and the leader agent returns its (empty) instructions, so both substrings are missing.
Post-fix, the gate `task.IsLeaderTask && task.SquadID.Valid` evaluates true, briefing is appended.

#### Test 2 (task #3 — NULL squad_id): `TestClaim_LeaderTaskWithoutSquadID_NoBriefing`

Leader task with `is_leader_task=true, squad_id=NULL` — the legacy / pre-migration / new-fleet-without-squad row.

- **Then** response is 200.
- **And** `instructions` does NOT contain `## Squad Operating Protocol` or `## Squad Roster`.
- **Reason**: even pre-fix this also returns false at the gate (`issue.AssigneeType != "squad"`) — so we need to slightly escalate the gate semantics: post-fix, the gate is `task.IsLeaderTask && task.SquadID.Valid`. The `SquadID.Valid` half is what we're testing. To make this a **distinct** failing case from the pre-fix behavior, the issue itself must have `assignee_type='agent'` (not squad), and we must assert that it stays non-leader-framed even when the task says "I'm a leader".

#### Test 3 (task #3 — dangling squad_id): `TestClaim_LeaderTaskWithDanglingSquadID_NoBriefing`

Squad hard-deleted AFTER task enqueue. Locked in by migration 12X design choice (no FK).

- Insert leader task with `is_leader_task=true, squad_id=fx.SquadID`.
- `DELETE FROM squad WHERE id = fx.SquadID`.
- Confirm task still carries the (now orphaned) `squad_id` by `SELECT squad_id = $2 FROM agent_task_queue WHERE id = $1` returning true.
- **Then** claim is 200.
- **And** no squad briefing substring appears.
- **Reason**: locks the no-FK / no-stale-briefing contract explicitly stated in migration 12X's comment block.

#### Test 4 (extra, mirrors upstream `TestClaim_NonLeaderTask_NoBriefing`): `TestClaim_NonLeaderTaskWithSquadID_NoBriefing`

- `is_leader_task=false, squad_id=fx.SquadID` on the agent-assigned issue.
- **Then** no briefing injection.
- **Reason**: guard against the gate becoming "any task on the same workspace has squad briefing if `squad_id` is set". The fix is `IsLeaderTask && SquadID.Valid` — both required.

#### Negative control (Test 0, ~10 lines): `TestClaim_NonLeaderTask_SquadID_NULL_NoBriefing`

Same fixture as Test 2 but `is_leader_task=false`. Trivial; cheap insurance.

### Migration test (task #4): `/Users/jiangjianyan/jjy/multica-main/server/migrations/12X_task_squad_id_test.go`

This is a Go integration test in the `migrations` package, run against `make test`. (The local fork uses migration files alone; pair this with a sqlc roundtrip test.)

A minimal sqlc-model sanity check (~20 lines):

```go
func TestAgentTaskQueueSquadIDColumnReadsAndWrites(t *testing.T) {
    // 1. Insert a row with squad_id=NULL  → reload, assert SquadID.Valid==false
    // 2. Update that row to squad_id=$squad  → reload, assert SquadID.String matches
    // 3. Insert a leader task with non-null squad_id from the start → reload, assert SquadID.Valid==true
    // 4. Insert a non-leader task with squad_id=NULL → reload, assert SquadID.Valid==false AND IsLeaderTask.Valid==false
}
```

This is the **only** test that doesn't depend on the daemon claim path — it just proves the sqlc model round-trips. The schema test in the daemon block above proves the column gets queried correctly.

The upstream comment on migration `127` explicitly states:

> No FK to squad(id) on purpose ... the daemon's GetSquadInWorkspace lookup simply returns no row ... claim path skips injection ... never emits a stale briefing.

This rule should be encoded as a **second** tiny migration-test (~10 lines): assert `agent_task_queue.squad_id` has **no** foreign-key constraint referencing `squad(id)` (query `pg_constraint` / `information_schema`). The point is to lock in the explicit non-FK design — a future migration that accidentally re-adds the FK would re-introduce the cross-table lock risk the comment warns about. Keep it small.

### Total line budget

| File | New lines | Why |
|---|---|---|
| `server/internal/handler/squad_briefing_claim_test.go` | ~225 | 4 positive/negative tests + fixture + 2 helpers. Mirrors upstream at 216 lines. |
| `server/migrations/12X_task_squad_id.up.sql` (existing) | n/a | One migration; the patch is small (`ALTER TABLE ... ADD COLUMN squad_id UUID NULL; CREATE INDEX ... WHERE squad_id IS NOT NULL`). |
| `server/migrations/12X_task_squad_id.down.sql` | ~5 | `DROP INDEX; ALTER TABLE ... DROP COLUMN squad_id;` |
| `server/internal/handler/12X_task_squad_id_test.go` (or `migrations/12X_task_squad_id_test.go`) | ~30 | sqlc roundtrip + no-FK invariant. |
| `pkg/db/generated/*.sql.go` (sqlc) | regenerated | New column appears on `AgentTaskQueue` struct → SqlcModel `SquadID pgtype.UUID`. No hand edit. |

### Estimated total lines of **test code**: **~255 lines**.
Estimated total lines of **non-test (production) code changed**: ~95 lines
- daemon.go gate swap (~10 lines diff)
- service/task.go signature extension on 2 methods (~6 lines diff)
- ~15 caller updates passing `squad.ID` (autopilot.go, comment.go, squad.go, issue.go, issue_child_done.go — 5 sites at ~1 line each)
- migration up/down (~10 + 5 lines)
- sqlc regeneration (no hand count)
- Drop a comment in daemon.go's briefing block explaining the gate is keyed off `task.SquadID` now

## 5. Open Question / Review Item

The briefing gate in daemon.go lines 1391-1409 is currently **in the `if task.IssueID.Valid` branch** — i.e. it only runs when the leader task carries an `IssueID`. Upstream preserves this exact placement. The new gate `task.Issuer.Task && task.SquadID.Valid` still sits inside `if task.IssueID.Valid`. Confirm with reviewer whether leader tasks **without** an issue ID (none observed in production; comments always carry a target issue) should ever be briefed. If yes, gate moves up one level; that is **out of MUL-3724 scope** and should be a separate test/change.

---

## Test files to write (final list)

1. `/Users/jiangjianyan/jjy/multica-main/server/internal/handler/squad_briefing_claim_test.go` (~225 lines, new) — locks in 4 briefing-gate behaviors keyed on `(is_leader_task, squad_id)`.
2. `/Users/jiangjianyan/jjy/multica-main/server/migrations/12X_task_squad_id_test.go` (~30 lines, new) — sqlc model roundtrip + asserts no FK on `squad_id`.
3. `/Users/jiangjianyan/jjy/multica-main/server/internal/handler/squad_briefing_test.go` — **unchanged** (existing `TestClaimTask_LeaderGetsBriefing` and `TestClaimTask_NonLeaderGetsNoBriefing` stay; they exercise the issue-assigned-to-squad branch, which the new gate passes through too).
4. `/Users/jiangjianyan/jjy/multica-main/server/internal/service/task.go` — **no new test file**; the signature extension is mechanically covered by the call-site tests in callers, and any unintended behavior change here (e.g. dropping `SquadID` somewhere) is caught by the handler-level tests since `enqueueClaimTask` in the new test file reads back via the same sqlc model.

**Estimated total lines of test code: ~255 lines.**
