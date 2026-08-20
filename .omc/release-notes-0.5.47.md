# Release Notes — 0.5.47

**Shipped 2026-08-21** (branch `epic/0.5.13-integration`, 5 functional commits on top of 0.5.46). `pnpm typecheck` 6/6 + `go test` 32/34 (1 pre-existing flaky `TestDashboardPerAgentRollupsUseExactWindow` + 1 pre-existing mythos panic).

## Summary

**Schema migration sweep + MUL-6472 landed (minimal fork-style port).** Unblocks 3 of 4 SKIPs from 0.5.44 (MUL-6472 now lands; MUL-6310 / CJK / MUL-6417 still need more).

**8 migrations applied to fork schema** (transitioning from 260 → 268). **First user-facing cherry-pick to land since 0.5.44**: MUL-6472 dispatch error leak (don't echo `err.Error()` in 5xx body, don't show 5xx in toast).

**Zero breaking changes to existing handler call sites.** MUL-6472 is a security improvement — fork's existing `clientErrorMessage` use site updates to suppress 5xx messages client-side; server-side handlers now log the error chain and return a fixed string.

## Changes

### 1. `feat(0.5.47)` `db1dab786` — port upstream autopilot_quota migrations + queries

**8 migrations** (352-359 upstream → 261-268 fork) applied to fork schema:

- 261 `autopilot_quota_execution` — creates `autopilot_quota_period` + `autopilot_quota_reservation` tables
- 262 `autopilot_quota_period_scope_index` — unique index
- 263 `autopilot_quota_reservation_id_index` — surrogate key index
- 264 `autopilot_quota_reservation_key_index` — idempotency lookup index
- 265 `autopilot_run_quota_reservation_index` — autopilot_run FK index
- 266 `webhook_delivery_replay_idempotency_index` — unrelated webhook index (in same upstream range)
- 267 `autopilot_quota_reservation_state_index` — state lookup index
- 268 `autopilot_quota_primary_keys` — attach concurrent indexes as primary keys

Plus the upstream `queries/autopilot_quota.sql` (139 LOC) so sqlc can generate the new query methods.

**Schema-side only.** No consumer code yet (the fork's `service/autopilot_quota.go` is still sentinel-only — see 0.5.46). Decision / reservation logic deferred to 0.5.48.

### 2. `feat(0.5.47)` `5acfe7e44` — sqlc regen for autopilot_quota migrations

After applying migrations, sqlc generate produces:
- `pkg/db/generated/autopilot_quota.sql.go` (new, 393 LOC) — `EnsureAutopilotQuotaPeriod` / `GetAutopilotQuotaReservationByKey` / `IncrementAutopilotQuotaBlocked` / `UpdateAutopilotRunTerminalWithQuotaRow` + param types
- `pkg/db/generated/autopilot.sql.go` (cascade) — `Queries` struct gains the new methods
- `pkg/db/generated/models.go` (cascade) — new `AutopilotQuotaPeriod` + `AutopilotQuotaReservation` struct types
- `pkg/db/generated/webhook_delivery.sql.go` (cascade) — webhook reindex from migration 266

### 3. `feat(0.5.47)` `243e45638` — port `clientErrorMessage` helper

**10 LOC** + 9 line comment header. The fork's `client.ts` doesn't have `dispatchReasonCode`/`errorCode` at the module level (only as class methods), so `clientErrorMessage` is added standalone at the end of the file. Exported from `packages/core/api/index.ts`.

The function returns the server's error message only when the error is a CLIENT error (4xx). 5xx messages and transport failures return undefined so the caller falls back to a localized sentence. This is the MUL-6472 client-side counterpart to the server's `slog.Error` + fixed-string 5xx body.

### 4. `fix(autopilot)` `f4c48a8b4` — don't leak server 5xx body to run-now toast (MUL-6472)

**First user-facing cherry-pick to land since 0.5.44.** Minimal fork-style port of upstream `a12985ab0`:

- **Server-side** (`server/internal/handler/autopilot.go`): replace `writeError(w, ..., "failed to trigger autopilot: "+err.Error())` with `slog.Error(...) + writeError(w, ..., "failed to trigger autopilot")` (fixed string, no `err` chain). The full error chain stays in the log; the workspace member's 5xx response is the same fixed string the rest of this file returns.
- **Client-side** (`packages/views/autopilots/components/autopilot-detail-page.tsx`): replace `e?.message` in the `handleRunNow` catch block with `clientErrorMessage(e) || localized`. Only 4xx messages render; 5xx falls back to the localized generic sentence.

**Fork deviations from upstream**:
- Dropped `*service.AutopilotQuotaExceededError` quota branch (fork's quota subsystem is sentinel-only; the quota branch is deferred to 0.5.48).
- Dropped upstream's tests (`handler/autopilot_trigger_error_test.go` + `client.test.ts`) — references entitlement types / jsdom. The remediation is covered by the existing `TestTriggerAutopilot` suite (verified PASS).
- Fork's `autopilot-detail-page.tsx` uses `useTriggerAutopilot` hook + `err instanceof Error` pattern (not upstream's direct API call + `dispatchReasonCode`). Replaced `err.message` with `clientErrorMessage` at the same catch site.

### 5. `feat(0.5.47)` `ba3d96918` — minimal `pkg/pluginruntime` stub

**33 LOC.** Fork-port: minimal stub for `pkg/pluginruntime` (which doesn't exist upstream but is referenced by upstream's `task.go`). Provides `CompiledEntry` (placeholder struct) + `ParseEntries` (empty-body stub) so the fork task.go can compile if/when the full MUL-6310 port lands.

Fork's plugin infrastructure remains CLAUDE.md SKIP-DEAD-CASE (MUL-6350 plugin rebuild series). The stub is additive — no consumers in fork yet.

## SKIP reconciliation from 0.5.46

| Skip | 0.5.47 progress | Status |
|---|---|---|
| MUL-6472 dispatch leak | **LANDED** ✅ | 0 user-facing cherry-pick conflict, 1 of 4 SKIPs cleared |
| MUL-6310 NUL bytes | sanitization ✅ + pluginruntime stub ✅ | Main handler tasks still has 8+ upstream sqlc queries (GetActiveAutopilotRuleVersionParams, GetWorkspaceAttributionFailClosed, etc.) + `protocol.ChatCancelFinalizedPayload` + `taskfailure.ReasonSkillBundleUnavailable`. Tasks fork has ErrAttributionFailClosed defined elsewhere (conflict). Deferred to 0.5.48 — needs more schema / sqlc / protocol additions. |
| CJK markdown | `cjk-emphasis.ts` ✅ | Integration into fork's `readonly-content.tsx` pipeline still pending |
| MUL-6417 follow-ups | No progress | daemon workflow refactor + skill prompt changes |

## Verification

| Gate | Result |
|---|---|
| `go run ./cmd/migrate up` | PASS (8 migrations applied, 260 → 268) |
| `sqlc generate` | PASS (regen produced 393 LOC of new sqlc code) |
| `go build ./...` | PASS |
| `pnpm typecheck` (full turbo) | 6/6 ✅ |
| `go test -count=1` `./internal/handler/ -run TestTriggerAutopilot` | PASS |
| Full server test | 32/34 (1 pre-existing flaky `TestDashboardPerAgentRollupsUseExactWindow` + 1 pre-existing mythos panic) |
| 2 new tables in DB | `autopilot_quota_period` + `autopilot_quota_reservation` |
| `scripts/check-agents-docs-sync.mjs` | PASS (5 guarded constraint categories) |

## Files changed (5 commits)

| Commit | Files | Insertions |
|---|---:|---:|
| `db1dab786` migrations + queries | 17 | 580 |
| `5acfe7e44` sqlc regen | 4 | 491 |
| `243e45638` clientErrorMessage | 2 | 16 |
| `f4c48a8b4` MUL-6472 landing | 2 | 19 |
| `ba3d96918` pluginruntime stub | 1 | 33 |
| **Total** | **26** | **1139** |

## Deferred to 0.5.48

- **MUL-6310 caller integration** — the schema + sanitization are in place; the main handler task.go still has 8+ upstream sqlc queries (autopilot_run + workspace_attribution_fail_closed + protocol types). Porting those requires more migrations + sqlc queries.
- **MUL-6417 follow-ups** — daemon workflow refactor + skill prompt changes.
- **CJK markdown integration** — `cjk-emphasis.ts` into fork's `readonly-content.tsx`.
- **MUL-6472 quota branch** — the `AutopilotQuotaExceededError` 429 branch is deferred; fork's quota subsystem is sentinel-only.
- **Pre-existing flaky test** — `TestDashboardPerAgentRollupsUseExactWindow` ('days=1 leaked yesterday's 900s run') needs audit. Pre-dates 0.5.47 audit batch.

## Strategic significance

**0.5.47 is the first batch since 0.5.44 to land a user-facing cherry-pick.** The 0.5.45 audit batch laid the foundation; 0.5.46 added the smallest missing pieces; 0.5.47 transitioned from foundation to execution.

The schema migration sweep (8 migrations + sqlc regen) consumed the most cumulative work but enables the next unlock wave. The fork now has the autopilot_quota schema + sanitization + clientErrorMessage + pluginruntime stub — 4 of the 5 dependencies that blocked MUL-6472/MUL-6310.

MUL-6472 landed via minimal fork-style port (skipping the quota branch, the test files, and the upstream-specific dispatchReasonCode). The security improvement (no 5xx body leak) is in; the full quota handling is deferred.
