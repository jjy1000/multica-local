# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

> **TL;DR**: **localized single-user fork** of Multica (no telemetry, no OAuth, no cloud, username-only login — see **Localized Fork** below). Memory: `~/.claude/projects/-Users-jiangjianyan-jjy-multica-exploration-dev/memory/`. Backup: `.omc/backups/<date>/<release>-ship/` (auto per `ship-mac`). Single-command ship: `bash scripts/ship-mac.sh --yes`. Sub-domain guides table below; cross-cutting product/ship/desktop rules in this file. First-time readers: start with the fork notice in [`README.md`](README.md) ("This checkout is a localized single-user fork of multica-ai/multica ... governed by CLAUDE.md"), then re-read this file's **Localized Fork** section before any product decision.
>
> **Current release: 0.5.131** (2026-10-02; **实验室三系统完善批——修复 + 参考源移植**). 两批改动,零迁移。批 1 修复:pythia 监控页健康条轮询引擎从未定义的 `/status` 路由三个版本(改走 `/links` + allowlist 清理 + preload path 字面量联合两处同步),`oracle.health()` 桌面契约下恒 False 改为探测桥可达(vendor+resources 镜像双写),`PythiaTrajectory` 轨迹图接线(0.5.111 写好从未接线)+内嵌空动画槽修复,停止钮文案/续推表单运行中禁用,SSE 断流 1s→15s 指数退避重连(404/410 弃跟),`run.rounds` 语义=计划总轮数(创建写 planned,进度写不再压成落地数——重载后待定轮占位可见,新 Go 钉 TestPythiaForecastPlannedRoundsPersistMidRun),start 后闪屏修复+历史时间戳本地化;claude lab 的 Run research invalidation 键错写修复(原键无人持有,刷新承诺从未生效)+快照/产物失败错误条浮出+共享产物视图硬编码中文全 i18n 化(runtime_* 键族)+tLab 强转清除;causal 聚焦模式建议队列 wsId 改路由直传(原从 nodes[0] 推导,无节点 issue 上静默失效)+工作区图边过滤条(端点 type/status/min_confidence 参数 0.5.83 起无客户端发送,i18n 键都埋好了)+空态/脚注/by{agent} i18n。批 2 参考源移植(**三实验室代码全 fork 自研,参考源=open-science/MiroFish/semantica 只提供模式**):**因果路径追溯**(semantica pathPulse/路径智能卡:useCausalGraphPath 首次接线,画布顺序点亮+流动虚线+链外 0.15 调暗+起终点环,两击选点+距离带/最弱环节置信度红黄绿条/全图度数瓶颈节点,纯函数 causal-path-summary 带测试)、**pythia 全员问卷**(MiroFish 批量访谈:一问并行扇出 Oracle+4 persona,复用 /chat 零后端,单人设失败只红该卡)、**council 动作卡**(相对 oracle 基线 ±5pp 立场徽章,整数化比较防浮点漏判)、**run 状态动画条**(open-science session-card:运行 shimmer/排队琥珀脉冲/完成祖母绿+对勾弹入,媒体查询门控)、**会话终端输出视图**(红绿灯头+暗色 mono,stderr 红系)、pythia-pulse-ring 死动画接线监控页 live 心跳点。门禁:`pnpm typecheck` 6/6 + lint 8/8;views 1940 + core 969 + desktop 381 vitest 绿(33 skipped 在案);go ./internal/...+pkg/agent 全绿。详见 `.omc/release-notes-0.5.131.md`。
>
> **Prior: 0.5.130** (2026-09-30; 上游 v0.6.0 时代移植批——12 落地含任务历史分页 keyset+mig293、图片全尺寸堆叠;已 SHIPPED & INSTALLED). 详见 `.omc/release-notes-0.5.130.md` 与账本 `.omc/upstream-sync-2026-09-30.md`。
>
> **Prior: 0.5.128** (2026-09-29; **设默认即一键迁移 + 运行时删除 tombstone 化——误删重加不再丢智能体/用量**). 两批改动,零迁移。①**设默认即迁移**:运行时行菜单「设为默认并迁移智能体」一键完成——写 workspace.settings.default_runtime_id 后自动 bulk-move 旧默认上的全部智能体(含归档)到新默认,反向点击即切回;首次设默认只设不迁;迁移失败默认仍生效并 toast 警告;其他运行时上的智能体不动,任意对迁移仍走「迁移智能体到其他运行时…」菜单。②**删除 tombstone 化**:DeleteAgentRuntime / ArchiveAgentsAndDeleteRuntime 不再硬删归档 agents + 行(旧行为=「误删 Opencode 后重加回来智能体消失」的字面根源),改为 metadata.deleted_at 标记——列表查询过滤 tombstone(agent_runtime.metadata 按既有 UNIQUE(workspace,daemon,provider) 仲裁器,daemon 重注册**复用同一行 id**),agent 绑定/runtime 用量历史(task_usage_hourly 等)从未断开;DaemonRegister 内置分支 upsert 前快照 tombstone 的 deleted_archived_agent_ids(upsert 的 metadata 覆盖正是清标记动作),upsert 后恢复这批 agents 并广播 agent:restored——删除时归档的智能体在重加回来时自动复活,删除前就归档的不受影响;删除的是默认运行时时 FE 清 default_runtime_id 键(tombstone 行保留给复活,不应静默续任默认)。自定义 profile 运行时的级联(runtime_profile.go)保持硬删语义未动。门禁:go 40 包绿(internal/.../pkg/agent/cmd/multica;旧钉 TestArchiveAgentsAndDeleteRuntime_HappyPath/RemovesArchivedSquads/NoSquadsRegression 把硬删当基线钉着,反向重写为 tombstone 契约),cmd/server 仍 2 条在案 baseline 红;`pnpm typecheck` 6/6;core 965 + views 1907 vitest 绿(33 skipped 在案)。详见 `.omc/release-notes-0.5.128.md`。
>
> **Prior: 0.5.127** (2026-09-29; **默认 CLI 可切换 + 存量智能体批量迁移 + 实验室委托发现断链修复**). 三批改动,零迁移。①**实验室委托断链**(agent 委托 claude_science_lab 不可见的根因):委托简报的 enabled 集合来自 pref-only 投影(`ListEnabledFlagKeys`),DefaultVal=true 且用户从未拨过开关的实验室(0.5.114 起 claude_science_lab/pythia_oracle 默认开)无 pref 行 → 简报在每台默认安装上恒空;新增 `experimental.EffectiveEnabledKeys`(pickEnabled 合并语义:pref 行覆盖默认值,缺行回落默认值)+ `ListAllExperimentalPrefs`;`issue create/update` 新增 `--lab-source`(快速失败闸对齐 `lab delegate`,空串走 null 解绑);`multica-labs` 目录技能反转(从"⚠️需用户绑定/CLI 已废弃"改为 ✅ `multica lab delegate --parent <id> claude_science_lab "<task>"`),`multica-claude-science` Step 1 去 curl 化,`multica-labs` 纳入 skill_cli_contract;旧 DB 钉把 bug 当基线钉着,`TestDelegationBriefListsEnabledAssigneeLabs` 反向重写为"无 pref 行也必须广告 claude_science_lab"。②**默认 CLI**:workspace.settings.default_runtime_id(append-only,纯前端),运行时页行菜单设默认/取消+⭐徽章,新 agent 的 RuntimePicker 种子优先默认(可用性过滤),只影响新建。③**存量迁移**:POST `/api/agents/bulk-move-runtime`(owner/admin,from→to 整批搬含归档,事务内逐 agent 与单更新 runtime-switch 同语义——已知不兼容 model 清空(MUL-3341)/字面非法 thinking 清空(批量放宽为清而非 400),opencode 未知模型族保留),运行时行菜单「迁移智能体到其他运行时…」,切回=反向迁移。门禁:go 40 包绿(internal/.../pkg/agent/cmd/multica),cmd/server 仍 2 条在案 baseline 红;`pnpm typecheck` 6/6;core 965 + views 1905 vitest 绿(33 skipped 在案)。详见 `.omc/release-notes-0.5.127.md`。
>
> **Prior: 0.5.126** (2026-09-29; **审计事实修正批 — 冷启动门禁的数据对账首次真正生效**). repo 内 5 文件全是 docs/注释级(零行为变更/零迁移/零 wire):三份指南修掉三处本机跑不通的命令(`.env` 在 repo root 的 silent-skip 陷阱 / native PG 数据完整性验证 / daemon 日志分叉入档),catalog.go 两处 stale 注释修正(claude_science_lab 0.5.81 已翻回,opt-out 现行集=pythia_oracle+causal_graph)。repo 外修复 `~/.multica/scripts/verify-desktop-cold-start.sh` row parity:原 SQL 恒语法错误+`|| echo` 吞错,ship 数据对账自诞生起空转;现标量子查询+失败 exit 4 硬门禁+端口 5432→5433 自动探测,双场景实测。详见 `.omc/release-notes-0.5.126.md`。
>
> **Prior: 0.5.125** (2026-09-28; **实验室可被 agent 调用 — pythia skill 认知修复 + 实验室目录页 + mention 假 id 回归**). 3 commits:`b7e22e427..216e1bdbe`,零迁移零 wire 改动。一个 issue 要求"委托群体推演做假想推定",squad leader 答"工作区没有该插件"并用 4 个角色自行角色扮演顶替 —— 能力其实一直都在且**不需要绑实验室** (`multica pythia issue-forecast --issue <id> --wait` → 报告自动落为 issue 评论)。真因在 agent 可见的文本, 见下方 **Builtin skill 认知契约**;附带修 `bareMentionRe` 无尾锚导致 `mention://member/Alice` 解析出假 id `A` 并照常派工。
>
> **Prior: 0.5.124** (2026-09-28; **代理协作断流三连修 — mention 派工静默丢失 / blocked 无人接管 / 实验室交付链三修**). 6 commits:`e922b8b0e..d48ee3044`,零迁移。(1) **mention 派工静默丢失**(JYF-490):`util.ParseMentions` 只认 `[@Label](mention://type/id)` 规范形状,squad 主管派工评论写出 `**@名** ([mention://agent/id])` 变体全部漏配 → 零任务派发、领队空等永不到来的 received;新增裸 URL 容错扫描 `bareMentionRe`(`all` 分支必须排在 hex 类之前——该模式无尾锚,否则 `all/all` 的 a 被 hex 类吃掉解析出假 id),按 type:id 去重合并,规范形式保持权威;`TestParseMentionsBareURL` 钉事故变体/散文裸 URL/去重/@all/扫描序。(2) **blocked 无人接管**(JYF-497):worker 把 issue 置 blocked 是交还信号,但状态变更路径零触发、报告评论无 mention → 流程死等;新 `notifyParentOfChildBlocked` 接入 UpdateIssue + BatchUpdateIssues(父 issue 系统评论 + `dispatchParentAssigneeTrigger` 唤醒,无 stage barrier,cancelled/backlog/member 父闸镜像 done 路径,自唤醒按 MUL-2808 允许、pending 去重兜底);5 钉测试族(agent/squad 唤醒 + 三抑制器)。(3) **实验室交付链三修**(JYF-489/496):`notifyParentOfChildDone` 的 done 父抑制闸放宽——done 父 + done 子照常唤醒(委托回传流:Helper 派工后即关父,子完成是唯一唤醒源),cancelled 子/父仍抑制,旧 pin `TestChildDoneSkippedWhenParentDone` 重写为新契约;`hides_deliverable_in_issue_timeline` 收窄为只藏 agent **回复**(0.3.49.1 的过滤器把 0.5.118 顶层报告一起藏了——DB/API 都有、UI 零渲染的根因),旧 pin 反向重写;by-issue 产物端点 0.5.114 起 renderer schema 漂移修复(`LabArtifactStubListSchema` 补 `{artifacts,total}` 包装对齐 Go `runtimeArtifactsResponse`,终结「暂无产物」);claude-science runtime 两评论路径补 `comment:created` WS publish + artifact 摘要评论补 `WorkspaceID`(此前 insert 必败且 `_ = cerr` 吞掉)。门禁:go 全量(internal/.../pkg/agent/cmd/multica)绿;cmd/server 仍 2 条在案 baseline 红(/tmp/wt-ctrig 干净 worktree 复验与本批无关);`pnpm typecheck` 6/6;core 958 + views 1889 vitest 绿。运营恢复(装后执行):JYF-490 重新派工 / JYF-497 解除阻塞续推 / JYF-489 唤醒 Helper 回传。
>
> **Prior: 0.5.114** (2026-09-24; **pythia 定名「群智推演」+ 默认启用 + labs usage hints; claude_science_lab 完整修复与改进 — 默认启用/补装 + issue 嵌入面 + artifact 溯源 + critique 审稿闭环**). b69c24364 + 31fb779f4 + c1f10ef9a + 8b7107c56..ee47eaf19。(1) **pythia**: 更名群智推演 8 面 4 语言; 默认启用双源; Labs usage hints(full-mapping 测试); embed 报告预览移除。(2) **claude_science_lab**: 默认启用 + ClaudeLabInstallAutoStart 幂等补装; mig 156 artifact.issue_id 死列接线 + by-issue 产物端点; ClaudeHeaderPill + ClaudeIssueEmbed(AgentTaskSnapshot 驱动, 产物 blob 预览, 0-runs-ready 折叠, 报告不重复投递); 第 6 agent critique — research 完成自动派审稿(断循环 + fail-soft), reproduce 技能入列, 研究循环三纪律。Prior: 0.5.113 (2026-09-23; pythia issue 优先嵌入 4-tab 改造). ba8ca85d5 + 18212c285。(1) **issue 优先交互**: 推演全部可交互面(发起/续推/停止/实时轮次/council/报告/回放/追问)只在 issue 属性面板 + 主任务区; create-issue 对 pythia 不再跳转; issue→实验室跳转全删(0.5.112)。(2) **`/experimental/pythia` = 被动监控台**: 健康条 + workspace 级列表 + 点击进 issue, 报 400 已修(handler-local X-Workspace-ID header fallback, ctxWorkspaceID 在实验路由组下永远空)(0.5.112 + monitor 400 fix)。(3) **新主区嵌入组件 `PythiaIssueEmbed`**(评论之下/输入框之上): 实时轮次 + council 票据 + 概率轨迹 + 报告 markdown 渲染; 无 run 时折叠 null 不占空间。(4) **新标题区状态徽章 `PythiaHeaderPill`**: 与 agent run pill 同一位置, 状态枚举 running/completed/aborted/failed/idle, lab_source=pythia_oracle 才挂; 与 embed 共享 usePythiaIssueLab hook 同步订阅 SSE。(5) **属性面板 4-tab→3-tab**: 删 live + report 两个 tab(已迁到 embed), 保留 继续推演 / 历史·回放 / 追问。(6) **客户端 monitor 错误不再静默**: 抛到 runsQuery.isError 路径(404 仍映射 [])。(7) **i18n**: 4 语言 experimental.json#pythia_lab 加 10 key(embed_*, pill_*, tab_continue)。契约记忆: `pythia-forecast-continuity-contract.md`。Prior: 0.5.112 (2026-09-23, pythia 续推重构: issue 绑定异步 run + 总线/SSE 流 + 引擎 council/报告端点 + mig 290 + 4-tab 面板全可视化; c7f6e70bc), 0.5.111 (2026-09-23, pythia 续推重构异步 run+总线+流/引擎 council+报告端点/mig 290/面板 4-tab; ba8ca85d5), 0.5.110 (2026-09-22, 上游价值移植批 45 提交 13 port/5+ skip: OpenCode 2.x runtime + 仓库起始分支 UI; 账本 `.omc/upstream-sync-2026-09-22.md`), 0.5.109 (2026-09-19, 22 提交 12 port/10 skip, MUL-7471 终态回调 outbox 重放 + Codex delta 流式 + Pi 静默错误守卫 + hermes 有界关闭; 账本 `.omc/upstream-sync-2026-09-19.md`), 0.5.108 (同日第二波上游批 9 提交 5 port + PG 外部实例僵尸事故记录), 0.5.107 (labs env+插件修复 + 上游 242 提交价值移植批), 0.5.106 (claude_science_lab 技能链路修复), 0.5.105 (实验室审计修复批), 0.5.104 (Pythia 真实 LLM 推演链), 0.5.103 (看板计数断链 + Pythia auto-start), 0.5.102 (board-dedupe hotfix). Full per-release history: `.omc/<ver>-ship-<date>.md`.
>
> **Gate-integrity reset (2026-09-01, no version bump).** `pnpm typecheck`, `pnpm lint`, and `pnpm test` are GREEN at HEAD, so **any red test from here is a regression**, not background noise. The "N failures = pre-existing baseline" convention is RETIRED. If a suite must be parked, skip it explicitly with a comment naming the gate — never by leaving it red. Current parked: 33 (`inbox-page.test.tsx` + 2 marker tests in `description-preview.test.ts`; MUL-6632 decision gate still open).

## Sub-domain Guides (read the nearby file when working in a sub-domain)

Each large sub-domain has a co-located `CLAUDE.md`. When your work is scoped to one, that nearby file is sufficient. This root file is navigation + cross-cutting rules.

| Working in | Read first |
| --- | --- |
| `server/` (Go backend, handlers, migrations, experimental catalog) | [`server/CLAUDE.md`](server/CLAUDE.md) |
| `packages/` (`core` / `ui` / `views` shared FE) | [`packages/CLAUDE.md`](packages/CLAUDE.md) |
| `packages/views/` (shared business pages/components) | [`packages/views/CLAUDE.md`](packages/views/CLAUDE.md) |
| `apps/desktop/` (Electron app, packaging, self-contained backend) | [`apps/desktop/CLAUDE.md`](apps/desktop/CLAUDE.md) |
| `apps/mobile/` (Expo / React Native) | [`apps/mobile/CLAUDE.md`](apps/mobile/CLAUDE.md) |
| `apps/web/` (Next.js App Router, platform wiring) | [`apps/web/CLAUDE.md`](apps/web/CLAUDE.md) |

Each guide directory also carries an auto-synced `AGENTS.md` mirror. Co-located `CLAUDE.md` is source of truth; parity enforced by `scripts/check-agents-docs-sync.mjs`.

## Commands

> **Single-command release**: `bash scripts/ship-mac.sh --yes` runs snapshot → bundle-cli → build → package → nested-binary signing → cold-start verify. `--build-only` stops before `/Applications` overwrite. Full ship chain is in the script; this file is not a duplicate.

```bash
# Single Go test (from server/)
cd server && go test -run TestName -count=1 -timeout 60s ./internal/handler/

# Single Vitest test file (from repo root; path is relative to the package)
pnpm --filter @multica/views exec vitest run modals/create-project.test.tsx
pnpm --filter @multica/core exec vitest run github/repo-ref.test.ts

# Docs-sync check (run after any root CLAUDE.md edit, before commit)
node scripts/check-agents-docs-sync.mjs
```

### Before packaging (fork-specific, NOT in CONTRIBUTING.md)

```bash
bash ~/.multica/scripts/pre-update-snapshot.sh   # 1. mandatory; exit 1 blocks packaging
cd server && go run ./cmd/migrate up             # 2. apply pending migrations BEFORE bundle-cli
```

### Version source (fork-specific)

`git describe --tags` returns `pre-update-...-g<sha>` (existing tags are snapshot markers, not release tags). `bundle-cli.mjs` falls back to `apps/desktop/package.json` → `version`. **`apps/desktop/package.json` is the canonical version source. Bump only that file.**

## Localized Fork

This is a **fully localized, single-user fork** of Multica. Primary target: macOS desktop app.

- **No telemetry**: `analytics.NewFromEnv()` always returns `NoopClient{}`. Frontend analytics functions are no-ops. `server/internal/analytics/posthog.go` deleted.
- **No auto-update**: CLI update command stubbed. Daemon does not start `autoUpdateLoop`. Desktop `updater.ts` is no-op. `electron-builder.yml` has no `publish:` block. `electron-updater` dependency removed.
- **No Google OAuth / email verification**: `SendCode`, `VerifyCode`, `GoogleLogin` all 410 Gone. Only `UsernameLogin` (`POST /auth/login {"name":"..."}`) works.
- **No cloud features**: billing, cloud runtime, CloudFront, contact sales, cloud PAT, invitations, workspace members — all deleted.
- **No external support UI**: HelpLauncher, JoinDiscordCard, Discord icon, FeedbackModal — all deleted.

Do **not** re-add any of the above.

- **Username-only login upserts a new user on every login.** `POST /auth/login` creates a new user row when the name is unseen. Workspace membership is bound to the creator user_id; any username change across restarts yields a fresh user with zero workspaces. Do NOT "fix" by auto-binding (let typo grant ownership). See `.omc/incidents/2026-06-27-username-only-login-loses-workspaces.md`.

- **i18next selector block-body incident (2026-07-14).** Block-body selectors `t(($) => { const v = $.foo; return v; })` return a plain string instead of the proxy; i18next's `keysFromSelector` reads `[PATH_KEY]` off the return, `path` becomes `undefined`, and `if (path.length > 1 && nsSeparator)` throws `TypeError`. The error escapes React's render pass, unmounts the surrounding tree, and (because the offending selector was inside `AppSidebar`) blanked the entire desktop window. Fix: selectors must be arrow expressions, e.g. `t(($) => $.sidebar[item.labelKey])`. Three layers of protection: (1) comment block in `packages/views/i18n/use-t.ts`; (2) `no-restricted-syntax` rule in `packages/views/eslint.config.mjs`; (3) `AppSidebar` wrapped in `error-boundary` with "Sidebar failed to render / Retry" fallback.

## Retired Features (do NOT re-add)

- **`constitution_agent` lab** (retired 0.3.57, migration 165). Removed the `宪法智能体` agent, 3 autopilots, 4 visibility rows, bundled skill. If upstream re-adds, do NOT cherry-pick back.
- **`agent_self_optimization` + `agent_creation_studio` experiment flags** (promoted 0.5.5/0.5.5.1; catalog entries deleted 0.5.6). Runtimes live as product-level resources — self-opt via `service/agent_self_optimization/*` controlled by the weekly `[自进化]` autopilot row's `status`; the studio as an issue-bound lab (`lab_source='agent_creation_studio'`, leader `agent_creation_expert`) entered via LabPicker. Do NOT re-add catalog entries, Labs-tab toggles, or `flagEnabled(...)` gates for these keys — a re-added gate on a removed key resolves `false` forever and silently kills the feature. Rationale: `.omc/0.5.6-ship-2026-08-02.md`.
- **Username-only login user-creation side effects** (see Localized Fork above).
- **Inline lab workspace panel on issue detail** (removed 0.3.38). `LabWorkspacePanel`, `pickLabInlineView`, `IssueDetailProps.renderLabInline`, `*Inline` view wrappers (`ClaudeLabInline`/`PythiaInline`/`MythosInline`/`LLMWikiBridgeInline`) all gone. Lab surfaces reachable ONLY via `/experimental/<suffix>` from sidebar or `<IssueLabsSection>` "open panel" link.
- **`mythos_swarm` lab** (retired 0.5.122, comprehensive removal). The multi-agent role-graph topology runtime — `server/internal/service/mythos/` (runner / supervise / brief / reaper), the 5-agent install bundle (`mythos_prelude` leader + `mythos_loop_coder`/`mythos_loop_researcher`/`mythos_loop_analyst` + `mythos_coda`), the `mythos_swarm` flag key, the `/experimental/mythos` route + `mythos-view` page, the `issue-openmythos-icon` affordance, and the 4-language `mythos.json` locales — all removed. Catalog entry deleted (along with the concurrent `code_canvas` / `semantica` / `timesfm` / `chat_pin_ui` cleanups). DB tables stay (forward-only law); existing `mythos_swarm`-bound issues render inert (no routes, no leader, no pill) the same way `swarm_topology` did — `DetachMythosRunsByIssue` still runs on issue delete to handle legacy `experimental_mythos_run` rows. No successor lab; analogous multi-agent workflows must use `user_*` plugins (`manifest.json` declares leader + agents + capabilities). Do NOT re-add the catalog entry or surface; a re-added gate on a removed key resolves `false` forever and silently kills the `issue_assignee_lab_lock` switch that derives its key set from the catalog (`TestBatchUpdateIssuesLabAssigneeLockParity` derives from `self` if a layer is missed).

## Conventions

The source of truth for code naming, i18n glossary, and Chinese product voice is:

- `apps/docs/content/docs/developers/conventions.mdx`
- `apps/docs/content/docs/developers/conventions.zh.mdx`

Read it before editing translations in `packages/views/locales/`, naming routes/packages/files/DB columns/types, or writing Chinese UI/docs copy.

## Project Shape

Multica is an AI-native task management platform for small teams, with agents as first-class assignees that can own issues, comment, and change status.

- `server/` — Go backend (Chi router, sqlc, gorilla/websocket). Three entrypoints: `cmd/server` (HTTP + WS), `cmd/multica` (CLI), `cmd/migrate` (forward/back SQL).
- `apps/web/` — Next.js App Router. `apps/desktop/` — Electron desktop (primary). `apps/mobile/` — Expo React Native. `apps/docs/` — Nextra docs.
- `packages/core/` — headless business logic, API client, React Query hooks, Zustand stores.
- `packages/ui/` — atomic UI components only. `packages/views/` — shared business pages/components for web+desktop.
- Shared packages export raw `.ts`/`.tsx`, compiled by consumers. Dependency direction: `views -> core + ui`; `core` and `ui` must stay independent.

### Data Flow (60-second mental model)

```
  Renderer (desktop/web)  ─HTTP+WS─▶  server/internal/handler → service/* → sqlc → Postgres+pgvector  ─WS push─▶  Renderer
                                                                                                            ▲
  Local Daemon (server/cmd/multica + apps/desktop daemon-manager.ts) ─spawns─▶  Claude Code / Codex / copilot / openclaw / ...
```

Lifecycle of an assigned task: **PATCH `issue.assignee_*`** → server `assignDefaultLabAgent` (if lab-bound) → daemon claim on `agent_task_queue` → daemon `LoadAgentSkillsForClaim` injects builtin + workspace skills → subprocess spawns agent CLI → progress over WS → renderer patches Query cache via `["agent-task-snapshot"]` invalidation. Labs add a parallel path via `issue.lab_source`.

## State Rules

Server state and client state stay separate.

- **TanStack Query** owns server state: issues, users, workspaces, inbox, agents, members, anything fetched from API.
- **Zustand** owns client state: selected workspace, filters, drafts, modals, tab layout, navigation history.
- Shared Zustand stores live in `packages/core/`, never in `packages/views/` or apps.
- React Context is for platform plumbing only (`WorkspaceIdProvider`, `NavigationProvider`).
- Only auth/workspace stores may call `api.*` directly. Other server interaction belongs in queries/mutations.
- Workspace-scoped query keys must include `wsId`.
- Mutations are optimistic by default: patch locally, send request, roll back on failure, invalidate on settle.
- WebSocket events invalidate or patch Query cache; never write directly to Zustand stores.
- Persist durable preferences/drafts/layout. Do NOT persist server data or ephemeral UI state.
- Zustand selectors must return stable references.
- Hooks that need workspace context should accept `wsId`; do not call `useWorkspaceId()` internally unless guaranteed under the provider.

## Package Boundaries

- `packages/core/`: no `react-dom`, `localStorage` (use `StorageAdapter`), `process.env`, or UI libraries.
- `packages/ui/`: no `@multica/core` imports and no business logic.
- `packages/views/`: no `next/*`, `react-router-dom`, no stores. Use `NavigationAdapter`, `useNavigation()`, `<AppLink>`.
- `apps/web/platform/`: only place for Next.js navigation/platform APIs.
- `apps/desktop/src/renderer/src/platform/`: only place for `react-router-dom` wiring.
- Every workspace under `apps/` and `packages/` declares directly imported external packages in its own `package.json`.
- Shared deps use `catalog:` from `pnpm-workspace.yaml`; `apps/mobile/` pins Expo/React Native directly.

Full state model + testing rules: [`packages/CLAUDE.md`](packages/CLAUDE.md).

## Sharing Rules

1. Next.js, Electron, router APIs stay in the app/platform layer.
2. Headless logic → `packages/core/`.
3. Shared UI/business views → `packages/views/`.
4. Shared primitives → `packages/ui/`.

Mobile is independent: imports only types + pure functions from `@multica/core` (`import type`), owns its UI/state/hooks/providers/i18n/React/build/release.

## Toolchain Baseline (do NOT bump casually)

| Tool | Version | Source |
| --- | --- | --- |
| Node | 22.x | CI workflows |
| pnpm | 10.28.2 | `package.json` (`packageManager`) |
| Go | 1.26.1 | CI workflows |
| TypeScript | ^5.9.3 | `pnpm-workspace.yaml` catalog |
| React | 19.2.3 | `pnpm-workspace.yaml` catalog |
| PostgreSQL | 17 with pgvector | `pgvector/pgvector:pg17` (CI service) |

`apps/mobile/` pins Expo/React Native directly; excluded from root turbo pipelines.

## Authentication

Username-only. `POST /auth/login` accepts `{"name":"alice"}` — first call creates the user (email = `name + "@local"`), returns a JWT. No email verification, no Google OAuth, no password. Login pages: `apps/desktop/src/renderer/src/pages/login.tsx` + `apps/web/app/(auth)/login/page.tsx`. Both call `useAuthStore.getState().loginWithUsername(name)`.

## API Compatibility

Frontend code must survive backend response drift, especially in installed desktop builds. zod schemas + `parseWithFallback` for every endpoint consumed by UI logic, explicit `=== true` boolean checks, `default` branch on server-driven enums. Full contract: [`packages/CLAUDE.md`](packages/CLAUDE.md) §API compatibility.

## Backend UUID Rules

In `server/internal/handler/`, know where a UUID came from before using it in write queries: path params via loaders (`loadIssueForUser`, `loadSkillForUser`, `loadAgentForUser`, `requireDaemonRuntimeAccess`), pure UUID inputs via `parseUUIDOrBadRequest`, trusted round-trips via `parseUUID`, outside handlers via `util.ParseUUID`. Full table: `server/CLAUDE.md` §UUID rules.

## Coding Rules

- TypeScript strict mode on; keep types explicit.
- Go follows standard conventions: `gofmt`, `go vet`, checked errors.
- Code comments must be English.
- Prefer existing patterns/components over new parallel abstractions.
- Avoid broad refactors unless required by the task.
- For internal, non-boundary code: no compatibility layers, fallback paths, dual writes, legacy adapters, temporary shims unless explicitly requested.
- If a flow or API is being replaced and the product is not live, prefer removing the old path instead of preserving both.
- New global pre-workspace routes: single word (`/login`, `/inbox`) or `/{noun}/{verb}` (`/workspaces/new`). No hyphenated root routes.
- Reserved slugs: `server/internal/handler/reserved_slugs.json`. Edit it, run `pnpm generate:reserved-slugs`, commit the generated `packages/core/paths/reserved-slugs.ts`.
- When changing CLI commands/flags, API fields, or product behavior documented by built-in skills under `server/internal/service/builtin_skills/*`, update the relevant `SKILL.md` and `references/*-source-map.md` in the same PR.

## Builtin skill 认知契约 (0.5.125)

**一个 builtin skill 的 front-matter `description` 是 agent 判断是否加载它的唯一依据。** 正文不参与选择。

链路: `loadBuiltinSkill` (`internal/service/builtin_skills.go`) 只填 `Name` + `Content`,**`AgentSkillData.Description` 恒为空**; description 是 daemon 在 agent 机器上从正文重新 parse (`internal/daemon/local_skills.go`), 正文写到 `{workDir}/.claude/skills/<name>/SKILL.md` (`execenv/context.go` `resolveSkillsDir` / `writeSkillFiles`), 由 provider CLI 原生扫描加载。`ensureSkillFrontmatter` 对合法 YAML **原样返回**, 所以你改的 front-matter 就是模型最终看到的那一行。

推论 —— 改 builtin skill 时:

1. **description 必须用用户的语言写触发词。** 全英文 description 对中文请求零命中。
2. **不要在 description 里写与正文自相矛盾的排除条款。** 0.5.125 的事故: `multica-pythia` 的 description 写着 "Do not use it for chat / **issues**", 而同一文件正文的 issue-forecast 章节就是给 issue 用的 —— 模型读到排除条款直接跳过该 skill, 然后自行角色扮演顶替。
3. **正文里不能有让 agent 自我阻断的指令。** 同一事故的第二层: 正文要求 agent 先跑 `multica pythia status`, 不 ready 就 stop here; 而该命令从 CLI 恒返回 `status="unknown"` (`cmd_pythia.go` 自陈 CLI 无法内省桌面子进程, 设计使然)。agent 忠实执行 → 放弃 → 报错。**当一条前置检查在目标环境下恒定失败时, 它不是保护, 是陷阱。**
4. **正文里的"不要用 A 顶替 B"类禁令对 agent 无效** —— 正文只有在 skill 被加载后才可见, 而没加载正是问题所在。这类规则要放进 description。
5. 新增 builtin skill 只需建目录 (`loadMainProductSkills` 走目录扫描), 无需改代码。
6. 契约类改动补一条回归测试, 断言 description 的**实际解析结果** (走 `skill.ParseSkillFrontmatter(Content)`), 不是 struct 字段 —— 并做一次 mutation 验证, 确认破坏契约时测试会红。
- **Bundle-cli 镜像资源必须随 source 同步 commit**。`apps/desktop/scripts/bundle-cli.mjs` 每次跑都 wipe + re-copy from `server/migrations/`, `apps/desktop/vendor/pythia-src/`, 等 source 目录到 `apps/desktop/resources/{server/migrations,pythia,}/`。source 改了 → 镜像在 build 时自动覆盖 → ship 出包用了新镜像,但 git tree 还显示 source 是新版本、镜像路径 untracked — 一个 commit 只改 source 不 commit 镜像会让 ship 用上未跟踪文件,而 fresh checkout 会 build 出不一样的产物(0.5.122 C1 fix:mig 292 镜像漏 commit 17 天)。Rule:每次改 source 同时 `git add` 对应镜像路径,确认 `git status` 不残留 untracked 资源镜像。

## Web/Desktop Features

When adding a shared page/feature for web + desktop:

1. Page/component in `packages/views/<domain>/`.
2. Platform wiring in both `apps/web/app/` and desktop router (unless desktop flow is a transition overlay).
3. Use `useNavigation().push()` or `<AppLink>` in shared code.
4. Use shared guards/providers (`DashboardGuard` from `packages/views/layout/`).
5. Platform-only UI in app or via props/slots.
6. Hooks needing workspace context accept `wsId`.

CSS shared from `packages/ui/styles/` — use semantic tokens (`bg-background`, `text-muted-foreground`), never hardcoded Tailwind colors.

When reviewing/auditing UI code (a11y, UX, visual design), invoke `web-design-guidelines` skill.

## Mobile Rules

Read `apps/mobile/CLAUDE.md` before touching. Mandatory pre-flight, import limits, parity rules, tech stack, UI rules, data helpers, realtime, release flow.

- Mobile shares only `@multica/core` types and pure functions.
- Match web/desktop semantics: counts, permissions, enums/transitions, identity.
- May differ in UI/interaction when phone context requires.

## UI Rules

- Prefer shadcn/Base UI components. Add with `pnpm ui:add <component>` from repo root.
- Design tokens + semantic classes; no hardcoded colors.
- No extra local state unless design requires.
- Handle overflow, long text, scrolling, alignment, spacing deliberately.
- If a component is identical between web and desktop, it belongs in a shared package.

## Desktop Rules

> Full desktop lifecycle, routing, packaging, data-safety: [`apps/desktop/CLAUDE.md`](apps/desktop/CLAUDE.md).

**P0 data-safety contract** (2026-07-02 incident destroyed 69 user tables when a Docker pgdata was migrated by the bundled `migrate` binary; do NOT remove either layer, do NOT make `backend` optional without re-reading `memory/multica-0.3.0-standalone-2026-07-02.md`):

> `runMigrate(profile, env, backend?)` REFUSES `backend === "external"`.
> The call site in `ensureServerUp` ALSO skips for defense in depth.
> Any new caller that invokes `runMigrate` must pass `backend` explicitly.

**P1.8 sentinel atomicity**: `runMigrationFlow` creates `~/.multica/.pg-migrating-v1` with `O_EXCL` BEFORE destructive `pg_restore`, renames to `~/.multica/.pg-migrated-v1` on success. SIGKILL between restore-success and rename leaves the in-progress file; next launch refuses auto-retry. **Do NOT write the final sentinel before the operation succeeds.**

**Desktop runtime config**: `~/.multica/desktop.json`:
```json
{"apiUrl": "http://localhost:8090", "wsUrl": "ws://localhost:8090/ws", "appUrl": "http://localhost:3000"}
```
Per-profile server env (`.env`) at `~/.multica/profiles/<name>/.env`.

**BrowserWindow off-screen guard.** Electron 39 on macOS restores stale bounds from system window-state cache; if bounds fall outside every connected display's workArea the window is invisible. `apps/desktop/src/main/index.ts` clamps bounds in `ensureWindowOnscreen()` — called synchronously after `new BrowserWindow(...)`, on `ready-to-show`, and on every `move`/`resize`/`display-removed`.

**Pythia engine source-of-truth is `apps/desktop/vendor/pythia-src/engine/`, NOT `apps/desktop/resources/pythia/engine/`.** `bundle-cli` (`apps/desktop/scripts/bundle-cli.mjs:281-283`) wipes `resources/pythia/` and re-copies from `vendor/pythia-src/` on every run. Edits directly under `resources/pythia/engine/*.py` are silently overwritten at bundle time. Edit the vendor copy, then `pnpm --filter @multica/desktop bundle-cli`. (Bit 0.3.21 Pythia i18n pass — three rounds of Edit to `resources/pythia/engine/*.py` all looked successful until re-bundle reverted every change.)

## Data Safety & Version Upgrades

DMG install **only replaces `/Applications/Multica.app`**. All user data lives in independent paths:

| Data | Path |
|------|------|
| PostgreSQL | Bundled **native** PG — pgdata at `~/Library/Application Support/Multica/pgdata` (NOT a Docker volume; the `multica_pgdata` Docker volume is upstream's dev form and this machine has no Docker) |
| Config / tokens | `~/.multica/profiles/<name>/config.json` |
| Server env | `~/.multica/profiles/<name>/.env` |
| Workspace files | `~/multica_workspaces_<profile>/` |
| KB vaults | `~/Documents/` |
| Desktop config | `~/.multica/desktop.json` |

Rules:

- **Migrations are forward-only**: never drop a table or column. Schema changes must be additive.
- **Config fields are append-only**: don't delete/rename existing keys in `config.json` or `.env`. New fields have defaults.
- **Pre-update snapshot** mandatory before DMG rebuild (script `~/.multica/scripts/pre-update-snapshot.sh`).
- **Verify data integrity** after upgrade — the server's PG is the bundled native instance on `127.0.0.1:5432`: `PGPASSWORD=multica psql -U multica -d multica -h 127.0.0.1 -p 5432 -tAc "SELECT COUNT(*) FROM workspace"` should return expected count. If 5432 isn't listening, check 5433 (same fallback as `pre-update-snapshot.sh`). The upstream form `docker exec multica-postgres-1 psql ...` cannot work here (no Docker).

## Testing

AAA pattern. Tests follow the code:

| What | Location |
| --- | --- |
| Shared business logic, stores, queries, hooks | `packages/core/*.test.ts` |
| Shared UI components, pages, forms, modals | `packages/views/*.test.tsx` |
| Platform wiring (cookies, redirects, search params) | `apps/web/*.test.tsx` or `apps/desktop/` |
| E2E flows | `e2e/*.spec.ts` |
| Backend | `server/` Go tests |

Rules:

- Never test shared component behavior in an app test file.
- `packages/views/` tests must not mock `next/*` or `react-router-dom`.
- Mock `@multica/core` stores with Zustand callable-store shape (`selectorFn` + `getState`).
- Mock `@multica/core/api` for API calls.
- E2E uses `TestApiClient` for setup/teardown.
- Prefer writing the failing test in the correct package before implementation when change is behavioral.
- **新 fixture INSERT 前必查 migrations/*.up.sql 的 NOT NULL FK 列**。agent.task queue / agent / workspace / issue 等核心表的 NOT NULL 列由 mig 0xx 系列沉淀,迁移随版本持续追加;新 fixture 如果漏填一列,Postgres 立刻抛 `null value in column "<col>" of relation "<table>" violates not-null constraint (SQLSTATE 23502)`,测试在 INSERT 阶段就死 — 错误不会被测试本身捕获(测的是后续读取路径),fix 极易被遗漏直到 audit / 下次 ship。Rule:fixture 写 INSERT 前用 `grep -E "NOT NULL\|PRIMARY KEY" migrations/*.up.sql | grep -A1 "CREATE TABLE <表名>"` 列出所有 NOT NULL 列 + 对每个 FK 列(`agent.runtime_id` mig 004 等)查 source table 是否需要先建父行。参照:`TestCausalReadsList` 0.5.122 fix 在 `causal_graph_refresh_test.go:319` 把 INSERT agent 加 `runtime_id` 之前先 INSERT agent_runtime + RETURNING id,跟 `agent_access_test.go:425-438` 同模式。

## Verification

```bash
pnpm typecheck                  # full turbo pipeline
pnpm lint
pnpm test
make check-fast                 # affected TS typecheck + unit + lint; no DB/Go/E2E
make test
cd server && go test -count=1 -timeout 600s ./internal/... ./pkg/agent/...   # mandatory after any cherry-pick or Go source edit
pnpm exec playwright test
make check
```

Do NOT claim verification passed unless you ran it. If you skip (docs-only or asked not to), say so.

**Ship gate (mandatory before every release)** — all three must be green:

- `pnpm typecheck` (full turbo pipeline) — catches TS breakage.
- `cd server && go test -count=1 ./internal/... ./pkg/agent/...` — catches Go breakage.
- `bash scripts/ship-mac.sh --yes` runs snapshot → bundle-cli → build → package → nested-binary signing → **cold-start verify (FATAL since 0.5.100)**. Step 7 now refuses to ship when any of these fail: (a) the verify script is missing, (b) the verify script exits non-zero, (c) no server PID is bound on `:8090` after the script reports PASS, (d) `/health` does not return `{"status":"ok"}` after the script reports PASS. The (c) + (d) checks are the belt-and-suspenders layer that catches the bug class where the verify script exits 0 incorrectly (e.g., 0.5.98 ReferenceError in `pickEnvForSpawn` shipped a broken app because the verify path was bypassable — fixed in 0.5.99 + hardened in 0.5.100). If any check fails, `die` aborts the ship before `/Applications/Multica.app` is overwritten. If you skip the ship chain (docs-only change or asked not to), say so explicitly.

0.5.15 lesson: only `pnpm typecheck` was run; broken `#6199` cherry-pick broke `TestBuildMetaSkillContentIssueBodyFormatting` 4/4 subtests in the legacy verbose brief path (default in production) — caught only by post-ship code-reviewer, not the gate. Future batches must run both typecheck + go test + the cold-start verify.

**`cmd/server` is NOT covered by the ship-gate go-test list (0.5.121 audit finding).** The mandatory command above runs `./internal/... ./pkg/agent/...`; `cmd/server` integration tests (`TestCommentTriggerOnComment`, `TestCommentTriggerAtAllSuppression` — comment-trigger semantics) went red sometime around 0.5.117/0.5.118 and stayed red because no gate runs them. Before shipping anything that touches comment triggers or the claim/dispatch path, run `go test ./cmd/server/` explicitly. The reds are a recorded baseline pending a dedicated fix batch — do not cite them as pre-existing cover for new failures in other packages, and fix-forward is expected.

**Silent-skip trap (0.5.79 lesson)**: with `DATABASE_URL` unset, DB-backed tests SKIP silently — suite "passes" in ~15s having run nothing. `.env` lives at the **repo root**, NOT `server/` — export it from the repo root BEFORE `cd server`: `export $(grep -E '^DATABASE_URL=' .env | xargs)`. Running that same command after `cd server` silently no-ops (grep finds no `server/.env`, the substitution expands empty, `DATABASE_URL` stays unset) and the trap stays armed. Confirm the runner printed its DB-set marker before trusting a suspiciously fast green run. `scripts/check.sh` hard-fails on that precondition (it sources repo-root `.env` itself).

## Upstream Port Workflow

**Fork and upstream `multica-ai/multica` share ZERO commits** (`git merge-base HEAD upstream/main` returns empty). Every "port" is a manual diff transplant; **`git cherry-pick` is unusable**. Fuller 6 步流程: `~/Documents/llm wiki/projects/multica-exploration-dev/upstream-port-workflow.md` (作者本机路径). The two gates you cannot skip even without it:

```bash
# per-file sanity gate — fork-side diff must be ≤ 5× upstream's per-file stat
git show <upstream-sha> --stat | head -60
git diff --stat HEAD -- <file>
```

Wholesale adoption (`git checkout --theirs`) silently destroys fork localization. Never wholesale-swap.

**Localization conflict scan (before any port)** — grep the upstream diff for `posthog` / `PostHog`, `electron-updater` / `autoUpdate`, `SendCode` / `VerifyCode` / `GoogleLogin`, `CloudFront`, `workspace_invitation`, `billing` / `subscription`, `contact-sales`. Hits → classify strip / port-sans-X / OK-as-is against the Localized Fork list above.


## Commits and Releases

- Atomic commits with conventional prefixes: `feat(scope)`, `fix(scope)`, `refactor(scope)`, `docs`, `test(scope)`, `chore(scope)`.
- Tags `pre-update-*` are snapshot markers, not release tags; `git describe` never yields a release version. For local releases, bump `apps/desktop/package.json` `version` and document in `.omc/release-notes-<ver>.md`.
- **DMG creation hangs on create-dmg 1.2.3** (`electron-builder --mac` produces no `.dmg` on this fork). Use `pnpm exec electron-builder --mac --dir` to produce `dist/mac-arm64/Multica.app` directly and ship that. Every 0.3.x release ships via `--dir`.
- **`pnpm build` does NOT run electron-builder** — asar replacement is silent if skipped. Verify with `grep -c rawRequest apps/desktop/dist/mac-arm64/Multica.app/Contents/Resources/app.asar` after each build.
- Bump patch by default unless user specifies a version.

### Cherry-pick verification (0.5.36 lesson)

After ANY agent-assisted cherry-pick, compare each resolved file's diff size against upstream's per-file stat:

```bash
git show <upstream-commit> --stat | head -60
git diff --stat HEAD
```

Files whose diff is 5-50x larger than upstream's = wholesale adoption (`git checkout --theirs` replaced fork's file with upstream's ENTIRE current file). Fix: revert that file to the pre-cherry-pick commit (`git checkout <base> -- <file>` — NOT `HEAD`), re-apply only semantic change.

Pre-existing test bisect: prove a failing test predates a port with `git worktree add /tmp/wt-check <base-commit>` + symlinked `node_modules`.

## Labs Platform

Full architecture spec lives in `server/CLAUDE.md` and `.omc/labs-runtime-lifecycle-map.md` (refreshed 2026-08-21 for 0.5.46 canonical state). Core invariants:

- **Flag = off MUST completely bypass experimental code.** No new imports, no module init in legacy path. View toggles use `{flagEnabled ? <NewCode /> : null}`.
- **Users cannot create flags.** Catalog is developer-only at `server/internal/experimental/catalog.go`. Labs UI only renders what server returns.
- **Labs tab is the only entry point.** No nav bar, no CLI shortcuts, no `api.*` callers outside `labs-tab.tsx`.
- **Not a plugin system.** Simple toggle pattern, not dynamic load.
- **No reserved workspace for new labs.** Lab resources isolated by `experimental_resource_lock` + visibility table.
- **Manifest → catalog → registry → IPC dispatcher → proxy mount → sidebar.** Single chain — no per-flag hardcodes.
- **RuntimeKind enum**: `none` / `inline` / `subprocess` / `headless`. `ManagerFactory` dispatches per flag key.
- **User plugin layer** (`user_*` keys, 0.3.60): merged at boot via `RegisterUserPlugins()` + `MergeUserPlugins()`. Built-in flags always win on key collision.

When adding a new experiment: 1) manifest in `apps/desktop/resources/experiments/<flagKey>/manifest.json`; 2) append to `Catalog` in `server/internal/experimental/catalog.go`; 3) migration if needed + visibility seeds; 4) install handler if `installable: true`; 5) route + view if dedicated surface (network calls via `api.rawRequest`, never bare `fetch`); 6) builtin skill if agents invoke; 7) `bundle-cli` + `electron-vite build` + `electron-builder --dir`.

**Lab ↔ Assignee Mutex (widened by `interaction_model` since 0.5.86; effective live-lab set 0.5.122):** the lock covers every lab classified `assignee` — `claude_science_lab`, `pythia_oracle` and any `user_*` plugin whose manifest declares `interaction_model: assignee`. `mythos_swarm` (sole-mode no-leader) + `semantica` + `timesfm` were retired 0.5.122 and are no longer in the live set; the historical 4-case contract is still valid for the remaining labs (and any future ones) — `sole` mode rewrites to leader, `enhancer` mode is the inverse (an assignee is REQUIRED). Auxiliary labs (`llm_wiki_bridge`, `causal_graph`) and unclassified flags accept a manual assignee; on `lab_source` flip without explicit assignee, server auto-rewrites to lab leader (Active Contract #2). Authority: `experimental.IsAssigneeModelLab` + `handler.assigneeLabLockError`. Full rules + the 0.5.107 batch-parity fix: `server/CLAUDE.md` §Labs.

**Network calls from Labs tabs MUST go through `api.rawRequest`** (`packages/core/api/client.ts`). Never bare `fetch()` (e.g. `fetch("/api/experimental/...")`) — fails in desktop (renderer origin `file://`, not bundled backend `:8090`; no Bearer = 401). Converted: claude-lab, forecast-stream, llm-wiki-bridge, experimental-artifact, pythia-report. (`mythos` was converted too but the lab retired 0.5.122 — the rawRequest wiring stays in the codebase as a reference; semantica / timesfm / chat_pin_ui were never wired because they had no in-app surface before retire.) Exception: Pythia loopback calls (0.5.112: the monitor page's `/status` health strip) go through `window.experimentalAPI.pythia.proxy` (allowlisted + rate-limited in `apps/desktop/src/main/pythia-manager.ts`), never rawRequest, never bare fetch — the loopback engine is not the Multica backend. **`api.rawRequest` never sends `X-Workspace-ID`** — only `X-Workspace-Slug` (the ID header exists solely on `mat_` task-token requests, stamped by the auth middleware). A new endpoint registered on an auth-only route (no workspace middleware → `ctxWorkspaceID` always empty) MUST take an explicit `workspace_id` query param + `h.workspaceMember` — the GetClaudeLabContext contract. A header fallback there can never fire: the pythia monitor 400'd on every single load for three releases (0.5.112–0.5.114) on exactly this wrong assumption (fixed 0.5.115).

**Lab auto-dispatch opt-out** (per-catalog): `experimental.Flag.AutoDispatch *bool` — nil/true = unchanged; false = leader-rewrite still applies (so IssueLabsSection + workbench header show the right agent) but enqueue gate short-circuits; user must explicitly trigger via lab workbench "Run research" button. Today: `pythia_oracle` + `timesfm` (records-only) and `causal_graph` (auxiliary) are opt-out. `multica lab delegate` fails fast naming AutoDispatch=false + frozen labs.

## Active Contracts

Load-bearing cross-cutting contracts. Honour these when adding/refactoring lab-class surfaces. Fuller set (10 条, 含 P0 audit 清单): `~/Documents/llm wiki/projects/multica-exploration-dev/active-contracts.md` (作者本机路径; 缺失时以下 4 条是必须自行验证的最小集).

1. **Lab leader rewrite on `lab_source` flip (4-case contract)** — `handler/issue.go::shouldRewriteAssigneeForLabLeader` + `assignDefaultLabAgentOnUpdate`. untouched → noop; → no-leader lab → noop; → leader lab, already leader → noop; → leader lab, otherwise → **rewrite**. `BatchUpdateIssues` computes post-state as previous row + batch fields and honours the same contract. **Do NOT add a lab key to the batch switch** — it already calls `assigneeLabLockError`; `TestBatchUpdateIssuesLabAssigneeLockParity` derives its key set from the catalog and fails if a layer is missed.

2. **chi route order — literal slug BEFORE `{param}`.** Registering `/sessions/by-issue` after `/sessions/{sessionID}` makes chi capture the literal as the param → handler 400 "not a UUID". Comment the order rationale inline.

3. **`lab_managed` DTO marker** — when `experimental_resource_visibility` has a row for a flag, every agent/squad it owns is hidden from all selection surfaces. Server stamps `lab_managed?: boolean`; renderer gates on `lab_managed: false`. NOT a column — derived from visibility rows. Do not remove the stamp even when `useActorName` shares the query.

4. **Causal-graph trust ladder.** Tier A (native task hooks) > B (Semantica) > C (Pythia closure) > D (LLM proposals). Tier D ALWAYS lands `status='suggested'` at confidence ≤ 0.5, invisible to subgraph/path until a human confirms. **Reject is a tombstone, never delete** (mig 280) — proposers re-derive, so a hard-deleted rejection re-proposes nightly. Probe `FindCausalEdgeBetween` (ANY status) BEFORE INSERT. A new `Source` constant must be added to `experimental.AllSources` in the same commit.


## Known Stability Surfaces

22 条真实调试过的失败模式 (PG SIGTERM zombie / handler env 泄漏 / experimental_pref 消失 / ship chain EOF / Pythia engine ensure-up / cache dedupe 3 层 / etc.). 检索: `~/Documents/llm wiki/projects/multica-exploration-dev/known-stability-surfaces.md` (作者本机路径). 每条都是"读代码看不出来、只有踩过才知道"的类型, 改对应子系统前先查。


## Memory Index (cross-session)

Memory lives in `~/.claude/projects/-Users-jiangjianyan-jjy-multica-exploration-dev/memory/` (re-pointed 2026-09-02 from `multica-main` slug). Full index in `MEMORY.md` (one line per memory, descriptive title). **Read before editing any subsystem with a known-regression surface.** These files are outside this repo (per-user, cross-session context) — `git` lookups at repo root will not find them.

Most recent lessons worth re-reading before any upstream port or ship:

- `0.5.108-pg-external-instance-zombie-2026-09-17.md` — an adopted external PG + a later SIGTERM = zombie app: /health never probes the DB, the daemon's 401 "invalid token" storm is actually a DB outage, snapshot query errors map to 404 (zero 5xx all day); diagnose via the 5432 listener + server-manager.log `backend selected` line BEFORE touching auth code; bundled pg_ctl recovery heals the daemon within one heartbeat. Same session also shipped 0.5.108 (9-commit upstream port batch; MUL-7409 plan archived in `.omc/upstream-sync-2026-09-17.md`).
- `0.5.103` (this release, notes in `.omc/0.5.103-ship-2026-09-07.md`) — a board bug can be a PARAM problem: verify what the client actually serializes and the endpoint actually parses against the live API before rewriting render code (live-probe caught `status_category` dead end-to-end after a client-side dedupe hotfix treated symptoms). Subprocess engines need an explicit bring-up story for every entry point that needs them, not just the panel route. Global `core.hooksPath` (user dotfiles) silently disables repo hooks.
- `0.5.99-h9-import-fix-...md` — `export { x } from "..."` re-export pattern is NOT a local bind; bare re-export in audit-fixed code caused 0.5.98 ReferenceError that bypassed the verify gate.
- `0.5.100-upstream-port-batch-with-ship-chain-verify-fatal-...md` — ship-chain verify must be FATAL (now is); belt-and-suspenders post-verify checks; `CodexResumeOverflowError` / `annotateHermesProviderUnconfigured` / `keyboard-shortcuts-tab.tsx` are fork-absent.
- `0.5.101-upstream-port-batch-with-deep-dive-reversal-lessons-...md` — 3 parallel deep-dive research agents catch scope miscounts (MUL-7008 was 3× undersold); wholesale-swap trap on partial pre-ported files; `dbstartup` is fork-missing; partial pre-ports can include broken imports.
- `0.5.102-board-dedupe-hotfix-...md` — cache → render pipeline needs 3-layer dedupe (addIssueToBuckets / flattenIssueBuckets / buildColumns); per-bucket-only check is insufficient; defense in depth at every transformation stage.
- `0.5.102-ship-chain-electron-zip-eof-...md` — `electron-builder` can EOF on the 112 MB Electron download; `rm -rf ~/Library/Caches/{electron,electron-builder}/` clears poisoned partials; network is back, retry ship.

## Domain Reminders

3 套 lab 系统契约 + schema 提醒。检索: `~/Documents/llm wiki/projects/multica-exploration-dev/domain-reminders.md` (作者本机路径)。动任一层前先读对应条目:

- **`causal_graph`** — flag key `causal_graph` 在 catalog / lock.go / mig 279 CHECK / router **五处 verbatim**, 改名即全断。`RecordSubIssueEdge` 对任何带 `parent_issue_id` 的 CreateIssue 生效 (无 lab_source 闸)。画布力导向是按 membership signature 缓存的确定性纯函数 —— 破坏该契约会让 5s poll 每次重排, 图跟着光标爬。
- **`pythia_oracle`** — 所有交互面只在 issue 属性面板 + 主任务区; `/experimental/pythia` 是**被动**监控台。任何取消路径必须在同一 commit 调 `abortPythiaRunsForIssue`。`api.rawRequest` 从不发 `X-Workspace-ID`, monitor 路由走显式 `workspace_id` 查询参数。
- **`claude_science_lab`** — default-on 靠 `DefaultVal: true` + `ClaudeLabInstallAutoStart` (renderer 幂等补装, agent 数 < 3 视为 stale)。report 是 comment 不是 artifact 两者去重, 走 embed 不重复投递。

