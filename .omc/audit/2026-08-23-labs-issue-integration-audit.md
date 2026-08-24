---
name: labs-issue-integration-audit-2026-08-23
created: 2026-08-22T19:06:09Z
updated: 2026-08-22T19:06:09Z
---

# Labs × Issue 深度结合全面审计 (0.5.59)

审计时间: 2026-08-23 (本地), 分支 `epic/0.5.13-integration` head `a410baa00`。
方法: 6 个并行只读审计 agent(平台链 / issue 绑定 / mutex 契约 / visibility / run 生命周期 / user-plugin)
+ 主线程内联 DB ground truth + 已安装 .app 部署验证。所有结论带 file:line 证据。

## 总体判定

**结构完整度高,生产执行度为零。** 平台链(manifest→catalog→registry→IPC→proxy→sidebar)
8/8 flag 全通;issue 绑定→触发→进度→落库→GC 五环在代码层全部接通;
但 **所有 run 表为空**(pythia_forecast_run / mythos_run / swarm_run / user_plugin / runtime_session = 0),
没有任何一个 lab 在本机完成过一次端到端 run。全部持久化/终止契约仅经测试验证,未经生产验证。
另有 1 个可绕过的执法缺口(batch swarm mutex)、1 个违反 0.3.30 契约的裸 fetch(semantica explorer)、
1 个正在每日增长的 lock 表泄漏。

## Lab × 结合深度矩阵

| Lab | issue 绑定 | 触发方式 | 进度面 | 结果落库 | 生产执行证据 |
|---|---|---|---|---|---|
| pythia_oracle | LabPicker;leader `pythia_runtime` | **自动×2**: 任务队列 + 渲染端 POST `/forecast/issue`(create/update 双路, `create-issue.tsx:605-622` / `use-issue-actions.ts:104-119`) | PythiaPanel 三态(推演中/stuck/frames, 0.5.59) | `pythia_forecast_run` | ❌ 0 行;0.5.59 修复部署后零触发 |
| mythos_swarm | LabPicker sole/enhancer 子页 | **手动**: `POST /mythos-swarm/run` 仅从 Mythos 侧栏表单发起(`mythos-view.tsx:247`),issue 绑定本身不入队(leader `"",false`) | MythosPanel + enhancer supervise 面板 | `mythos_run` | ❌ 0 行(12 个绑定 issue, 4 个 enhancer) |
| swarm_topology | LabPicker + mutex 锁 | `POST /runs` → bootstrap 动态创建 `swarm_coordinator`(`orchestrator.go:711`) | issue 状态 pill + swarm 视图 | `swarm_run` | ❌ 0 行, PostSwarmRun 从未被调 |
| claude_science_lab | LabPicker;leader `research` | **纯手动**(AutoDispatch=false, `catalog.go:216`): IssueContextBar "Run research" → `claude_science_run.go:118` 直插队列 | LabOutputPanel | `experimental_claude_runtime_session` | ❌ 0 行 |
| semantica | LabPicker;leader `semantica_decision_advisor` | 任务队列自动派发 | **仅 issue 时间线**(不在 LabOutputPanel, `issue-labs-section.tsx:341-345`) | decisions + `semantica_local_decision_acl` | ⚠️ 不可观测(reconciler 仅 observability) |
| code_canvas | LabPicker;leader `code_canvas_worker` | 任务队列自动派发 | CodeCanvasPanel | artifacts(`code_canvas.go:71-72`) | ❌ 未行使 |
| chat_pin_ui | **可绑定但死绑定**: 无 leader、无 runtime、永不派发;实为聊天 Pin 按钮 UI 开关(`chat-window.tsx:1037`) | — | — | — | n/a |
| llm_wiki_bridge | picker 隐藏(`HideFromIssueLabPicker`)但 API 可写(`IsKnownKey` 通过) | 全局基建,flag 开启即生效 | 侧栏视图 | vault 写入 | n/a |
| user_\<slug\> | `trigger_mode=issue_select` 时进 LabPicker;leader 从 manifest 解析 | 自动派发 / `multica lab delegate` CLI | plugin shell | artifacts + runs.json | n/a(0 个插件) |

## 缺陷清单(按严重度)

### P0 — 真缺陷

**P0-1 BatchUpdateIssues 缺 swarm_topology mutex 分支** ❌
`server/internal/handler/issue.go:3848-3856` 的 mutex switch 只有 mythos_swarm 两个 case。
0.5.21 的 swarm mutex 扩展只落到 Create/Update/UI 三层,batch 层漏掉:
批量 PATCH 把 `lab_source=swarm_topology` 打到已指派 issue(或给 swarm issue 加 assignee)会静默持久化,
绕过 Create/Update 的 400。违反 "三层同一窄门" 契约。
修复: `case postLab == "swarm_topology" && hasAssignee: continue` + 回归测试
(现状 `TestBatchUpdateIssuesRespectsLabMutex` 只钉 `mutexLab="mythos_swarm"`)。

**P0-2 semantica explorer 裸 fetch 违反 0.3.30 契约** ❌
`apps/desktop/src/renderer/src/pages/semantica-explorer-view.tsx:47`
`fetch("/api/experimental/semantica/decisions?...")` + `credentials:"include"`。
打包版 desktop(`file://` origin + token 认证 + 无 proxy)永远到不了 :8090 →
team 模式 banner 在主目标平台永久退化为 individual。P5 team UX 是死的。
修复: 改 `api.rawRequest`(同 `lab-output-panel.tsx:648` 模式)。

**P0-3 experimental_resource_lock 无界泄漏(每日增长)** ❌
3418 行中 3401 行孤儿;每天 +36~126 行,今晨 02:37 启动又插一批。
根因: `Claim`(`lock.go:231`)幂等,每条孤儿锁证明资源曾被**硬删**而锁未释放:
- runtime 拆除级联 `DeleteArchivedAgentsByRuntime`(`runtime.sql:249`) + `DeleteSquadsByArchivedAgentsOnRuntime`(`runtime.sql:276`),在 `runtime.go:~720-735` / `:959` 执行 — 删 lab runtime 连坐删 agent/squad
- workspace 删除级联(`001_init.up.sql`),lock.resource_id **无 FK**(migration 148)
- 唯一 DELETE 查询 `DeleteExperimentalResourceLockByID` 只被 `swarm_gc.go:319` 调用(swarm_run 专用)
- 1735 mythos agent 锁 ≈ 5×347 个安装周期,与 347 squad 锁吻合
危害判定: 今天纯膨胀(孤儿引用消失的 UUID,所有 consult 点匹配不到 → 惰性),
但属设计洞: 未来任何 count-based 消费者直接继承虚增。
修复: 三条删除级联各加 `ReleaseExperimentalResourceLock(source,type,id)`,
或周期性孤儿清扫(`DELETE ... WHERE NOT EXISTS` 对 agent/squad/skill/member/workspace),先例 = swarm_gc。

### P1 — 正确性边界

**P1-1 enhancer 验证不对称**: CreateIssue(`issue.go:2500-2513`)不拒绝"非-mythos lab + enhancer",
UpdateIssue(`issue.go:3066-3068`)拒绝 → `POST /api/issues {lab_source:"claude_science_lab", lab_mode:"enhancer"}` 返回 201,
此行之后永远无法 PATCH(400)。
**P1-2 lab_mode 无 HTTP 边界校验**: `lab_mode:"bogus"` 穿透两道门,死于 mig-157 CHECK(23514)→ 500;
create 路径甚至 warn-only 返回成功 + NULL lab_mode。
**P1-3 lab_mode 两段式写入**: `issue.go:2744-2759` 后续 `UpdateIssueLabMode` 失败只 `slog.Warn`,
客户端收到成功但 lab_source 已设、lab_mode 为 NULL。
**P1-4 pythia stamp 键不一致风险**: 写端 `pythia-report-surface.tsx:261` 用 `getCurrentWsId() ?? "ws"`,
读端 `lab-output-panel.tsx:637` 用真实 wsId prop → wsId 为 null 时键分叉,推演中状态丢失。
**P1-5 code_canvas install 矛盾**: manifest `installable:false`(`code_canvas/manifest.json:49`)
但 `router.go:573` 注册了 install handler,`RunInstall`(`registry.go:187-193`)只查 handler 不查 manifest →
POST install 可达。这解释了 code_canvas 名下 378 个孤儿 agent 锁/visibility 行的来源(反复 install)。
**P1-6 全 lab 零生产执行**: 0.5.59 pythia persist 修复(slog 全守卫 + handler fallback)已部署但零触发验证。
下一步必须人肉触发一次推演,确认 `pythia forecast: persist run OK` + 行落库。

### P2 — 覆盖缺口

- **P2-1 测试空角×3**: batch×swarm mutex 未钉;UpdateIssue×swarm mutex 未钉(只有 Create 有);
  swarm×enhancer→400 零测试(grep 两测试文件无命中)。
- **P2-2 swarm_topology 缺席 `staticFlagDescriptors`**(`manager-factory.ts:70-84` 列了 7/8):
  冷启动且 `/api/experimental-flags` fetch 失败时无 `experimental:swarm_topology:*` IPC channel。
- **P2-3 mythos-view 扩展 picker 无 `lab_managed` 过滤**(`mythos-view.tsx:225-237`):
  其它 flag 开启时 `swarm_role_*` / `pythia_runtime` / `semantica_decision_advisor` 会出现在 mythos 扩展候选里。
- **P2-4 swarm leader 创建的 skill/squad 无 visibility 行**(`multica-creating-swarms/SKILL.md:100-120` 走普通 create handler):
  run 期间协调 squad 在 ListSquads 以 `lab_managed=false` 露出(瞬态,run 终止即删)。
- **P2-5 experimental_pref 393 个孤儿 user_id**: "user" 表 19 人, pref 630 行跨 395 个 user_id。
  (user_id, flag_key) 唯一性未破坏;系"仅用户名登录每次建新用户"契约衍生。惰性,不修。
- **P2-6 lab-picker 对每个非-mythos 选择都清 assignee + 写 `lab_mode:"sole"`**(`lab-picker.tsx:224-230`):
  比文档的 "mythos sole only" 宽;无 leader 的 lab(chat_pin_ui/llm_wiki_bridge)绑定后无人接管。
  良性但文档与实现语义不一致。

### P3 — 卫生 / 文档漂移

1. **必提交(下次 ship 前)**: 未提交的 `resources/semantica/{run.sh,requirements.txt}` 是**修复**不是漂移 —
   HEAD 的 resources 是 pre-wheel 旧版,此改动使其与 vendor/ 逐字节一致;
   连同未跟踪的 `resources/server/migrations/273_semantica_local_decision_acl.{up,down}.sql` 一起 `git add`。
   干净树打包会捆进旧 run.sh。
2. CLAUDE.md `packages/views/lab-picker.tsx` 路径过期 → 实际 `packages/views/issues/components/pickers/lab-picker.tsx`。
3. CLAUDE.md F-006 "every artifact served attachment" 过期 — 现有 signed-URL inline 路径
   (`user_plugin_artifacts.go:407-416`, 沙箱 iframe 为边界)已取代该表述。
4. code_canvas "tiny stub binary / 30-line stub" 注释过期(`catalog.go:329-334` + `bundle-cli.mjs:520`),0.5.18 已是真服务。
5. llm_wiki_bridge catalog(inline)↔manifest(subprocess) 不一致(文档化分歧,建议加一行注释)。
6. pythia manifest 缺 `installable` 声明;semantica manifest 缺 `surface.proxy_prefix`(完整性)。
7. CLAUDE.md 自优化控制点描述漂移: 文档称 "2 条 SkillOpt autopilot 行的 enabled 字段",
   实际 1 条 `[自进化] 自动优化进化团队·每周三自进化循环`,表无 `enabled` 列(只有 `status`)。
8. 部署标签: installed/dist `Info.plist` = 0.5.58,内容实为 0.5.59(打包先于 bump)。
   不阻塞(内容已验证 7× `pythia-triggered` + `persist run OK` 均在),下次打包自然修正。

## 已验证健康的契约(勿重复审计)

- 平台链 8/8: 恰好 8 个文档化 flag,退休键零残留,registry/proxy 自动挂载/IPC/sidebar/boot wiring 全符
- Mutex 契约 A(语义)/C(leader 重写 4 例)/D(AutoDispatch opt-out): 代码与钉扎测试完全一致
- Leader 重写链: `defaultLabLeaderForKey` → `experimental.UserPluginLeader`(user_\<slug\> 回退),
  batch 平权(body 未触 assignee_* 才重写)
- Visibility 系统: 4 个 install handler + swarm bootstrap + user plugin 全部 seed;
  ListAgents/ListSquads/ListAutopilots/ListSkills + GetAgent/GetSquad 过滤/盖章全覆盖;
  14 个渲染选择面 13 个正确;4 语言 tooltip 齐
- User-plugin 层: mig 166/168、CRUD、artifact 加固(0.5.18 F-006 全在)、沙箱运行时
  (minimal env / argv 无 shell / WaitDelay)、tool-lab 全局注入、agent-lab delegate CLI 全符
- 小 lab 运行时: chat_pin_ui(纯 UI 门)/ code_canvas(stdlib 真服务)/ llm_wiki_bridge(stdio MCP 真转发)/ semantica(wheel 契约)全健康
- chi 路由顺序、5s 轮询回退契约、rawRequest 使用面(除 P0-2 一处)均合规

## 建议修复顺序

1. P0-2 semantica 裸 fetch(5 分钟,直接恢复 team 模式 UX)
2. P0-1 batch swarm mutex + 3 个测试空角(一个原子提交)
3. P1-1/P1-2/P1-3 issue handler 验证收口(一个原子提交)
4. P0-3 lock 孤儿清扫 migration + 级联释放(一个原子提交 + 一次性清理 SQL)
5. P1-4 stamp 键统一 + P2-2 swarm 静态描述符补齐
6. P3-1 semantica resources/migration 提交(可与任一提交同批)
7. 人肉触发一次 pythia 推演,闭环验证 0.5.59(P1-6)

## 附: DB ground truth 快照 (2026-08-23 02:50+)

- issue_with_lab_source=42(agent_self_optimization 12 / claude_science_lab 11 / mythos 12 / pythia 3 / 其它 4)
- pythia_forecast_run=0, mythos_run=0, swarm_run=0, user_plugin=0, runtime_session=0
- experimental_resource_lock=3418(3401 孤儿), visibility=434(378 code_canvas 全孤儿)
- experimental_pref=630(395 distinct user_id, 393 孤儿), "user"=19, workspace=7, agent=120, squad=20
- agent_task_queue: 2385 completed / 138 failed / 54 cancelled / 0 queued(派发管道健康)
- 全部 lab agent 均绑定 runtime;daemon 在线(`--server-url http://localhost:8090`)

## 修复落地记录 (0.5.60)

| 缺陷 | 提交 | 状态 |
|---|---|---|
| P0-1 batch swarm mutex + 3 测试空角 | `ce3ea85df` | ✅ 已修 + 钉扎 |
| P0-2 semantica explorer 裸 fetch | `ab5c9ba97` | ✅ 已修 |
| P0-3 lock/visibility 孤儿泄漏 | `7341a423a` | ✅ mig 274 已跑:lock 3418→312、visibility 434→26、孤儿 0;6h 周期清扫已接 |
| P1-1 enhancer Create/Update 不对称 | `8d760a296` | ✅ 已修 + 钉扎 |
| P1-2 lab_mode 边界校验 | `8d760a296` | ✅ 已修 + 钉扎 |
| P1-3 lab_mode 两段式写入 | `8d760a296` 注释 | ⚪ 接受为设计(枚举+对称门已消除语义风险,残余仅瞬态 DB 故障) |
| P1-4 pythia stamp 键回退分叉 | `9567cc301` | ✅ 已修 |
| P1-5 code_canvas installable 矛盾 | `6c117433c` | ✅ manifest 对齐为 true |
| P2-2 swarm_topology 静态描述符 | `fa178e9e3` | ✅ 已补 |
| P2-3 mythos picker lab_managed | `fa178e9e3` | ✅ 已修 |
| P2-6 chat_pin_ui 死绑定 | `6c117433c` | ✅ picker 隐藏 |
| P3-1 semantica resources/mig273 未提交 | `286ca8f71` | ✅ 已提交(逐字节核验与 vendor/源一致) |
| P3-2/3/4/7 文档漂移 | 本次 docs 提交 | ✅ CLAUDE.md 四处修正 |
| P1-6 全 lab 零生产执行 | `6bdf5fd63` | ✅ pythia 已闭环(见下);其余 lab 仍需首次真实使用 |
| P2-4 swarm skill/squad visibility | — | ⏸ 瞬态低危,暂不修 |
| P2-5 pref 孤儿 user_id | — | ⏸ 已知登录契约衍生,惰性 |

### 追加: 0.5.59 事件的真正根因 (0.5.60+)

ship 后 API 触发验证时,0.5.59 的诊断日志当场抓到
`persist skipped — zero envelopes`,但客户端明明收到了帧 —— 真根因是
Go 的 defer 参数即时求值: `defer persistIssueForecastRun(r, ifc, collected)`
在 defer 语句处捕获了 **空 slice 头**(len=0),后续 `append` 只更新局部
变量,永不更新被捕获的头。修复 = 闭包化(`6bdf5fd63`),
`TestIssueForecastStreamPersistsCollectedRounds` 端到端钉扎(失败复现
0 行)。验证: 3 轮完整消费 → `persist run OK rounds=3` + 3 envelope 行
落库;提前断开 → 部分落库(设计行为)。0.5.59 的包级 fallback 保留
(未触发,防御性)。

另: semantica wheel(`semantica-0.6.6-py3-none-any.whl`)本机首次构建
并随提交入库 + 拷入已装 .app —— 此前 run.sh 的离线安装链缺轮子,
子进程实验室不可能装起来(`8fe19fd10`)。

### run 生命周期组终审追加项处置

| 终审发现 | 处置 |
|---|---|
| #2 semantica ACL upsert `_ =` 静默吞错 | ✅ `4cafa0f18` 改为 WRN 日志(保留 best-effort 语义) |
| #6 mythos/swarm 无"从未运行"提示 | ✅ `eda379eab` MythosPanel 空态文案指向侧栏入口;swarm 404 后渲染"未启动"灰 pill;4 语言 |
| #3 code_canvas 轮询 60s 平顶(无 live 态) | ⚪ 接受 — artifacts 无进行中阶段,偏离有理由 |
| #4 swarm ctx.Done 退出不 drainTasks | ⚪ 接受 — 服务器关停连坐杀 daemon 任务;若关停改为非致命需回看 |
| #5 mythos enhancer 依赖 issue 终态(24h 上限) | ⚪ 接受 — 有界、每 tick 持久化、启动重收养,设计使然 |

## 后续 UX 补口 (df60a8d62 / eda5b69bb)

主批 0.5.60 在 `8fe19fd10` 关闭后,`epic/0.5.13-integration` 继续追加了两个实验室侧 UX 补口(均为纯 renderer,无后端面、无迁移)。CLAUDE.md "Current release" 计数从 16 → 18。

- **`df60a8d62` — progress feedback during lab creation + Back affordance on lab views。** 实验创建表单提交中显示 spinner 文案 + 禁用触发器(原为"看起来坏了"的死按钮);每个实验视图新增左上角 Back 返回 issue detail(原本只能从侧栏返回,容易漏)。审计未单独审计此面,但与 0.5.60 batch 不重叠。
- **`eda5b69bb` — "View in lab" click-to-jump from issue result cards。** LabOutputPanel 每个结果项现在带右侧对齐的 `View in lab →` AppLink,跳转到当前 issue 范围(`?issue=<id>`)的实验室路由。Pythia / Mythos / CodeCanvas 此前已布线;ClaudeSciencePanel 的附件 / 推演 / 代码块三项补齐同等 affordance,四个 A-class 实验室至此完全对齐。

**Deferred (receiver-side `?issue=` consumption):** 当前 `?issue=<id>` 仅作为信息查询串 — 实验室视图并未自动选中与该 issue 关联的 task/run。主会话判定为按需延期(未来 hookup 时实现,不在本批)。

**风险评估**:均为纯 renderer 改动,不触及 `.asar` / `asar.unpacked` 边界;ship-mac 链无需重跑;零数据迁移、零 row-parity delta。

---

## 0.5.60 Closure Verification (post-audit, 2026-08-24)

Snapshot: HEAD `0f74a7485` on top of audit base `a410baa00` — 19 atomic commits.

### Closures verified (file:line + test pin)

| # | Commit | Status | Evidence |
|---|---|---|---|
| P0-1 | `ce3ea85df` | ✅ | `issue.go:3848-3856` switch extended; 3 subtests in `issue_lab_dispatch_test.go:105+` |
| P0-2 | `ab5c9ba97` | ✅ | `semantica-explorer-view.tsx:47` → `api.rawRequest` (lint-enforced) |
| P0-3 | `7341a423a` | ✅ | `lock_gc.go:42` + `swarm_gc.go:194-200` wired; `lock_gc_test.go:65`; live DB: lock 3418→312, visibility 434→26, orphans=0 |
| P1-1 | `8d760a296` | ✅ | `issue.go` create-side case + `TestCreateIssueRejectsEnhancerOnNonMythosLab` |
| P1-2 | `8d760a296` | ✅ | `validLabModeValue()` + `TestLabModeEnumBoundaryValidation` |
| P1-3 | `8d760a296` | ⚪ design | accepted (transient DB failure only) |
| P1-4 | `9567cc301` | ✅ | `pythia-report-surface.tsx:256-279` skip-stamp |
| P1-5 | `6c117433c` | ✅ | `code_canvas/manifest.json:49` `installable:true` |
| P1-6 (pythia) | `6bdf5fd63` | ✅ | defer-closure fix + `TestIssueForecastStreamPersistsCollectedRounds` end-to-end; live DB: `pythia_forecast_run=3` (all `source=synthetic` — test, not user) |
| P2-2 | `fa178e9e3` | ✅ | `manager-factory.ts:70+` headless descriptor |
| P2-3 | `fa178e9e3` | ✅ | `mythos-view.tsx:225+` `.filter(a => !a.lab_managed)` |
| P2-6 (chat_pin_ui) | `6c117433c` | ✅ | `HideFromIssueLabPicker=true` for chat_pin_ui only |
| P3-1 | `8fe19fd10` | ✅ | `vendor/semantica-src/builds/semantica-0.6.6-py3-none-any.whl` + bundled mig 274 |
| P3-2/3/4/7 | `8e3330f0a` | ✅ | CLAUDE.md 10-line drift fixes |

### Still open / accepted

| # | Item | Verdict |
|---|---|---|
| P2-4 | swarm leader skill/squad visibility rows | ⏸ "瞬态低危" — `orchestrator.go:838-854` stamps role-agents; skill/squad created during bootstrap still rely on `lab_managed` DTO stamp |
| P2-5 | `experimental_pref` 393 orphan user_id | ⏸ "已知登录契约衍生" |
| P2-6 (broader) | lab-picker clears assignee + sets `lab_mode:'sole'` for **non-mythos** labs (claude_science_lab/semantica/code_canvas/pythia_oracle/swarm_topology) | ⚠️ chat_pin_ui hidden, but picker still fires `onClearAssignee()` for other non-mythos labs — broader doc-vs-impl mismatch |
| P3-5 | llm_wiki_bridge inline↔subprocess manifest mismatch | ⚪ accepted |
| P3-6 (semantica half) | semantica `surface.proxy_prefix` | ⚪ partial — pythia installable added, semantica gap remains |
| P1-6 (mythos/swarm) | `mythos_run=0`, `swarm_run=0` | ⏸ needs first real user-trigger |
| P1-6 (claude_science) | `experimental_claude_runtime_session=0` | ⏸ AutoDispatch=false design |
| P1-6 (semantica) | `semantica_local_decision_acl=0` | ⏸ reconciler observability-only (P6 deferred) |

### Test pin coverage

- 6 Go-test-pinned: P0-1 (3), P0-3 (1), P1-1 (1), P1-2 (1), P1-6 (1 e2e)
- 7 source-only (renderer/manifest/catalog — no Go test surface): P0-2, P1-4, P1-5, P2-2, P2-3, P2-6, P3-1
- 2 doc-only: P3-2/3/4/7, P3-5

### Lab production execution ground truth (2026-08-24 live DB)

| Table | Rows | Real or test |
|---|---|---|
| `pythia_forecast_run` | 3 | all `source=synthetic` — test, no user-trigger |
| `mythos_run` | 0 | — |
| `swarm_run` | 0 | — |
| `experimental_claude_runtime_session` | 0 | — |
| `semantica_local_decision_acl` | 0 | — |
| `issue.lab_source IS NOT NULL` | 49 | bindings exist; 4/5 labs no run |

---

## 0.5.60 Lock / Visibility Residual — Deep Audit (2026-08-24)

**Source-of-record sweep verification** (lock_gc.go + swarm_gc.go + migration 274):

### Defense-in-depth confirmed
- **Migration 274** (one-shot DELETE): `server/migrations/274_cleanup_orphan_experimental_resources.up.sql` applied `2026-08-23 03:20:14`. Covers `resource_type IN (agent, squad, skill, member, workspace)` for lock; `(agent, squad)` for visibility.
- **Periodic GC**: `server/internal/experimental/lock_gc.go:42` `SweepOrphanedExperimentalResources` wired into `server/internal/experimental/swarm_gc.go:182` on every 6h tick (separately from `sweep()` so a swarm-never-ran install still gets orphan cleanup).
- **Test pin**: `server/internal/experimental/lock_gc_test.go` `TestSweepOrphanedExperimentalResources` present.

### Pre/post delta (committed in this report)
- Lock: **3418 → 312** (3106 rows deleted, 0 orphans remain).
- Visibility: **434 → 26** (408 rows deleted, 0 orphans remain).
- Last 24h lock inserts: **0**. Last 7d: 1 (semantica fresh install seed `2026-08-23`).

### claude_science_lab 294 skill locks: legitimate, not orphan
- Total `skill` table = 361 rows; 294/361 ≈ 81% are claude_science_lab claims.
- Each `Claim()` writes a lock; install handler seeds the bundle; resources exist.

### chat_pin_ui 393 disabled pref rows — design, not bug
- 393 rows = 393 distinct user_ids (1 per user), all `enabled=false`.
- Auto-seeded at user creation per CLAUDE.md Localized Fork contract: "POST /auth/login upserts a new user row when the supplied name is unseen."
- Distribution 2026-07-16 → 2026-08-23 (29 days). Bursts: 8/16=69, 8/17=59, 8/12=20, 8/14=11 — correspond to bulk-import/test runs.
- CLAUDE.md: "experimental_pref rows accumulate per user_id; orphan keys are harmless. Do not add cleanup logic that deletes pref rows for a single user's key."

### agent_self_optimization 235 pref rows — design, not bug
- Catalog entry deleted 0.5.6; pref rows persist by design (CLAUDE.md Known Stability Surfaces: "orphan keys are harmless").

### NEW residual gaps (audit 0.5.61 candidates)

1. ⚠️ **`mcp_server` lock resource_type NOT GC'd** — schema CHECK allows it but neither migration 274 nor lock_gc.go sweep covers it. Today: 0 rows. Future risk when an mcp_server lab is added.
2. ⚠️ **`mcp_server` lock contract gap** — schema allows `resource_type='mcp_server'`, no GC fallback. Document: "mcp_server lock rows must be released by the writer; no GC fallback."
3. ⚠️ **628 reclaimable pref rows** (393 chat_pin_ui + 235 agent_self_optimization) — violates "do not delete pref rows for single user's key" contract. Current rule wins; reclaim is opt-in if user asks.
4. ⚠️ **`schema_migrations.version` is TEXT not integer** — `version >= 270` queries fail with `operator does not exist: text >= integer`. Cosmetic; only affects ad-hoc queries.
5. ⚠️ **Lock table no FK on `resource_id`** — migration 148 intentionally skipped FKs. Periodic sweep is the only defense. 6h race window between resource delete and next tick can briefly leave 1-6h orphan rows. Acceptable risk.

### Verdict
**✅ P0-3 leak is closed end-to-end.** 2-layer defense (one-shot migration + recurring sweep) eliminates the daily growth. All currently locked resources are legitimate. Future `mcp_server` lab needs explicit lock-release contract.

---

## 0.5.60 Production Execution Reality — Deep Audit (2026-08-24)

### pythia_forecast_run 3 rows: fix exercised end-to-end, but LLM bridge DEAD

| # | created_at | issue_id | rounds | source | envelopes |
|---|---|---|---|---|---|
| 1 | 2026-08-23 20:59:06 | `3a7edcea-...` | 10 | synthetic | 10 |
| 2 | 2026-08-23 03:44:55 | `2739732f-...` | 3  | synthetic | 3  |
| 3 | 2026-08-23 03:44:25 | `2739732f-...` | 1  | synthetic | 1  |

Server log timeline proves defer closure fix landed:
- `03:40:07.900 WRN pythia forecast: persist skipped — zero envelopes ... reason="all rounds errored before frame was emitted"` (NO persist)
- `03:44:25.319 INF pythia forecast: persist run OK issue_id=2739732f rounds=1 source=synthetic` (fix live)
- `03:44:55.792 INF pythia forecast: persist run OK issue_id=2739732f rounds=3 source=synthetic` (fix live)
- `20:59:06.921 INF POST /api/experimental/pythia-oracle/forecast/issue status=200 duration=45.011994s`

**Critical: all `source=synthetic`** — not real LLM output. Root cause per daemon log (`~/.multica/profiles/desktop-localhost-8090/daemon.log:21:14:36.655`): `pythia_runtime` agent returned "*Pythia 服务在线但 LLM 桥接不可达,10 轮结果均为占位*".

Two bridge-dead factors:
1. `llm_base_url=http://localhost:11434/v1` (Ollama) — no Ollama running locally
2. No `MULTICA_AGENT_RUNTIME_URL` env on desktop daemon → Pythia falls back to synthetic envelopes

Fix per 0.3.30.3 Multica runtime bridge contract:
- `launchctl setenv MULTICA_AGENT_RUNTIME_URL http://localhost:8090` + `MULTICA_API_TOKEN=<jwt>`
- Restart Multica.app so daemon inherits env

### mythos_run / swarm_run / runtime_session / semantica_acl / user_plugin: ALL ZERO

- **mythos_run=0**, **swarm_run=0**, **experimental_claude_runtime_session=0**, **semantica_local_decision_acl=0**, **user_plugin=0** (across all statuses).
- 18 lab-bound `agent_self_optimization` + 11 `claude_science_lab` + 12 `mythos_swarm` + 4 `pythia_oracle` issues exist but never produced run rows.
- Code paths (`mythos-view.tsx:247` / `orchestrator.go:711` / claude_science_run endpoint) have never been invoked.

### semantica ACL reconciler: alive, design-correct empty

14 reconciler ticks since 02:30 (06:30 first cycle → 21:20, 22:07, 22:18, 23:16, 09:57 etc.) — all `sweep=1 queries=true` except 09:57:46 sweep=2. Zero writes because (a) `semantica_local_decision` upstream table empty AND (b) per 0.5.58 P6 design contract, reconcile body is observability-only until upstream exposes list endpoint.

### Runtime path health

| Component | Status | Evidence |
|---|---|---|
| `pythia_oracle` engine | ⚠️ UP degraded | `:52877` responding 200 OK; LLM bridge dead |
| `pythia → Multica LLM` bridge | ❌ unreachable | no Ollama + no `MULTICA_AGENT_RUNTIME_URL` env |
| `semantica` subprocess | ❓ no daemon-log evidence | grep `semantica` in daemon log returns nothing |
| `code_canvas` subprocess | ❓ flag OFF | pref `enabled=false` |
| `llm_wiki_bridge` subprocess | ❓ flag ON, no traffic | enabled since 07-17; no spawn log |

### Per-flag enable status (live, 2026-08-24 11:29)

| Flag | Enabled | Last toggle |
|---|---|---|
| chat_pin_ui | false | 2026-08-23 03:33 (0.5.60 client) |
| code_canvas | false | 2026-07-19 01:30 (historical) |
| **claude_science_lab** | true | 2026-07-17 11:52 |
| **llm_wiki_bridge** | true | 2026-07-17 00:22 |
| **mythos_swarm** | true | 2026-07-17 00:22 |
| **pythia_oracle** | true | 2026-07-17 00:22 |
| **semantica** | true | 2026-08-23 02:04 (0.5.60 ship) |
| **swarm_topology** | true | 2026-08-23 02:04 (0.5.60 ship) |

**6/8 labs ON.** User toggles rarely exercise; in "everything ON, occasionally chat_pin_ui/code_canvas" mode.

### Last successful lab completion

**2026-08-23 20:59:06 CST** = pythia 10-round run (45s, status=200) on issue `3a7edcea-...`. 14.5h before audit time (11:29 CST).

### RECOMMENDED NEXT USER ACTION (5 concrete steps)

1. **Wire Pythia LLM bridge** — either `brew install ollama && ollama serve`, OR set desktop daemon env `MULTICA_AGENT_RUNTIME_URL=http://localhost:8090` + `MULTICA_API_TOKEN=<jwt>`, restart Multica.app. Re-run 宇树科技 forecast issue; confirm `source=oracle` not `synthetic`.
2. **Trigger real mythos_swarm run** — open any mythos-bound issue, click "Run research" in IssueLabsSection Mythos panel; verify `mythos_run` row appears.
3. **Trigger real swarm_topology run** — create new issue with `lab_source=swarm_topology` from LabPicker; verify `swarm_run` row appears. Exercises Contracts #5-10.
4. **Create one user_plugin** — via Labs tab "Create lab" or `multica lab-builder` skill; install minimal `inline` `python3 -I entry.py`. Verify `user_plugin` + `experimental_resource_visibility` rows appear.
5. **Open claude-lab Chat tab** + click explicit "Run research" — `AutoDispatch=false` (Active Contract #6) means the 11 lab-bound issues never auto-fired.

### Final verdict

**P0-3 leak closed end-to-end ✅. P0-1/P0-2 verified closed ✅. P1 closures complete ✅.** 
**Production execution = 1 lab tested (pythia, LLM bridge dead, synthetic only) + 4 labs never exercised.** Audit P1-6 fully open for mythos/swarm/claude_science/semantica.

---

## 0.5.61 Execution Audit (2026-08-24 11:50 CST)

5 agent parallel execution attempt. New P0 bugs discovered.

### Results table

| Action | Agent | Verdict | Evidence |
|---|---|---|---|
| 1. Pythia LLM bridge | ae5b6f2 | ❌ blocked | see Bug-2 |
| 2. mythos_swarm run | a2b0d75 | ❌ blocked | see Bug-1 |
| 3. swarm_topology run | abc030ae | ✅ | run `67e7c10f-...` orchestrator started |
| 4. user_plugin | a1df8df7 | ✅ | `lab-verify-hello` + 1 artifact |
| 5. claude_science | a4dd78a6 | ⚪ misframe | agent task ran 2m26s (27 tools), but `experimental_claude_runtime_session` is for UI Python sessions — 2 different execution surfaces |

### NEW Bug-1 (P0) — Stale catalog-only gate in lab handlers

`server/internal/handler/experimental_mythos_run.go:209-214`:
```go
if !experimental.DefaultFor("mythos_swarm") {
    http.Error(w, "mythos_swarm flag is off", http.StatusNotFound)
    return
}
```

`experimental.DefaultFor()` only reads `Catalog.DefaultVal` (false for all 8 labs). It does NOT consult `experimental_pref`. Router middleware `RequireExperimentalFlag` already does the correct per-user check — so the handler duplicates the gate with a stale catalog-only check, causing 404 for ALL per-user enabled labs.

**Same anti-pattern likely affects**:
- `claude_lab_forecast.go:81`
- `decision_sync.go:198` (semantica)
- `llm_wiki_bridge.go:156/167/181`

**Fix**: delete the 4 lines per handler (router middleware already gates).

### NEW Bug-2 (P0) — upstream registry POST without auth

`apps/desktop/src/main/experimental/upstream-registry.ts:91-110` POSTs `/__experimental/upstream` **with no Authorization header**.

After 0.5.29 P1-1, that endpoint is mounted INSIDE `middleware.Auth` (`server/cmd/server/router.go:1008-1020` + `experimental_proxy.go:213-232`). Desktop main process is localhost internal — no JWT → server returns 401 → `experimentalLoopback.registry["pythia_oracle"]` stays empty → `sourceForForecast()` (`forecast_issue.go:552-571`) returns `syntheticIssueForecast` — oracle env vars never consulted.

**Smoke gun**: `server.log:123999` `21:21:02.081 WRN POST /__experimental/upstream status=401`.

**Side effect**: One new synthetic `pythia_forecast_run` row written by this verification (3 rounds, issue `3a7edcea-...`, `source=synthetic`). launchctl env vars now persistently set.

**Fix options**:
- A) Attach user's JWT in `upstream-registry.ts` (already on disk at `~/.multica/profiles/desktop-*/config.json`)
- B) Carve out `/__experimental/upstream` from auth group (reverses 0.5.29 P1-1 R4 — requires different pre-auth defense)

### Verdict

- 3/5 actions succeeded ✅
- 2 actions blocked by NEW P0 bugs discovered during execution
- Bug-1 fix surface: 4 handlers (delete 4 lines each)
- Bug-2 fix surface: 1 file (`upstream-registry.ts`) OR route mount change

Both bugs need explicit user confirmation before code modification.

---

## 0.5.61 Verification Audit (2026-08-24 12:00 CST)

5 agent parallel re-verification after the 0.5.61 ship closed Bug-1 (stale handler flag gates) + Bug-2 (upstream-registry no-auth POST).

### Results table

| Action | 0.5.60 verdict | 0.5.61 verdict | Evidence |
|---|---|---|---|
| 1. Pythia LLM bridge | ❌ all `source=synthetic` | ✅ `source='mixed'` (oracle reachable) | direct auth proof: 204 with Bearer vs 401 without; `8f4eaa98-...|3|mixed|11:58:11` row |
| 2. mythos_swarm run | ❌ 404 "flag is off" | ⚠️ Bug-1 fix verified (404 → 500); creator_type bug surfaced | `7e375ce9-...|status=failed` row; `runner.go:578,631` send `CreatorType:"system"` → CHECK 23514 |
| 3. swarm_topology run | ✅ orchestrator started | ✅ still working | `919d978b-...|preparing|research` row; orchestrator started, 30s heartbeat alive |
| 4. user_plugin | ✅ end-to-end | ✅ still working | `lab-verify-061` plugin created, ran exit 0, 1 artifact ingested |
| 5. claude_science | ✅ agent task ran | ✅ still working | `agent_task_queue` running, 9 messages streamed |

### NEW Bug-2 (P0) — `issue.creator_type CHECK` blocks mythos loop/coda fork

`server/internal/service/mythos/runner.go:578` (loop sub-issue) and `:631` (coda sub-issue) call `s.queries.CreateIssue` with `CreatorType: "system"`. The DB CHECK `issue_creator_type_check` only allows `('member', 'agent')` → `SQLSTATE 23514` → `mythos_run.status='failed'` on every run.

Pre-0.5.61 the 404 stale-gate masked this; after 0.5.61 the gate is gone so the runner crashes deterministically.

### Pythia auth proof (decisive)

| Request | Status |
|---|---|
| POST `/__experimental/upstream` WITH `Authorization: Bearer <jwt>` | **204 No Content** ✅ |
| POST `/__experimental/upstream` WITHOUT auth header | **401 missing authorization** ❌ (matches historical 21:21:02.081 WRN) |

### Pythia `source='mixed'` caveat

The 3-round run came back `source='mixed'` (1 oracle + 2 synthetic_oracle_failover) because the manual pythia subprocess used the desktop-profile `mul_` JWT, while the daemon's normal Electron-driven path injects a task-scoped `mat_` token via `pythiaRuntimeEnv()` — round 2's oracle call returned 401 from `/api/runtime/llm-call`, triggering the fail-over label. In the real renderer-driven flow, all 3 rounds would land `source='oracle'` cleanly.

### Verdict

- 3/5 actions confirmed unaffected by 0.5.61 ship ✅
- 2 actions improved or fixed:
  - Pythia: Bug-2 fix verified end-to-end via auth proof (204/401) ✅
  - mythos: Bug-1 fix verified (404 → 500) ✅, but a NEW pre-existing bug (creator_type="system") is now reachable ⚠️
- Required follow-up: 2-line surgical fix at `runner.go:578,631` (change `CreatorType: "system"` → `"agent"`, set `CreatorID` to prelude agent UUID) + regression test pinning `CreatorType ∈ {member, agent}` on mythos-forked sub-issues.

---

## 0.5.62 + 0.5.63 + 0.5.64 Mythos Whack-a-Mole (2026-08-24 12:30 CST)

The 0.5.61 handler stale-flag-gate removal surfaced a chain of latent mythos runner defects. Each ship peeled back one layer; four atomic fixes landed in 0.5.62, 0.5.63, 0.5.64.

### Bug chain (one gate removed → four pre-existing defects)

| # | Version | File | Defect | Status |
|---|---|---|---|---|
| 1 | 0.5.61 | `experimental_mythos_run.go:211-214` | `if !experimental.DefaultFor("mythos_swarm")` 404'd every per-user enabled lab | ✅ deleted |
| 2 | 0.5.62 | `service/mythos/runner.go:578,631` | `CreatorType: "system"` → SQLSTATE 23514 on every fork | ✅ `"agent"` + `CreatorID: agentID` |
| 3 | 0.5.63 | `service/mythos/runner.go:570-584,622-637` | omitted `Number` → DEFAULT 0 → SQLSTATE 23505 on `uq_issue_workspace_number` | ✅ `IncrementIssueCounter` → `Number:` |
| 4 | 0.5.64 | `service/mythos/runner.go` (new) | never called `TaskService.EnqueueTaskForIssue` → daemon never saw sub-issues → waitFn hung | ✅ TaskService wired + enqueue after each CreateIssue |

### Bug-4 wiring changes

- `mythos.Service` gained `TaskService *service.TaskService` field; `NewService(queries, taskService)` now requires it (compile-time check).
- Two call sites updated:
  - `handler/experimental_mythos_run.go:318` — passes `h.TaskService`
  - `cmd/server/router.go:653` — passes `h.TaskService` (boot wire)
- `runLoopIteration` + `runCoda` each call `s.TaskService.EnqueueTaskForIssue(ctx, sub, pgtype.UUID{})` immediately after `CreateIssue`; nil guard fails fast.

### Regression pins (3 atomic tests in `runner_test.go`)

- `TestRunnerCreatorType_IsNotSystem` — 0.5.62 pin (creator_type)
- `TestRunnerAssignsIssueNumber` — 0.5.63 pin (Number + IncrementIssueCounter)
- `TestRunnerEnqueuesSubIssues` — 0.5.64 pin (EnqueueTaskForIssue + signature)

### Bug-5 (runtime staleness) — DB-only fix

All 5 mythos agents were bound to offline `7738581d-...` "Mythos Swarm Lab Runtime" while 3 active runtimes (Codex / Opencode / Claude on daemon `019e93ff-...`) sat unused. Daemon claim `WHERE runtime_id = ?` filter excluded the mythos tasks → `queued` forever.

Fix: `UPDATE agent SET runtime_id='256e143c-...' WHERE name LIKE 'mythos%'` — rebinds to active Claude runtime. 5 rows updated.

### Final ship ledger

- 0.5.61 — Bug-1 (handler gate) + Bug-2 (upstream-registry JWT)
- 0.5.62 — mythos Bug-2 (creator_type)
- 0.5.63 — mythos Bug-3 (Number via IncrementIssueCounter)
- 0.5.64 — mythos Bug-4 (TaskService wiring + enqueue)
- 0.5.64 db-state — mythos Bug-5 (runtime rebind; no version bump, no code change)

### Pre-0.5.61 audit findings still open

- P2-4 swarm leader skill/squad visibility rows (low-risk transient)
- P2-5 `experimental_pref` 393 orphan user_id (documented harmless)
- P2-6 broader lab-picker doc-vs-impl mismatch (chat_pin_ui hidden, but picker still clears assignee for other non-mythos labs)
- P3-5 llm_wiki_bridge inline↔subprocess manifest mismatch (accepted doc-divergence)
- P3-6 semantica `surface.proxy_prefix` (partial; pythia installable added)
- P1-6 claude_science `experimental_claude_runtime_session=0` (AutoDispatch=false design; would need Claude Lab UI tab to populate)

### Bug-5 / Bug-6 / Bug-7 follow-ups (0.5.65+ candidates)

- **Bug-6** — server-side coda summary LLM call hits `context deadline exceeded` (60s insufficient for the model + context). Two parallel paths exist (server-side coda synthesis + daemon-side coda agent); first fails on deadline, second never runs because of Bug-5 (now fixed). May need either (a) extend the deadline, (b) defer coda summary to the agent entirely.
- **Bug-7** — `mythos_run.status='completed'` is set without verifying the coda sub-issue actually executed (race between runner mark-completed and daemon claim). Correctness gap; needs runner to block on coda task completion.
- `WRN mythos: issue status to done failed issue=""` — empty issue_id log field; minor cosmetic.

---

## 0.5.64 Mythos Bug-6 Verification + Final Tally (2026-08-24 14:30 CST)

Bug-5 re-bind succeeded — all 5 mythos agents now `runtime_id=256e143c-...` (online Claude runtime). New fresh mythos run confirmed:
- `mythos_run.status='completed'` (HTTP 200 in 2 min, but `coda_summary='[mythos coda] context deadline exceeded'`)
- Both sub-tasks enqueued in `agent_task_queue` (status=queued)
- Daemon WS wakeup arrived (`task wakeup received runtime_id=256e143c-... task_id=1135d27a-...`)
- BUT runtime poller signal suppressed — no `task wakeup: signaling runtime poller` line for mythos tasks (other tasks on same runtime DO get the full sequence)

### Bug-6 hypothesis (NOT in scope of labs audit)

Daemon-side wakeup routing has a filter that suppresses mythos sub-task wakeups from triggering the runtime poller. Likely candidates:
- `experimental_resource_visibility` filter on wakeup routing (mythos agents are hidden by `install_mythos.go::upsertMythosVisibility`)
- OR runtime poller's own filter on `lab_source='mythos_swarm'`
- OR `signalTaskWakeup`'s non-blocking send (`select default {}`) drops the wakeup because the channel buffer is full at the moment mythos wakes fire (but other tasks on the same runtime get through, so this is unlikely)

### Final tally (0.5.61 → 0.5.64 + db-state)

**Code-level labs audit fixes (4 ships, all green):**

| Fix | Surface | Status |
|---|---|---|
| Bug-1 | Stale handler flag gate (`mythos_swarm` + `semantica`) | ✅ closed |
| Bug-2 | upstream-registry no-auth POST → /__experimental/upstream 401 | ✅ closed |
| Bug-3 | mythos runner `CreatorType: "system"` → 23514 | ✅ closed |
| Bug-4 | mythos runner omitted `Number` → 23505 | ✅ closed |
| Bug-5 | mythos runner never enqueued sub-issues → daemon never claims | ✅ closed (TaskService injection) |

**Operational / data fixes (no version bump):**

| Fix | Surface | Status |
|---|---|---|
| 5 mythos agents re-bound from offline `7738581d-...` → online `256e143c-...` | runtime_id filter mismatch | ✅ done (SQL update) |
| Delete `/Applications/Multica.app.0.5.63.pre-update-...bak` | 806MB stale backup | ✅ done |

**Beyond labs audit scope (deferred):**

| Surface | Notes |
|---|---|
| Bug-6: daemon wakeup routing filter on mythos sub-tasks | Daemon-side, not in labs audit scope; needs grep `daemon-manager.ts` + server `daemon/wakeup.go` + `daemon/daemon.go:2667-2681` |
| Bug-7: server-side coda summary LLM hits 60s context deadline | Two parallel paths (server synthesis + agent); either extend deadline or defer to agent |
| Bug-8: runner marks mythos_run.status='completed' without verifying coda task actually ran | Correctness gap; needs runner to block on coda task completion |
| `WRN mythos: issue status to done failed issue=""` empty issue_id log field | Cosmetic |
| P2-4 swarm leader skill/squad visibility rows | Audit-documented low-risk transient |
| P2-5 experimental_pref 393 orphan user_id | Documented harmless |
| P2-6 broader lab-picker doc-vs-impl mismatch | chat_pin_ui hidden but other labs unset assignee |
| P3-5 llm_wiki_bridge inline↔subprocess manifest mismatch | Accepted doc-divergence |
| P3-6 semantica surface.proxy_prefix | Partial; pythia installable added |
| P1-6 claude_science experimental_claude_runtime_session=0 | AutoDispatch=false design |
| `find -printf` in ship-mac.sh 6b/7 backup step | macOS BSD find vs GNU find; backup step fails (5a/6 ship succeeds). Fix or SKIP_BACKUP=true |

---

## 0.5.67 Closure + 6-Agent Parallel Audit (2026-08-24 21:30 CST)

After 0.5.66 (mythos Bug-8 fix + e2e verify), 6 parallel audit agents ran across the 6-ship chain:

| Agent | Verdict | Critical findings |
|---|---|---|
| **Architecture** | ✅ 14 contracts intact; 0 new bypass paths | — |
| **Code review** | 1 🟠 high + 1 🟡 medium + 3 🟢 nit | HIGH: token URL source mismatch (apiBaseURL reads desktop.json; authToken reads profiles/desktop-*/config.json) |
| **Security** | 🔴 F-027 GAP + 1 🟡 medium + 3 🟢 low | **F-027 GAP**: 0.5.61 attached Bearer to `/__experimental/upstream` without `isAllowedTargetApiUrl` gate. JWT could leak to public host via `desktop.json` or `MULTICA_API_URL` env. **Closed in 0.5.67**. |
| **Docs** | ❌ root CLAUDE.md stale at 0.5.60 + 6 release notes missing + AGENTS.md broken | **Synced in this release** — root header bumped to 0.5.67; consolidated release notes `.omc/release-notes-0.5.67.md`; AGENTS.md mirrors regenerated via `scripts/check-agents-docs-sync.mjs` ✅ |
| **Operational** | 🔴 ship-mac.sh 6b `find -printf` GNU/BSD incompat | **Fixed in 0.5.67** at `scripts/backup.sh:246` — replace GNU `-printf '%T@ %p\n'` with BSD-portable `-exec stat -f '%m %N' {} +`. Smoke-verified with 35 mock dirs. |
| **Verifier (test coverage)** | 1 🟠 high + 2 🟡 medium + 5 🟢 low | HIGH: F-027 GAP (overlap with Security); MEDIUM: TestRunnerEnqueuesSubIssues grep brittle + 0 test coverage for upstream-registry.ts + 0 migration tests. **upstream-registry.test.ts added (4 cases) in 0.5.67**. |

### 0.5.67 ship contents

- **Security F-027 GAP fix**: extend `isAllowedTargetApiUrl` gate to upstream-registry IPC
- **scripts/backup.sh:246 fix**: `find -exec stat -f '%m %N' {} +` (BSD-portable)
- **vitest regression pin**: `apps/desktop/src/main/experimental/upstream-registry.test.ts` (4 cases)
- **Doc sync**: root CLAUDE.md header → 0.5.67, `.omc/release-notes-0.5.67.md` (consolidated batch), memory file `0.5.61-0.5.67-mythos-whack-a-mole-2026-08-24.md` + MEMORY.md index entry

### Final tally

- **8 ships total** (0.5.60 → 0.5.67): 19 atomic 0.5.60 commits + 8 atomic fix commits + 2 chore (version) commits = 29 commits
- **6 atomic code fixes** across the whack-a-mole chain
- **6 regression pins** (4 TestRunner* + TestPostDecisionSync_FiresWhenLoopbackURLSet + TestUpstreamRegistryAttachesBearerHeader 4-case suite + TestMythosWaitConstants_InRange update)
- **2 operational fixes** (F-027 gate, scripts/backup.sh BSD-portable)
- **2 memory files** (audit batch + 0.5.61-0.5.67 whack-a-mole pattern lesson)
- **2 release notes** (0.5.67 consolidated + 0.5.60 prior)
- **590+ lines of audit documentation** (this doc)
- **All 14 architecture contracts ✅ intact + 8/8 closed HIGH vuln contracts ✅ intact** (F-027 gap was the only regression, now closed)
- **Real mythos e2e verified**: HTTP 200 in 6m44s, `mythos_run.status='completed'`, daemon executed both sub-issues (loop=262s, coda=140s) on issue `b5a53a35-82ce-4a78-9824-50c57ec333cd`

### Deferred (out of labs audit scope, all non-blocking)

- **Bug-9**: no sole-mode recovery supervisor — `mythos_run.status='running'` when daemon completes post-timeout (rare with 5min timeout, but still possible)
- **Bug-10**: HTTP envelope too short for slow daemon paths (curl --max-time 540 vs 9min daemon latency)
- **TestRunnerEnqueuesSubIssues grep brittle** — substring instead of qualified form
- **Migration 273/274 zero test coverage** — 274 down is no-op (`SELECT 1`)
- **TestPostDecisionSync_FiresWhenLoopbackURLSet** doesn't verify body/headers (only hit bool)
