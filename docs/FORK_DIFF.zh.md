# Multica Local — Fork 对比文档（vs 上游 multica-ai/multica）

本文档详细说明 `jjy1000/multica-local` 与上游 [multica-ai/multica](https://github.com/multica-ai/multica) 的差异、动机、关键取舍，以及为什么值得作为独立分支运行。English overview 见 [`../README.md`](../README.md)。

---

## 0. 一句话定位

**本 fork 是面向单用户 macOS 桌面场景的 Multica 本地化分支**：保留完整的「人类 + AI agent 共用一张任务看板」平台，移除所有为云 SaaS 业务而存在的部件（遥测、OAuth、云运行时、auto-update、计费、Discord 支持入口），把 macOS 桌面 App 当成首要交付物自包含打包。

---

## 1. 删掉了什么（vs 上游）

| 类别 | 上游 | 本 fork | 状态 |
|---|---|---|---|
| **遥测** | PostHog 全链路 + FE analytics | `analytics.NewFromEnv()` 恒返 `NoopClient{}`；FE analytics 全 no-op；`server/internal/analytics/posthog.go` 文件删除 | ✅ 删除 |
| **自动更新** | `electron-updater` + 桌面 `updater.ts` + daemon `autoUpdateLoop` | 依赖卸载；CLI update 命令 stubbed；daemon 启动不调 autoUpdateLoop；`electron-builder.yml` 无 `publish` 块 | ✅ 删除 |
| **OAuth / 邮箱验证** | Google OAuth + 邮箱验证码 | `SendCode` / `VerifyCode` / `GoogleLogin` 全部 410 Gone；仅 `UsernameLogin`（`POST /auth/login {"name":"alice"}`） | ✅ 删除 |
| **云业务** | hosted runtime + 计费 + CloudFront + 联系销售 + 云 PAT | 全部删除 | ✅ 删除 |
| **外部支持 UI** | HelpLauncher / JoinDiscordCard / Discord 图标 / FeedbackModal | 全部删除 | ✅ 删除 |
| **Email verification** | 有 | 无（首次登录即 upsert 用户，email = `name + "@local"`） | ✅ 删除 |
| **Marketing** | multica.ai / Cloud / Discord / X(Twitter) 链接 | README 内全部移除 | ✅ 删除 |

---

## 2. 留下了什么（vs 上游）

平台本体一行没动：

- **后端**：Go 1.26.1 + Chi + sqlc + gorilla/websocket + pgvector；handlers、migrations、service 层、WS push 全部保留
- **桌面**：Electron 39 + Vite + React 19.2.3 + TypeScript 5.9；renderer / main / preload 全套
- **Web**：Next.js App Router，路由与平台胶水
- **移动**：Expo / React Native，与 web+desktop 共享 `@multica/core` 的类型与纯函数
- **共享包**：`packages/core` / `packages/ui` / `packages/views` 完整保留；view → core + ui 的依赖方向不变
- **数据库**：PostgreSQL 17 + pgvector，迁移 forward-only 永不 drop 列/表
- **Labs 平台**：所有 flag catalog 完整保留（`pythia_oracle` / `mythos_swarm` / `claude_science_lab` / `timesfm` / `llm_wiki_bridge` / `causal_graph` / `semantica` / `user_*` plugin 机制等）

---

## 3. 加了什么（fork 独有）

### 3.1 单用户登录 + 工作区所有权

`POST /auth/login` 仅接收 username，**首次登录即新建 user 行**。user_id 与 workspace 成员绑定，跨重启改 username 即拿到一张新 workspace —— 这是**有意的设计**：自动绑定会让一次 typo 静默把所有权拱手送人。教训来自 `2026-06-27 username-only-login-loses-workspaces` 事故，详见 fork 内文档。

### 3.2 桌面自包含打包

macOS 桌面 App 是首要交付物：

- DMG 内打包 Go 后端 + daemon + CLI + Electron renderer + 嵌入式 Postgres
- `--mac --dir` 路径（DMG 创建在 `create-dmg 1.2.3` 上 hang，已记录在 known quirks）
- 嵌套二进制重新签名（`scripts/desktop-sign-nested-binaries.sh`）
- 冷启动 `verify` 强制 gate（ship-mac.sh step 7 FATAL since 0.5.100）
- 浏览器窗口 off-screen guard（Electron 39 + macOS workArea 边界 clamp）
- `~/.multica/desktop.json` + 每 profile 独立 `~/.multica/profiles/<name>/.env`

### 3.3 上游同步工程化

本 fork 与上游 `git merge-base HEAD upstream/main` 是 **空集** —— 所有「移植」都是手动 diff 移植，cherry-pick 完全不可用。每次发版（0.5.x → 0.5.x+1）走 6 步：

1. **Pre-flight import audit**：grep 上游 commit diff 中的 fork-absent 包，确认本 fork 是否仍有对应目录
2. **三个并行 deep-dive research agent**：(a) 性能/安全 (b) UX (c) 未触及/新增候选；每个 agent 必跑 `git show <sha>` 看实际 diff，不靠「已 dormant」假设
3. **AskUserQuestion 锁定 ship scope**：合并发现 → 决策表 → 询问用户
4. **per-file diff sanity gate**：fork 单文件 diff ≤ 5× upstream per-file stat，杜绝 `git checkout --theirs` 整文件覆盖
5. **验证**：`pnpm typecheck` + `cd server && go test -count=1` 必跑；发版前全量 typecheck + go test + cold-start verify
6. **commit body 记录 fork-vs-upstream gap**：每个 fork 缺失/重命名/test infra 缺失的 commit 都在 message 里写清 WHY

本 fork 已记录 22 批 upstream 移植，详见各次 `release/0.5.x` 的 ship notes（fork 内部 `.omc/0.5.*-ship-*.md`，未公开）。

### 3.4 已知稳定性修复（fork 独有）

| 事故 | 简述 | 修复 commit |
|---|---|---|
| `0.5.25 RuntimeGC bug` | 3 个 expires_at 梯子从未接线（task_token / workspace_invitation / daemon_token）有 DELETE 但无 caller；`experimental_claude_runtime_session` 永久泄露 8 个版本 | `AuthTokenGC` + migration 249 |
| `0.5.27/28 PORT env leak` | GUI daemon-manager spawn 时漏传 `--server-url`，daemon fallback 到 shell 里的 `PORT=8080`（用户 SearXNG 占用），所有 agent 入队 | `e4a69d314` + `0ff8c64a4` |
| `0.5.30 stale renderer build` | Vite 把 highlight.js Zig 模块塞进 out/renderer/index.html 头，ship 静默崩溃 | `4a/7 renderer integrity check` |
| `0.5.99 ReferenceError on spawn` | H9 audit fix 用了 `export { x } from "..."`（bare re-export），ReferenceError → ensureServerUp 60s timeout | `0.5.100` ship-chain verify 必须 FATAL |
| `0.5.102 board 渲染 N 张同 id 卡` | 创建 1 个 issue 在同一列渲染 5/4 张重卡；服务端永远只 1 行；纯客户端 fan-out 污染 | 3 层防御去重（`addIssueToBuckets` + `flattenIssueBuckets` + `buildColumns`）+ 上游对不上（upstream `useCreateIssue` byte-identical） |
| `0.5.108 PG 外部实例僵尸事故` | app 启动采纳外部救场 PG → 之后该实例 SIGTERM → app 僵尸 8.5h；/health 不探库恒 ok；daemon 401 "invalid token" 实为 DB 宕机；snapshot 错误映射 404（全天 0 个 5xx） | 诊断顺序：先查 5432 listener + server-manager.log `backend selected`，再谈鉴权 |

---

## 4. 上游平台不变量（fork 内完整保留）

8 条负载型 cross-cutting 模式，本 fork 内完全保留并在 fork 调整后重新测试：

1. **5s 轮询兜底** — lab-class query keys 没 WS push 时的 fallback（`refetchInterval: (q) => isLive(q.state.data) ? 5_000 : idleMs`）
2. **Lab leader rewrite on `lab_source` flip** — 4-case contract + `assignDefaultLabAgentOnUpdate`；本 fork 0.5.107 加固了 Batch 路径（之前仅 single-issue PATCH 通过锁检查）
3. **chi route order — literal slug BEFORE `{param}`** — `extractWorkspaceSlug` 误把 `experimental` 当 workspace slug 引发 AppSidebar 全屏失效（0.5.73 修）
4. **`lab_managed` DTO marker** — 来自 `experimental_resource_visibility` 行，render gate 用 `lab_managed: false`
5. **Swarm Topology 整层 retired** — 0.5.105 审计 H3 决策，`swarm_topology` 标 `Frozen: true` 墓碑；后续在 `mythos_swarm` 上重建
6. **Lab auto-dispatch opt-out** — `experimental.Flag.AutoDispatch *bool` per-catalog
7. **Workflow file-overlap graph** — batch 并行化时同 file PR 必须分阶段
8. **Causal-graph trust ladder** — Tier A→D 优先级；Tier D `status='suggested'` 阈值 0.5；reject 是墓碑绝不删

---

## 5. 什么时候适合用本 fork

✅ **适合**：
- macOS arm64 单机开发机，想把任务看板当成「带 AI agent 的本机 GTD」长期跑
- 不接受任何形式 telemetry / 联网账号系统的隐私敏感用户
- 想跟踪上游 Multica 但被云 SaaS / OAuth 绑架而无法落地的团队
- 研究 Multica 平台机制（handler / sqlc / WS / Labs / Electron 集成）而不必登录多账号

❌ **不适合**：
- 需要 SaaS 多用户协作（应直接用上游 multica.ai）
- 需要 Google OAuth / 团队成员邀请 / 计费（上游有，本 fork 删了）
- 需要 Windows / Linux 桌面（fork 仅 macOS arm64 验证；其他平台代码在但 DMG 未打）
- 需要云端 agent 长时间任务（fork 是本机 daemon + 本机 agent subprocess）

---

## 6. 如何从上游重新同步

本 fork 设计上**永不自动 merge 上游 main**（merge-base 永远是空的）。要拉一批上游 commit 进 fork，follow `CONTRIBUTING.md` 的 6 步流程：pre-flight import audit → 三并行 deep-dive → scope 决策 → per-file sanity gate → 验证 → commit 记录 gap。

每个发版批（0.5.x → 0.5.x+1）通常 port 10-20 个 upstream commit，SKIP 10-30 个 fork-absent infra 的 commit。详细账本 fork 内部 `.omc/upstream-sync-*.md`，未公开。

---

## 7. License

本 fork 沿用上游 **modified Apache License 2.0**，见 [`../LICENSE`](../LICENSE)。两条非平凡附加条件：

1. **不得转售为 hosted / embedded 服务**（除非拿到 Multica Inc. 的商用 license）。单组织内自用（含多 workspace）无需授权。
2. **`apps/web/` 前端必须保留 LOGO 与 copyright**（从源码运行时适用；从 Docker 跑时「web 镜像」同样适用）。

贡献者同意（per LICENSE）其贡献的代码可被用于商业用途，包括云业务。

---

## 8. 联系与反馈

- Issues：本仓库 [`jjy1000/multica-local`](https://github.com/jjy1000/multica-local/issues)
- 上游问题：[multica-ai/multica](https://github.com/multica-ai/multica/issues)
- 本 fork 不提供 Discord / X / 任何社交渠道——这是单用户工具的承诺，不是失能

---

> 最后更新：2026-09-20（fork HEAD = 0.5.109）。后续发版时本文件同步更新差异表与稳定性修复条目。
