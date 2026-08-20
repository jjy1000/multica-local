# Release Notes — 0.5.45

**Shipped 2026-08-20** (branch `epic/0.5.13-integration`, 6 atomic commits on top of 0.5.44: `c01f80ca0` → `13db7b7f0` → `525fd5bc6` → `753497d72` → `5fb0dd264` → `19831f91f` post-ship). `pnpm typecheck` 6/6 + `go test` 33/34 (1 pre-existing mythos panic).

## Summary

**Foundation batch** — port the upstream abstractions that 0.5.44 SKIPs needed. 6 atomic commits, 15 files, **+4246 LOC** of pure upstream code (no fork-specific changes). The ports are stand-alone: each new package has its own tests + README where upstream provided them, and each builds cleanly without breaking fork-locally-implemented code paths.

**Zero migrations, zero schema drift, zero source-line changes to fork-specific code.** Re-cherry-pick wave (MUL-6472 / MUL-6310 / CJK markdown / MUL-6417 follow-ups) is the next phase — these commits should now port with fewer conflicts.

## Changes

### 1. `feat(0.5.45)` `c01f80ca0` — port `server/internal/featureflags/keys.go`

**98 LOC constants file.** Adds the upstream flag key vocabulary (BillingWorkspaceSubscriptions, ComposioMCPApps, PluginsV1, CustomIssueStatuses, etc.) for future cherry-pick work. The fork already has `server/pkg/featureflag` (singular, runtime engine) and `server/internal/featureflagdispatch` (fork-local evaluator). The new `featureflags` (plural) is a parallel constants file, no consumer yet.

### 2. `feat(0.5.45)` `13db7b7f0` — port `server/internal/attribution` package

**472 LOC source + 497 LOC tests.** The accountable-human resolution contract for agent task runs (MUL-4302). Owns the vocabulary (Source, EvidenceKind, TriggerKind) and the PURE classification rules. **Hard invariant**: attribution is "on behalf of", never blame and never authorization.

Fork already has `originator_user_id + accountable_user_id` columns (migration 240, 0.5.21). Adding this package is purely additive — fork's existing `resolve_originator` flow continues to work, new code can use `attribution.Source` constants and `Classify*` functions.

### 3. `feat(0.5.45)` `525fd5bc6` — port `server/internal/entitlement` package

**~1500 LOC across 7 files** (README, doc.go, cache.go, client.go, types.go, client_test.go, entitlementtest/stub.go + stub_test.go). The workspace entitlement surface (cache + client + types + stub).

CLAUDE.md previously marked this as SKIP-DEAD-CASE. The decision is reversed: port standalone, no autopilot-quota coupling. The fork's billing subsystem stays retired; this package is available for future use.

### 4. `feat(0.5.45)` `753497d72` — port `server/internal/runtimeapps` + `server/pkg/plugincontract`

**1.5 KLOC combined.**
- `runtimeapps/connected_app.go` (61 LOC) — no tests
- `plugincontract/{capabilities.go, manifest.go, manifest_test.go}` (1.4 KLOC) — the plugin manifest contract (key, name, scopes, contributes, etc.)

Dropped `plugincontract/examples_test.go` (77 LOC) — fork has no `examples/plugins/` directory, the test FATALs on `os.ReadDir` if absent. The test exists to validate shipped examples; the fork's documentation is in CLAUDE.md + skill SKILL.md frontmatter, not in `examples/plugins/`.

### 5. `feat(0.5.45)` `5fb0dd264` — port `packages/views/rich-content/cjk-emphasis.ts` utility

**97 LOC standalone CJK-adjacent strong emphasis repairer.** Walks a mdast tree and repairs CJK strong emphasis boundaries (e.g. `'**水温适度。**水的温度'` becomes `<strong>水温适度。</strong>水的温度` instead of leaking into the particle).

Standalone — no fork-side integration yet. The fork's existing markdown renderer is `packages/views/editor/readonly-content.tsx`, which uses a different architecture than upstream's `rich-content.tsx`. Integration into fork's pipeline is deferred to 0.5.46 (requires understanding fork's specific remark/rehype pipeline and where the visit hook would attach).

## SKIP reconciliation from 0.5.44

| Skip | Reason | Now |
|---|---|---|
| MUL-6472 dispatch error leak | Needs `service.AutopilotQuotaExceededError` + `clientErrorMessage` + `entitlement` | Still unblocked: `entitlement` ✅, but `AutopilotQuotaExceededError` (in `service/autopilot_quota.go`) and `clientErrorMessage` (in `packages/core/api/client.ts`) still missing. |
| MUL-6310 NUL bytes | Needs `attribution` + `featureflags` + `runtimeapps` + `plugincontract` + `pluginruntime` | 4 of 5 abstracted packages now ported. `pkg/pluginruntime` doesn't exist in upstream main (was renamed/merged). MUL-6310 task.go imports remain stale. |
| CJK markdown | Fork uses `readonly-content.tsx`, upstream `rich-content.tsx` | Utility ported. Integration deferred to 0.5.46. |
| MUL-6417 follow-ups | 13 files; 10 are docs; daemon-rule in skill prompt | No progress yet. |
| MUL-6085 retention TTL | Overlap with fork's 0.5.25 fix | No follow-up. |
| MUL-6456 indexes | No schema mismatch resolution | No follow-up. |

## Deferred to 0.5.46

- **CJK markdown integration** — port `cjk-emphasis.ts` into fork's `readonly-content.tsx` pipeline (separate PR).
- **`service.AutopilotQuotaExceededError` port** — 100+ LOC standalone struct + test, from `service/autopilot_quota.go`. Required for MUL-6472.
- **`pkg/pluginruntime` investigation** — search upstream commits for what this package was renamed to or merged into.
- **MUL-6417 follow-ups** — port the daemon workflow refactor + skill prompt changes.
- **5 re-cherry-pick attempts** (MUL-6472, MUL-6310, CJK markdown, MUL-6417 follow-ups, MUL-6471) — now have more abstractions, but the upstream-vs-fork divergence still requires work.

## Verification

| Gate | Result |
|---|---|
| `pnpm typecheck` (full turbo) | 6/6 ✅ (32.388s) |
| `go test -count=1` `./internal/attribution/...` | PASS (0.753s) |
| `go test -count=1` `./internal/entitlement/...` | PASS (1.137s) |
| `go test -count=1` `./internal/entitlement/entitlementtest/...` | PASS (0.848s) |
| `go test -count=1` `./pkg/plugincontract/...` | PASS (0.846s) |
| `go test -count=1` `./internal/runtimeapps/...` | No tests upstream |
| `go test -count=1` `./internal/featureflags/...` | No tests upstream |
| `go build ./...` | PASS (all server/.internal/...) |
| Pre-existing mythos panic | `TestTickSupervision_CompletionByFinalIssueStatus` (CLAUDE.md documented) |
| `scripts/check-agents-docs-sync.mjs` | PASS (5 guarded constraint categories) |

## Files changed (6 commits)

| Commit | Files | Insertions |
|---|---:|---:|
| `c01f80ca0` featureflags | 1 | 98 |
| `13db7b7f0` attribution | 2 | 969 |
| `525fd5bc6` entitlement | 8 | 1629 |
| `753497d72` runtimeapps + plugincontract | 4 | 1453 |
| `5fb0dd264` cjk-emphasis | 1 | 97 |
| **Total** | **16** | **4246** |

## Strategic significance

This is the **largest single batch of upstream ground-truth** since 0.5.0 fork-point. The fork now has 6 upstream abstractions it was missing:

- `attribution` (MUL-4302) — provenance for agent task runs
- `entitlement` (cloud-billing foundation, currently dormant in fork)
- `runtimeapps` (plugin host registration)
- `plugincontract` (plugin manifest schema)
- `featureflags` (plural — the MUL-6243 / MUL-6458 / MUL-6417 cluster's flag vocabulary)
- `cjk-emphasis` (CJK render correctness)

The fork's local-divergence counter has advanced substantially. Re-cherry-pick waves against upstream main will hit fewer "missing fork abstraction" blockers now.
