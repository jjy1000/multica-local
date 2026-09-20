# Multica Local

**[multica-ai/multica](https://github.com/multica-ai/multica) 的本地化单用户 fork。**

同一个「人类 + AI agent 共用一张任务看板」的托管 agent 平台，砍掉遥测 / OAuth / 云运行时 / 自动更新，重新配置为 macOS 上单用户自包含桌面工具。

[![License](https://img.shields.io/badge/License-Apache_2.0_with_additions-blue)](./LICENSE)
[![Platform](https://img.shields.io/badge/Platform-macOS%20arm64-lightgrey)](https://github.com/jjy1000/multica-local/releases)

> 英文 README 见 [`README.md`](./README.md)。本文件是中文镜像。

---

## 为什么有这个 fork

上游 Multica 是个云 SaaS 产品：PostHog 遥测、Google OAuth、hosted runtime、auto-update、计费、electron-updater 通道。这套架构对 SaaS 是正确的，对一个单用户桌面工具是错误的。

本 fork **保留平台本身**（Go 后端、Electron 桌面、Next.js Web、React Native 移动、共享 packages、Postgres + pgvector），**删掉为云业务而存在的部分**。macOS 桌面 App 成为首要交付物——一个自包含、本地优先、username-only、完全由你掌控的工作区。

代码库是上游 Multica commit 的长期搬运：每次发版拉一批上游 patch 进来，应用到 fork，打包到 macOS。**与上游无法 merge**（`git merge-base HEAD upstream/main` 为空）——所有移植都是手动 diff，每文件 sanity gate。

详细对比文档见 [`docs/FORK_DIFF.zh.md`](./docs/FORK_DIFF.zh.md)。

---

## vs 上游改了什么

| 维度 | 上游 Multica | 本 fork |
|---|---|---|
| **遥测** | PostHog 全链路 + FE analytics | `analytics.NewFromEnv()` 恒返 `NoopClient{}`；FE analytics 全 no-op；`server/internal/analytics/posthog.go` 文件删除 |
| **自动更新** | `electron-updater` + 桌面 updater + daemon autoUpdateLoop | 依赖卸载；CLI update 命令 stubbed；daemon 不调 autoUpdateLoop；`electron-builder.yml` 无 `publish` 块 |
| **认证** | Google OAuth + 邮箱验证码 | 仅 `UsernameLogin`（`POST /auth/login {"name":"alice"}`）；`SendCode`/`VerifyCode`/`GoogleLogin` 返回 410 Gone |
| **云业务** | hosted runtime、计费、CloudFront、contact sales | 全部删除 |
| **外部支持 UI** | HelpLauncher、Discord、FeedbackModal | 删除 |
| **首要交付物** | 云 Web + Electron 桌面 | macOS 桌面（Electron）——自包含、打包 Postgres、嵌套二进制已签名 |
| **登录模型** | 持久身份 + workspace 绑定 | 仅 username；每次登录 upsert 新用户（有意为之——typo 自动绑定会让所有权拱手送人） |
| **Labs & 实验面** | 标准 | 完整保留 flag catalog；fork 的 `interaction_model` 加宽 + `auto-dispatch` opt-out 都铺在上面 |
| **上游同步** | n/a | 每次发版手动 diff 移植，按域分批，deep-dive research agent + per-file sanity gate |

平台本体**全功能对等**：同一套 Go 后端（handlers、migrations、WS push）、同一套 Electron renderer、同一套共享 packages、同一套 React Native 移动、同一套 Labs catalog。所有不为云业务存在的功能都还在。

---

## 快速开始 — macOS 桌面

预构建的 0.5.109 桌面 App 已发布在 [Releases](../../releases) 页面。

> ⚠ DMG 是 **adhoc 签名**（无 Apple Developer ID）。首次启动需要右键 App → **打开** → 确认。macOS 会记住这个例外，后续启动正常。

1. 从 Releases 下载 `Multica-0.5.109-arm64.dmg`
2. 打开 DMG，把 `Multica.app` 拖进 `/Applications`
3. 启动 Multica。首次启动时自带 Postgres 自动拉起
4. 登录页输入任意用户名（比如 `alice`），回车即入

桌面 App 全自包含：自带 Postgres、自带后端（:8090）、自带 daemon、自带 renderer，全在同一个 `.app` bundle 里。没有外部服务、没有认证、没有遥测。

从源码构建见 [`CONTRIBUTING.md`](./CONTRIBUTING.md)（共享 Postgres，每 checkout 一个 DB，worktree 隔离开发模型）。

---

## 架构（60 秒理解）

```
  Renderer（Electron 桌面，macOS 首要交付物）
        │   HTTP + WebSocket
        ▼
  server/internal/handler  ──▶  service/*  ──▶  sqlc  ──▶  Postgres + pgvector
        ▲                                                       │
        │                       WS push                          │
        └───────────────────────────────────────────────────────┘

  Local Daemon（apps/desktop daemon-manager.ts）
        │ spawns
        ▼
  Claude Code / Codex / copilot / openclaw / ...
```

分配任务的完整生命周期：`PATCH issue.assignee_*` → 服务端 `assignDefaultLabAgent`（如果绑了 lab）→ daemon claim `agent_task_queue` → daemon `LoadAgentSkillsForClaim` 注入 builtin + workspace skills → 子进程 spawn agent CLI → 进度通过 WebSocket → renderer 改 React Query cache。

Labs 加了一条平行路径：一个 issue 可以带 `lab_source`（比如 `pythia_oracle` / `mythos_swarm` / `claude_science_lab`）。`interaction_model: assignee` 的 lab 接管 assignee 槽；auxiliary lab（`llm_wiki_bridge` / `causal_graph`）接受手动 assignee。Flag toggle 是唯一入口——没有 plugin loader、没有动态模块面。

---

## 技术栈

| 层 | 选择 |
|---|---|
| 后端 | Go 1.26.1、Chi 路由、sqlc、gorilla/websocket、pgvector |
| 桌面 | Electron 39、Vite、React 19.2.3、TypeScript 5.9.x |
| Web | Next.js（App Router） |
| 移动 | Expo / React Native |
| 数据库 | PostgreSQL 17 + pgvector |
| 包管理 | pnpm 10.28.2（共享依赖用 catalog 协议） |
| 构建 | electron-builder `--mac --dir`（DMG 创建在本环境是坏的，见已知 quirks） |

---

## 已知 quirks（本 fork）

下面是 fork 与上游的负载型差异，任何要跑代码的人都得知道。这也是为什么公开 DMG 是 adhoc 签名的原因。

- **DMG 创建是坏的**：`electron-builder --mac` 在 `create-dmg` 1.2.3 上 hang。每次发版走 `--dir`（原始 `.app` bundle）。想要 DMG 用 `hdiutil create` 直接打。
- **嵌套二进制必须重新签名**：`electron-builder --mac --dir` 只签顶层 `.app`。`app.asar.unpacked/resources/bin/{multica,server,migrate}` 三个二进制首次启动会被 macOS Gatekeeper SIGKILL。每次安装后跑 `bash scripts/desktop-sign-nested-binaries.sh /Applications/Multica.app`。
- **`/health` 不探库**：hang 住的 Postgres 在写操作前看不见。Daemon 的 PAT 验证需要 DB，所以死的 Postgres 表现为 401 风暴、看起来像 auth bug。诊断顺序：`lsof -nP -iTCP:5432 -sTCP:LISTEN` 先看，再谈鉴权。
- **Username-only 登录每次登录 upsert 新用户**：typo 直接得到一张新 workspace。这是**有意设计**——自动绑定会把所有权拱手送人。跨重启改名字等于重来。
- **`apps/desktop/dist/mac-arm64/Multica.app` 在 .gitignore 内**：从 `apps/desktop/` 跑 `pnpm --filter @multica/desktop build && pnpm exec electron-builder --mac --dir` 构建。**不要从仓库根目录跑**——builder 会 walk `.claude/worktrees/` 静默卡死。

---

## License

Modified Apache License 2.0 —— 见 [`LICENSE`](./LICENSE)。两条非平凡附加条件：

1. **不得转售为 hosted / embedded 服务**（除非拿到 Multica Inc. 的商用 license）。单组织内自用（含多 workspace）无需授权。
2. **`apps/web/` 前端必须保留 LOGO 与 copyright**（从源码运行时适用；从 Docker 跑时「web 镜像」同样适用）。

贡献者同意（per LICENSE）其贡献的代码可被用于商业用途，包括云业务。

---

## 致谢

本 fork 基于 [multica-ai/multica](https://github.com/multica-ai/multica) ——平台、设计、大部分代码都来自上游，请给上游点 star。桌面打包修复、lab-class 契约、PG-zombie 防护、上游同步工具链是 fork 独有贡献。

更详细的中文对比 + 取舍理由 + 稳定性修复记录见 [`docs/FORK_DIFF.zh.md`](./docs/FORK_DIFF.zh.md)。

---

🇬🇧 English readers: see [`README.md`](./README.md) for the English version of this overview.
