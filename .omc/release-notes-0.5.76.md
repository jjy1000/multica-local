---
name: release-notes-0.5.76
created: 2026-08-27T08:30:00Z
updated: 2026-08-27T08:30:00Z
---

# 0.5.76 — Upstream Integration Batch #2 (Parallel-Agent Cherry-Picks)

**Released:** 2026-08-27
**Branch:** `epic/0.5.72-followups`
**Builds on:** 0.5.75 (4 cherry-picks B2/B3)

## TL;DR

5 parallel-batch agent workers ran against `tmp/cp-batch-{a,b,c1,c2,d}-0723-2` worktrees against the upstream triage plan `Batches A-D`. Result: **2 clean surgical cherry-picks** + 4 already-present + 13 skipped due to fork-divergence or CLAUDE.md §Localized Fork violations.

## What changed (2 commits)

### Batch B — CLI/skill surgical (1 applied of 7 attempted)

**MUL-6658 `fix(cli): tell a rejected task token to stop, not to sign in again`** (`f17f6c0d2`) — `server/internal/cli/{client,errors}.go` adds `serverMessagePrefixes` map (handles both validation + conflict) + `KindAuthRequired && httpErr.TaskScoped` short-circuit. **Fork-specific**: removed `TestSetHeaders_AdvertisesStableAttachmentURLs` from the cherry-pick because it asserts `X-Client-Capabilities: stable_attachment_urls` header from MUL-5372 Phase1 which is not ported to this fork (per CLAUDE.md §Known Stability Surfaces "Cherry-pick completeness check" lesson). 11 new test cases pin behavior.

### Batch C2 — UX/cosmetic (1 applied of 5 attempted)

**#6081 `fix(autopilots): keep configuration panel visible for wide runbooks`** (`feea2c536`) — `packages/views/autopilots/components/autopilot-dialog.tsx:604` adds `min-w-0` to the Runbook container div so wide runbooks don't squeeze the configuration panel.

## What was ALREADY_PRESENT (6 commits, no work needed)

### Batch A — CVE/security (4 commits)

- `62c5ba11` builder-util-runtime CVE-2026-54673 → fork `e57d0b57c` (0.5.40)
- `79d13b6b` brace-expansion CVE-2026-69152 → fork `f5de3a927` (0.5.43)
- `4c1c8709` CVE-2026-9277 (shell-quote) → fork `d03025093` (0.5.39)
- `658b0b7d` JWT secret fail-fast → fork `7a14de758` (0.5.71)

### Batch C2 (2 of 5 already present)

- `16f9f252` inbox archive keyboard shortcut → fork `a4b362e70`
- `a3bd38da` autopilot webhook credential redaction → `redactAutopilotWebhookCredentials` in `cmd_autopilot.go:274`

## What was SKIPPED (11 commits — fork-divergence + CLAUDE.md violations)

### Batch B (4 skipped — fork-feature-missing)

- `39ddc82c` MUL-6228 (full-UUID resolver) — commit 2 modifies `runIssueReorder` which doesn't exist in fork's `cmd_issue.go`
- `bdcb231ba` MUL-5466 (qwen stdin) — `server/pkg/agent/qwen.go` not in fork
- `844250420` runtime visibility — touches `machine-cli-section.{tsx,test.tsx}` deleted by upstream but still in fork; 16 conflict zones
- `750fc0cc5` MUL-6655 (MCode ACP) — `server/pkg/agent/mcode.go` not in fork

### Batch C1 (4 of 4 skipped — fork surface retirements)

- `e5b28564b` transcript end ticks — `build-steps.ts` rewritten to `build-timeline.ts` by fork `42dbf60dc8`
- `dd147440` chat footer spacing — `chat-message-list.test.tsx` deleted by fork's revert of #7018; surrounding code assumes the deleted props
- `beb3e9be` chat gutter widening — 3 deleted files (`agent-creation-studio.tsx`, `chat-page.tsx`, `archived-agent-banner.tsx`) per CLAUDE.md §agent_creation_studio SUPERSEDED + chat structural divergence
- `92c1a4df` de-emphasize share-link URLs — `members-tab.tsx` deleted per CLAUDE.md §Localized Fork (workspace-members-management removed)

### Batch C2 (2 of 5 skipped — wholesale port, not surgical)

- `9b013e34` inbox arrow-keys — `inbox-list.tsx` upstream-only; fork uses `inbox-display.ts` + `inbox-page.tsx`
- `f9b7bf52` i18n `custom_property.none` reuse — depends on upstream `66af64b9a` no-value filtering (never ported); `filter-chips-bar.tsx` upstream-only

### Batch D (all skipped — CLAUDE.md §Localized Fork violations)

3 user-listed candidates all failed pre-flight:
- `c7d66071e` v0.4.35 changelog entry — explicitly advertises Cloud/billing/seat-management features
- `21b938bfd` reverts `c7d66071e` lines (depends on it being applied)
- `f74a71060` perf(web) preload landing hero — touches `landing-hero.tsx` (perf code change, not docs-only)

All other docs-only commits in upstream (v0.4.34 down to v0.4.17) have identical shape (cloud/billing/seat-management marketing copy) — same skip reason.

## Files changed (cumulative, this batch)

```
M  CLAUDE.md                                          (release header)
M  apps/desktop/package.json                          (version 0.5.75 → 0.5.76)
A  .omc/release-notes-0.5.76.md                       (this file)
A  .omc/0.5.76-ship-2026-08-27.md                     (ship log)
M  server/internal/cli/client.go                      (MUL-6658)
M  server/internal/cli/client_test.go                 (MUL-6658 new tests)
M  server/internal/cli/errors.go                      (MUL-6658)
M  server/internal/cli/errors_test.go                 (MUL-6658 new tests)
M  packages/views/autopilots/components/autopilot-dialog.tsx (6081)
```

LOC delta: +2 commits → 5 src + 2 test files; ~+330/-30

## End-to-end verification

- `pnpm typecheck` (full turbo): **6/6 OK** (cached, 82 ms)
- `go test -count=1 -timeout 600s ./internal/... ./pkg/agent/...`: all packages green except 2 pre-existing failures (locked): `TestDashboardPerAgentRollupsUseExactWindow` + `TestQuickCreateIssueParentTrustBoundary`
- Targeted new tests PASS: `TestFormatErrorRejectedTaskTokenDoesNotSuggestAnotherCredential` + 6 `TestExtractServerMessage` subtests + 4 `TestHTTPErrorKind/status_4xx` subtests
- **Ship**: `bash scripts/ship-mac.sh --yes` — cold-start ~4s, server PID verify, Info.plist = 0.5.76, 7/7 steps PASS, nested binaries re-signed

## Lessons (for future parallel-batch integration)

1. **Pre-flight ALREADY_PRESENT check before cherry-pick attempt.** Batch A's 4 CVE fixes were all already in fork history; the agent correctly identified this via `git log --grep` + grep of override files. Saved ~5 commits of conflict-resolution churn. Future batches: ALWAYS check upstream SHA against fork history first.

2. **Fork-feature-missing is a clean SKIP, not a conflict.** Batch B's qwen/mcode/reorder/runtime-visibility targets don't exist in fork — surgical cherry-pick impossible. Don't try to wholesale-port during an integration batch; defer to a dedicated port cycle.

3. **Cherry-pick completeness check still critical.** MUL-6658's upstream tests include `TestSetHeaders_AdvertisesStableAttachmentURLs` referencing MUL-5372 `X-Client-Capabilities: stable_attachment_urls` header not in fork. Without removing this test, `pnpm typecheck` would have failed. The CLAUDE.md §Known Stability Surfaces "Cherry-pick completeness check MUST verify both code paths AND test imports" lesson continues to pay off.

4. **CLAUDE.md §Localized Fork as filter for docs-only.** Batch D's changelog entries advertise Cloud/billing/seat-management features — all skipped. This is the right behavior: even docs-only marketing copy can violate the localization contract. Future docs batches: scan content for prohibited terms before cherry-pick.

5. **Parallel worktrees via `git worktree add -B <branch> <path>` (not relative paths).** Initial worktree creation with relative path silently failed (returned exit 0 but didn't create dirs); using absolute path with `-B` flag worked. Future batches: always use absolute paths + `-B` for worktree creation.

## Deferred (all non-blocking, future-cycle candidates)

- **Batch 5 REVIEW-tier** (8 large / fork-heavy): `f8ec870f` per-agent starter prompts, `8c563b49` source context for sub-issues (10K LOC), `54027ba76` skill listings + stall timeouts (1441 LOC), `46b5d9e6` process tree ownership, `b2b4699f` inbox status/priority filters (1282 LOC), etc. — user decision required per triage §Batch 5.
- **Inbox arrow-keys / i18n no-value filter wholesale ports** (Batch C2 skipped items) — separate dedicated port cycles if user wants the features.
- **`TestDashboardPerAgentRollupsUseExactWindow` fix** — 900s delta leak in days=1 window (locked 0.5.75 batch). Likely a date-window aggregation bug in `pkg/dashboard` rollup query.
- **Remaining 41 docs-only changelog commits** (v0.4.30..v0.4.34) — same content-violation reason, skip until upstream sanitizes marketing copy.
