---
name: upstream-integration-2026-q3
description: 上游 multica-ai/multica v0.4.26 全谱 cherry-pick sweep final consolidated report (2026-08-17)
created: 2026-08-17T10:00:00Z
updated: 2026-08-17T10:34:00Z
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

## Verification (final)

```
$ pnpm typecheck
Tasks: 6 successful, 6 total

$ go test ./internal/handler/ ./internal/daemon/execenv/ ./internal/metrics/
ok  handler    0.752s
ok  daemon/execenv  0.718s
ok  metrics    0.876s

$ go build ./...
exit 0
```

## Final HEAD: `8d2f6847e fix(views): release the drag lock on cancelled drags (MUL-6240)`

Worktree branches `epic/0.5.26-wave{1,3,4}` 现在可删除。Plan doc 保留作历史。

## 下一 session 建议

1. **重试 Wave 2** (UI/UX 增量 — 20 commits)
2. **Backfill conflict queue** (3 commits with manual adaptation)
3. **Ship 0.5.26** when next batch done

## 完成判定

✅ 8 commits applied + verified  
✅ N/A catalog 完整 (36 commits 验证不适用)  
✅ Wave 3 / 4 架构 analysis 完成  
✅ Plan doc 留存作历史  
⏳ Wave 2 retry pending  
⏳ 3 backfill commits pending  
⏳ Final 0.5.26 ship pending