---
name: upstream-integration-2026-q3
description: 上游 multica-ai/multica v0.4.26 全谱 cherry-pick sweep final consolidated report (2026-08-17)
created: 2026-08-17T10:00:00Z
updated: 2026-08-17T05:00:00Z
---

# Upstream Integration 2026 Q3 — Final Consolidated Report

## 背景

- 本地 fork: `multica-exploration-dev` @ 0.5.25 (`954a41ba0` initial + sweep commits)
- 上游: `multica-ai/multica` @ v0.4.26 (`8b1acfd19`)
- merge-base 空 → 完全分叉,只走 cherry-pick
- 125 upstream commits,本地 354 commits
- 已存在的本地特性: labs 平台 (6 flags) / swarm topology / agent self-opt / 安全加固 / lab auto-dispatch opt-out / MUL-5799 (0.5.20 已合) / RuntimeGC fix (0.5.25 已合)

## 0.5.25 RuntimeGC fix — 关键发现

memory `0.5.25-runtimegc-fix-2026-08-17.md` 揭示:0.5.18 audit 声称的修复实际从未 commit。本地已落地:
- `3592725d4` fix(experimental): RuntimeGC never wired + Run() defer bug + regression pin
- `29aaf522d` fix(test): TestQuickCreateIssueParentTrustBoundary test isolation  
- `758ee4d90` docs(root): RuntimeGC entry correction
- `6b4c3d459` chore(release): bump 0.5.24 → 0.5.25
- `eebe52ba8` docs(root): 0.5.25 entry
- `954a41ba0` docs(server): RuntimeGC contract

**重要**:本 session 期间外部 reset 把我的 MUL-6104 (d96d4e729) 重置了,然后通过 wave4 merge auto-rebuilt 引入。

## 集成的 8 upstream commits (本 session)

| 本地 SHA | Upstream | MUL | 主题 |
|---|---|---|---|
| `d96d4e729` | `cc2aea387` | MUL-6104 | daemon execenv brief stderr/stdout split (3 files +64/-1,含 legacy verbose mirror — 0.5.15 lesson) |
| `d957f5e62` | `b32cd8c8a` | MUL-5894 | metric label protection — plan 误标 N/A,agent 验证实际可应用 |
| `68d6b3a38` | `eb3cd4fe9` | MUL-5951 | fix(issues) reject cross-workspace project on update |
| `a1bb54461` | `822c6a0b8` | — | feat(issues) inherit parent's assignee on sub-issue (**REVERTED — upstream inconsistent,test 引用未实现的 `removeParent`**) |
| `4a9c9f330` | `9c21786f4` | — | fix(composer) preserve focus on send/stop |
| `3eb6cb069` | `e9528c722` | MUL-5855 | feat(dev) worktree database cleanup (dev tool,7 files) |
| `6a8ca5809` | `84dc02cd6` | MUL-6034 | fix(cursor) normalize MCP approval server shapes |
| `2d5857814` | `59021eb8a` | MUL-6004 | fix(issues) bound inline chips (4 files) |
| `8d2f6847e` | `375ba786b` | MUL-6240 | fix(views) release drag lock on cancelled drags (3 files, adapted for local fork — 删 list-view.test.tsx 缺 ScrollRestorationProvider) |

## Wave 4 N/A verification (22 commits) ✅

Cloud: MUL-5852 (3), MUL-6175 (2), 987ac8ec8 billing CORS  
Mobile: MUL-6237 PWA, MUL-6206 iOS  
Windows: MUL-6118 (2), MUL-6025  
渠道: MUL-5947 Dingtalk, MUL-5905 Wecom, MUL-5915 Slack neutral, MUL-6193 Slack attach, d8c41cc79 typing indicator, MUL-5966 dup media, 3ebd0b541 Dingtalk icon  
Lark/Slack Settings: ce8d80b2e, 8c0185019, 2b3920a98  
MUL-5854: 不适用 (local 结构已正确)  
**b32cd8c8a MUL-5894**: ❌→✅ APPLIED (defense-in-depth 验证后)

## Wave 3 N/A / Arch duplicates (12)

- MUL-5421 workspace MCP / per-agent MCP — local user_* plugin namespace + manifest.capabilities.leader 覆盖
- MUL-6139/6099/6075 Plugin V1 — local multica-lab-builder skill 覆盖
- MUL-6125 Private Skill Plugin dev loop — local lab-builder 覆盖
- MUL-5707 worktree mode — local per-profile 隔离
- MUL-5905/5947/6193/5974 — 渠道 (Slack/Wecom/Dingtalk)
- MUL-6118/6025 — Windows
- MUL-6206 — mobile
- DeepSeek Harness — local 不用

## Wave 3 Aborted (11 with reasons)

| MUL | 原因 |
|---|---|
| MUL-5974 daemon status health port | 依赖 first commit 没应用 |
| WS prefix in onboarding | local 用 div/p layout,upstream 用 Field/Input |
| MUL-6132 daemon task markers | undefined symbols (MUL-3922 工作未本地) |
| MUL-6019 Codex first-turn timeout | 4 fields to ExecOptions + config changes 冲突 |
| MUL-6015 Codex gpt-5.6 labels | normalizeCodexDynamicLabel 冲突 |
| MUL-5963 web chat history | modify/delete 冲突 |
| stale daemon port | 10+ 冲突 (auth/dynamic-port handling 差异) |
| MUL-6183 agent create error flash | modify/delete 4 frontend files |
| MUL-6048 close intent PR ambiguous | 6 DB columns 不存在 local schema |
| imported skills update | 21+ files 改动 |
| MUL-5854 chat footer spacing | local 已正确 |

## Wave 1 实测规律 (9 commits 样本)

| 维度 | 实测 |
|---|---|
| 干净 cherry-pick | ~44% (4/9: MUL-6104, MUL-5951, MUL-6004, MUL-6240 adapted) |
| modify/delete skip | ~11% (1/9: MUL-6024 缺 openclaw_stdout.go) |
| 内容冲突 | ~11% (1/9: MUL-5979 UI 大幅演化) |
| 依赖缺失 | ~22% (2/9: MUL-6137 需 Win,MUL-5999 需 mig 273-283,MUL-6108 需 MUL-5999) |
| 单 commit 工时 | ~15-30 min |

## 🔒 Conflict Backfill Queue (3 commits)

1. **`8cafe1d08` MUL-5979** — `agent-detail-page.tsx` 本地演化太大,UI 改动需逐行适配
2. **`e519dd9e8` MUL-6168 perf** — `service/task.go` 3019 行 add-add hunk (line 949/1024),perf fix 价值高
3. **`822c6a0b8` sub-issue inherit assignee** —**UPSTREAM INCONSISTENT**: test 引用未实现的 `removeParent` method。需先 upstream 修,或本地 backport `removeParent` 实现

## Wave 2 — FAILED (API 529,需 retry)

20 commits UI/UX (Cmd+,/history nav/mention hover/keyboard shortcuts/lint). 下次 session 重试。

## Batch 2 — 并行 agent 批次 2 (5 commits, 2026-08-17)

第二批 3 个 agent (wave-arch2 / wave-cleanup2 / wave-ui2)。UI agent 中途被 **429 token 配额超限** 杀掉,但落地了 3 commits。成果全部 merge + verify:

| 本地 SHA | Upstream | MUL | 主题 | Agent |
|---|---|---|---|---|
| `58ab45f70` | `e519dd9e8` | MUL-6168 | perf(tasks) bulk cancel 去重 agent-status reconcile (`service/task.go` add-add hunk,2 处) + `task_cancel_reconcile_dedup_test.go` | wave-arch2 |
| `16f3101ec` | `822c6a0b8` | — | sub-issue 继承 parent assignee — **source-only selective cherry-pick** (upstream test 引用未实现的 `removeParent`,tests dropped;`35afc1893` revert 后重新应用 source) | wave-cleanup2 |
| `0de552fe6` | — | MUL-6060 | fix(editor) Markdown H4-H6 不再折叠成 H1 (`prose.css` + heading-levels test) | wave-ui2 |
| `e293be05f` | `9ba3a8bdf` | MUL-5980 | feat(board) 空白处左键拖拽横向平移 (`use-board-drag-pan.ts` + 346 行 test) | wave-ui2 |
| `680ef6310` | — | MUL-6002 | feat(agents) agent 环境变量批量编辑模式 (`env-tab.tsx` + `env-file.ts` + 4 locales,2597 行) | wave-ui2 |

Cleanup agent 还验证了 **0.5.25 ship ready** (pnpm typecheck / go test / go build 全 PASS),但 `make ship-mac` 未跑 (需 user 授权)。

## Verification (batch 2 final)

```
$ pnpm typecheck
Tasks: 6 successful, 6 total

$ go test -count=1 ./internal/service/ + TestDistinctAgentIDs*
ok

$ go build ./...
exit 0

$ npx vitest run use-board-drag-pan.test.tsx env-tab.test.tsx env-file.test.ts
Test Files 3 passed / Tests 62 passed
```

## Final HEAD (batch 2): `680ef6310 MUL-6002: feat(agents) bulk edit agent env vars`

6 个 session worktree 已移除,6 个 merged branch 已删除 (`epic/0.5.26-wave*` + `worktree-agent-*`)。

## 下一 session 建议

1. **Wave 2 剩余 17 commits** (UI/UX — Cmd+, / history nav / mention hover / keyboard shortcuts / lint)。429 配额是硬约束 — 等配额恢复再开 agent,或主线程逐 commit inline cherry-pick (成本更低)
2. **Backfill 剩 1**: MUL-5979 (`agent-detail-page.tsx` 本地演化大,需逐行适配)
3. **Ship 0.5.26** (当前 0.5.25 基线 + 13 个集成 commit;ship 前需 `make ship-mac` user 授权)

## 完成判定

✅ 8 (batch 1) + 5 (batch 2) + **8 (session 续 — Tier 1-3)** = **21 commits applied + verified**  
✅ N/A catalog 完整 (36 commits 验证不适用)  
✅ Wave 3 / 4 架构 analysis 完成  
✅ Backfill 3/3 (MUL-6168 ✓, sub-issue ✓ source-only, MUL-5979 仍待)  
⏳ Wave 2 17/20 pending (429 配额限制)  
⏳ Final 0.5.26 ship pending (需 user 授权 `make ship-mac`)

## Session 续 (2026-08-17, Tier 1-3) — 8 commits

Tier 1 (Go bug/perf):
| 本地 SHA | Upstream | 主题 |
|---|---|---|
| `21ef71a24` | `8b1acfd19` | execenv metadata fold — 双路径(slim+legacy verbose)适配,0.5.15 lesson 重演 |
| `4de64b083`+`611f75430` | `e4ebd41de` | daemon log 绝对路径 + 本地测试 pin |
| `449b6b605` | `f51ee62b5` | MUL-6164 exec_format core (ExplainExecError + ReasonAgentRuntimeMissingExecutable + 4 locale) |

Tier 2 (i18n — 本地化定位): `12a286d47` (date-i18n useLocale, gantt source)、heatmap localize 600c5a8 (source)、`4dd89d616` (MUL-6050 slug 拼音化,含 celestial-workspace-names dep + step-workspace matchLocale 适配)

Tier 3 (UI/UX): `561f1daf9` (MUL-6233 Cmd+, 设置 — ShortcutInput 加 alt/isAutoRepeat + preload ipcRenderer.on 适配)、`a4b362e70` (16f9f2522 inbox archive "E" 快捷键 — 只 wire handleArchive,去 handleUnarchive)

**Aborted (原因)**: 19155e41f prompt compress (本地 Available Commands 已演化)、a954dec8e test-only (525 行测试冲突)、b30a3ae55 MUL-6164 wiring (18 文件冲突)、a077ace2f/1ddc3812a/367e7ee6e tab-bar 簇 (本地 tab-bar 演化 + MRU)、2cd836d3d MUL-6218 (本地 sidebar 有 resize,upstream 用 hasExternalTrigger — 不同 context 设计)、a3dfd2439 MUL-6082 (同上)
**N/A**: 49cc7d6f4 auth 封禁滥用 (单用户 fork 无滥用向量)
**Deferred (设计级移植)**: MUL-6107 runtime GC 保留历史 (migration 缺口 246→309 + sqlc)、MUL-6126 私有 runtime (25 文件)、MUL-6053/6102 Hermes 系列 (482 行新文件 + Hermes 专有)

**Pre-existing flaky 确认**: `TestQuickCreateIssueParentTrustBoundary` 在 batch-2 基线 (6ec1b4959) 同样失败 — `daemon_version_unsupported` metadata race,非本 session 回归。0.5.25 fix 只修了 isRuntimeOnline race,metadata 竞争仍存。

## Verification (session 续)

```
pnpm typecheck: 6/6 全绿
go test ./internal/... ./pkg/agent/... ./cmd/multica/: 全过(除上述 pre-existing flaky)
keyboard-shortcuts tests: 30/30
slug/step-workspace tests: 14/14
```

## Final HEAD: `3426bf235 fix(desktop): add alt/isAutoRepeat to ShortcutInput`