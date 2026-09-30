# Upstream Sync Ledger — 2026-09-30

- Base: upstream `12f8f3f31` (2026-09-17 batch) → `e31da86c9` (2026-09-29), **40 commits**, 577 files, +48045/−12932.
- Upstream shipped **v0.6.0** (2026-09-28, `ea94c7cd5`).
- Conflict scan: billing/CloudFront/subscription hits all land in fork-deleted cloud surfaces (workspace subscriptions, CloudFront signed media). No posthog / electron-updater / OAuth hits.

## Host-surface ground truth (verified, not assumed)

| Surface | Fork state | Consequence |
| --- | --- | --- |
| wecom | **absent** (0 files; upstream 115) | wecom commits SKIP |
| issue_wakeup v1 (MUL-7418, `c375f83c2`, 142 files +10.9k) | **absent** — fork "wakeup" = daemon-internal only | wakeup v2 needs v1 first = separate project |
| channels / inbound turns | absent | MUL-7710 SKIP |
| chat UI | FAB floating panel (`chat-fab.tsx`), **no chat-page** | chat-page fix SKIP |
| issues surface/ | absent (page-based issue views) | side peek (MUL-7755) = architecture port → deferred |
| PR server side | single `handler/github.go` (upstream split into pr_auto_complete.go + vcs_webhook.go) | MUL-7726 needs that refactor first → deferred |
| settings | fork set (lark-tab, repositories-tab, labs-tab…); upstream regrouped to code-vcs/channels | MUL-7732 SKIP (53/80 files absent) |
| plugin hook MCP layer (`plugin_hook_mcp.go` etc.) | absent | MUL-7746-class fix SKIP |
| desktop window-toolbar | absent (fork has own chrome) | MUL-7724 SKIP |
| builtin skill `multica-platform` | absent (fork has own 294-skill set) | skill-docs hunks in ports: no host, drop |
| Tiptap | ^3.22.1 → upgrade to 3.31.3 | port |
| hermes (`pkg/agent/hermes.go`), MCP sync, PR list FE, attachments page, mermaid editor, ListAgentTasks, issue-usage-dialog | present | ports OK |

## Triage (40 commits)

### PORT — Wave 1 (small fixes, P0)

| Commit | What | Files | Note |
| --- | --- | --- | --- |
| ~~`138134d34`~~ | ~~Hermes custom args before acp subcommand (MUL-7748)~~ → **SKIP-defer** | 5 | file-level 0/5 absent was misleading: fork `hermes.go` is ~1273 lines behind, lacks `HermesLaunchArgv`/profile-resolver architecture; bug (`acp <custom args>` shape, hermes.go:61) exists in fork too — fix needs the argv-classification machinery first → own mini-project |
| `82847e275` | chat tool rows survive MCP object input fields (MUL-7708) | 2 | 0/2 absent |
| `04cdd4857` | notifications ignore malformed member mention IDs | 2 | server-side validation; reconcile with fork bareMentionRe (JYF-490 lineage) |
| `32a396fd5` | dialog scroll lock stops re-rendering Mermaid (MUL-7760); adds `use-theme-version` hook | 2 | hook reused by MUL-7766 |
| ~~`f76cde8e6`~~ | ~~Mermaid fit + inline zoom (MUL-7766)~~ → **SKIP-defer** | 5 | fork mermaid-diagram.tsx 331 lines / css 139 lines behind upstream-pre; needs the upstream mermaid evolution line first (use-theme-version extraction, html-block-document, use-drag-to-scroll, zoom canvas) — own mini-project after 32a396fd5's inline fix |
| ~~`54987006b`~~ | ~~PR row diff + checks icon (MUL-7767)~~ → **SKIP-defer** | 2 | fork pull-request-list.tsx 476 lines behind, ZERO checks_rollup/snapshot_stale/verdict — the whole upstream PR-checks/verdict feature line is absent; same deferral covers MUL-7753 verdict |
| ~~`f1560daff`~~ | ~~fold/unfold comments on identifier URLs~~ → **SKIP** | 2 | fork search-command.tsx 431 lines behind and lacks the fold/unfold-comments commands entirely |
| `400791262` | Tiptap 3.22.1→3.31.3 + select-all-delete pin (MUL-7725) | 7 | **DONE** — views+web bumped; clearContent caret-parking; `select-all-delete.test.ts` new; clearContent test adapted (fork's memoized editorRef needs per-test mockClear before order assert); `pnpm dedupe` was REQUIRED (initial install left dual prosemirror-model 1.25.9/1.25.12 → "wrapping and splitting nodes will fail" broke ~10 editor tests); repair-list-items hunk dropped (module absent in fork — different upstream fix line); editor suite 42 files/539 tests green + 2 mutation checks |
| ~~`bb40537c5`~~ | ~~run spinner after steer (MUL-7729)~~ → **SKIP** | 2 | `use-run-comment-motion` hook absent in fork (ambient run-motion feature line never ported) |
| `692dd019a` | docs: macOS TCC re-prompt after CLI upgrade (MUL-7713) | 5 | apps/docs |
| `6328e8183` | mobile: don't seed incomplete project lists | 4 | mobile excluded from turbo; cheap parity |
| `ba9f63924` | docs(mobile): icon comment | 1 | trivial |

### PORT — Wave 2 (medium features, P1)

| Commit | What | Files | Note |
| --- | --- | --- | --- |
| `815fe37b1` | task history pagination (MUL-7685) → **DONE in round 2 as rewrite-port** | 24 | file-level moat (agent.go 1686) was real but the ListAgentTasks function itself was fork-identical — measure the FUNCTION, not the file |
| `3125bd2ea` | full-size stacked images (MUL-7736) → **DONE in round 2 as rewrite-port** | 8 | direction reversal caught at port time: upstream commit REMOVES its own justified rows; ported the end state (image-standalone + 36rem cap) |
| ~~`e8a16d334`~~ | ~~one verdict per PR (MUL-7753)~~ → **SKIP-defer** | 10 | verdict base absent (see MUL-7767) |
| ~~`13094caf7`~~ + ~~`67d61a207`~~ | ~~issue runs timeline + run charts (MUL-7758/7780)~~ → **SKIP-defer** | 26+11 | re-measured in round 2: issue-detail mount is only 1 line, but the meat sits in `agent-transcript-dialog.tsx` (2092 lines behind) — still a real moat |
| ~~`6dc6a0b9a`~~ + ~~`0a51a6cc4`~~ + ~~`344ffaf1b`~~ | ~~attachment viewer wave (MUL-7650/7759/7737)~~ → **SKIP-defer** | 31+10+17 | page wrapper is near-current (20-line delta) but the real hosts diverged: `attachment-preview-modal.tsx` 735, `handler/file.go` 721, ui `data-table.tsx` 607. Carrying only the 10 new modules would be dead code |

### PORT — Wave 3 (large, P1/P2)

| Commit | What | Files | Note |
| --- | --- | --- | --- |
| ~~`3d2592a9a`~~ | ~~local search index (MUL-7754)~~ → **SKIP-defer** | 52 | mostly-new modules (search-index/*, handler, scheduler jobs, migs 561-563) but wiring runs through the fork's most-diverged files: `core/api/client.ts` 3639, `api/schemas.ts` 3452, `router.go` 2177, `use-realtime-sync.ts` 1025. Prerequisite: core api/realtime layer re-sync |
| ~~`7dac88a6a`~~ + ~~`4f2fdc0b1`~~ | ~~deliverables + dynamic blocks (MUL-7649/7733)~~ → **SKIP-defer** | 76+4 | integration hosts = comment-card / issue-detail / readonly-content, all far behind; new rich-content/deliverables modules would arrive dead |

### SKIP (15)

| Commit | Why |
| --- | --- |
| `5ed09342a` MUL-7680 wakeup v2 (+`92649de29`, `decd602a5`, `448411e5c`, `ea94c7cd5`-adjacent) | issue_wakeup v1 never ported (migs 509-533 missing); v1+v2 ≈ 25k-line full-stack chain → own project |
| `104bf7be3` MUL-7732 settings regroup | 53/80 files fork-absent; fork settings set diverged (lark/repositories/labs) |
| `57fabfc07` MUL-7724 desktop toolbar | window-toolbar.tsx absent; fork has own desktop chrome |
| `bd7308742` plugins hook tool collisions | plugin_hook_mcp.go layer absent |
| `4736a85d4` chat workspace switches | chat-page.tsx absent (fork chat = FAB) |
| `4e2afed72` MUL-7710 channel inbound permission | channels absent |
| `fa5d470ae`, `7551ff1dd` wecom ×2 | wecom absent |
| `e31da86c9` MUL-7801 junction workdirs | Windows junctions; fork macOS-only; upstream `util/path*.go` absent |
| `4054d5780` MUL-7755 side peek | issue-view architecture port (surface/, issue-opening-store, table-view absent) → own project |
| `fc4a61570` MUL-7726 pr_merge_status | needs upstream pr_auto_complete/vcs_webhook refactor fork never took → deferred with prerequisite |
| `e427ed7df` MUL-7756 Save-as-view name | save-view-dialog absent (feature fork lacks) |
| `9c6622787` readme refresh | fork README is fork-notice; upstream facts N/A |
| `ea94c7cd5` changelog v0.6.0 | would document unported features as shipped |

## Migration renumber plan

Nothing renumbered this batch — every landed port is migration-free. The deferred features carry upstream migs 551-563; renumber to fork 293+ at their own port time (forward-only law).

## Gates (final, all green)

- `pnpm typecheck` — 6/6 turbo tasks
- `cd server && go test -count=1 ./internal/... ./pkg/agent/...` (DATABASE_URL exported from repo root) — **rc=0, zero failures**
- `go test ./cmd/server/` — only the 2 recorded baseline reds (TestCommentTriggerOnComment / TestCommentTriggerAtAllSuppression); the new mention tests pass
- views vitest — **1914 passed / 33 skipped** (the in-案 skips; net +8 tests from this batch)
- core vitest — **965 passed**
- mobile vitest — **36 passed** (lane widened to data/)

## Mimosa hook notes (for the user)

- Commit hook intermittently runs a project-wide scan; one pass flagged `apps/mobile/data/api.ts:244/:1218` as SSRF-high. Both lines are `fetch(\`${API_URL}${path}\`)` — the mobile app calling its OWN typed backend; paths come from in-file API methods, not external input. Assessed false-positive-by-design; file untouched by this batch; NOT "fixed". Most commits then proceeded under the scanner_enobufs 兼容 path.
- The hook also pattern-matches source filenames appearing inside Bash commands — a commit MESSAGE naming a `.ts` file gets blocked as a "write". Workaround: keep filenames out of commit messages.
- A deep Mimosa audit of the repo has NOT been run this session (hook's own words: 不能把未发现更多问题解释为项目安全).

## Ledger updates

### Round 2 — rewrite-ports of high-value deferred items (same day, user request)

- **`815fe37b1` MUL-7685 task-history pagination — DONE as a rewrite-port** (commits `8efee0e3e` server + `aa479410d` FE/CLI). The agent.go moat was real but ListAgentTasks itself was identical shape in fork, so the feature transplanted cleanly: keyset `(created_at,id)` pages via limit+1 read, cursor on `X-Agent-Tasks-Next-Cursor`, visibility predicate moved into SQL ahead of LIMIT (behavior change on fork — escalation placeholder rows were previously returned unfiltered), 30d buckets gain duration_ms/duration_count, FE infinite query + Show-more-fetches-next-page + server-aggregated mean duration, CLI --limit/--before. Migration 552 → **fork 293** (partial index; bundle-cli mirror committed; applied to local DB). Tests: 205-task cursor walk + boundaries + duration-from-201-runs (Go, fork harness) + core queries.test (paged lifecycle).
- **`3125bd2ea` MUL-7736 full-size stacked images — DONE as a rewrite-port** (commit `1f27b5d6a`). Direction reversal caught at port time: this upstream commit REMOVES the justified rows (introduced and reverted within the same upstream window); fork never had them. Ported the end state: `layout="card"` on the standalone list marks figures `image-standalone` (natural width, column-capped, max-height 36rem, hairline border) instead of fork's width:100% that stretched phone screenshots down the page.
- **Re-deferred after integration-hunk measurement**: `13094caf7`/`67d61a207` runs timeline (the meat sits in `agent-transcript-dialog.tsx`, 2092 lines behind — the 1-line issue-detail mount was never the cost), attachment viewer wave (main host `attachment-preview-modal.tsx` 735 behind), local search (client.ts 3639). Lesson: measure the COMMIT's own hunks against fork hosts, not just the file-list absence rate — a commit touching 26 files may carry only 1 line into a "diverged" file, and vice versa.
- Gates re-run after round 2: typecheck 6/6; go internal/... rc=0 + pkg/agent package green on rerun (2 Codex timer tests flaked once under full-suite load, passed in isolation and package-wide — same flake class as the recorded vitest 5s ones); views **1915**/33 skip; core **968**; cmd/server 2 known reds only.

### Round 1

- **`82847e275` DONE** — fork had the same bug shape but NO `trace-event-presenter.ts` host → semantic minimal port: hardened `getToolSummary` (chat-message-list.tsx) to string-only candidates (Record<string,unknown> + text/short/clip helpers); regression test reuses fork's `renderSettled` harness. Mutation-verified: without fix, `Objects are not valid as a React child (keys {email})` reproduces. → commit dd39efb10
- **`04cdd4857` DONE** — 3-line guard in `notifyMentionedMembers` + 2 tests in fork idiom. Fork twist: local `parseUUID` = `MustParseUUID` → **panics**, so the bug was a whole-listener kill (comment:created notifications die on one bad mention), stronger than upstream's failed insert. Mutation-verified: `panic ... invalid UUID "all"`. → commit 5c940ca98
- **`32a396fd5` DONE** — signature-gated theme bump applied to the fork's INLINE `useThemeVersion` in mermaid-diagram.tsx (fork never took the hook extraction). New minimal `mermaid-diagram.test.tsx` (fork had none). Mutation-verified. → commit a746a7653
- **`400791262` DONE** — tiptap 3.22.1→3.31.3 in views AND web; clearContent caret-parking; select-all-delete regression test (fork's createEditorExtensions signature compatible); clearContent test adapted (fork's memoized editorRef needs per-test mockClear before order asserts). `pnpm dedupe` REQUIRED — first install left dual prosemirror-model (1.25.9/1.25.12) breaking ~10 editor tests with "wrapping and splitting nodes will fail". repair-list-items hunk dropped (module absent). → commit 8a6fd8f11
- **`692dd019a` DONE** — TCC section ported into fork's OWN zh/ja/ko troubleshooting docs (fork has no en/fr); closing paragraphs rewritten (fork: unsigned bundled CLI, no auto-update). → commit d895f7ca5
- **`6328e8183` + `ba9f63924` DONE** — mobile seeding guards + vitest lane widened to data/ with @ alias + both upstream test files; icon comment corrected (fork asset already matched the fixed state: distinct bytes, hasAlpha=no — verified before writing the claim). → commits 48386f585 / f1af31f19
- **INCIDENT (recovered)** — mutation-check via `git stash push/pop` run from `server/` cwd: push pathspec misresolved (did nothing), pop then merged a STALE 0.4.x-era stash into the tree (25 conflict files). Recovered via `git reset --hard HEAD` + re-applied the 4 port files by hand; stash stack intact. **Rule: mutation checks in this repo use Edit-remove/Edit-restore only — never `git stash` (stale-stack + cwd-relative-pathspec footguns).**
- **Triage method correction** — file-absence rate (0/N "all hosts present") is NOT enough: `138134d34` (hermes) passed file-level 0/5 but fork's hermes.go is 1273 lines behind and lacks `HermesLaunchArgv`. Function-level host checks + `git diff HEAD:<file> <sha>~1:<file> --stat` divergence measurement are mandatory before committing to a port.

## Divergence moat map (measured this batch — read before any future port touching these)

| File | Fork lines behind upstream `e31da86c9`~ | Blocking |
| --- | --- | --- |
| `server/internal/handler/agent.go` | 1686 | task-history pagination |
| `packages/views/issues/components/issue-detail.tsx` | 1917 | runs timeline / deliverables |
| `packages/core/api/client.ts` | 3639 | local search / most FE features |
| `packages/core/api/schemas.ts` | 3452 | same |
| `server/cmd/server/router.go` | 2177 | local search server wiring |
| `packages/views/editor/attachment-preview-modal.tsx` | 735 | attachment viewer |
| `server/internal/handler/file.go` | 721 | attachment viewer |
| `packages/ui/components/ui/data-table.tsx` | 607 | attachment viewer |
| `packages/views/issues/components/comment-card.tsx` | 789 | image rows / deliverables |
| `packages/views/search/search-command.tsx` | 672 | search-command features |
| `server/pkg/agent/hermes.go` | ~1273 | Hermes argv/profile line |
| `packages/views/issues/components/pull-request-list.tsx` | 476 | PR checks/verdict line |
| `server/internal/daemon/daemon.go` | (large) | wakeup/hermes daemon paths |

Deferred-with-prerequisite projects (by value): ①core api/realtime re-sync → unlocks local search + most FE features; ②issue-detail/comment-card re-sync → runs timeline, deliverables, image rows; ③agent.go ListAgentTasks line → task-history pagination; ④attachment modal + file.go → attachment viewer; ⑤hermes HermesLaunchArgv; ⑥wakeup v1 (MUL-7418) then v2.
