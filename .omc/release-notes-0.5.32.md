---
name: release-notes-0.5.32
created: 2026-08-17T08:45:00Z
updated: 2026-08-17T08:45:00Z
---

# 0.5.32 Release Notes — MUL-6233 Revert + MUL-6288 + MUL-6254 Test Port (2026-08-17)

Upstream alignment + dormant-hazard fix + test coverage closure.

## Highlights

### MUL-6233 revert — `revert(desktop)` `5efa66945`
Cmd/Ctrl+, settings shortcut removed, mirroring upstream revert `2829798b5` (upstream shipped it 2026-08-16, reverted 1.5 days later). **User decision: REVERT-ALSO.** Two defect vectors existed byte-identically in the fork:
1. Global main-process interception of the comma chord in every window/focus context including text editors (no isComposing guard — zh-Hans IME relevant)
2. `","` added to PRIMARY_RESERVED_KEYS — dropped user shortcut overrides

Reverted 14 files: keyboard-shortcuts.ts branch + union, index.ts routing, preload bridge + d.ts, main-renderer-messages channel, `use-open-settings-shortcut.ts` deleted, App.tsx hook call, definitions.ts `","` (kept `goSettings` with `defaultShortcut: null`), definitions.test.ts pin, 4 locale keys.

### MUL-6288 — `fix(migrate)` `967a991c1`
Concurrent index build retry cleanup — **the fork had the exact dormant hazard**: 20+ `CREATE INDEX CONCURRENTLY` migrations with NO cleanup registry. A torn build leaves an INVALID index, `IF NOT EXISTS` marks it applied, uniqueness silently unenforced. Ported the total registry mechanism:
- **24-entry `concurrentIndexCleanups` map** (fork-true basenames, built from actual fork `.up.sql` content — fork numbering diverges from upstream after ~218)
- Auto-generated pre-migration hooks: `DROP INDEX CONCURRENTLY IF EXISTS` only when `indisvalid=false` (fail-closed)
- 3 tests: `TestEveryConcurrentUpBuildHasCleanup` (invariant — no CONCURRENTLY migration left unregistered), `TestConcurrentIndexCleanupsMatchTheirMigrations` (reverse guard), `TestRunMigrationsRepairsInvalidRuntimeIDIndex` (live repair on migration 248 — blocker tx + 2s statement_timeout interrupts build → INVALID → hook repairs on retry)

### MUL-6254 test port — 8 commits (`e217d6790`…`95487d3d2`)
6 of 7 deferred test files landed + 1 source-only drop:
- `test(core)` — workspace queries (workspaceBySlugOptions), workspace hooks (useWorkspaceList + EMPTY_WORKSPACES), auth-initializer (fork's Promise.all probe + retryGeneration semantics)
- `test(desktop)` — auth-session-bridge (3 tests pin `authSessionReportValue` contract)
- `test(web)` — workspace layout gate (undefined→loading / null→NoAccess), redirect-if-authenticated (ready-gated)
- `test(views)` — onboarding-flow-mode **dropped** (fork `OnboardingFlow` has no `mode`/`onCancel` props — feature gap, source-only note)

**2 production bugs exposed by the tests (fixed first, then tests landed):**
- `fix(core)` `d5d860b11` — MUL-4985: `useActorName` unstable empty-array default → stable empty array (prevents infinite re-render)
- `fix(web)` `b4513c0ac` — MUL-6254 selector contract: workspace list query error left `undefined` data which was treated as authoritative `null` → rendered NoAccessPage instead of keeping the loading veil

**21 tests green** (core 14 + web 4 + desktop 3).

## Verification
- `pnpm typecheck --force` 6/6 (0 cached, 35.6s)
- `go test ./cmd/migrate/` ok (3.7s — MUL-6288 tests)
- 21 vitest tests green
- Ship chain: 4a/7 integrity check PASS → cold-start PASS, server 0.5.32
- /Applications/Multica.app = 0.5.32

## Deferred (tracked)
- MUL-6243 per-workspace custom issue statuses — PORT-AFTER (needs MUL-6288 machinery ✓ landed + migration renumbering 250–257 + consumer audit)
- MUL-6286 actor/multi_actor properties — PORT-AFTER (needs @multica/core/properties base)
- MUL-6291 jsdom removal in pure-logic suites — mechanical batch
- 21e77f362 changelog link / 2014ae361 bulk skill update / b946f3a72 CI split — DEFER
- MUL-5991 / 0c69f1f95 — user-deferred