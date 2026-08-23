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
