---
name: labs-plugin-upstream-hardening-0.5.78-plan
created: 2026-08-27
type: plan
status: draft (待用户批准后执行;P3-B/P3-C/P1-3 为 STOP-AND-ASK 决策门)
---

# 实验室环境 × 插件稳定性 × 上游集成收尾 — 任务计划(0.5.78+)

> 三支柱目标:
> **G1** 实验室环境(labs runtime environment)完善,深化与 Multica 的集成质量;
> **G2** 插件功能(8 内置 lab + 用户插件层)在 Multica 中的可用性与稳定性;
> **G3** 上游官方(multica-ai/multica)最新功能 / bug / 性能修复的持续集成 —— **硬排除**:云端遥测、云端协作(billing/seats/OAuth/subscription)、远端连接类非本地化面(Telegram/Discord/channels/electron-updater)。

## 0. 现状基线(2026-08-27 已核实)

- **仓库/应用一致**:分支 `epic/0.5.72-followups` @ `3bea85b77`(0.5.77)= `/Applications/Multica.app`;备份为 0.5.76。工作区 clean。
- **上游状态**:upstream/main @ `09a2410e8`(2026-08-26 19:01,v0.4.35)。fork 与上游无共同祖先(重建历史),集成靠 cherry-pick。`.omc/upstream-integration-triage-2026-08-26.md` 已完成全量分诊(675 提交 → 330 适用候选 → Batch 1–4 + Batch 5),基线即当前 HEAD,**fetch 复核 0 个新提交**(执行日需重新 fetch 做 P0-1 增量)。
- **已落地**:0.5.71(Batch 1 安全/CVE)→ 0.5.72(渲染进程 sandbox)→ 0.5.73/74(worktree 恢复+修复)→ 0.5.75(Batch 2 CLI/skill + Batch 3 UX)→ 0.5.76(批次 A–D,2 净落地 + 6 already-present + 13 正确跳过)→ 0.5.77(**Batch 5 REVIEW 级:MUL-6639 skill 元数据/stall 探测 5 子提交 + MUL-6658 全后端进程树所有权 17 子提交**,共 22 子提交)。
- **实验室目录**:8 内置 flag(chat_pin_ui / claude_science_lab / pythia_oracle / mythos_swarm / swarm_topology / llm_wiki_bridge / code_canvas / semantica)catalog ↔ manifest 一一对应;用户插件层(user_plugins CRUD/runtime/artifacts/sign/scanner)测试覆盖良好(crud 8、scanner 7、runtime 2、sign/auth/serve 4+)。`go build ./internal/experimental ./internal/handler ./cmd/server` 干净。
- **历史大缺口已闭合**:user_plugin `subprocess` 运行时(0.5.18 关闭 reserved slot,`user_plugin_runtime.go:237`);pythia `/agent/events` `/scorecard*` 路由已在 engine;semantica wheel 0.6.6 已 staged(vendor + resources 双侧)。

### 残余输入清单(本计划的直接事实来源)

| 来源 | 未闭环项 |
|---|---|
| labs 审计 `.omc/audit/2026-08-23-labs-issue-integration-audit.md`(Still open) | P2-6b lab-picker 对非 mythos 实验仍触发 `onClearAssignee()` + 写 `lab_mode:'sole'`(文档-实现不一致);P3-6 semantica `surface.proxy_prefix` 半边缺失(pythia 半边已补);P2-4 swarm leader 自建 skill/squad 无 visibility 行("瞬态低危"接受态);P2-5 experimental_pref 孤儿 user_id(登录契约衍生,接受态);mythos_run/swarm_run = 0(无真实用户触发) |
| 同上(New residual gaps,0.5.61 候选) | mcp_server lock resource_type 无 GC 无释放契约(schema 允许但 migration 274 / lock_gc 均不覆盖);schema_migrations.version 为 TEXT(cosmetic) |
| 0.5.77 release notes(Deferred) | B5-starter(MUL-6629+MUL-6709,fork 有概念不同的本地 starter_prompts);B5-inbox(MUL-6632,缺 10 文件+类型形状+服务端投影);B5-locale(MUL-6660,依赖未移植 source-context);`8c563b49` 10K LOC source-context sub-issue;`e5f976144` sweeper 与 fork RuntimeGC 冲突待审 |
| 预存测试失败 | `TestDashboardPerAgentRollupsUseExactWindow`(900s 泄漏,0.5.47 起)、`TestQuickCreateIssueParentTrustBoundary` race(0.5.74 起)、`TestDashboardEndpoints`(bisect 至 `6cefe25be`) |
| CLAUDE.md 已知项 | `vendor/openscience-bin/openscience` 二进制缺失(bundle-cli 每次 build 告警;claude_science_lab 走 inline 不受影响);CLAUDE.md:824 引用的 `scripts/build-semantica-wheel.sh` 在 `apps/desktop/scripts/` **不存在**(文档漂移或脚本缺失,待核) |

## 排除清单(G3 硬过滤器,执行全程有效)

沿用 triage 的 HARD/SOFT kill 关键词与路径 kill,**并按本次指示追加为绝对排除**:

- 云端遥测/analytics:`posthog|analytics|telemetry|helplauncher|feedbackmodal`
- 云端商业化:`billing|seat|entitlement|subscription|checkout|invoice|cloudfront`
- 云端认证/OAuth:`oauth|googlelogin|sso|send[_-]?code|verify[_-]?code`
- 远端连接类外部渠道:`telegram|discord|channels`(MUL-6166 Telegram、MUL-6661 channels/new-clear 默认 SKIP)
- 自动更新:`electron[_-]?updater|auto[_-]?update`
- 协作席位:`workspace[_-]?(invite|member)|invitation`

其他永久纪律:不 rebase upstream;不做 wholesale adoption;退役 catalog 键不复活;migration forward-only;Labs tab 一切网络调用走 `api.rawRequest`。

---

## Phase 0 — 基线对账与卫生(≈0.5 天,无决策门)

- **P0-1 执行日增量 re-triage**:`git fetch upstream --tags`,对 `09a2410e8..upstream/main` 新提交跑同一方法论(HARD/SOFT/path kill → 分级),结果追加到 triage 文档 "Post-triage re-grade" 章节(现成格式可复制)。预计 0–15 commit/天。
- **P0-2 Batch 4 docs-only 对账**:对照 triage §Batch4 + §P3(P51 docs 清单),核对 v0.4.30–35 changelog、README/CONTRIBUTING 类提交是否捡完;缺的逐个单独 cherry-pick(不批量)。预估 ≤5 个、~30 分钟。
- **P0-3 三个锁定测试专项修复**(不再封存,本周期清账):
  - `TestDashboardPerAgentRollupsUseExactWindow`:数据完整性(昨日 900s run 泄漏到 days=1 窗口),先例记录见 `.omc/0.5.49-retrospective.md:90`;
  - `TestQuickCreateIssueParentTrustBoundary`:race,需 -race 复现定位;
  - `TestDashboardEndpoints`:新锁,bisect 显示基线 `6cefe25be` 即失败(agent-runtime task count 断言)。
  - 门禁:修复后 `go test ./internal/handler/... -count=1` 连续两轮绿;若确属产品语义变更则改断言并写明理由。

## Phase 1 — 实验室环境完善(≈1 天)

- **P1-1 Pythia 一致性冒烟**:跑 `apps/desktop/scripts/pythia-smoke.sh` 全绿;bundle-cli dry-run 验证 `vendor/pythia-src/engine/` ↔ `resources/pythia/engine/` diff=0(防 0.3.21 类静默回滚)。
- **P1-2 Semantica 链路收口**:
  - 核实 `scripts/build-semantica-wheel.sh` 缺失问题(CLAUDE.md:824 引用):从 `vendor/semantica-src` 补一个最小构建脚本,或修正文档引用为"预构建 wheel 由 bundle-cli 直接镜像";
  - semantica subprocess 启动冒烟(enabled → :port 就绪 → decisions API 可达);
  - 决策(轻量,非阻塞):reconciler 是否从 observability-only 升级强制模式 —— 默认维持现状,P6 记录在案。
- **P1-3 openscience 二进制【STOP-AND-ASK】**:`vendor/openscience-src/` 仅 agent-prompts 文本资产。选项:**A)** 从上游 OpenScience 源码构建原生二进制 drop 到 `vendor/openscience-bin/openscience`(需引入构建链);**B)(推荐)** 正式声明 inline-only 路线(bundler 警告降级为 info 提示"仅独立子进程路径需要"),同步更新 CLAUDE.md 对应条目。两者都消除重复警告,B 成本≈0。
- **P1-4 code_canvas 定级【默认 defer】**:`vendor/code-canvas/` 仅 `run.sh`,仍是 0.5.18 计划所述 stub。0.5.18 的"Phase 3 真实现(Monaco / tldraw)"不在本期实施,保持 backlog;仅在 release notes 标注现状。
- **P1-5 审计残余缺口收口**(来源见 §0 清单):
  - **P2-6b lab-picker 行为对齐**:非 mythos 实验室在 picker 选择时是否应触发 `onClearAssignee()`——按"mutex narrowed to mythos_swarm only"契约改代码(picker 仅对 mythos_swarm clear + 写 sole),并补 views 层回归测试;
  - **P3-6** 补 semantica manifest/installable 侧 `surface.proxy_prefix`(对齐 pythia 半边先例);
  - **mcp_server lock 契约**:至少落代码注释 + lifecycle-map 文档行("mcp_server lock rows must be released by the writer; no GC fallback"),可选预置 lock_gc sweep 分支(含 `resource_type='mcp_server'`);
  - P2-4(swarm leader visibility 瞬态低危)/ P2-5(pref 孤儿)/ schema_migrations TEXT 维持接受态,不动作;
  - 本阶段不改 catalog → lifecycle-map 只做复核比对,不触发强制 refresh 条款。

## Phase 2 — 插件可用性与稳定性(≈1–1.5 天)

- **P2-1 测试补强**(优先级序):
  1. P2-6b 的 lab-picker 回归(desktop views);
  2. 用户插件软删除清理链断言强化:visibility rows / pref rows / artifacts 在 DELETE 后确实清理(handler 现有 `TestUserPluginSoftDeleteThenRecreate` 之上加清理面断言);
  3. signed artifact URL 边界负路径:过期、篡改 signature、跨 slug 引用拒绝(现有 Verify/Auth 测试之上补时间维度);
  4. subprocess 用户插件:sandbox env(per-task TMPDIR 注入链)、`manifest.runtime.command` 缺失时的报错路径、init_timeout/burst 触发 IsBroken 黑名单端到端;
  5. UpdateIssue×swarm mutex 回归钉(audit P2-1 列出的空角确认与补齐)。
- **P2-2 可用性端到端手工冒烟**(desktop shell,一次性 checklist 写入 ship log):
  Labs tab「用户插件」创建(auto 与 issue_select 两种 trigger_mode)→ LabPicker 出现性(auto 应隐藏)→ 运行 → Artifacts 查看/signed 下载 → 删除 → 重名可复用;同时点检 0.5.60 的 Back affordance 与 "View in lab →" jump 四实验室对齐。
- **P2-3 稳定性实测**:一次真实 inline + 一次真实 subprocess 插件长运行观察(env 隔离、进程回收、MUL-6658 进程组所有权是否覆盖插件路径),结论追加进 lifecycle-map 附录。
- **P2-4【决策门】上游 MUL-6350 plugin rebuild 系列**(hook engine `659bb41a`、durable scheduled hooks `5bf10d48`、hosted surfaces `bedad9e2`、host artifacts `f45b5916`、agent integration `4bf3c73a`):维持 **SKIP-DIVERGENCE** 总方针(fork 插件层是自研且 F-006/F-008/F-013 加固过)。若用户想要"durable scheduled hooks"能力:另立 fork-local 设计提案(给 user_plugin 增加 schedule trigger),**不在本期实施**。
- **P2-5 技能映射核对**:按 `.omc/lab-skill-coverage-matrix.md` 对用户插件声明 skills 做 spot check(声明 → 实际可触发),差异只记录不改行为。

## Phase 3 — 上游集成收尾(≈1.5–2.5 天,视决策门结果)

顺序=前置条件链:

- **P3-A inbox 归档铺垫批 + 主件 MUL-6632(`b2b4699f`,1282 LOC)**:
  先移植铺垫(~3–5 commits):`inbox-list.tsx`、`inbox-context-menu.tsx`、`inbox-view.ts`(`InboxView` 类型 + `ARCHIVED_VIEW_PARAM`)、`archivedInboxListOptions` query、`listArchivedInbox` API、`issue_priority` projection 入 `server/pkg/db/queries/inbox.sql`,`useStatusOptions` 形状对齐(上游期望扁平 `StatusOption[]`);然后主件原子拆分移植。门禁同纪律节。
- **P3-B starter_prompts 政策【STOP-AND-ASK,三选一】**(~2074 LOC):
  - A) 删除 fork 3-button 本地实现,采用上游 per-agent JSONB 方案 → 移植 `f8ec870f` + `09a2410e8`(改名 conversation_starters 全程跟随,**只 port 一套架构**,triage re-grade 明示二选一);
  - B)(默认推荐)保留 fork 实现,正式 SKIP 两提交并在 lifecycle/release notes 记录理由;
  - C) 共存设计(fork EmptyState 外挂上游数据源)——成本最高,除非用户明确要 per-agent 开场语,否则不建议。
- **P3-C source-context sub-issue `8c563b49`(10K LOC)【决策门,默认 SKIP】**:SKIP 则连带 SKIP MUL-6660(B5-locale)一并记录;若用户要该功能→排多会话专项(先 viewer/comment-list/preview modal + ~40 i18n keys×4 locale + anchor_comment_id plumbing)。
- **P3-D sweeper `e5f976144`**(source_context_sweeper.go 新文件):与 fork `experimental/runtime_gc.go`(swarm_gc tick 承载)做调度对照审查 → APPLY / 适配(合并 tick)/ SKIP 三选一,小批处理。
- **P3-E(可选,默认 defer)ZeroClaw ACP backend `8a5a6adb`(+1694)**:前置证明其与现有 openclaw 非克隆关系再议。
- **P3-F 增量插入批**:P0-1 re-triage 产出的 APPLY 小批(CLI/UX/docs 型)按既定纪律插队处理。

## 执行纪律(每 commit/批次通用,沿用 0.5.77 lessons)

1. 单 cherry-pick 逐个落地;逐 commit 门禁:`pnpm typecheck` + `cd server && go test -count=1 ./internal/... ./pkg/agent/...`。
2. >200 LOC 上游提交必须拆原子子提交;可用 `tmp/cp-bX-*` worktree 并行(0.5.76/77 先例),但每个 worktree 独立验证后才合入。
3. 每个 STOP(冲突/分歧/排除)写入当期 release notes:blocker 文件 + 所需前置 + 下一步建议。
4. 触碰 catalog/RuntimeKind/manifest 的任何改动必须 refresh `.omc/labs-runtime-lifecycle-map.md`(`06852ecbb` 条款)。
5. migration 类 cherry-pick 先对比 fork `server/migrations/` 历史(mig 155–273 为 fork 侧),禁止直接套上游编号。
6. 正文聊天使用中文沟通;文档遵循仓库双语惯例(key 描述中英并列)。

## 发布切分

| 版本 | 内容 | 出厂门禁 |
|---|---|---|
| **0.5.78** | Phase 0 全部 + Phase 1 全部 + P2-1 高优测试 | typecheck 6/6;go test 三个锁定失败关闭或书面说明;pythia-smoke 绿;ship-mac 7/7;lifecycle-map 复核记录 |
| **0.5.79** | Phase 2 其余 + P3-A(inbox 归档链) | 同上 + inbox e2e 手工清单 |
| **0.5.80+** | P3-B/C/D/E 按决策门结果分批 | 同上 |

## 工作量与风险总览

| Phase | 工作量 | 主要风险 | 缓解 |
|---|---|---|---|
| 0 | 0.5 天 | 锁定测试修复可能牵出 dashboard 数据语义问题 | bisect 信息已有;必要时改断言+书面理由 |
| 1 | 1 天 | openscience 决策若选 A 需外部构建链 | 推荐选 B;A 作为后续独立任务 |
| 2 | 1–1.5 天 | 手工冒烟依赖 desktop 打包环境 | 用现有 pythia-smoke + package.mjs 流程 |
| 3 | 1.5–2.5 天 | P3-A 铺垫链可能暴露更多 fork 分歧文件 | 铺垫批先行、逐 commit 门禁、随时 STOP 记录 |

## 相关文件

- `.omc/upstream-integration-triage-2026-08-26.md`(G3 唯一分诊真源)
- `.omc/audit/2026-08-23-labs-issue-integration-audit.md`(G1/G2 缺口真源)
- `.omc/plans/lab-environment-0.5.18-plan.md`(前作计划,大缺口已闭)
- `.omc/labs-runtime-lifecycle-map.md` / `.omc/lab-skill-coverage-matrix.md`
- `.omc/release-notes-0.5.7{1..7}.md`(Batch 1–5 落地详情)
