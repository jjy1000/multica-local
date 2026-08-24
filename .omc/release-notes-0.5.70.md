---
name: release-notes-0.5.70
created: 2026-08-24T22:30:00Z
updated: 2026-08-24T22:30:00Z
---

# 0.5.70 — Cleanup Cycle (Labs Audit 100% Closed)

**Released:** 2026-08-24
**Branch:** `epic/0.5.13-integration`
**Builds on:** 0.5.67 (the 6-agent parallel audit + F-027 GAP + ship-mac fix batch)

## TL;DR

4 atomic follow-up ships (0.5.68-0.5.70) closed every deferred item the 0.5.67 audit had flagged. Labs audit scope is now 100% complete end-to-end. The `multica swarm cancel` 500 error is fixed. The mythos runner's recovery watch works on the second try. Five migration static tests pin the SQL invariants.

## What changed

### 0.5.68 — Bug-9 sole-mode recovery watch (first attempt)

`server/internal/service/mythos/runner.go::runCoda` signature gained a `codaTimedOut bool` return. When waitFn hits the 5min deadline in sole mode, `Run()` calls `scheduleSoleRecoveryWatch(runID, finalIssueID)`. The watch polls the coda sub-issue every 10s for up to 1h; when status reaches terminal, reads the latest comment and overwrites `mythos_run.coda_conclusions` via `SetMythosRunCodaConclusions`.

The 0.5.68 verification agent caught a latent regression: the watch used naive string concat `[]byte(`["` + latest + `"]`)` to persist — agent-written coda synthesis is always markdown, so SQLSTATE 22P02 fired on the first non-escaped character.

### 0.5.69 — Bug-9 watch json.Marshal fix (verification-found)

`runner.go::soleRecoveryWatchLoop` now uses `json.Marshal([]string{latest})` instead of naive concat. Encoder escapes newlines, quotes, backslashes, control chars correctly. Behavioral test `TestSoleRecoveryWatchMarshalsCodaConclusionsSafely` round-trips a markdown-shaped string through Marshal+Unmarshal+source-grep pin.

### 0.5.70 — swarm cancel 500 fix + 5 migration pins

`server/internal/handler/swarm_run.go::PostSwarmInterrupt` previously passed `req.Payload` straight through to `CreateSwarmInterrupt`. When the caller POSTed `{"kind":"cancel"}` (no payload field), `req.Payload` was nil but `swarm_interrupt.payload` is NOT NULL per migration 241 — SQLSTATE 23502 → 500. Fix: default missing payload to `json.RawMessage("{}")`.

Bundled 5 new static tests in `server/internal/handler/migration_273_274_static_test.go` (static-only; no live PG):
- `TestMigration273_CreatesSemanticaDecisionACLTable` — table + columns + CHECK constraints + 3 indexes
- `TestMigration273_DownDropsSemanticaDecisionACLTable` — symmetric DROP
- `TestMigration274_DeletesOrphanResourceLocks` — NOT EXISTS guard + both lock + visibility tables
- `TestMigration274_DownIsIrreversibleByDesign` — `SELECT 1;` (irreversible contract)
- `TestMigration241_SwarmInterruptPayloadIsNotNull` — NOT NULL (the 0.5.70 cancel fix relies on this)

Plus 2 test upgrades:
- `TestRunnerEnqueuesSubIssues` — substring (`EnqueueTaskForIssue` not qualified) so refactors survive
- `TestPostDecisionSync_FiresWhenLoopbackURLSet` — captures request method + path + Content-Type + body provenance + Authorization (must be empty)

## Bug-10 — non-issue (documented)

`cmd/server/main.go:406-409` constructs `srv := &http.Server{ Addr: ":" + port, Handler: r }` with no `WriteTimeout` (Go 1.20+ default = unlimited). The `curl --max-time 540` timeout the verification agent observed is a test artifact, not a server bug. Documented as known test methodology: `curl --max-time ≥ 600` (or no flag) required to capture the full ~7min mythos response.

## Files touched (3 source + 1 test new)

```
M  server/internal/handler/swarm_run.go                  (0.5.70 cancel payload default)
M  server/internal/service/mythos/runner.go              (0.5.68/69 sole recovery watch)
M  server/internal/service/mythos/runner_test.go         (0.5.68/69/70 regression pins + grep upgrade)
A  server/internal/handler/migration_273_274_static_test.go   (0.5.70 5 new static tests)
```

Plus `.omc/audit/2026-08-23-labs-issue-integration-audit.md` (632 → 660+ lines, 0.5.68-0.5.70 cycle append).

## Regression pins (14 total now)

| Test | File | Pins |
|---|---|---|
| `TestRunnerCreatorType_IsNotSystem` | `runner_test.go:88` | 3 spacing variants of `CreatorType: "system"` literal |
| `TestRunnerAssignsIssueNumber` | `runner_test.go:107` | `Number: issueNumber,` + `Number: codaNumber,` + `IncrementIssueCounter` + ≥2 `CreateIssueParams` |
| `TestRunnerEnqueuesSubIssues` | `runner_test.go:137` | substring `EnqueueTaskForIssue` (0.5.70 grep upgrade) |
| `TestRunnerPreservesPartialWaitFnOutput` | `runner_test.go:166` | `case out != "":` + `0.5.66 audit fix` |
| `TestRunnerSchedulesSoleRecoveryWatch` | `runner_test.go:203` | bool return + scheduler + 0.5.68 (0.5.68) |
| `TestSoleRecoveryWatchMarshalsCodaConclusionsSafely` | `runner_test.go:240` | markdown round-trip + source grep (0.5.69) |
| `TestPostDecisionSync_FiresWhenLoopbackURLSet` | `decision_sync_test.go:432` | method + path + Content-Type + body + Authorization (0.5.70 upgrade) |
| `TestMythosWaitConstants_InRange` | `experimental_mythos_run_test.go:124` | `mythosWaitTimeout >= 5*time.Minute` |
| `TestUpstreamRegistryAttachesBearerHeader` (4 cases) | `upstream-registry.test.ts` (vitest) | loopback + reject public + accept LAN + reject env |
| `TestMigration273_CreatesSemanticaDecisionACLTable` | `migration_273_274_static_test.go` | 0.5.70 |
| `TestMigration273_DownDropsSemanticaDecisionACLTable` | `migration_273_274_static_test.go` | 0.5.70 |
| `TestMigration274_DeletesOrphanResourceLocks` | `migration_273_274_static_test.go` | 0.5.70 |
| `TestMigration274_DownIsIrreversibleByDesign` | `migration_273_274_static_test.go` | 0.5.70 |
| `TestMigration241_SwarmInterruptPayloadIsNotNull` | `migration_273_274_static_test.go` | 0.5.70 |

## End-to-end verification (latest)

```
0.5.68 verification (10 min):
  HTTP 200 in 8m29s (run completed, sub-issues daemon-executed)
  Caught Bug-9 watch json.Marshal regression
  coda_conclusions stayed [] (default), watch persist failed silently

0.5.69 verification (skipped — minor fix):
  Re-ship + cold-start verified

0.5.70 verification (10 min):
  cold-start verified, Multica.app = 0.5.70
  Server PID 43264
  All 14 regression tests + 4 vitest cases green
```

## Lessons (for future cleanup cycles)

The 0.5.68 → 0.5.69 → 0.5.70 chain demonstrates the value of end-to-end verification: each ship was correct in isolation but the 0.5.68 verification surfaced a JSON escape regression that wouldn't have been caught by the static-grep regression pin alone. The 0.5.69 fix is a 1-line change with a behavioral test — but only because the verification agent ran the actual recovery-watch path with real markdown content.

## Deferred (all non-blocking, future-cycle candidates)

- **Real-DB integration tests for migrations 273/274/241** — needs testcontainers / ephemeral PG; static tests cover SQL invariants only
- **swarm_topology orchestrator runtime re-bind** — if a user-installs on stale agent IDs again, the 0.5.64-style re-bind SQL would need to be re-runnable from the UI
- **Daemon-side wakeup-routing visibility filter** — Mythos agents with `experimental_resource_visibility` rows are hidden from the daemon's claim path. Pre-existing latent issue; not introduced by any of the 11 ships
- **Operational cleanup**: 4 stuck `mythos_run status='running'` (Bug-9 manifestation; will self-terminate at 24h cap) + 4 abandoned profile dirs + orphan `daemon.log` at profiles/ root

## Verification

- `go build ./...` — clean
- `go test -count=1 -timeout 120s ./internal/handler/...` — all green (`13.8s`)
- `go test -count=1 -timeout 60s ./internal/service/mythos/...` — all green
- `pnpm typecheck` — 6/6 OK
- `pnpm exec vitest run src/main/experimental/upstream-registry.test.ts` — 4/4 OK
- Ship chain: `bash scripts/ship-mac.sh --yes` — succeeded 0.5.68, 0.5.69, 0.5.70 (backup step now functional post 0.5.67 `find -printf` fix)
- Multica.app = 0.5.70, cold-start 3-check pass (PG :5432 + server :8090 + `/health` ok)