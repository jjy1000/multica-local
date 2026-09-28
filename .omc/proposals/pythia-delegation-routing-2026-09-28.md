---
name: pythia-delegation-routing
status: proposed
created: 2026-09-28T08:39:37Z
updated: 2026-09-28T08:39:37Z
version_target: 0.5.125
source_incident: issue #505 (2026-09-28 15:26–16:26)
---

# 提案: 恢复「对话式委托群体推演」链路

## 1. 问题陈述

用户在 issue #505 的对话中说「委托群体推演实验功能对该方案做假想推定」。
预期系统开一个任务、把问题独立交付给 pythia lab 引擎推演。
实际 squad leader 自行用 squad 内 4 个角色做了人工角色扮演式假想推演, 引擎全程未参与。

预期链路: `issue 评论 → 语义识别 → 绑 lab_source → 建 agent_task_queue → daemon 跑 pythia → 报告回评论`
实际链路: 该链路的第 2 环起就不存在。

## 2. Baseline (2026-09-28 实测, 非推断)

### 2.1 事故现场
```
issue #505 「关于对拟写方案进行优化和审议」
  created_at  = 2026-09-28 15:26:23 +08
  assignee    = squad / ff1ee606 (党政机关AI智能团队, 14 角色)
  lab_source  = NULL
  lab_mode    = NULL
  推演评论落库 = 16:26 (情境组 A/B/D 三条, 全部由 squad 角色产出)
```
对照: issue #496 `lab_source=claude_science_lab` —— 用户在 UI 里手选的。

### 2.2 历史基线 (说明「手动路径可用, 对话路径从未存在」)
| 指标 | 实测值 |
|---|---|
| issue 总数 | 494 |
| `lab_source='pythia_oracle'` 的 issue | 8 (#187 #193 #367 #366 #452 #455 #482 #483) |
| `pythia_forecast_run` 总行数 | 19 |
| 最近一次成功 run | **2026-09-24 10:35** (#483, 3 轮, `source=oracle`, 有报告评论) |
| 最近 30 天 run 数 | 13 |
| 09-24 至今 (4 天) run 数 | **0** |
| 标题含「推演/预演」且绑了 pythia 的 issue | #452 #455 #482 #483 —— **全部由用户手动绑 + 手动 Run forecast** |
| 含「推演/pythia」字样的评论 | 62 条 |

### 2.3 引擎可用性
```
pgrep -fl "pythia|uvicorn"   → 空
lsof :8000-9000 LISTEN       → 8090 (Multica server) + 8009 (Kev), 无 pythia loopback
```
Pythia 引擎当前**未运行**。

## 3. 根因 — 四层断链 (按发生顺序)

### L1 没有「关键词 → lab_source」路由 (根因)
`issue.lab_source` 的唯一写入入口是 UI:
- `packages/views/issues/components/pickers/lab-picker.tsx:42` — "This picker writes both `issue.lab_source` and `issue.lab_mode`"
- `packages/views/issues/components/issue-detail.tsx:1948` — `handleUpdateField → PATCH issue.lab_source`

issue 评论文本里说「委托群体推演实验功能」, **没有任何代码把它读成 lab 绑定**。
用户要的是语义路由, 系统提供的是固定下拉框。

### L2 AutoDispatch=false 让「绑了也不派工」
`server/internal/experimental/catalog.go:298` → `AutoDispatch: ptrBool(false)`。
设计理由 (catalog.go:290-297): pythia 每次 forecast 是 10 轮 SSE × ~5s = 50s 阻塞, 用户偏好显式触发。

后果: leader-rewrite 仍写 assignee, 但 **service 层 `maybeEnqueueOnAssign` / `WillEnqueueRun` 被 short-circuit, 不建 `agent_task_queue` 行**。
而 issue 未绑 lab → Pythia 面板不渲染 → 手动按钮也不存在 → **闭环里没有可按的启动点**。

补刀: `server/cmd/multica/cmd_lab.go:570` 显式拒绝
```
lab pythia_oracle opts out of auto-dispatch (AutoDispatch=false):
its runs are triggered from the lab panel, not by issue assignment,
so it cannot be delegated to
```
→ **agent 即使想委托, 唯一那条 CLI 通道是被明文拒绝的。**

### L3 agent 认知层零线索 (三层叠加, 比初版更严重)
- squad `ff1ee606` instructions (4483 字符) 实测: `pythia` 0 次 / `推演` 0 次 / `实验室` 0 次 / `lab` 0 次。
- `server/internal/service/builtin_skills/multica-pythia/SKILL.md` **存在**, 且 builtin skills 全量注入不过滤
  (`server/internal/service/task.go:2427` — `skills = append(skills, s.BuiltinSkills()...)`), 所以 skill 确实在 agent 眼前。
- **但进 prompt 的只有 name + frontmatter description 一行, 正文完全不渲染。**
  `server/internal/daemon/execenv/runtime_config_sections.go:456-479`:
  ```go
  func writeSkills(b *strings.Builder, provider string, ctx TaskContextForEnv) {
      ...
      for _, skill := range ctx.AgentSkills {
          if desc := strings.TrimSpace(skill.Description); desc != "" {
              fmt.Fprintf(b, "- **%s** — %s\n", skill.Name, desc)
  ```
  → 中文触发词「方案推演 / 推演一下这个方案」只写在 `SKILL.md:58` 的**正文**里, agent 永远看不到。
- 中文名「群智推演」只存在于 `server/internal/experimental/catalog.go:260-263` 的 `Title.Zh`, **不进 agent 上下文**。
- 于是 agent 眼里这个 skill 长这样 (description 原文, **零中文字符**):
  > Use when the user asks about predictions, scenarios, world briefings, geopolitical risks, market forecasts, or "what happens next" questions…

  「假想推定」语义上确实是 what-if, 字面零命中。
- 且该 description 还有一句**自相矛盾的排除条款**:
  `"Do not use it for chat / issues / Multica platform operations"` — 紧跟着又写
  `"For issue-bound deliberations call issue-forecast"`。
  模型看到「issues 禁用」很容易直接跳过, 尤其当它正在一个 issue 上干活。

### L4 引擎按需拉起, 而没人调 ensure-up
`apps/desktop/src/main/index.ts:724-727` 自陈:
```
Brings the Pythia subprocess up on demand when the pythia_oracle flag is enabled;
sees no traffic when the flag is off because no renderer code path calls pythia:ensure-up.
```
`setupPythiaIPC` 只注册 handler, 不自启。issue 未绑 lab → 面板不渲染 → renderer 不调 ensure-up → 引擎永不起来。
→ 即使 L3 修好, SKILL.md 的 Step 1 `multica --json pythia status` 也会查到来不及起。

### 附: 降级路径会掩盖问题
`pythia_forecast_run.source` 有 `synthetic_oracle_failover` 值 (#455 于 09-08 连续 6 次)。
即引擎挂掉时系统有合成降级 —— 但产出是**假数据**。若未来接了自动路由, 必须禁止静默降级。

## 3.5 关键发现 — 有一条比初版更好走的路

初版我把推荐定成「引导评论 → 面板深链」。深挖后推翻, 原因有三条:

**(a) 面板深链不存在。**
`packages/views/issues/components/issue-detail.tsx:1966-1968` 对 pythia **显式排除** open-panel 链接:
```tsx
issue.lab_source !== "pythia_oracle" &&
```
注释: 「0.5.112: pythia no longer links out」。所以「点深链去面板」这个引导对 pythia 是空头支票。

**(b) 真实推演入口与 agent 队列完全解耦。**
真正的推演走独立 HTTP 端点, **不经过 lab_source, 不经过 AutoDispatch, 不建 agent 任务**:
```
POST /api/experimental/pythia-oracle/forecast/issue
  → handler/forecast_issue.go:271  pythiaIssueForecast
  → CreatePythiaForecastRun + startPythiaForecastJob  (forecast_run_runner.go:48, 后台 goroutine)
```
- `grep EnqueueTaskForIssue forecast_*.go` = **0 命中** → 该路径从不产生 agent 任务。
- `forecast_issue.go:976-996 buildIssueForecastContext` 只是把 lab_source 读出来填 context, **不校验 issue 是否 lab-bound**。

**→ 也就是说: 想跑一次推演, 既不需要绑 lab, 也不需要动 AutoDispatch。**

**(c) 存在一条已存在但没人用的 comment 旁路。**
`server/internal/handler/comment.go:1895-1900` 的 `computeCommentAgentTriggers` **完全不查 AutoDispatch**。
issue 一旦 `lab_source=pythia_oracle` 且 assignee 被 leader-rewrite 成 `pythia_runtime`, 成员评论会**绕过 `WillEnqueueRun` 直接 enqueue pythia_runtime**。
以「用户先点过 LabPicker」为前提, 不是关键词触发 —— 但它证明闸门不是铁的, 只是当前没人走。

**(d) 已排除 0.5.124 回归。**
本批三个 commit 均未触碰这条链:
- `d48ee3044` (core schemas) / `910ea7329` (issue-detail 过滤器) / `ccfe25023` (claude_science_runtime 专属)
- 均未改 `issue_trigger.go`、catalog 的 `AutoDispatch` 行、`forecast_issue.go`
- `git log -- forecast_issue.go` 最近改动是更早的 `ec595571c`
- `notifyParentOfChildDone` (`issue_child_done.go:273,361,702`) 只对有 `parent_issue_id` 的 child 生效, #505 无 child → 链路根本不启动

## 4. 候选方案

### 方案 A — 认知层 (最轻, ~3 行)
改 `server/internal/service/builtin_skills/multica-pythia/SKILL.md` 的 description:
1. 追加中文触发词: 推演 / 预演 / 假想 / 情景 / 预测 / 沙盘 / 多视角。
2. 删掉 `"Do not use it for chat / issues / Multica platform operations"` 里的 `issues`, 改成
   `"For issue-bound deliberations use issue-forecast — 报告回写到 issue 评论"`.

**风险**: 治标。若 L4 引擎不起, agent 仍会失败 —— 建议同时加一句「status 不通则先跑一次 ensure-up 再重试」(见方案 B2, 不要写成「去打开面板」—— pythia 没有面板链接, §3.5(a))。

### 方案 B — 引擎可用性 (中, 碰 desktop main)
在 pythia 启动链补一个 ensure-up:
- 选项 B1: server 启动时若 `pythia_oracle` flag on → 通过 IPC 通知 desktop 拉起引擎。
- 选项 B2: agent 执行 pythia skill 时, 若 status 失败 → 走一次 `pythia:ensure-up` 再重试。

**风险**: 碰 `apps/desktop/src/main/*` 需按 `apps/desktop/CLAUDE.md` 跑三检查冒烟测试 (5432/8090 监听 + `/health` + 行数一致)。

### 方案 C — 语义路由 (最贴近原始预期)
在 issue 评论创建路径加意图识别: 命中「推演/预演/假想/情景/pythia」→ 建 run。

**必须先在 C-0 / C-1 / C-2 三条里选一条:**

- **C-2 (新增, 推荐)** — 识别到意图后直接 `POST /api/experimental/pythia-oracle/forecast/issue` 建 run, **完全不碰 `lab_source` 和 `AutoDispatch`**。
  - 依据 §3.5(b): 该端点不校验 lab-bound、不建 agent 任务。
  - 好处: 绕开 L1 和 L2 两个坑, **不推翻 0.5.81 的产品决策**, 改动面只在评论创建路径 + 一个 HTTP 调用。
  - 代价: issue 的 `lab_source` 仍是 NULL, 所以 UI 里的实时轮次/概率轨迹 embed 不会渲染 (那两处硬判 `issue.lab_source === "pythia_oracle"`, 见 `issue-detail.tsx:2510` / `:2847`)。用户只拿到最终报告评论。
  - 需要 user 拍板: 「只要报告」够不够, 还是必须要有可视化面板。
- **C-1a** — 为「显式命中关键词」开 `AutoDispatch` 例外, 走 leader-rewrite + 建 agent 任务。
  - 推翻 0.5.81 决策, 且让 50s 阻塞推演变成默认行为。**不推荐。**
- **C-1b (初版推荐, 已作废)** — 识别后回一条引导评论。
  - 作废原因: §3.5(a) 证实 pythia 根本没有「open panel」链接可指, 这是空头支票。

**C 共同要求**:
- 关键词必须窄 (宁可漏, 不可错绑), 且命中后要在 issue 上留痕。
- 禁止静默降级: 若 `CreatePythiaForecastRun` 走成 `synthetic_oracle_failover`, 评论里必须显式标注「本次为合成降级, 非真实引擎」。

**风险**: 中间件新增, 触碰评论创建主路径; 需要误判率监控。

## 5. 推荐

**A + B2 + C-2**, 按此顺序分三个 commit:
1. **A** (改 description, ~3 行) 单独可 ship, 零风险, 解决「agent 愿不愿意试」。
2. **B2** (status 失败则 ensure-up 再重试) 让「试了能不能成」—— 必需, 因为 §3.5 说明 run 是 HTTP 直发, 但**引擎仍需 renderer 的 ensure-up 先拉起**。
3. **C-2** (识别意图 → 直发 forecast/issue 端点) 让「人根本不用知道要点哪里」, 且不碰任何既有产品决策。

**不推荐 C-1a** (给 AutoDispatch 开后门) —— 推翻 0.5.81 明确记录的产品决策, 且让阻塞式推演变默认行为。
**先于 C-2 需 user 拍板**: 接受「只有最终报告评论、没有可视化面板」, 还是宁可绑 lab 换面板 (那就得回到 C-1a 的代价)。

## 6. KPI (measure-first)

| 指标 | Baseline (2026-09-28) | 目标 (改动后 30 天) |
|---|---|---|
| 含「推演/预演/假想」意图的 issue 中, 绑定 pythia 的比例 | 4/4 = 100%, **但 100% 靠人工** | 保持 100% 且**自动绑定占比 ≥ 60%** |
| `pythia_forecast_run` 月 run 数 | 09-24 后归零 (4 天 0 次) | ≥ 4 次/月 |
| run 的 `source='oracle'` 占比 | 13/19 ≈ 68% (含 6 次 synthetic failover) | **≥ 95%**, 且 `synthetic_oracle_failover` 必须伴随显式告警 |
| agent 自述「没有该功能」的次数 | 本次 1 次 (#505) | **0 次** |
| 误绑定 lab 的 issue 数 | 0 | **必须 0**, 任何 >0 即回滚 (C-2 不写 lab_source, 该项天然为 0, 风险转移到「误建 run」) |
| 误建 forecast run 数 (意图误判) | 无从测 (当前无自动路径) | **必须 0**, 任何 >0 即 revert 该 commit |
| L2 `lab delegate` 拒绝率 | 100% (对 pythia) | **不变** (设计如此, §5 不推荐 C-1a) |

**验证方法**: 改前先抓基线快照 (本文件 §2 已是), 改后用同一 SQL 重跑, 逐行对比。
**门禁**: 每个 commit 独立跑 `pnpm typecheck` + `cd server && go test -count=1 ./internal/... ./pkg/agent/...`;
若动 desktop 则加 `bash ~/.multica/scripts/verify-desktop-cold-start.sh`。

## 7. Rollback

- 方案 A: 单文件 revert (`SKILL.md` front-matter)。`git revert` 即可, 无状态。
- 方案 B2: `git revert` + 重启 desktop。若 ensure-up 引发重复 spawn, 先摘 `apps/desktop/src/main/pythia-manager.ts` 的调用点。
- 方案 C-1b: 回滚即恢复当前行为 (只多一条引导评论)。**无需数据迁移回滚**, 因为 C-1b 不写 `lab_source`。
- 任何一步发现「误绑定 lab」> 0 → 立即 revert 该 commit, 不做修补。
- 通用回滚点: ship 前跑 `bash ~/.multica/scripts/pre-update-snapshot.sh` (`~/.multica/backups/<date>/`)。

## 8. 未决问题 (需 user 决策)

1. **C-2 够不够?** C-2 不绑 lab → 用户拿到报告评论, 但 UI 的实时轮次 / 概率轨迹 embed 不渲染
   (`issue-detail.tsx:2510` / `:2847` 硬判 `lab_source === "pythia_oracle"`)。
   要可视化面板就必须绑 lab → 必须动 AutoDispatch → 得推翻 0.5.81。**这是本次唯一的真权衡点。**
2. **synthetic_oracle_failover 要不要保留?** 引擎挂掉时现在会静默产出假数据 (#455 于 09-08 连续 6 次)。
   提案倾向: 改成显式失败 + 提示重试, 但这是行为变更。
3. **squad 层要不要注入 lab 认知?** 现在 squad instructions 对 lab 完全无感知。
   可选: 创建 squad 时自动附一段「本工作区可用 lab 清单」到 instructions。
4. **comment 旁路要不要收敛?** `comment.go:1895` 不查 AutoDispatch, 意味着绑了 pythia 的 issue 上,
   一句普通评论就会 enqueue pythia_runtime。这是既存行为, 不是 bug, 但一旦上 C 类自动化, 它会成为第二个派工入口。

## 9. 附录 — 关键代码位置

| 事实 | 位置 |
|---|---|
| pythia catalog 定义 (含 AutoDispatch=false) | `server/internal/experimental/catalog.go:253-299` |
| `AutoDispatch()` 解析 (未知 key 返回 true) | `server/internal/experimental/catalog.go:610-624` |
| **AutoDispatch 唯一 short-circuit 点** | `server/internal/service/issue_trigger.go:97-99` (`WillEnqueueRun` 顶部) |
| leader-rewrite 照常发生 | `server/internal/handler/issue.go:3448-3460` → `assignDefaultLabAgentOnUpdate:3588`, leader `pythia_runtime` 来自 `:3632-3638` |
| `lab delegate` 拒绝 AutoDispatch=false | `server/cmd/multica/cmd_lab.go:564-571` |
| **真实推演端点 (与 agent 队列解耦)** | `server/internal/handler/forecast_issue.go:271` → `forecast_run_runner.go:48` |
| 该端点不校验 lab-bound | `server/internal/handler/forecast_issue.go:976-996` (`buildIssueForecastContext`) |
| comment 触发不查 AutoDispatch (旁路) | `server/internal/handler/comment.go:1848,1895-1900` |
| 评论文本解析 (只认 mention, 无关键词) | `server/internal/util/mention.go:40-61` |
| builtin skills 全量注入 (不过滤) | `server/internal/service/task.go:2425-2429`; claim 两路径见 `handler/daemon.go:1376-1386` |
| **prompt 里只有 name + description, 正文不渲染** | `server/internal/daemon/execenv/runtime_config_sections.go:456-479` (`writeSkills`) |
| builtin skills go:embed 根 | `server/internal/service/builtin_skills.go:16-19`, loader `:64-68` |
| pythia skill description (零中文) | `server/internal/service/builtin_skills/multica-pythia/SKILL.md:1-8` |
| 中文触发词只在正文 (agent 看不到) | `server/internal/service/builtin_skills/multica-pythia/SKILL.md:58` |
| 中文名「群智推演」只在 catalog (不进 agent) | `server/internal/experimental/catalog.go:260-263` |
| lab_source 唯一常态 UI 写入点 | `packages/views/issues/components/pickers/lab-picker.tsx:42` + `issue-detail.tsx:1950-1957` |
| **lab 创建路径刻意留空 lab_source** | `packages/views/modals/create-issue.tsx:592` |
| pythia 无 open-panel 链接 (深链空头支票) | `packages/views/issues/components/issue-detail.tsx:1966-1968` |
| UI 硬判 lab_source 才渲染推演面 | `packages/views/issues/components/issue-detail.tsx:2510`, `:2847` |
| pythia 按需启动注释 | `apps/desktop/src/main/index.ts:724-732` |
| `multica lab list` 实现 | `server/cmd/multica/cmd_lab.go:119-175` |
| 事故 issue | #505 `bdd336c0-3636-4ae4-9fc9-87dc9a3f5112` |
| squad | `ff1ee606-a40e-4135-8628-a0f6a8760fad` 党政机关AI智能团队 |

## 10. 落地记录 (2026-09-28)

用户决定「只出方案不改码」后又追加要求「确保实验室插件功能可以被任务问题其他 agent 调用推演并返回结果, 且角色 agent 能看到插件实验室功能」。据此执行了方案 A + 认知层通用化, **零 wire protocol 改动, 零迁移**。

### 10.1 关键发现: 能力早已存在, 缺的是 agent 的认知

调研确认落地所需的全部能力**早已就位**:

| 能力 | 状态 | 位置 |
|---|---|---|
| `multica pythia issue-forecast --issue <id> --wait` | ✅ 存在, 报告自动回 issue 评论 | `cmd/multica/cmd_pythia.go:63-67, 253-280` |
| agent 的 PATH 含 `multica` | ✅ daemon 前置自身 binDir | `internal/daemon/daemon.go:4228-4231` |
| agent 有 API 凭据 | ✅ `MULTICA_API_TOKEN` / `MULTICA_SERVER_URL` | `internal/daemon/daemon.go:4174-4196` |
| forecast 不要求 issue 绑 lab | ✅ 无 gate | `internal/handler/forecast_issue.go:976-996` |

**唯一的缺口是 agent 知不知道。** 且比初版判断更严重 —— 内置 skill 的 `AgentSkillData.Description` 本来就是空的
(`loadBuiltinSkill` 只填 Name + Content), description 是 daemon 在 agent 机器上从正文重新 parse
(`internal/daemon/local_skills.go:399,492`) 再写盘, 由 provider CLI 原生加载。
→ **改 frontmatter 是正确的且唯一的注入点。**

### 10.2 事故的精确机制 (原方案未预见)

SKILL.md 正文 Step 1 要求 agent 先跑 `multica --json pythia status`, 并写明
「若不是 ready 就 **stop here** 并告诉用户服务没运行」。但该命令**从 CLI 恒返回 `status="unknown"`**,
`cmd_pythia.go:100-108` 注释自陈 "The CLI cannot introspect the desktop-managed subprocess — that is by design"。

**→ agent 忠实执行 Step 1 → 得到 unknown → 按指令放弃 → 告诉用户「服务没跑」。它没撒谎, 它是被 skill 指令挡死的。**
而这条指令在正文里, 正文不进 prompt —— 所以这段自我阻断逻辑对模型不可见, 但**改对之后它就会生效**。

### 10.3 实际改动 (3 个文件)

**A. `internal/service/builtin_skills/multica-pythia/SKILL.md`** — frontmatter + 正文重排
1. description 590 字符, 加中文触发词 (推演/预演/假想/情景/沙盘/预测/推一下/模拟推演/假如…会怎样)
2. 删除自相矛盾条款 `Do not use it for chat / issues` (它与同文件的 issue-forecast 段落直接冲突)
3. description 内直接给出可执行命令 `multica pythia issue-forecast --issue <id> --wait`
4. 正文 Step 1 重写: issue 场景**跳过 status 探针**; 并明确写出
   "`status=unknown` 是 CLI 的预期答案, **不代表服务已停**, 不要因此放弃推演"
5. issue 章节加禁止条款: **不得用 squad 角色扮演顶替推演** (无概率分布 / 无多轮收敛 / 无反事实权重)
6. 加诚实义务: 报告带 `synthetic` / `synthetic_oracle_failover` 标签时必须声明是合成降级
7. 加一条兜底: **在宣布「没有该功能」之前先跑命令**, 失败报错误原文

**B. `internal/service/builtin_skills/multica-labs/SKILL.md` (新增)** — 实验室能力目录页
`loadMainProductSkills` 是目录扫描, **加目录即自动注入, 零代码改动**。内容:
- 4 个 lab 的能力表, 标注**每个 lab agent 能否自主调用**
- 关键区分 (原方案未发现): `multica claude-science research` / `get-result` 均已 **Deprecated**
  ("research work now flows through the standard Multica agent runtime"),
  → **pythia 是唯一有 CLI 可自主调用路径的 lab**; causal_graph 有 `subgraph`; claude_science 只能 UI 绑定
- 让 agent 知道 `issue-forecast` 与 `AutoDispatch` / `lab_source` 无关

**C. `internal/service/builtin_skills_labs_test.go` (新增)** — 4 条回归测试

### 10.4 测试有效性已用 mutation 验证

不是只测「绿」, 而是逐条把契约杀掉确认测试会红:

| 变异 | 被杀测试 |
|---|---|
| description 改回纯英文 (无「推演」) | `TestLabSkillsCarryChineseTriggerWords` + `TestPythiaSkillDescriptionAdvertisesTheIssueForecastCommand` (2 条) |
| 删掉 `multica pythia issue-forecast` 命令 | `TestPythiaSkillDescriptionAdvertisesTheIssueForecastCommand` |
| 恢复 `Do not use it for chat / issues` 条款 | `TestPythiaSkillDescriptionDoesNotExcludeIssues` |
| 删掉「unknown 是预期答案」诚实声明 | `TestPythiaSkillBodyDoesNotGateIssueForecastOnStatusProbe` |

4/4 变异全部被杀。

### 10.5 门禁结果 (2026-09-28)

```
pnpm typecheck                                       → 6/6 绿, EXIT=0
go test ./internal/... ./pkg/agent/...               → 38 包绿, 1 FAIL
```

**唯一 FAIL 是 0.5.124 既有回归, 非本次引入** —— `git stash` 移除全部改动后同样失败:
```
TestMentioningSkillTeachesTheParserContract/name_where_a_uuid_belongs_is_silently_dead
  ParseMentions("[@Alice](mention://member/Alice) please review") = [{Type:member ID:A}], want []
```

**根因 (已定位, 未修 — 范围外)**:
`internal/util/mention.go:29` 的 `bareMentionRe` **无尾锚** —
```go
var bareMentionRe = regexp.MustCompile(`mention://(member|agent|squad|issue|all)/(all|[0-9a-fA-F-]+)`)
```
输入 `mention://member/Alice` 时, hex 类吃掉了首字母 `A` 就停 (后面 `lice)` 不在类里), 于是解析出假 id `member:A`。
**这正是该正则注释里警告的同一形态** —— 注释防住了 `all/all`, 但没防住非 UUID 前缀。
建议修法: id 部分收紧为 `{8,}` 并加尾随负向前瞻 `(?![0-9a-zA-Z])`。
卡着 ship gate, 但属于另一个模块的 fix-forward, 未在本次改动里顺手修改。

### 10.6 本次未做 (按 §5 分批, 后续批)

- 方案 B2 (agent 调 pythia 前自动 ensure-up) —— 引擎当前未运行, 但 `issue-forecast` 走服务端解析,
  需实测确认引擎未起时的行为后再决定是否需要兜底
- 方案 C (评论语义路由) —— 待 §8.1 权衡拍板
