# Release Notes — 0.5.44

**Shipped 2026-08-20** (branch `epic/0.5.13-integration`, 2 atomic commits on top of 0.5.43: `127bc8e8c` → `252cea88` → `c8f609bcd`). pnpm typecheck 6/6 + go test 32/33 (1 pre-existing mythos panic documented in CLAUDE.md, unrelated).

## Summary

Cherry-pick batch — 2 landable commits out of 12 Tier 2 candidates surveyed. The 10 SKIPs are documented below with rationale and follow-up paths.

**Zero migrations.** Both commits are pure code + i18n changes.

## Changes

### 1. `fix(daemon)` `127bc8e8c` — keep skill cache failures from blocking skill loads (MUL-6445)

Fork-port of upstream `0d036ee5a` (4 files, +97/-7 upstream stat).

**Source fix (the bug fix itself)**:
- `server/internal/daemon/daemon.go`: 10 lines — cache failure now logs + continues, doesn't propagate to skill load (returns empty skills rather than erroring).
- `server/internal/daemon/skill_cache.go`: 31 lines — recover from `EEXIST` on concurrent-cache-write race + cache removal failure must not discard an already-downloaded bundle.

**The bug**: a transient `skill_cache.Remove` (e.g. permission denied) would discard an already-downloaded bundle so the agent would get an empty skills list. The fix: continue serving the in-memory download even if the cache-flush path failed.

**Auto-merged**: `skill_cache_test.go` (5 new tests, +32 lines) — passes in fork.

**Fork deviations**:
- Dropped upstream's `skill_bundle_resolve_test.go` entirely (130 LOC). The new tests reference upstream abstractions the fork does not have: `skillbundle.SourcePlugin`, `errSkillBundleUnavailable`, `taskfailure.ReasonSkillBundleUnavailable`, `taskRunFailureReason`. These are upstream refactors that the fork has not ported; porting them is its own PR. The source fix lands + `skill_cache_test.go` (auto-merged, 32 new tests) provide the regression coverage that's buildable in fork.

**Verification**: `go build ./internal/daemon/...` clean. `go test -count=1 -timeout 180s ./internal/daemon/` PASS (21.971s, including 5 new tests in `skill_cache_test.go`).

### 2. `fix(autopilots)` `c8f609bcd` — label webhook event-filter add/remove controls (MUL-6451)

Fork-port of upstream `fc15a297e` (5 files, +13/-2 in fork; upstream had +90/-2 with the test file we dropped).

**Source + i18n (the actual UX win)**:
- `packages/views/autopilots/components/webhook-event-filter-section.tsx`:
  - Remove `x` button on each filter row now has `aria-label` + `title` "Remove filter" (matches an existing i18n key).
  - Add button next to event filter now renders visible text "Add" instead of bare `+` icon, plus colored disabled-state and `shrink-0` layout so the input doesn't push it off-screen.
- 4 locales (`en`/`ja`/`ko`/`zh-Hans`): `event_filter_add` string added.

**Fork deviations**:
- Dropped upstream's `webhook-event-filter-section.test.tsx` (77 LOC). The fork removed `jsdom` in 0.5.33 (MUL-6291) but kept `packages/views/vitest.config.ts` `environment:'jsdom'` — so component tests calling `render()` fail with "document is not defined". 21 component tests in 14 files are broken across the repo; this is a separate fork-wide audit batch (add `happy-dom` or alternative DOM env). The a11y improvement itself is testable via a downstream DOM-test-infrastructure restoration, not via this commit.

**Verification**: `pnpm typecheck` 6/6 (33.1s).

## Skipped from the 12 Tier 2 candidates surveyed

| Candidate | Reason | Follow-up |
|---|---|---|
| MUL-6472 dispatch error leak (`a12985ab0`, 6 files / 146 LOC) | Conflicts in 5 files (server + client + UI). Requires `service.AutopilotQuotaExceededError` (fork missing), `clientErrorMessage` client helper (fork missing), `entitlement` test deps (SKIP-DEAD-CASE). | **0.5.45 audit batch** — port entitlements + quota error type + `clientErrorMessage` first, then re-cherry-pick. |
| MUL-6456 issue last activity index retire (`7186cf1b2`, 5 files / 128 LOC) | Migration 375_drop_issue_last_activity_index is far ahead of fork's current 260; main.go conflict on migration chain config. The fork doesn't have `idx_issue_workspace_last_activity` (no upstream migrations 261-374 ported), so the migration is a no-op for fork. | No follow-up needed — fork doesn't have the index to drop. |
| MUL-6456 comment indexes mutually exclusive (`fa805238f`, 6 files / 439 LOC) | Migration 371 + CLAUDE.md + `migrate/main.go` conflict; upstream migrations 262-371 not in fork. | No follow-up needed — fork's schema doesn't have the indexes. |
| MUL-6085 completed-task environment retention TTL (`8787003ca`, 5 files / 283 LOC) | 6 conflicts; fork has different retention ladder (MUL-6107 + 0.5.25 RuntimeGC fix already in `internal/experimental/runtime_gc.go` with `SessionTTL`/`ArchiveTTL`/`TrashTTL` = 30/90/120 days). | No follow-up — fork already has the retention via 0.5.25 fix. |
| CJK markdown render (`4ef294496`, 5 files / 226 LOC) | Fork uses `packages/views/editor/readonly-content.tsx` as primary markdown renderer; upstream's `rich-content.tsx` is a different module that imports abstractions fork doesn't have (`remark-cjk-friendly/parseOnly`, `isIssueIdentifier`, `resolveClickIntent`, `WorkspaceEntityRef`, etc.). | **0.5.45 audit batch** — port `cjk-emphasis.ts` utility standalone + integrate into fork's `readonly-content.tsx` (separate PR). |
| MUL-6417 follow-up 1 in_progress start (`4ff37edf9`, 13 files / 74 LOC) | 13 conflicts; 10 of 13 files are docs/markdown. The actual rule change is in `runtime_config_sections.go` (workflow description text) + `squad_briefing.go` (comment). Fork's `multica-working-on-issues` skill has different structure. | **0.5.45 audit batch** — port the two-moment rule ahead of the MUL-6417 master refactor. |
| MUL-6417 follow-up 2 (not attempted) | Same — depends on MUL-6417 master. | Defer with follow-up 1. |
| MUL-6310 NUL bytes (`d6301091e`, 7 files / 577 LOC) | 5 conflicts in `task.go` require upstream-only packages (`internal/attribution`, `internal/featureflags`, `internal/runtimeapps`, `pkg/plugincontract`, `pkg/pluginruntime`) — fork has none of these. The fix is otherwise solid (sanitize all task payload strings before persistence). | **0.5.45 audit batch** — port the attribution/featureflags/runtimeapps/plugincontract/pluginruntime subpackages first, then re-cherry-pick. |
| MUL-6168 prevent duplicate self-assignment (`b5a48eca1`, 26 files / 790 LOC) | 26 files too large; likely needs the same upstream abstractions. | Defer to 0.5.46 Tier 3. |
| MUL-6471 pi/opencode reach custom providers (`20ceaccc4`, 10 files / 805 LOC) | Runtime fix, relevant to fork; not attempted this batch. | 0.5.45 Tier 2 candidate. |
| MUL-6146 stop cancelling rerun (`8c9b7503a`, 6 files / 1391 LOC) | Mostly tests (1326 LOC); the actual fix is small. | 0.5.45 Tier 3 — needs assessment. |
| MUL-6368 cross-agent contamination (`eb2a94e86`, 18 files / 662 LOC) | Security win; large surface. | 0.5.45 Tier 3. |
| MUL-6458 realtime status catalog sync (`9900e9668`, 8 files / 712 LOC) | Depends on MUL-6243 (already ported in fork). | 0.5.45 Tier 3. |

## Verification

| Gate | Result |
|---|---|
| `pnpm typecheck` (full turbo) | 6/6 ✅ (31.204s) |
| `go test -count=1` `./internal/... ./pkg/agent/...` | 32/33 ✅ |
| `internal/daemon` (MUL-6445) | PASS (25.304s) |
| `internal/handler` (handler suite) | PASS (14.800s) |
| `pkg/agent` (agent suite) | PASS (12.121s) |
| Pre-existing mythos panic | `TestTickSupervision_CompletionByFinalIssueStatus` (CLAUDE.md documented, unrelated to this batch) |
| `scripts/check-agents-docs-sync.mjs` | PASS (5 guarded constraint categories) |

## Files changed (2 commits)

| Commit | Files | Insertions | Deletions |
|---|---:|---:|---:|
| `127bc8e8c` MUL-6445 | 3 | 66 | 7 |
| `c8f609bcd` MUL-6451 | 5 | 13 | 2 |
| **Total** | **8** | **79** | **9** |

## Deferred to 0.5.45

- **3 SKIPs revert to 0.5.45** (need upstream abstractions ported first): MUL-6472 (entitlement + quota), MUL-6310 NUL bytes (attribution + pluginruntime), CJK markdown (fork's `readonly-content.tsx` integration).
- **3 SKIPs defer to Tier 3** (need deep review): MUL-6146 rerun, MUL-6368 cross-agent, MUL-6458 status sync.
- **2 SKIPs no follow-up** (already covered in fork): MUL-6085 retention TTL (overlaps with 0.5.25 fix), MUL-6456 indexes (fork doesn't have them).
- **Future**: 0.5.45 batch candidates (MUL-6471, MUL-6417 follow-ups 1+2 once master refactor ported).
