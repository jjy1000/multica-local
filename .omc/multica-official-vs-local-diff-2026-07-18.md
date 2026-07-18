# Multica 官方 vs 本地 fork 功能差异对比

**对比日期**: 2026-07-18
**官方**: `/Users/jiangjianyan/Downloads/multica-main 2`(version 字段误标 `0.1.0`,实际为 upstream `multica-ai/multica` 主干快照,无 `.git`,migration 跑到 **201**)
**本地**: `/Users/jiangjianyan/jjy/multica-main`(`@multica/desktop` v`0.3.43`,分支 `feat/0.3.29-integration`,migration 跑到 **157**,稀疏 git 检出)

> 注意:`apps/desktop/package.json` 里的 `version` 字段不可信 — 上游快照里写 `0.1.0`,但目录结构和 migration 编号都说明它已是 upstream 主干的近期状态。本地 `0.3.43` 是沿用 fork 的本地版本号,并非 upstream 同步版本。

---

## 1. 量化总览

| 维度 | 数量 |
| --- | --- |
| 文件总差异 | **1839** |
| 仅官方有 | **632** |
| 仅本地有 | **209** |
| 内容不同 | **998** |

**目录结构差异**(仅列出独有顶层目录):

| 官方独有 | 本地独有 |
| --- | --- |
| `server/internal/{cloudruntime,runtimeapps,attribution,attributionbackfill,dispatch,featureflags,selfexec}` | `server/internal/{experimental,featureflagdispatch,llmwiki}` |
| `server/internal/analytics/posthog.go` | `apps/desktop/src/main/{experimental/,util/,pg-bootstrap.ts,server-manager.ts,pythia-manager.ts,experimental-safety.ts}` |
| `packages/core/{agents,analytics,chat,dashboard,feature-flags,feedback,i18n,inbox,issues,notification-preferences,projects,runtimes,workspace,composio}` | `apps/desktop/src/renderer/src/components/{experimental-artifact-view,pythia/,pg-download-progress,migration-dialog,server-status-banner,pageview-tracker}` |
| `apps/web/features/landing/`, `apps/web/lib/public-host.ts` | `apps/desktop/resources/{bin,claude-science,code-canvas,experiments,openscience-prompts,pg,pythia,server}` |
| `packages/views/{agents,labels,editor/{extensions,hooks,utils},runtimes,projects,common/task-transcript,inbox/components,layout/{discord,help-launcher,join-discord-card,collection-page}}` | `packages/views/issues/components/pickers/{lab-picker,assignee-picker}` |
| `e2e/`, `docs/`, `deploy/`(独立目录,官方带更多文件) | `packages/views/locales/{en,ja,ko,zh-Hans}/{claude-lab,mythos,pythia,server-status}.json` |
| `apps/mobile/lib/markdown/` | `apps/desktop/scripts/{build-claude-science-manifest.mjs,setup-claude-science.sh}` |

---

## 2. 上游(官方)新增的功能 — 本地未集成

### 2.1 商业 / 云服务
- **Cloud 订阅与计费** — `server/internal/cloudruntime/`、`handler/cloud_billing.go`、`handler/cloud_runtime.go`、`handler/contact_sales.go`
- **CloudFront CDN 签名** — `auth/cloudfront.go`(本地 CLAUDE.md 明确"已删除 cloud CDN",**不要回引**)
- **Cloud PAT(个人访问令牌)** — `auth/cloud_pat.go`
- **Workspace 邀请** — `handler/invitation.go`
- **Workspace 成员管理** — `handler/daemon_workspace.go`

### 2.2 Agent / Runtime 扩展
- **Agent Builder(可视化构建 agent)** — `handler/agent_builder.go`
- **Agent permission 系统** — `handler/agent_permission.go`、`handler/agent_template_permission_test.go`
- **Claude plugins(agent 可挂载 plugin)** — `daemon/claude_plugins.go`
- **OpenClaw runtime config** — `daemon/openclaw_runtime_config.go`(自有 daemon runtime 配置)
- **Attribution / attribution backfill** — `server/internal/attribution/`、`server/internal/attributionbackfill/`、`handler/attribution_response_test.go`
- **Self-exec runtime** — `server/internal/selfexec/`
- **Dispatch 系统** — `server/internal/dispatch/`
- **Runtime MCP 集成** — `daemon/runtime_mcp*.go`(测试名)

### 2.3 第三方集成
- **Slack 集成** — `handler/slack.go`、`apps/web/app/slack/`
- **Composio 集成** — `packages/core/composio/`、`handler/integrations_composio.go`
- **OAuth 回调页(Google 等)** — `apps/web/app/auth/callback/`

### 2.4 前端 UX / 视图
- **Discord 入口 + Help 启动器 + Feedback modal** — `packages/views/layout/{discord,help-launcher,join-discord-card}.tsx`、`packages/views/modals/feedback.tsx`
- **全局快捷键** — `packages/views/layout/global-shortcuts.tsx`
- **侧边栏可拖拽 resize** — `packages/views/layout/sidebar-resize.test.tsx`
- **Collection page(集合视图)** — `packages/views/layout/collection-page.tsx`
- **Property 系统(自定义属性)** — `handler/property.go`、`packages/views/issues/components/pickers/custom-property-picker.tsx`、`packages/views/labels/resource-label-picker.tsx`
- **Project 日期选择器(due / start)** — `packages/views/projects/components/project-{due,start}-date-picker.tsx`
- **Agent Inspector + tabs** — `packages/views/agents/components/{inspector,tabs}/`
- **Chat pinning / pinned agent** — `handler/chat_pinned_agent.go`
- **Chat title 自动生成** — `handler/chat_title.go`
- **Chat history 回放** — `handler/chat_history.go`
- **Chat 草稿恢复(防 race)** — `handler/chat_draft_restore_race_test.go`
- **Task transcript 视图** — `packages/views/common/task-transcript/`
- **Search 端点** — `handler/search.go`
- **Editor 扩展** — `packages/views/editor/extensions/{code,issue-identifier-autolink}.ts`
- **Inbox 组件** — `packages/views/inbox/components/`
- **Runtime 视图** — `packages/views/runtimes/components/{runtime-docs,runtime-settings-page,rename-machine-dialog,machine-cli-section}.tsx`
- **Notification preference 系统** — `packages/core/notification-preferences/`、`handler/notification_preference_test.go`
- **Analytics(PostHog)** — `server/internal/analytics/posthog.go`、`packages/core/analytics/`(本地 CLAUDE.md 明确"已删除,永远不要回加")

### 2.5 底层 / 工具链
- **`webhook_delivery_worker.go`** — 异步 webhook 投递
- **`mcp_overlay.go`** — MCP overlay 服务
- **OpenAI Go SDK** — `github.com/openai/openai-go/v3 v3.41.1`(新增依赖)
- **goldmark(markdown 解析)**、`go-redis/redismock`、`resty`、`lumberjack(滚动日志)`、`tidwall/gjson`、`tidwall/sjson` — 新增 Go 依赖
- **`electron-updater`** — 桌面自动更新(本地 CLAUDE.md 明确"已删")
- **Migration 158 → 201 共 ~44 条** — 集中在 attribution、inbox 索引、agent task、runtime redis 等
- **PostHog 客户端 + JSON 落表** — `handler/featureflag.go` 重新出现,local 已删

---

## 3. 本地 fork 独有的功能 — 上游没有

### 3.1 Labs 实验平台(核心增量)
**这是一个完整的"实验功能子系统",上游完全没有对应代码。**

#### 后端(`server/internal/`)
- **`experimental/` 包** — Catalog + Registry + Lifecycle Marker + Manifest 驱动
- **`featureflagdispatch/`** — 实验 flag 调度分发
- **`llmwiki/`** — LLM Wiki bridge 子系统
- **25 个 handler 独占文件** 全部围绕 Labs:
  - `experimental_flags.go` / `experimental_proxy.go` / `experimental_resources.go` / `experimental_guard.go` / `experimental_mythos_run.go` — 实验平台基础设施
  - `install_{claude_science,claude_science_runtime,constitution_agent,mythos,agent_self_opt}.go` — 5 个 lab 的安装器
  - `claude_lab_{forecast,issues}.go` — Claude Lab 端点
  - `claude_science_skills.go` / `claude_science_runtime.go` — Claude Science 端点
  - `forecast_issue.go` — Pythia 预测
  - `issue_lab_dispatch.go` / `issue_lab_source.go` — Issue → Lab 派发
  - `lab.go` — Lab 通用接口
  - `labs_visibility_filter.go` — 默认隐藏实验资源
  - `llm_wiki_bridge.go` — LLM Wiki 桥接
  - `mythos_supervise.go` — Mythos swarm 监督
  - `runtime_llm_call.go` — Pythia runtime LLM 调用

#### 桌面主进程(`apps/desktop/src/main/`)
- **`pg-bootstrap.ts`** — 自包含 PostgreSQL 17.4 引导(下载/校验/initdb/pg_ctl/migration flow)
- **`server-manager.ts`** — 自启动 Go 后端(从 `resources/bin/server` spawn)
- **`pythia-manager.ts`** — Pythia Python 子进程管理(loopback JWT 鉴权、60/min 限流、25+ endpoint 白名单)
- **`experimental/` 目录** — IPC 通道命名空间 + ManagerFactory + Lifecycle
- **`experimental-safety.ts`** — panic/5xx burst/init timeout → 黑名单机制

#### 桌面渲染层
- **`experimental-artifact-view.tsx`** — artifact 查看器
- **`pythia/` 目录** — Pythia 视图组件
- **`pg-download-progress.tsx`** + **`server-status-banner.tsx`** — 启动期 UX
- **`migration-dialog.tsx`** + **`pageview-tracker.tsx`** — 迁移 / 埋点

#### 桌面资源(共 ~150-200 MB)
- `apps/desktop/resources/bin/` — 自带的 `server` / `multica` / `migrate` 三个 Go 二进制
- `apps/desktop/resources/pg/` — PostgreSQL 17.4 二进制树
- `apps/desktop/resources/server/migrations/` — 内嵌的 schema migrations
- `apps/desktop/resources/pythia/` — Pythia Python 引擎(FastAPI/Uvicorn)
- `apps/desktop/resources/claude-science/` + `apps/desktop/resources/openscience-prompts/` — Claude Science manifest + prompt 集
- `apps/desktop/resources/code-canvas/` — Code Canvas lab 运行时
- `apps/desktop/resources/experiments/<flag>/manifest.json` × 8 — 8 个 flag 的 manifest

#### 视图层(`packages/views/`)
- **`issues/components/pickers/lab-picker.tsx`** — 选 lab 的下拉(mythos 双模式 sole/enhancer)
- **`issues/components/pickers/assignee-picker.tsx`** — 支持 `lockedReason`,与 lab mutex 联动
- **`locales/{en,ja,ko,zh-Hans}/{claude-lab,mythos,pythia,server-status}.json`** — 4 语言 × 4 namespace

#### 内置 Skills
- 8 个内置 lab skill(基于 `multica-claude-science` 等 SKILL.md)随 desktop 打包

### 3.2 视图层细节增量
- **`use-issue-actions.test.tsx`** 改造 — Labs 派发联动
- **`issue-detail.tsx`** 改造 — 支持 `renderLabInline`(ClaudeLab/Pythia/Mythos/LLMWiki 4 种 inline 视图)
- **`issue-labs-section.tsx`** — Issue 详情侧栏的 Lab section
- **`MythosEnhancerSupervisePanel`** — enhancer 模式下显示 supervisor 状态
- **`labs_visibility_filter.go`** — 自动从普通 picker 隐藏 mythos 5 agents + swarm squad

### 3.3 删除的上游功能(主动剥离,见 `CLAUDE.md`)
| 删除项 | 证据 |
| --- | --- |
| PostHog 遥测 | `analytics.NewFromEnv()` 返回 `NoopClient{}`,posthog.go 已删 |
| CloudFront CDN / Cloud PAT / Cloud billing / Cloud runtime | `cloudruntime/` `cloud_pat.go` `cloud_billing.go` `cloudfront.go` 均不在本地 |
| Google OAuth / 邮箱验证 | `auth/SendCode.go` `VerifyCode.go` `GoogleLogin.go` 均返回 410 Gone(本地保留 UsernameLogin) |
| 自动更新 | `updater.ts` 是 no-op stub,`electron-updater` 依赖移除 |
| Discord / Help / Feedback modal | `layout/{discord,help-launcher,join-discord-card}.tsx` 不存在本地 |
| Contact sales | `contact_sales.go` 不存在本地 |
| Workspace 邀请 / 成员管理 | `invitation.go` `daemon_workspace.go` 不存在本地 |
| Slack 集成 | `slack.go` 不存在本地 |
| Composio 集成 | `integrations_composio.go` `core/composio/` 不存在本地 |
| Agent Builder / Permission | `agent_builder.go` `agent_permission.go` 不存在本地 |
| Search 端点 | `handler/search.go` 不存在本地 |

---

## 4. 本地相对官方的新增"Bug 修复 / 稳定性" 增量(CLAUDE.md 记录)

按项目 0.3.0 → 0.3.43 期间累积的事故修复(本地 fork 主动合并了,但上游 0.1.0 快照里没有对应代码):

| 事故 | 时间 | 修复位置 | 上游状态 |
| --- | --- | --- | --- |
| **PG 二进制树 ship 时被清空** | 2026-07-14 | `pg-bootstrap.ts:892` `fetch(url, {signal: AbortSignal.timeout(60_000)})` + `cache/<version>.dmg.sha256` 命中路径 | 上游 0.1.0 快照的 `pg-bootstrap` 是更老版本,无此保护 |
| **launchd daemon watchdog `set -u` 引用未声明变量** | 2026-07-14 | watchdog.sh:68 `START_TIME="${START_TIME:-$(date +%s)}"` | 上游没有这条脚本 |
| **i18next block-body selector crash → 整个 window 白屏** | 2026-07-14 | `use-t.ts` 注释 + ESLint `no-restricted-syntax` + `AppSidebar` ErrorBoundary | 上游没有这条 contract |
| **BrowserWindow 窗口跑到屏外** | - | `apps/desktop/src/main/index.ts::ensureWindowOnscreen()` | 上游没有 |
| **Username-only login 跨重启丢 workspace** | 2026-06-27 | 显式拒绝自动绑定 | 上游没有 |
| **`runMigrate` 外部 backend 误调用→ 销毁 69 张表** | 2026-07-02 | `runMigrate` P0 守卫,`backend !== "external"` 才允许 | 上游没有 |
| **Daemon 不自动启动** | 2026-02-97 | `tryAutoStartFromMain()` + `maybeRecoverDaemon()` + launchd watchdog + `&!` spawn | 上游没有 |
| **Pythia `already started` / Claude Lab `Failed to fetch`** | 2026-07-16 | pythia_oracle/llm_wiki_bridge ensureUp idempotent guard | 上游没有 Pythia 集成 |
| **Mythos swarm 单模式** | 2026-07-16 | 改为 sole + enhancer 双模式,migration 157 | 上游没有 Mythos 集成 |
| **Pythia 走 Ollama 而非 Multica 自身 provider** | 2026-07-14 | `oracle.py` + `osiris_intake.py` + `pythia-manager.ts` 注入 `MULTICA_AGENT_RUNTIME_URL`/`MULTICA_API_TOKEN` | 上游没有 Pythia |

---

## 5. 上游的新"Bug 修复" 但本地未集成

通过 `server/internal/handler/` 独有的 28 个测试文件反推(都是 upstream 在 migration 158→201 期间累积的修复):

- `admission_security_mul4525_test.go` — admission 安全加固(MUL-4525)
- `daemon_batch_claim_finalize_test.go` / `daemon_batch_claim_test.go` / `daemon_claim_channel_type_test.go` — 批量 claim 最终化
- `daemon_comment_delivery_test.go` / `daemon_comment_workspace_scope_test.go` — 评论投递 scope 校验
- `issue_create_labels_test.go` / `issue_reassign_no_cancel_test.go` / `issue_cancel_status_no_cancel_test.go` — 状态机 bug 修复
- `issue_agent_create_e2e_test.go` / `issue_agent_create_origin_test.go` — agent 创建端到端
- `autopilot_attribution_transfer_test.go` / `autopilot_mention_authority_test.go` / `autopilot_permissions_test.go` / `autopilot_cron_preview_test.go` — autopilot 授权链
- `chat_pending_tasks_test.go` / `chat_attachment_reply_test.go` — chat 任务/附件
- `comment_content_sanitize_test.go` / `comment_decision_test.go` / `comment_merge_failclosed_test.go` / `comment_reconcile_test.go` / `comment_reply_authz_test.go` / `comment_trigger_outcomes_test.go` — 评论 fail-closed + 授权
- `runtime_update_authorization_test.go` / `runtime_custom_name_test.go` / `runtime_redis_keys_test.go` — runtime 鉴权
- `task_terminal_wakeup_test.go` / `rollup_guard_test.go` — 终端唤醒 / 滚动守护
- `mcp_overlay_test.go` — MCP overlay 端到端
- `migration_127_task_squad_id_test.go`(本地独有) — task squad id 迁移
- `daemon_legacy_fallback_test.go`(上游独有) — daemon legacy fallback

这些测试反映上游修复过:**attribution 严格约束**(mig 198/199)、**inbox archived/active 索引**(mig 200/201)、**多 daemon RPC**、**agent_task 严格约束校验**。

---

## 6. 结论 / 一句话总结

**上游主线** 在 0.1.0 → 0.201 migration 这段时间里集中做的是:
1. **商业化层**(Cloud 订阅 / CloudFront / Cloud PAT / Contact Sales / Invitations)
2. **第三方集成**(Slack / Composio / Google OAuth / Discord 社区 UI / Help)
3. **Agent 体系扩展**(Agent Builder / Permission / Claude plugins / OpenClaw runtime / Attribution)
4. **Property 系统**(自定义属性 / Resource labels)
5. **运行时底层加固**(Redis mock / OpenAI SDK / lumberjack 日志 / MCP overlay)

**本地 fork (0.3.43)** 在 0.3.0 → 0.3.43 这段时间里集中做的是:
1. **Labs 实验平台** —— 完全独立的 8-flag 实验子系统(Pythia 预测 / Claude Science / Mythos Swarm / LLM Wiki Bridge / Code Canvas / Agent Self-Opt / Constitution Agent / Chat Pin)
2. **自包含桌面 backend** —— bundled PostgreSQL 17.4 + 自启动 Go server + Pythia Python 子进程
3. **稳定化加固** —— 围绕 incident 后的 P0/P1 修复(PG 二进制守护 / Daemon watchdog / i18next selector / window bounds / 迁移安全)
4. **激进的去商业化剥离** —— PostHog / CloudFront / Cloud / OAuth / Discord / 自动更新 / Slack 全部删除

**两者**之间**没有任何重叠的功能代码**(本地没有 Cloud / Slack / Composio,上游没有 Labs / PG-bootstrap / 自启动 backend),属于**两条完全不同的产品路线**:
- 上游 = 多租户 SaaS,目标是把 Multica 卖给企业
- 本地 = 单用户本地 AI 工作台,目标是把实验 AI 能力塞进一个 `.app`

---

## 7. 引用证据

- `apps/desktop/package.json` — 版本号
- `server/migrations/` 编号上限 — `201` vs `157`
- `diff -rq` 输出 → `/tmp/multica-diff-list.txt`(1839 行)
- `server/internal/handler/` 独有文件 — `comm -23 /tmp/official-handler.txt /tmp/local-handler.txt`
- `server/go.mod` 新依赖 — `diff server/go.mod`
- 本地 `CLAUDE.md` 第 1-7 节(去商业化 + 自包含 backend 描述)
- 本地 `memory/*.md`(事故时间线)