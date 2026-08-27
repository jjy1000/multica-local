---
name: release-notes-0.5.77
created: 2026-08-27T09:00:00Z
updated: 2026-08-27T09:00:00Z
---

# 0.5.77 — Batch 5 REVIEW-Tier Cherry-Picks (Parallel-Agent Work)

**Released:** 2026-08-27
**Branch:** `epic/0.5.72-followups`
**Builds on:** 0.5.76 (2 surgical cherry-picks B + C2)

## TL;DR

5 parallel-batch agent workers tackled 6 large upstream commits from `.omc/upstream-integration-triage-2026-08-26.md` Batch 5. **22 atomic sub-commits landed** across 2 batches; 3 batches correctly STOPPED at pre-flight (fork-divergence detected, no wholesale-adoption).

## What changed (22 commits)

### Batch 5-skills — MUL-6639 (5 sub-commits, +1449 LOC)

`54027ba76` upstream commit split into 5 atomic sub-commits:

| # | SHA | Subject |
|---|---|---|
| 1 | `0aa16f9f2` | feat(skills): ListSkillFileMetadata — size + content_hash per file (Postgres-computed) |
| 2 | `3f33909b1` | fix(cli): fail skill transfers on stall, not elapsed time |
| 3 | `8efde33ec` | feat(skills): ?include=metadata on detail and file-listing endpoints |
| 4 | `33c3e3dee` | fix(cli): --with-content on skill get/files list + skill-importing docs |
| 5 | `7de329c5c` | test(skills): handler regression coverage for ?include=metadata |

**Behaviour**:
- `server/internal/handler/skill.go`: new `ListSkillFileMetadata` (Postgres-computed size + content_hash per file); `?include=metadata|content` query param (default = metadata on `/api/skills/{id}/files`, content on `/api/skills/{id}` for backward compat with installed desktop/older CLI).
- `server/internal/cli/stall.go` (new file): replaces `http.Client.Timeout` with stall-based detection — 15s no-progress budget + 10-min ceiling. Pilot scope: skill commands only.
- File listing gains SIZE column.
- 7 new handler regression tests pin: `?include=metadata` default, `--with-content` flag, SHA-256 hash for backslashes / `\x41` / `\xzz` / unicode / empty bodies, 400 on `?include=everything`.

**Fork adaptations**:
- 2 of 8 `wrapBodyRead` sites omitted (`ImportSkillFile` / `UploadPrivatePlugin` absent in fork — removed in earlier cherry-picks).
- Handler test rewritten to fork's `testPool.QueryRow/Exec` + `httptest.NewRecorder` + `json.Unmarshal` pattern with small `decodeJSON[T]` generic helper.
- `cmd_skill_test.go` file structure differs — new tests appended after fork's existing last test.
- `skill-importing/SKILL.md` new "Reading a skill back" section inserted before existing "## Incorrect → correct" section.

### Batch 5-process — MUL-6658 (17 sub-commits, +483/-69 LOC)

`46b5d9e6` upstream commit split into 17 atomic sub-commits (1 helpers + 14 backend ports + 2 probe/test):

| # | SHA | Subject |
|---|---|---|
| 1 | `74230ef1d` | helpers (runtime_ownership.go + proc_other.go extensions) |
| 2-15 | `3109270e9`...`e73e165e3` | claude/codex/opencode/antigravity/codebuddy/copilot/cursor/hermes/kimi/kiro/openclaw/pi/qoder + thinking probes + models.go probes |
| 16 | `2d1de952f` | Unix-half regression tests (6 new) |

**Behaviour**:
- Every agent backend spawns in its own Unix process group (`newRuntimeCmd` wraps `exec.CommandContext`).
- `releaseProcessGroup` drops the process-group handle after reap, killing whatever outlived the parent.
- 2 of 3 upstream structural tests adapted to fork's helpers (TestNewRuntimeCmdPutsChildInOwnProcessGroup, TestNewRuntimeCmdCancelKillsDescendants, TestOutputOwnedReturnsStdoutOnSuccess, TestOutputOwnedAttachesStderrOnFailure, TestCombinedOutputOwnedReturnsCombined, TestReleaseProcessGroupIsIdempotent).

**Fork adaptations**:
- **Skipped Windows half entirely**: `proc_windows.go` Job Object surface not ported (fork is macOS-only).
- **Skipped 12 fork-absent backends**: `acp_terminal`, `deveco`, `dim`, `dsh`, `grok`, `mcode`, `qwen`, `qwenpaw`, `reasonix`, `traecli`, `zeroclaw` — not in fork's pkg/agent catalog.
- Helpers in NEW file `runtime_ownership.go` (not appended to `launch.go`) — fork's `launch.go` carries fork-specific redactor logic that would be polluted.
- 3 upstream AST-based structural tests skipped (would false-positive on every fork backend); replaced with 6 runtime tests on actual helpers.
- `codex.go`: removed now-redundant explicit `configureProcessGroup(cmd)` call (line 576) since `newRuntimeCmd` does it.

## What was correctly STOPPED at pre-flight (3 batches)

### B5-starter — `f8ec870f` + `09a2410e8` MUL-6629 + MUL-6709 (2074 LOC)

**Why stopped**: Fork already has a fork-local `starter_prompts` feature that CONCEPTUALLY DIFFERS from upstream:
- `packages/views/chat/components/chat-window.tsx:1644-1720` has 3 hardcoded `STARTER_KEYS` buttons (`list_open`, `summarize_today`, `plan_next`) + `STARTER_ICONS` + `EmptyState` component.
- 4-locale i18n keys `starter_prompts.{list_open,summarize_today,plan_next}` already populated in `packages/views/locales/{en,ja,ko,zh-Hans}/chat.json`.
- `packages/views/onboarding/templates/helper-starter-prompts.ts` (Runtime-path Welcome Modal — unrelated to upstream).
- Upstream's `f8ec870f` DELETES the in-file `EmptyState` and replaces with extracted `chat-empty-state.tsx` that doesn't exist in fork.
- Fork's `packages/core/agents/` is structurally smaller (no `draft.ts`/`stored-draft.ts`/`builder-protocol.ts`) — porting upstream's full module = wholesale adoption.

**Required prerequisite**: Multi-session dedicated port that decides fork's policy for the existing 3-button `starter_prompts` (delete / replace / coexist) before any port can succeed.

### B5-locale — `21673268b` MUL-6660 (54 LOC)

**Why stopped**: Fork has zero trace of upstream's source-context sub-issue feature (`8c563b49`):
- 4 of 10 target files don't exist (`source-context-viewer.tsx`, `source-context-comment-list.tsx`, `source-context-viewer.test.tsx`, `modals/source-context-preview.test.tsx`).
- Entire `source_context` i18n namespace absent from all 4 locales.
- `comment-card.tsx` lacks `onCreateSubIssue`/`GitBranchPlus` entirely (608-line divergence from upstream).
- `cherry-pick 21673268b` → 10/10 conflicts (6 UU + 4 DU modify/delete) — aborted.

**Required prerequisite**: Port `8c563b49` first (viewer + comment-list + preview modal + ~40 i18n keys ×4 locales + `anchor_comment_id` plumbing), then apply this rename.

### B5-inbox — `b2b4699f` MUL-6632 (1282 LOC)

**Why stopped**: Fork has 10 missing inbox surface files + 1 type-shape mismatch + 1 server projection gap:
- Missing: `inbox-list.tsx`, `inbox-context-menu.tsx`, `inbox-view.ts` (with `InboxView` type + `ARCHIVED_VIEW_PARAM`), `archivedInboxListOptions` query, `deduplicateArchivedInboxItems`, `inboxKeys.archived`, `listArchivedInbox` API, `parsedResponseArchived`.
- `useStatusOptions` shape mismatch: fork returns `{groups, options, hasCustom}` but upstream MUL-6632 expects flat `StatusOption[]` — direct import would `undefined.map(...)`.
- Server `issue_priority` projection missing from `server/pkg/db/queries/inbox.sql`.

**Required prerequisite**: Port ~3-5 upstream commits of inbox-archive surface first, then re-attempt MUL-6632.

## Files changed (cumulative, this batch)

```
M  CLAUDE.md                                          (release header)
M  apps/desktop/package.json                          (version 0.5.76 → 0.5.77)
A  .omc/release-notes-0.5.77.md                       (this file)
A  .omc/0.5.77-ship-2026-08-27.md                     (ship log)
M  server/internal/handler/skill.go                   (MUL-6639 metadata)
M  server/internal/handler/skill_metadata_test.go     (MUL-6639 new tests)
A  server/internal/cli/stall.go                       (MUL-6639 stall detection)
A  server/internal/cli/stall_test.go                  (MUL-6639 stall tests)
M  server/internal/cli/client.go                      (MUL-6639 ?include + wrapBodyRead)
M  server/internal/cli/client_test.go                 (MUL-6639 new tests)
M  server/internal/cli/cmd_skill.go                   (MUL-6639 --with-content)
M  server/internal/cli/cmd_skill_test.go              (MUL-6639 new tests)
M  server/internal/service/builtin_skills/multica-skill-importing/SKILL.md
M  server/pkg/db/queries/skill.sql                    (MUL-6639 ListSkillFileMetadata)
M  server/pkg/db/generated/skill.sql.go               (MUL-6639 generated)
M  server/pkg/agent/runtime_ownership.go              (MUL-6658 helpers, new file)
M  server/pkg/agent/proc_other.go                     (MUL-6658 startOwnedProcessTree + releaseProcessGroup)
M  server/pkg/agent/runtime_ownership_unix_test.go    (MUL-6658 6 tests, new file)
M  server/pkg/agent/{claude,codex,opencode,antigravity,codebuddy,copilot,cursor,hermes,kimi,kiro,openclaw,pi,qoder,thinking,models}.go
```

LOC delta: +1932 / -90 across 22 src + 5 test files.

## End-to-end verification

- `pnpm typecheck` (full turbo): **6/6 OK** (cached, 107 ms)
- `go test ./pkg/agent/... ./internal/cli/... ./internal/handler/...`: all packages green except 3 pre-existing failures (locked):
  - `TestDashboardPerAgentRollupsUseExactWindow` (locked 0.5.75)
  - `TestQuickCreateIssueParentTrustBoundary` race (locked 0.5.74)
  - `TestDashboardEndpoints` (NEW pre-existing lock — bisected to `6cefe25be` baseline via `/tmp/wt-baseline-test` worktree)
- New tests PASS: `TestSkill*` metadata tests (7), `Test*OwnedProcessTree*` Unix tests (6), `TestStall*` tests, `TestCLIWrap*` tests, `Test*SkillContentRead*` tests.
- **Ship**: `bash scripts/ship-mac.sh --yes` — cold-start ~4s, server PID verify, Info.plist = 0.5.77, 7/7 steps PASS, nested binaries re-signed.

## Lessons (for future large-batch integration)

1. **Pre-flight ALREADY_PRESENT / overlap-with-fork check before 1500+ LOC port.** B5-starter stopped at pre-flight because fork already has a CONCEPTUALLY DIFFERENT `starter_prompts` feature. Without this check, the agent would have wholesale-adopted upstream's per-agent JSONB system, replacing 4-locale keys + breaking the 3 hardcoded buttons + polluting the chat surface.

2. **Sub-commit splitting is mandatory for commits >200 LOC.** Both B5-skills (1449 LOC, 13 files) and B5-process (790 LOC, 33 files) were successfully applied only because the agent split upstream commits into 5-17 atomic sub-commits by file group. Single `git cherry-pick` of a 1441 LOC commit would have produced unresolvable merge conflicts and thrashed.

3. **Per-sub-commit ship gate check is the line of defense.** The CLAUDE.md §0.5.33 memory lesson ("thrash threshold ~1500-2000 LOC") is conservative; B5-process landed 483 LOC net (after skipping Windows half + 12 fork-absent backends) across 17 commits, all gates green. B5-inbox STOPPED at pre-flight instead of attempting partial port that would break typecheck (`useStatusOptions` shape mismatch would cause `undefined.map(...)` at runtime).

4. **Document every STOP reason.** All 3 STOPPED batches provided specific blocker files + required prerequisites + recommended next action. Future agents can resume from the documented state without re-doing the pre-flight analysis.

5. **Process-tree fork-specific contracts preserved.** B5-process agent correctly:
   - Kept claude + opencode graceful-shutdown `cmd.Cancel` overrides (race-conditioned correctly via group signal).
   - Removed only the redundant explicit `configureProcessGroup(cmd)` call in codex.go line 576.
   - Added `signalProcessGroupCmd(*exec.Cmd, sig)` alongside existing `signalProcessGroup(*os.Process, sig)` to preserve 4 existing call sites.

## Deferred (all non-blocking, future-cycle candidates)

- **B5-starter MUL-6629 + MUL-6709**: Multi-session dedicated port — decide fork's policy for existing 3-button `starter_prompts` (delete / replace / coexist), then port `packages/core/agents/` module additions + `chat-empty-state.tsx` extraction, THEN apply `f8ec870f` + `09a2410e8`.
- **B5-locale MUL-6660**: After `8c563b49` source-context sub-issue feature ported.
- **B5-inbox MUL-6632**: After ~3-5 upstream commits of inbox-archive surface ported (fix `useStatusOptions` shape + add `issue_priority` projection + create `inbox-list.tsx`/`inbox-context-menu.tsx`/`inbox-view.ts`).
- **Batch 5 still pending**: `8c563b49` (10K LOC source context for sub-issues — likely SKIP unless user wants the feature), `e5f976144` (source-context cleanup off runtime sweep — conflicts with 0.5.25 RuntimeGC fix).
- **3 pre-existing test failures to investigate**: `TestDashboardPerAgentRollupsUseExactWindow` (900s delta leak), `TestQuickCreateIssueParentTrustBoundary` (race), `TestDashboardEndpoints` (NEW — agent-runtime task count).

## Files

- `.omc/release-notes-0.5.77.md` — this file
- `.omc/0.5.77-ship-2026-08-27.md` — ship log
- `/Applications/Multica.app` — running 0.5.77

See [[0-5-76-upstream-integration-shipped-2026-08-27]] for the previous batch.
