---
name: release-notes-0.5.67
created: 2026-08-24T21:45:00Z
updated: 2026-08-24T21:45:00Z
---

# 0.5.67 — Mythos Whack-a-Mole + F-027 GAP Closure

**Released:** 2026-08-24
**Branch:** `epic/0.5.13-integration`
**Builds on:** 0.5.60 (19 atomic commits from the labs audit batch)

## TL;DR

7 atomic commits between 0.5.60 and 0.5.67 closed every load-bearing mythos pipeline bug that had been latent since the lab was first written, plus one security HIGH found by a 6-agent parallel audit after the fact. End-to-end mythos_swarm now works: HTTP POST `/api/experimental/mythos-swarm/run` returns 200 in ~6-7 minutes with `mythos_run.status='completed'` and the daemon has actually executed both sub-issues.

## The whack-a-mole chain

The 0.5.61 handler stale-flag-gate deletion unmasked a stack of pre-existing latent bugs that had been hidden for the lab's entire lifetime. Each ship peeled back one layer:

| Ship | Commit | Layer | What it fixed |
|---|---|---|---|
| 0.5.61 | `a1df50662` | handler gate | `if !experimental.DefaultFor("mythos_swarm")` 404'ing every per-user enabled lab (also removed from `decision_sync.go` 198-200) |
| 0.5.61 | `fd296ad8e` | daemon IPC | `upstream-registry.ts` `authToken()` reads JWT + attaches `Authorization: Bearer` to POST/DELETE `/__experimental/upstream` |
| 0.5.62 | `18de563a1` | runner.SQL.1 | `runner.go:578,631` `CreatorType: "system"` → `"agent"` (was tripping 23514 `issue_creator_type_check` on every fork) |
| 0.5.63 | `316200394` | runner.SQL.2 | `IncrementIssueCounter` → `Number:` thread-through on both `CreateIssueParams` (was tripping 23505 `uq_issue_workspace_number`) |
| 0.5.64 | `3a09c6bf2` | runner.enqueue | `mythos.Service.TaskService` wired, both `runLoopIteration` + `runCoda` call `TaskService.EnqueueTaskForIssue` after each `CreateIssue` (was runner hangs forever — daemon never saw sub-issues) |
| 0.5.65 | `36afb959e` | waitFn.timeout | `mythosWaitTimeout` 60s → 5min (daemon takes 3-5min to first-claim, old timeout caused false-positive `completed`) |
| 0.5.66 | `2851a6ce1` | waitFn.output | preserve partial `waitFn` output when daemon completes 1-2s after timeout (was runner discarded captured body, `coda_conclusions` permanently empty) |
| 0.5.67 | `7a0df9acc` | security.F-027 | extend `isAllowedTargetApiUrl` gate to upstream-registry IPC (was 0.5.61 attached Bearer without URL allowlist — JWT could leak to public host) |

## Bug-5 (operational, no version bump) — runtime re-bind

5 mythos agents were bound to offline `7738581d-...` "Mythos Swarm Lab Runtime" while 3 active runtimes (Codex / Opencode / Claude on daemon `019e93ff-...`) sat unused. Daemon claim `WHERE runtime_id = ?` filter excluded the mythos tasks → `queued` forever. Fix: `UPDATE agent SET runtime_id='256e143c-...' WHERE name LIKE 'mythos%'` — single SQL statement, no version bump, no code change. 5 rows updated.

## Bug-9 + Bug-10 — DEFERRED (out of labs audit scope)

- **Bug-9** — no sole-mode recovery supervisor: when daemon completes post-timeout, `mythos_run.status` stays stuck at `running` indefinitely. The 0.5.65 timeout fix reduced this from "always" to "rarely" but didn't eliminate it. Fix would be a supervise goroutine for sole mode (enhancer-mode already has one).
- **Bug-10** — `curl --max-time 540` wasn't enough for daemon paths >9min. Async daemon callback + status update would fix this.
- **ship-mac.sh 6b `find -printf` GNU/BSD incompat** — every 0.5.61-0.5.66 ship left no `.omc/backups/` entry. Fixed at `scripts/backup.sh:246` (commit `00468bb20` at 0.5.67) — replace GNU `-printf '%T@ %p\n'` with BSD-portable `-exec stat -f '%m %N' {} +`. Smoke-verified with 35 mock backup dirs: retention branch fires correctly, oldest moved to `_archive/`, new write produces all 4 artifacts.

## Regression pins (4 atomic tests)

| Test | File | Pins |
|---|---|---|
| `TestRunnerCreatorType_IsNotSystem` | `runner_test.go:88` | 3 spacing variants of `CreatorType: "system"` literal |
| `TestRunnerAssignsIssueNumber` | `runner_test.go:107` | `Number: issueNumber,` + `Number: codaNumber,` + `IncrementIssueCounter` call + ≥2 `CreateIssueParams` blocks |
| `TestRunnerEnqueuesSubIssues` | `runner_test.go:137` | `TaskService.EnqueueTaskForIssue` + `0.5.64 audit fix` + `taskService *service.TaskService` |
| `TestRunnerPreservesPartialWaitFnOutput` | `runner_test.go:166` | `case out != "":` + `0.5.66 audit fix` |
| `TestPostDecisionSync_FiresWhenLoopbackURLSet` | `decision_sync_test.go:432` | behavioral (httptest server + flipSemanticaDefault + loopback URL set) |
| `TestMythosWaitConstants_InRange` | `experimental_mythos_run_test.go:124` | `mythosWaitTimeout >= 5*time.Minute` (was `< 5*time.Minute` pre-fix) |
| `TestUpstreamRegistryAttachesBearerHeader` (4 cases) | `upstream-registry.test.ts` (vitest) | loopback fallback + reject public desktop.json + accept LAN + reject public MULTICA_API_URL |

## End-to-end verification (0.5.66 final)

```
HTTP POST /api/experimental/mythos-swarm/run
  status=200 duration=404.03s
  run_id=91b0f48e-15e1-4a6b-9f49-c3a9076ac4c3
  final_issue_id=a3392d20-07c4-489b-bca2-c713f423722a

mythos_run:
  status=completed (was 'running' in 0.5.64 / 0.5.65 due to no recovery supervisor)
  current_loop=1
  started=20:07:19 → completed=20:14:03 (6m44s)

sub-issues (both daemon-executed):
  51bc3a9f [mythos] iter-1  mythos_loop_researcher  completed  262s  1573 chars
  a3392d20 [mythos] coda    mythos_coda             completed  140s  2108 chars

daemon log evidence:
  20:07:19.803 INF picked task ... task=c819effe issue=51bc3a9f agent=mythos_loop_researcher provider=claude
  20:11:42.816 INF picked task ... task=8bfb912c issue=a3392d20 agent=mythos_coda provider=claude
```

## Files touched (8 source + 1 test new)

```
M  apps/desktop/src/main/experimental/upstream-registry.ts         (0.5.61 Bug-2 + 0.5.67 F-027)
A  apps/desktop/src/main/experimental/upstream-registry.test.ts    (0.5.67 vitest regression)
M  server/internal/handler/experimental_mythos_run.go               (0.5.61 Bug-1 + 0.5.65 Bug-7)
M  server/internal/handler/experimental_mythos_run_test.go          (0.5.65 test update)
M  server/internal/handler/decision_sync.go                         (0.5.61 Bug-1 sister)
M  server/internal/handler/decision_sync_test.go                    (0.5.61 test pin replace)
M  server/internal/service/mythos/runner.go                         (0.5.62/63/64/66 fixes)
M  server/internal/service/mythos/runner_test.go                    (4 regression pins)
M  server/cmd/server/router.go                                     (0.5.64 NewService wire)
M  scripts/backup.sh                                               (0.5.67 BSD-portable fix)
```

Plus `.omc/audit/2026-08-23-labs-issue-integration-audit.md` (590 → 591 lines, 3 audit appends).

## Audit (6-agent parallel, 2026-08-24)

| Agent | Verdict |
|---|---|
| Architecture | ✅ 14 contracts intact; 0 new bypass paths; 2 stale CLAUDE.md cross-refs (now fixed in this release) |
| Code review | 1 🟠 high + 1 🟡 medium + 3 🟢 nit — all addressed except the 3 nits |
| Security | 🔴 **F-027 GAP** + 1 🟡 medium + 3 🟢 low — **F-027 GAP closed in 0.5.67** |
| Docs | ❌ root CLAUDE.md stale at 0.5.60 — **now synced to 0.5.67** |
| Operational | 🔴 `find -printf` GNU/BSD incompat — **fixed in this release** |
| Verifier | Test pin brittleness + 0 coverage gaps — 1 of 2 addresses (upstream-registry.test.ts added) |

## Lessons (for future gate deletions)

The pattern lesson from the whack-a-mole chain is:

> Every gate deletion must peel back **all** downstream correctness checks atomically, not layer by layer.

The audit-driven ship cadence (1 commit per layer) is fine for diagnosis but not for safe production — consider a single combined "mythos end-to-end fix" commit when 2+ consecutive ships are peeling layers off the same latent bug.

**Defensive recommendation**: add a one-shot DB sanity check before declaring a `fix(experimental)` or `fix(handler)` gate-deletion commit complete:
```sql
SELECT COUNT(*) FROM mythos_run WHERE status='running' AND updated_at < NOW() - interval '1 hour';
SELECT COUNT(*) FROM agent_task_queue WHERE status='queued' AND created_at < NOW() - interval '5 minutes';
```
Zero rows expected. Any positive count = a downstream bug surfaced, not yet fixed.

## Verification

- `pnpm typecheck` — 6/6 OK
- `go build ./...` — clean
- `go test -count=1 -timeout 60s ./internal/handler/... ./internal/service/mythos/...` — all green (handler `0.866s`, mythos `0.836s`)
- `pnpm exec vitest run src/main/experimental/upstream-registry.test.ts` — 4/4 OK (`190ms`)
- Ship chain: `bash scripts/ship-mac.sh --yes` — succeeded 0.5.61-0.5.67 (backup step was the only failed sub-step in 0.5.61-0.5.66, now fixed)
- Multica.app = 0.5.67, cold-start 3-check pass (PG :5432 + server :8090 + `/health` ok)
- Real mythos end-to-end: HTTP 200 in 6m44s, `mythos_run.status='completed'`, daemon executed both sub-issues (loop=262s, coda=140s)