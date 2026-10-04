# Multica Local

**[multica-ai/multica](https://github.com/multica-ai/multica) 的本地化单用户 fork。**

同样的人机协作平台 —— 人和 AI 队友共用一块任务板 —— 重建为面向 macOS 单用户使用，并在此基础上加了三间研究实验室、三张实时数据画布、以及一套密码学任务授权。遥测、OAuth、云运行时、自动更新全部移除。

**当前版本：`0.5.138`** · 一条命令出包，一个桌面应用，所有数据都在你本机。

<p align="center">
  <a href="./README.md"><b>🇬🇧 English</b></a> &nbsp;·&nbsp; <a href="./README.zh-CN.md">🇨🇳 中文</a>
</p>

---

[![License](https://img.shields.io/badge/License-Apache_2.0_with_additions-blue)](./LICENSE)
[![Platform](https://img.shields.io/badge/Platform-macOS%20arm64-lightgrey)](https://github.com/jjy1000/multica-local/releases)
[![Version](https://img.shields.io/badge/build-0.5.138-orange)](https://github.com/jjy1000/multica-local/releases/tag/v0.5.138)

---

## 这个 fork 为什么存在

上游 Multica 是个云产品：往 PostHog 发遥测、Google OAuth、云端运行时、自动更新、计费、`electron-updater` 流水线。对 SaaS 来说这形态是对的，对单用户桌面工具来说这形态是错的。

这个 fork 保留了平台本身（Go 后端、Electron 桌面、Next.js web、React Native mobile、共享包、Postgres + pgvector），砍掉了只为服务云业务而存在的部分。桌面应用成为主目标 —— 自包含、本地优先、用户名登录，端到端归你所有。

在这个底座上，本 fork 做了三件上游完全没有的东西：

1. **三间研究实验室**，让智能体获得代码执行之外的能力。
2. **三张实时数据画布**，把实验室真正在做的事渲染出来 —— 数据是真的，不编进度。
3. **密码学任务授权** —— 高危智能体操作的可签名、可验证授权凭证。

代码库是长期的上游移植：每个版本挑一批上游补丁，手工 diff 移植，在 macOS 出包。上游无法直接合并（`git merge-base HEAD upstream/main` 返回空），每次移植都带逐文件合理性闸门。

---

## 相对上游改了什么

| 方面 | 上游 Multica | 本 Fork |
| --- | --- | --- |
| **遥测** | PostHog 分析 | `analytics.NewFromEnv()` 恒返回 `NoopClient{}`；前端分析全部 no-op；`posthog.go` 已删 |
| **自动更新** | `electron-updater` 通道 | 依赖移除；CLI update 命令打桩；daemon 的 `autoUpdateLoop` 关闭 |
| **认证** | Google OAuth + 邮箱验证码 | 仅 `UsernameLogin`（`POST /auth/login {"name":"alice"}`）；`SendCode` / `VerifyCode` / `GoogleLogin` 一律 410 Gone |
| **云** | 云端运行时、计费、CloudFront、联系销售 | 全部云路径已删 |
| **支持入口 UI** | HelpLauncher、Discord、FeedbackModal | 已删 |
| **主目标** | 云 web + Electron 桌面 | macOS 桌面（Electron）—— 内置 Postgres、嵌套二进制签名、冷启动验证 |
| **登录模型** | 持久身份 + 工作区绑定 | 仅用户名；每次登录建新用户行（有意为之 —— 打错字丢权限，好过自动绑定到别人的工作区） |
| **研究实验室** | 只有 flag 目录 | **随包交付 4 间实验室**，其中 3 间默认开启 —— 见 [实验室](#实验室) |
| **签名授权** | 无 | **水印 Ed25519 签名**高危任务授权，附宪法注入与验证 CLI —— 见 [签名授权](#签名授权) |
| **上游同步** | — | 按版本手工 diff 移植，按领域分批，配深度调研 agent 与逐文件合理性闸门 |

---

## 实验室

实验室是**可选启用的 AI 能力面**，在标准任务板之上激活专门的研究与推演模式。入口只有一个 —— 侧边栏的 **Labs 标签页**。没有插件加载器、没有自动发现、没有 CLI 快捷方式。

### 本版本内的实验室

| 实验室 | 默认 | 派发方式 | 做什么 |
| --- | --- | --- | --- |
| **`claude_science_lab`** — 科研实验室 | ✅ 开 | 自动 | 六智能体研究循环，研究完成后自动触发一个自我审稿阶段（失败即软失败）。产物可溯源回原始 issue。 |
| **`pythia_oracle`** — 群智推演 | ✅ 开 | 手动 | 回环 Python 推演引擎。四个固定引擎人格展开审议；共识、投票分歧度、分裂票都是真实数据，逐轮记录。裁决在成真之前恒为 `pending`。 |
| **`causal_graph`** — 因果星图 | ✅ 开 | 手动 | 因果边提案器，四级信任阶梯（原生钩子 > 推演闭环 > 桥接 > 提案）。D 级提案恒以 `status='suggested'`、置信度 ≤ 0.5 落库，未经人工确认不可见。驳回是墓碑，永不硬删。 |
| **`llm_wiki_bridge`** | ⬜ 关 | 辅助 | 把本地 LLM Wiki 库暴露为智能体可查阅的技能。辅助型 —— 接人工指派，不抢占 assignee 槽位。 |

> **已退役的实验室** —— `mythos_swarm`、`timesfm`、`semantica` 在 0.5.122 移除。数据库表仍在（迁移只进不退）；已绑定这些实验室的 issue 渲染为惰性。**不要重新加回目录项** —— 挂在已删除键上的闸门会永远解析为 `false`，静默杀死实验室↔指派锁。

`user_*` 插件实验室仍然可用，可以挂你自己的 manifest（见下方*插件编写*）。

### 实时画布

三间主力实验室各自把真实状态渲染成动画画布 —— 不是样稿，不是转圈，**更不编造进度条**。所有动效统一走 `prefers-reduced-motion` 闸门，三张画布共同遵守一条纪律：**数据不存在就显示待命态，不拿假活动充数。**

| 画布 | 实验室 | 视觉隐喻 |
| --- | --- | --- |
| `ClaudeBrainCanvas` | `claude_science_lab` | 一颗科研大脑。六条智能体轨道，运行中的边拖出彗尾，核心温热发光，待机时随机 ping 脉冲会点亮一条非运行边 1.6 秒。 |
| `PythiaCouncilCanvas` | `pythia_oracle` | 一间推演议会。共识罗盘带分歧带与轮次徽章，四周是四个引擎人格席位；等待时内外两圈反向环持续转动。 |
| `CausalConstellationCanvas` | `causal_graph` | 一张星图。确定性播种的背景星空（种子取自 focus id，轮询永不重排星星），焦点星带辉光，决策光锥沿活跃边逐层涟漪扩散。 |

数据全部来自真实查询：智能体名册 + 任务快照、议会投票票据、真实工作区图。星图的边色直接骑既有的 `--causal-edge-*` 设计 token —— 组件零私有配色。

### 启用实验室

1. 打开 Multica → 侧边栏点 **Labs**。
2. 拨开关。（`claude_science_lab`、`pythia_oracle`、`causal_graph` 默认已开。）
3. **自动派发型**实验室：把任务派给绑定该实验室的 issue 即自动接手。
4. **手动型**实验室（`pythia_oracle`、`causal_graph`）必须显式点 **Run research** 或在面板里操作，不会从任务派发触发。
5. `multica lab delegate` 若把 `AutoDispatch=false` 的实验室当委派目标会快速失败 —— 这是设计，不是 bug。

### 插件编写（进阶）

实验室不是插件加载器 —— 没有动态模块加载。加一个 `user_*` 实验室：

```bash
# 1. 放 manifest
mkdir -p apps/desktop/resources/experiments/my_lab
#    → manifest.json 声明 leader、agents、capabilities

# 2. 注册进目录
#    server/internal/experimental/catalog.go —— 追加一条 user_ 前缀的 Flag
#    （若需要 DB 支撑的偏好，再加一条迁移）

# 3. 重新 bundle 后重启
pnpm --filter @multica/desktop bundle-cli
```

内置 flag 在键冲突时永远胜出。

---

## 签名授权

`0.5.137` / `0.5.138`。一套面向**高危任务授权**的密码学控制 —— 用 API 模型创建红蓝对抗 / 渗透测试类攻击智能体或技能，或执行其他危险任务行为。

**运作方式**：你用只存在本机的 Ed25519 密钥给一份授权资产加水印并签名。签好的授权以**宪法章节**形态注入智能体运行上下文（渲染上与内置宪法结构上不可区分，智能体无法判断它是低一档的规则），并走三条注入通道。任何人都可以事后按指纹验证：

```bash
multica signature verify <sha256-指纹>   # exit 0 = 有效
```

**安全姿态：**

- 私钥只存在 `~/.multica/signing/<assetID>.key` —— `0600`，不入库，不外发。**没有任何端点读取或传输私钥。**
- **默认关闭。** 工作区设置 `signature_authorization_enabled` 只认布尔 `true` —— 字符串 `"true"`、`1`、`null`、缺键一律视为关闭。
- **全部十个**签名面在服务端设闸（上传、列表、图片、退役、工作区历史、签署、issue 历史、撤销、按 id 验证、按指纹验证）。只在前端隐藏不算闸 —— 未启用时十个面一律返回 `403` 并附指引文案。
- **停用会暂停既有覆盖**：活跃签名行停止注入、停止验证。重新启用后无需重签即恢复，因为签名行与内容快照均未变。
- 开发中一条专门测试（`TestSignatureDisarmSuspendsExistingCoverage`）抓到了真实缺口 —— 一条内联的按指纹验证路径绕过了共用 helper。这道闸是测试保证的，不是靠约定维持的。

---

## 快速上手 —— macOS 桌面版

预编译桌面应用发布在 [Releases](https://github.com/jjy1000/multica-local/releases) 页面。

> ⚠️ 应用是**临时签名（adhoc）**，无 Apple Developer ID。首次启动请右键点应用 → **打开** → 确认。macOS 会记住这个例外，之后不再拦截。

1. 从 [Releases](https://github.com/jjy1000/multica-local/releases/tag/v0.5.138) 下载 `Multica-0.5.138-mac-arm64.zip`。
2. 解压得到 `Multica.app`，拖入 `/Applications`。
3. 启动 Multica。首次启动时内置 Postgres 会自动拉起。
4. 登录界面随便输个名字（例如 `alice`）回车 —— 进去了。

应用完全自包含：同一个 bundle 里带自己的 Postgres、自己的 `:8090` 后端、自己的 daemon 和渲染进程。没有外部服务、没有认证、没有遥测、不联外网。

**数据不在 app bundle 里**，每次重装都保留：

| 数据 | 路径 |
| --- | --- |
| PostgreSQL（**原生**内置，非 Docker） | `~/Library/Application Support/Multica/pgdata` |
| 配置 / token | `~/.multica/profiles/<name>/config.json` |
| 服务端环境变量 | `~/.multica/profiles/<name>/.env` |
| 工作区文件 | `~/multica_workspaces_<profile>/` |

迁移只进不退 —— 任何表和列都不会被删。要从源码构建，见 [`CLAUDE.md`](./CLAUDE.md) 与 [`CONTRIBUTING.md`](./CONTRIBUTING.md)。

---

## 架构

```
┌──────────────┐     ┌──────────────┐     ┌──────────────────┐
│   Next.js    │────>│  Go Backend  │────>│   PostgreSQL     │
│   Frontend   │<────│  (Chi + WS)  │<────│   (pgvector 17)  │
└──────────────┘     └──────┬───────┘     └──────────────────┘
                           │
                    ┌──────┴───────┐
                    │ Agent Daemon │  跑在你本机
                    └──────────────┘  (Claude Code、Codex、Copilot CLI、
                                       OpenCode、OpenClaw、Hermes、Gemini、
                                       Pi、Cursor Agent、Kimi、Kiro CLI、Qoder CLI)
```

| 层 | 技术栈 |
| --- | --- |
| 桌面 | Electron 39（主目标） |
| 前端 | Next.js 16（App Router）、React 19 |
| 后端 | Go 1.26（Chi 路由、sqlc、gorilla/websocket） |
| 数据库 | PostgreSQL 17 + pgvector（内置原生实例） |
| 移动端 | React Native / Expo |
| 智能体运行时 | Claude Code、Codex、GitHub Copilot CLI、OpenClaw、OpenCode、Hermes、Gemini、Pi、Cursor Agent、Kimi、Kiro CLI、Qoder CLI |

---

## 已知怪癖（这个 fork 的）

以下都是有意为之。改之前先读完。

- **每次登录都建新用户行。** 工作区成员关系绑定在创建者的 `user_id` 上，所以用户名打错会得到一个零工作区的新用户。这是有意的：自动绑定会让一次手滑把工作区所有权送人。
- **绑定 assignee 的实验室会抢占 assignee 槽位。** 归类为 `assignee` 的实验室在 `issue.lab_source` 翻转时会改写 `assignee_*`。辅助型实验室（`llm_wiki_bridge`、`causal_graph`）改为接受人工指派。
- **因果图的驳回是墓碑。** 硬删掉一条驳回记录，提案器当晚就会重新提出来 —— 因为它们是从实时状态重新推导的。
- **迁移只进不退。** 永不删表或删列。schema 变更一律是追加式的。
- **内置 Postgres 迁移曾经毁掉 69 张用户表。** `runMigrate` 在两层拒绝 `backend === "external"`。动桌面数据路径前先读 `apps/desktop/CLAUDE.md`。

---

## 开源协议

[Modified Apache 2.0（带商用限制）](LICENSE)

## 致谢

基于 [multica-ai/multica](https://github.com/multica-ai/multica) 构建。本 fork 与上游项目无隶属关系，亦未获其背书。
