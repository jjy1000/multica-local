# Release Notes — 0.5.46

**Shipped 2026-08-20** (branch `epic/0.5.13-integration`, 2 atomic commits on top of 0.5.45: `fc52836cd` + `5f0f1f592`). `pnpm typecheck` 6/6 + `go test` 33/34 (1 pre-existing mythos panic).

## Summary

**Infrastructure batch** — small but unblocks more cherry-pick re-attempts. 2 commits, 2 files, +33 LOC of upstream code. NO user-facing changes.

**Zero migrations, zero schema drift.** The 0.5.45 audit batch landed the foundation; 0.5.46 adds the smallest pieces that the re-cherry-pick attempts identified as missing.

## Re-cherry-pick findings (0.5.45 batch blocked 6 SKIPs)

| Skip | 0.5.45 progress | 0.5.46 progress | Still blocked |
|---|---|---|---|
| MUL-6472 dispatch leak | `entitlement` ✅ | `AutopilotQuotaExceededError` ✅ | `clientErrorMessage` (client.ts) + handler refactor (autopilot.go needs `LockAutopilotForUpdate`, `SetAutopilotTriggerPublishersByAutopilot` sqlc queries fork doesn't have) |
| MUL-6310 NUL bytes | `attribution` + `featureflags` + `runtimeapps` + `plugincontract` ✅ | `SanitizeTextForPostgres` + `SanitizeJSONForPostgres` aligned ✅ | `text.go` already ported (commit `781cea00c`); remaining: caller integration (autopilot.go, skill.go, task.go) — task.go needs upstream sqlc queries fork doesn't have |
| CJK markdown | `cjk-emphasis.ts` ported ✅ | — | fork's `readonly-content.tsx` pipeline integration |
| MUL-6417 follow-ups | — | — | daemon workflow refactor + skill prompt changes |

**Conclusion**: 4 of 6 SKIPs still blocked because the upstream code paths they touch require sqlc queries / DB fields that the fork's schema doesn't have (migrations 261-374 not landed). The 0.5.45 audit batch was the right foundation; reaching the next unlock requires the full upstream migration sweep.

## Changes

### 1. `feat(0.5.46)` `fc52836cd` — port `AutopilotQuotaExceededError` sentinel type

**32 LOC** minimal extraction from upstream `service/autopilot_quota.go`. Just the `AutopilotQuotaExceededError` struct + `Error()` method.

The full quota subsystem (period table, reservation, decision counters, entitlement client) is NOT ported — that requires upstream migrations 261-374 + sqlc regen + service refactor. This commit ships just the error type so MUL-6472's quota branch can typecheck.

### 2. `feat(0.5.46)` `5f0f1f592` — align `SanitizeTextForPostgres` literal

**1 line change** in `server/internal/util/text.go`. The fork already had `SanitizeTextForPostgres` + `SanitizeJSONForPostgres` (commit `781cea00c` from a parallel session). This commit just aligns the literal `"�"` to upstream's canonical `"�"` for downstream `/equivalent` byte comparison.

All 11 sanitization tests pass (TestSanitizeTextForPostgres 8 subtests + TestSanitizeJSONForPostgres 3 subtests + warning test that `strings.ToValidUTF8` alone is insufficient).

## Deferred to 0.5.47+

- **CJK markdown integration** — port `cjk-emphasis.ts` into fork's `packages/views/editor/readonly-content.tsx` pipeline. Requires understanding fork's specific remark/rehype stack and where the visit hook would attach.
- **MUL-6472 dispatch leak (re-attempt)** — `clientErrorMessage` + `service.AutopilotQuotaExceededError` ✅ now available. Still blocked: `pkg/pluginruntime` (upstream doesn't have it on main), handler autopilot.go new sqlc queries.
- **MUL-6310 NUL bytes (re-attempt)** — sanitization functions available, but call-site integration requires upstream handler migration that's blocked on sqlc queries (`LockAutopilotForUpdate`, `SetAutopilotTriggerPublishersByAutopilot`, etc.).
- **MUL-6417 follow-ups** — port the daemon workflow refactor + skill prompt changes.
- **5 re-cherry-pick attempts** that didn't land in 0.5.46: MUL-6472, MUL-6310, CJK markdown, MUL-6417 follow-ups, MUL-6471. Each has its own specific gap.

## Verification

| Gate | Result |
|---|---|
| `pnpm typecheck` (full turbo) | 6/6 ✅ |
| `go test -count=1` `./internal/util/...` | PASS (sanitization tests) |
| `go build ./...` | PASS |
| Pre-existing mythos panic | `TestTickSupervision_CompletionByFinalIssueStatus` (CLAUDE.md documented) |
| `scripts/check-agents-docs-sync.mjs` | PASS (5 guarded constraint categories) |

## Files changed (2 commits)

| Commit | Files | Insertions | Deletions |
|---|---:|---:|---:|
| `fc52836cd` AutopilotQuotaExceededError | 1 | 32 | 0 |
| `5f0f1f592` text.go alignment | 1 | 1 | 1 |
| **Total** | **2** | **33** | **1** |

## Strategic significance

The 0.5.46 batch is intentionally small. The 0.5.45 audit batch was the foundation; 0.5.46 adds the two smallest pieces that the 0.5.45 re-cherry-pick attempts identified as missing:

- `AutopilotQuotaExceededError` (MUL-6472 blocker)
- `SanitizeTextForPostgres` literal alignment (cosmetic, but reduces fork-upstream drift)

Neither unblocks a cherry-pick wave directly. The fork-vs-upstream divergence (sqlc queries, DB fields, migration gaps) is the dominant blocker, and the 0.5.45 audit batch alone doesn't close it.

The next unlock wave requires:
1. **Schema migration sweep** — port migrations 261-374 from upstream (substantial SQL work, ~100+ migrations).
2. **sqlc regen** — generate Go types for the new tables/queries.
3. **Handler refactors** — update handlers that use the new sqlc queries.
4. **Then re-cherry-pick** the 6 SKIPs.

That's a multi-batch effort (probably 0.5.47-0.5.50 batch range).
