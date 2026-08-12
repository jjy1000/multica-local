---
name: 2026-07-01-MUL-3724-implementation
created: 2026-07-01T23:50:00Z
updated: 2026-08-12T13:31:32Z
status: complete
target_version: 0.2.97
baseline: 0.2.96
upstream_fix: MUL-3724
---

# MUL-3724 Cherry-Pick Implementation Plan — Localized Multica 0.2.97

## Goal
Apply upstream fix MUL-3724 (squad leader boots with zero briefing) to the localized fork. Bump 0.2.96 → 0.2.97 (server-only release; desktop app repackaging deferred). Migration 127 + sqlc regen + service signature change + 6 caller updates + briefing-gate rewrite + read-time fallback + targeted tests. Pre-existing data stays intact (additive nullable column). Production data path stays online.

## Scope decisions (user-confirmed)
1. **History fallback**: add per-claim read-time fallback in daemon.go for `task.SquadID IS NULL` leader tasks; falls back to legacy `issue.AssigneeType=='squad'` lookup. Bounded; self-heals as new enqueues populate the column.
2. **Release shape**: server-only update. `apps/desktop/package.json` 0.2.97; no DMG rebuild; running 0.2.96 app pulls new bundled Go server on next launch, applies migration 127 automatically via `make migrate up` in `server-manager.ts`.
3. **Risk profile**: source commit + server hot-replace + 30s smoke; failure → 60s rollback (re-tag, revert commit, restart app).

## Reference plans (from review)
- Architecture: `/Users/jiangjianyan/jjy/multica-main/.omc/plans/2026-07-01-MUL-3724-arch-review.md`
- Migration: `/Users/jiangjianyan/jjy/multica-main/.omc/plans/2026-07-01-MUL-3724-migration-review.md`
- Test: `/Users/jiangjianyan/jjy/multica-main/.omc/plans/2026-07-01-MUL-3724-test-strategy.md`

## Phases

### Phase 1 — Schema + sqlc regen (Tasks #1, #2)

**1.1** Create `server/migrations/127_task_squad_id.up.sql`:
```sql
-- agent_task_queue.squad_id records the squad a leader-task belongs to.
-- See arch-review.md §1A. Additive nullable column; no FK by design (avoids
-- cross-table lock against squad archive / hard-delete). Partial index
-- serves admin/debug queries; the daemon claim path does NOT use it.
ALTER TABLE agent_task_queue
    ADD COLUMN squad_id UUID NULL;

CREATE INDEX agent_task_queue_squad_id_idx
    ON agent_task_queue (squad_id)
    WHERE squad_id IS NOT NULL;
```

**1.2** Create `server/migrations/127_task_squad_id.down.sql`:
```sql
DROP INDEX IF EXISTS agent_task_queue_squad_id_idx;
ALTER TABLE agent_task_queue DROP COLUMN IF EXISTS squad_id;
```

**1.3** `cd server && sqlc generate`. Verify diff in `pkg/db/generated/agent.sql.go` (SquadID added on `AgentTaskQueue` + every `Create*Params`) and `pkg/db/generated/models.go` (struct field added).

**1.4** Sanity check: `cd server && go build ./...` should still compile (sqlc generates `pgtype.UUID{}` zero-value fields which are safe).

### Phase 2 — Service signature change (Task #3)

In `server/internal/service/task.go`:
- `EnqueueTaskForSquadLeader` (L521): add `squadID pgtype.UUID` param.
- `EnqueueTaskForSquadLeaderWithHandoff` (L528): add `squadID pgtype.UUID` param.
- `enqueueMentionTask` (L532): thread `squadID` into `CreateAgentTaskParams.SquadID` (set `pgtype.UUID{Bytes: squadID.Bytes, Valid: squadID.Valid}`).

In `server/internal/service/task.go` `enqueueQuickCreateTask` (around L625): also stamp `SquadID` on the row from `payload.SquadID` (parity with comment path). Helper: `parseSquadID(payload.SquadID)`.

### Phase 3 — Caller updates (Task #4)

Six sites (matches arch-review §1E). All already have `squad` in local scope.

| # | File:line | Pass |
|---|-----------|------|
| 1 | `server/internal/service/autopilot.go:406` | `ap.AssigneeID` (when `ap.AssigneeType == "squad"`) |
| 2 | `server/internal/service/issue.go:471` | `squad.ID` (loaded at L457) |
| 3 | `server/internal/handler/comment.go:1328` | `trigger.Squad.ID` |
| 4 | `server/internal/handler/comment.go:1341` | `trigger.Squad.ID` |
| 5 | `server/internal/handler/comment.go:1410` | `trigger.Squad.ID` |
| 6 | `server/internal/handler/issue_child_done.go:519` | `squad.ID` (loaded at L477) |
| 7 | `server/internal/handler/squad.go:1003` | `squad.ID` (loaded at L988) |

After all 7 sites updated: `cd server && go build ./...` should be silent (no callers missed).

### Phase 4 — Briefing gate rewrite (Task #5)

In `server/internal/handler/daemon.go` L1387-1441:

**Extract helper** (placed near `buildSquadLeaderBriefing`):

```go
// shouldInjectSquadLeaderBriefing decides whether to inject the squad leader
// briefing (Operating Protocol + Roster) onto the claiming agent's Instructions.
//
// Inputs (gate):
//   - task.IsLeaderTask: stamped at enqueue time (migration 090). True means
//     this is a leader-role task.
//   - task.SquadID: stamped at enqueue time (migration 127). The squad whose
//     leader this task targets.
//
// Backfill (history safety):
//   When task.SquadID is NULL (pre-migration-127 in-flight task), fall back
//   to the legacy issue.AssigneeType=='squad' lookup. This path becomes
//   dead code as soon as all new enqueues populate the column.
//
// Defense-in-depth (always):
//   squad.LeaderID == agent.ID re-check — handles leader-swapped-after-enqueue
//   and squad-hard-deleted-after-enqueue (no FK). No stale briefing emitted.
//
// Returns the resolved squad row + a boolean. Caller does the briefing injection.
func shouldInjectSquadLeaderBriefing(
    ctx context.Context,
    q db.Querier,
    task db.AgentTaskQueue,
    agentID string,
    issue db.Issue,
) (db.Squad, bool) {
    if task.AgentID == (pgtype.UUID{}) { return db.Squad{}, false } // nil-agent guard
    if !task.IsLeaderTask.Bool { return db.Squad{}, false }

    // Primary: task-stamped squad_id (post-migration-127).
    if task.SquadID.Valid {
        squad, err := q.GetSquadInWorkspace(ctx, db.GetSquadInWorkspaceParams{
            ID: task.SquadID, WorkspaceID: issue.WorkspaceID,
        })
        if err != nil {
            return db.Squad{}, false // dangling squad_id → silent skip
        }
        if util.UUIDToString(squad.LeaderID) != agentID {
            return db.Squad{}, false // leader swapped → silent skip
        }
        return squad, true
    }

    // Fallback: pre-migration-127 in-flight tasks.
    if issue.AssigneeType.Valid && issue.AssigneeType.String == "squad" && issue.AssigneeID.Valid {
        squad, err := q.GetSquadInWorkspace(ctx, db.GetSquadInWorkspaceParams{
            ID: issue.AssigneeID, WorkspaceID: issue.WorkspaceID,
        })
        if err != nil {
            return db.Squad{}, false
        }
        if util.UUIDToString(squad.LeaderID) != agentID {
            return db.Squad{}, false
        }
        return squad, true
    }

    return db.Squad{}, false
}
```

**Replace the gate block** at daemon.go:1387-1441 with a call to this helper + the same `briefing` injection code.

### Phase 5 — WS payload + minor surface (Task #6)

`server/internal/service/task.go:2175` (`broadcastTaskEvent` payload):
- Add `"squad_id": util.UUIDToString(task.SquadID)` only when `task.SquadID.Valid`. The renderer schema already uses `parseWithFallback` and `.loose()` so unknown keys are ignored.

### Phase 6 — Tests (Tasks #7, #8, #9)

**6.1** New file `server/internal/handler/squad_briefing_claim_test.go` (~225 lines):
- 4 tests + fixture + helpers. Mirrors upstream `squad_briefing_claim_test.go` and uses local fork's existing test helpers (`testHandler`, `claimAndDecodeAgent`, `createHandlerTestAgent`, `seedSquadForBriefing`).
- Cover: leader-on-agent-issue briefing injection, non-leader no-injection, dangling-squad_id no-injection, NULL-squad_id no-injection.

**6.2** Modify `server/internal/handler/squad_briefing_test.go` `queueSquadIssueTaskFor` (L322-340):
- Add `squad_id` column to the INSERT so the existing `TestClaimTask_LeaderGetsBriefing` (L346) continues to pass after the gate change.

**6.3** Add new test `TestClaimTask_LeaderGetsBriefing_OnMentionTriggeredLeaderTask` in `squad_briefing_test.go` (~60 lines):
- The MUL-3724 reproduction: issue `assignee_type='agent'`, task `is_leader_task=true, squad_id=<leader's squad>`, claim → briefing injected.

**6.4** Add new file `server/migrations/12X_task_squad_id_test.go` (~30 lines):
- sqlc roundtrip (NULL ↔ non-NULL read/write).
- Asserts `agent_task_queue.squad_id` has no FK to `squad(id)` (locks the no-FK design choice).

### Phase 7 — Verification (Task #10)

Run in this order; capture all green output:

```bash
cd /Users/jiangjianyan/jjy/multica-main
pnpm typecheck
cd server && go build ./... && go vet ./... && go test ./...
cd .. && pnpm test
make check
```

Stop on first red. Re-run after any fix.

### Phase 8 — Version + commit (Tasks #11, #12, #13)

**8.1** `apps/desktop/package.json`: `"version": "0.2.96"` → `"0.2.97"`. (Only this file per repo convention — `bundle-cli.mjs` reads from here.)

**8.2** Add CHANGELOG entry under `## 0.2.97 (2026-07-01)`:
```
- fix(daemon): re-inject squad-leader briefing for comment-mention leader tasks (MUL-3724)
- feat(db): add agent_task_queue.squad_id column + partial index (migration 127)
- refactor: extract shouldInjectSquadLeaderBriefing helper with per-claim fallback
```

**8.3** `git init .` (if not already a repo) + initial commit + tag `0.2.97-source` + push.

**8.4** Pre-update snapshot: `bash ~/.multica/scripts/pre-update-snapshot.sh`. If exit 1 → abort and investigate.

**8.5** Bundle Go binaries: `pnpm --filter @multica/desktop bundle-cli`. Copies server/migrate binaries + migrations + docker-compose.yml into `apps/desktop/resources/`.

**8.6** Restart `Multica.app` (kill existing instance). Verify:
- `curl -s http://localhost:8090/health` returns 200.
- Migration 127 applied: `docker exec multica-postgres-1 psql -U multica -d multica -c "\d agent_task_queue"` shows `squad_id uuid` column.
- Smoke test: open Multica, post a comment `@<squad-name>` on an agent-assigned issue, claim as leader → briefing present.

**8.7** If smoke fails within 30s → rollback:
- `git revert HEAD` (or reset to 0.2.96-source tag).
- Re-run bundle-cli.
- Restart app. Total rollback time: ~60s.

### Phase 9 — Reviewer pass (Task #14)

`code-reviewer` agent reviews the final diff (≥10 files). Pass criteria:
- Zero re-added telemetry / OAuth / cloud / Slack / invitation / electron-updater / discord / help-launcher.
- Migration is additive; no data loss.
- Briefing gate is single-helper, unit-tested.
- All 7 callers compile.
- No silent footguns: `task.SquadID.Valid` checked before deref, `util.UUIDToString(squad.LeaderID) == agentID` gate kept, `GetSquadInWorkspace` err path silent-skip.

## Risks (top 3 from arch-review §5)
1. **HIGHEST**: briefing gate at daemon.go:1387-1441 has no direct unit test today. Mitigation: Phase 4 helper + Phase 6.1 dedicated tests.
2. **HIGH**: sqlc regen diff scope. Mitigation: pin sqlc version; visually diff; spot-check that no unrelated cosmetic re-orderings slipped in.
3. **HIGH**: caller signature mismatch at compile time. Mitigation: Phase 3 finishes with clean `go build ./...`.

## Rollback
- 60-second hot revert: `git reset --hard 0.2.96-source` → `pnpm --filter @multica/desktop bundle-cli` → restart Multica.app. Migration 127 is additive nullable; the rollback server simply never writes `squad_id`. The column will sit unused but harmless until next 0.2.97+ run repopulates it.

## Out of scope
- DMG repackaging (Electron 39 NSAlert blocker — separate workstream).
- Migration 128/130/131 (autopilot collaborator, comment routing, slack origin) — all Slack/cloud-adjacent, excluded by localization policy.
- 0.3.0 sunset of the per-claim fallback — track as separate todo for after 0.2.97 stabilizes.