# 上游同步 2026-09-22:8c4f4328f → f95c3b657(45 提交)

## 背景

- 0.5.109(2026-09-19,锚点 `8c4f4328f`)之后 fetch 到 `f95c3b657`,
  共 **45 个新上游提交**(09-19 → 09-22)。
- 全量 localization 冲突 token 扫描(posthog/electron-updater/autoUpdate/
  SendCode/VerifyCode/GoogleLogin/CloudFront/workspace_invitation/billing/
  subscription/contact-sales/Discord)**零命中**;本批**零 migration**
  (上游 500 号一律不落,fork 289 封顶)。
- 零 shared commits,全部手工 diff 移植;fork 无 `Command` 抽象、无上游
  daemon CLIVersion 填充链、无 integration/Composio/plugin-hook MCP 合成层。

## PORT(13 代码提交 + 1 docs 提交,随 0.5.110 发版)

| # | fork commit | 上游 | 主题 |
|---|---|---|---|
| 1 | `4757dd77d` | (docs 批) | MUL-7577 + MUL-5850 skill-docs halves;顺带修 fork 死文档 bug(--no-start 行) |
| 2 | `be04a7edf` | `da5843f38` | MUL-7528-adjacent KPI row mobile stack |
| 3 | `e7c0f71ca` | `55bc4df17` | MUL-7518 editor 图片已知 kind 保留(forceKind 机制) |
| 4 | `eb10e36a3` | `8bafd36ba` | MUL-7466 shimmer 半:文字 shimmer 降重绘(保留 fork 渐变色) |
| 5 | `1b3fddf3d` | `430a6a2a2` | MUL-7466 border-beam 半:compositor 动画 |
| 6 | `d53636b19` | `b02beb43a` | MUL-7528 单条评论 copy link(onCopyLink + more_actions aria + 4 locale) |
| 7 | `fdfe276de` | `3fbffd508` | MUL-7521 inbox agent activity 通知文案(4 locale) |
| 8 | `4efc88f36` | `6f52e833c` | MUL-7525 本地目录资源删 rename pencil(顺手清孤儿 updateResource) |
| 9 | `fc49fc0e6` | `8feab0abe`(部分) | web 路由 barrel imports 减载 |
| 10 | `8109664eb` | `f95c3b657` | desktop 启动 toolbar clearance(sidebarMounted + workspace loading overlay) |
| 11 | `cf77ae61d` | `80aa31c1a` | **MUL-7520 OpenCode 2.x runtime 支持**(--dir/--variant 移除、model#variant 折叠、MCP 拒跑、session interrupt;fork 适配:execPath 重建 interrupt 进程、lazy CLIVersion detect 加 BuiltinRuntime 门 + 10s 界、wiring 测试不落[合成层缺失]) |
| 12 | `9eb556480` | `9018df3ef` | **MUL-7504 checkout ref UI**(validateGitRef/splitGithubUrlRef/looksLikeCommitSha 落 core;GithubRefField/Dialog;create-project per-repo ref + 全行 submit gate;mobile sheet+list;server 400 前置;brief 双路径同语义 + KEPT-checkout 警告;fr locale 跳过) |

## SKIP(证据在案)

| 上游 | 内容 | 理由 |
|---|---|---|
| `2eef9760f` | comment-runs 层(MUL-7480 家族) | fork 无该整层;单移=死列 |
| `af62622e5` | thread-parent 路由层 | fork 缺宿主 + MUL-4304 replay 哲学冲突 |
| `2b62b2b52` | workdir guard | 整层缺失(比照 MUL-6813 形态) |
| `2aa20b035` | runtime_type 列 | 整层缺失(比照 MUL-6813,零 migration 红线) |
| `f8c90a31d` | issueIdentifierOptions 基建 | fork 无宿主面 |
| 其余 | changelog/docs-only、revert 对 | 会 clobber 版本源 / 净零 |

## Gate 结果(0.5.110)

- `pnpm typecheck`:6/6 ✅
- `go test -count=1 ./internal/... ./pkg/agent/...`:全 ok,DB-backed 确认
  (TestMain localhost 兜底连通,零 "Skipping" 标记)✅
- `pnpm test`:8/8 ✅
- 首跑 handler 包 FAIL 一次 = CLAUDE.md 在案的
  `TestQuickCreateIssueParentTrustBoundary` 共享行竞态;零改动重跑全绿。

## 深挖反转记录

- af62622e5:粗评 MED → 深挖 SKIP(哲学冲突,见上表)
- 5140b99d8:粗评 skip → PORT(同时修 fork 死文档 bug)
- 9018df3ef:存疑 → 确认 PORT(26 files 大件)
- f8c90a31d:LOW → SKIP(宿主缺失)

## 顺延(不动代码)

- source-map L152-154 历史死行、mobile status-pill 注释、desktop 侧
  desktop-layout.test.tsx(MUL-6231 gate 依赖,上游 3 新测试 skip 记
  divergence)、40a48c3c0 的 --recent 10 shape 问题
- MUL-7504 后续:#8572 list-remote-branches(输入辅助,永不成为唯一路径)
- MUL-7523:opencode 2.x MCP 经 --standalone + {env:NAME} 的安全通道
