---
name: 2026-07-01-MUL-3724-migration-review
created: 2026-07-01T00:00:00Z
updated: 2026-07-01T00:00:00Z
migration: 127_task_squad_id
verdict: ACCEPT-WITH-MODS
---

# Migration 127 `127_task_squad_id` — Compatibility Review

Upstream migration under review, intended for the localized fork (currently at 0.2.96, last applied migration 126). Two files:

- `127_task_squad_id.up.sql`
- `127_task_squad_id.down.sql`

Schema delta: `agent_task_queue.squad_id UUID NULL` + partial index `agent_task_queue_squad_id_idx ON agent_task_queue(squad_id) WHERE squad_id IS NOT NULL`.

## 1. Catalog conflict check

Local migrations directory enumerated; last applied is `126_runtime_profile_drop_gemini`. **No `127_*` file exists locally → no numbering conflict.**

The candidate filenames are compatible with the existing `NNN_short_snake.up.sql / .down.sql` convention (matches 117/124/125/126 style). Filename prefix `127_` is the next free number — correct.

## 2. Column-name conflict check

`squad_id` is already a column on two other tables in this fork:

| Table | Migration | FK | Nullable | Notes |
|---|---|---|---|---|
| `squad_member` | `084_squad.up.sql` | yes → `squad(id)` ON DELETE CASCADE | NOT NULL | junction table |
| `autopilot_run` | `096_autopilot_squad_assignee.up.sql` | yes → `squad(id)` ON DELETE SET NULL | NULL | attribution hook |

The new column lands on **`agent_task_queue`** — a different table, so no in-table collision. Per-table column scoping means the three columns coexist without conflict. (sqlc generated structs confirm: `SquadMember.SquadID` and `AutopilotRun.SquadID` exist; `AgentTaskQueue` does NOT yet have it — see §5.)

## 3. Index-name conflict check

Existing `squad_id` indices in the catalog:

| Index name | Table | Migration |
|---|---|---|
| `idx_autopilot_run_squad_id` | `autopilot_run` | 096 |
| `idx_squad_member_squad` | `squad_member` | 084 |

The proposed `agent_task_queue_squad_id_idx` does not collide with either. PG requires per-table index names to be unique within a schema; since no other index on `agent_task_queue` is named with this pattern, applying the up SQL is safe. Verified via `grep -rn 'agent_task_queue_squad_id_idx' server/` → no prior hits.

## 4. ALTER TABLE ADD COLUMN on a hot table

`agent_task_queue` is documented in the migration comment as "a hot, high-write task queue". In PG ≥ 11, `ADD COLUMN ... NULL` without a default is a metadata-only change — it does not rewrite the table and takes an `ACCESS EXCLUSIVE` lock only briefly enough to update the catalog. For a single-user local fork with at most hundreds of rows in flight, this is instantaneous.

If we ever scaled past ~1M rows, the partial index `CREATE INDEX` (non-CONCURRENTLY) would still take a `SHARE` lock and block writes for the duration of the build. The migration does **not** use `CONCURRENTLY`. The migration comment itself flags the daemon claim path does NOT use this index — so it is admin/debug-only. Acceptable for our scale; documented as an assumption: **for single-user embedded PG with thousands-of-rows tops, no special handling needed**. If we ever back-port to a multi-tenant server, switch to `CREATE INDEX CONCURRENTLY` and run in a separate post-deploy step (outside a migration).

## 5. Sqlc regeneration — biggest concrete concern

`pkg/db/generated/models.go` defines:

```go
type AgentTaskQueue struct {
    ID, AgentID, IssueID, ..., HandoffNote, PrepareLeaseExpiresAt
    // no SquadID
}
```

After applying 127.up.sql:

- **Database has the column.** Queries written via sqlc's `CreateAgentTask(...)` use an explicit column list (sqlc convention), so the `INSERT` will silently skip the new column — pre-existing tasks + new tasks both end up with `squad_id = NULL`. No crash.
- **Claim path reads via `SELECT * agent_task_queue WHERE id=$1`** (sqlc `GetAgentTask`). The row scan decodes whatever columns are listed in `models.go`. The actual row from PG will have an extra column that sqlc does not project → the Go `AgentTaskQueue` returned will have `SquadID` zero-valued `pgtype.UUID{}` (Valid=false) regardless of the DB value. No nil deref risk because nothing reads `task.SquadID` yet (see §6).
- **Net effect at 0.2.96**: the column exists in the DB, is always NULL for new rows, and is never read by the application. Pure schema overhead, no functional change.

To make the column actually useful (stamp at enqueue, read at claim), three coordinated changes are required:

1. Regenerate sqlc (`make sqlc`) so `AgentTaskQueue.SquadID pgtype.UUID` and `CreateAgentTaskParams.SquadID pgtype.UUID` exist.
2. Update `CreateAgentTask` SQL to include `squad_id` in its column list (sqlc may auto-detect from `INSERT ... RETURNING *` on a SELECT-style query, but `CreateAgentTask` is typically an explicit insert — verify after regen).
3. Stamp the new field in `enqueueMentionTask` when `isLeader==true`. Resolve the squad ID by looking up `squad` rows where `leader_id = agentID` (existing pattern from `GetSquadByLeaderAgent` or similar) — but this is the very ambiguity the migration is trying to solve. A cleaner stamp source is from the call site: `EnqueueTaskForSquadLeader` is only called when the comment's issue is assigned to a squad — pass the `issue.AssigneeID` (when `AssigneeType == 'squad'`) as `squadID` into the helper. Confirm at call sites (handlers in `internal/handler/comment.go`, `internal/handler/issue.go`) before plumbing.

**Without these three changes, migration 127 is a no-op that adds storage and an index for no runtime benefit.** That is not a blocker — additive nullable columns with no writers are legal per the repo's "forward-only, additive" convention — but the migration should land **together with** the corresponding Go code change, or it should land alone only as a deliberate "schema-only prep" PR clearly labeled as such.

## 6. Briefing path audit (`internal/handler/daemon.go`)

I traced every `SquadID` reference in `daemon.go`. The two relevant blocks:

**Block A (lines 1387–1411, issue-bound dispatch):**

```go
if resp.Agent != nil && issue.AssigneeType.Valid && issue.AssigneeType.String == "squad" && issue.AssigneeID.Valid {
    if squad, err := h.Queries.GetSquadInWorkspace(...); err == nil && uuidToString(squad.LeaderID) == resp.Agent.ID {
        briefing := buildSquadLeaderBriefing(...)
        ...
    }
}
```

Derives squad from `issue.AssigneeID`, not from the task row. **Does not read `task.SquadID`.**

**Block B (lines 1768–1800, quick-create dispatch):**

```go
if resp.Agent != nil && qc.SquadID != "" {
    ...
    if squad, err := h.Queries.GetSquadInWorkspace(...); err == nil && uuidToString(squad.LeaderID) == resp.Agent.ID {
        briefing := buildSquadLeaderBriefing(...)
        ...
    }
}
```

Derives squad from `qc.SquadID` (the JSONB context payload's `squad_id` field), not from the task row. **Does not read `task.SquadID`.**

**Audit conclusion**: there are zero call sites in 0.2.96 that dereference `task.SquadID`. The new column is write-never and read-never. There is no nil-deref risk because there is no deref at all. Block A and Block B are both safe today without migration 127, and they remain safe after applying 127 because they do not look at the new column.

The migration comment ("the daemon uses it at claim time to locate the squad...") describes the *target* state after the Go-side change lands. In 0.2.96 the daemon still does the reverse-lookup inference (`issue.AssigneeID` in block A, `qc.SquadID` in block B), which is fine for our single-leader-per-squad usage and harmless when `task.SquadID.Valid == false` (because we never check).

## 7. Enqueue path audit (`internal/service/task.go`)

`EnqueueTaskForSquadLeader` (line 521) and `EnqueueTaskForSquadLeaderWithHandoff` (line 528) both delegate to `enqueueMentionTask` (line 532). The DB write is:

```go
task, err := s.Queries.CreateAgentTask(ctx, db.CreateAgentTaskParams{
    AgentID:           agentID,
    RuntimeID:         agent.RuntimeID,
    IssueID:           issue.ID,
    Priority:          priorityToInt(issue.Priority),
    TriggerCommentID:  triggerCommentID,
    TriggerSummary:    s.buildCommentTriggerSummary(ctx, triggerCommentID),
    IsLeaderTask:      pgtype.Bool{Bool: isLeader, Valid: isLeader},
    ForceFreshSession: pgtype.Bool{Bool: forceFreshSession, Valid: forceFreshSession},
    HandoffNote:       pgtype.Text{String: handoffNote, Valid: handoffNote != ""},
})
```

**No `SquadID` field on `CreateAgentTaskParams`** (sqlc-generated, doesn't include it yet). The upstream pattern (per migration comment) is "stamp SquadID at enqueue time" — but the Go helper in 0.2.96 does not stamp it. The fix-up requires sqlc regen + plumbing `squadID pgtype.UUID` into `enqueueMentionTask` and `EnqueueTaskForSquadLeader*`. The call sites for these functions (likely `comment.go` for mentions, `issue.go` for assign/promote) need to pass `issue.AssigneeID` when `issue.AssigneeType.String == "squad"`.

`EnqueueQuickCreateTask` (line 625) is structurally similar: it stores `squad_id` in the JSONB `context` payload (`payload.SquadID` at line 647), **not** in the new column. After migration 127, it should additionally stamp the column for parity (and to enable future migrations away from JSONB-only access).

## 8. Downgrade (`127_task_squad_id.down.sql`)

```sql
DROP INDEX IF EXISTS agent_task_queue_squad_id_idx;
ALTER TABLE agent_task_queue DROP COLUMN IF EXISTS squad_id;
```

Per repo convention we never downgrade in production, but the file is safe: idempotent, drops the index first (correct order), uses `IF EXISTS` guards. Dropping a nullable column with no dependent view/function is a metadata-only PG operation.

## 9. Concerns summary

| # | Concern | Severity | Status |
|---|---|---|---|
| 1 | Filename / numbering conflict | — | NONE — 127 is the next free number |
| 2 | Column-name collision on `agent_task_queue` | — | NONE — fresh column on this table |
| 3 | Index-name collision | — | NONE — name unique within table |
| 4 | ADD COLUMN on hot table needs special handling | LOW | OK at our scale; documented assumption |
| 5 | Partial index built without CONCURRENTLY | LOW | OK at our scale; documented assumption |
| 6 | Sqlc regeneration required before column can be written/read | MEDIUM | Documented; column safely ignored until regen |
| 7 | No Go code writes `task.SquadID` in 0.2.96 | MEDIUM | Documented; stamp path requires call-site change |
| 8 | No Go code reads `task.SquadID` in 0.2.96 (briefing) | LOW | Documented; current paths use other sources |
| 9 | Brief migration comment says "the daemon uses it" — current daemon does NOT | MEDIUM | Documentation drift between migration rationale and 0.2.96 reality |
| 10 | No FK by design | — | INTENTIONAL — matches 096 pattern, comment explains |

## 10. Recommendation

**ACCEPT-WITH-MODS.** The migration is technically safe to apply to a 0.2.96 database — no conflicts, no breakage, additive nullable column with no readers is a legal forward-only change. The mods are about *what you do around it*:

1. **Land the migration with the corresponding Go changes in the same PR**, not standalone. Without `make sqlc` + enqueue-side plumbing, the migration is dead schema.
2. **Update the migration comment** to reflect 0.2.96 reality. Right now it says "the daemon uses it at claim time" but the 0.2.96 daemon doesn't. Either change to "intended for daemon use after the planned call-site plumbing lands" or remove the misleading sentence.
3. **Add a backfill step (optional, advisory only)**: pre-existing leader tasks (where `is_leader_task = true`) can have their `squad_id` derived by joining `squad_member WHERE member_type='agent' AND member_id=agent_task_queue.agent_id AND leader_id=agent_task_queue.agent_id` — but only when an agent leads exactly one squad. For agents leading multiple squads the reverse-lookup is ambiguous and the migration's whole point applies. Recommend: do **not** backfill; let those tasks have `squad_id = NULL`. The runtime will continue to work because the briefing path doesn't read this column.
4. **Test the down path locally** before merge. `psql` the embedded PG, apply up, apply down, apply up again, confirm no errors and no orphaned index.

## Verdict

**ACCEPT-WITH-MODS** — biggest risk: shipping the migration without the accompanying sqlc regen + enqueue-side stamping leaves a useless column on a hot table, which violates the repo's "no needless schema churn" spirit even though no rule forbids it.
