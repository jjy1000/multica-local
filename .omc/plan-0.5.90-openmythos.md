# 0.5.90 OpenMythos — enhancer-only 外循环（开发计划）

> 状态：待用户批准。前置调研：OpenMythos-main 参考（looped-RDT 架构仓库，移植原则不移植代码）+ 0.3.31 双模式现状 + 0.5.86 合并意图 + 0.5.89 打包环境实测。

## 一句话总结

蜂群（`mythos_swarm`）品牌化为 **OpenMythos**，**暂停 sole 独占模式**，只保留"搭配模式"外循环：mythos 规划（prelude）→ 迭代收敛（loop）→ coda 蒸馏 → **target assignee 带着蒸馏结果执行** → supervise 盯到终态；issue 右上角因果图图标旁新增启用指示图标（含一键启动/进度）。核心可用性修复 = 把蒸馏结果真正送达执行者。

## 用户已拍板（2026-08-30）

1. **命名与上游一致**：显示名 OpenMythos；flag key 保持 `mythos_swarm`（VERBATIM 律：catalog + 路由门 + 迁移 CHECK + 简报 + CLI + 锁表，不动）。
2. **暂不允许单独启用**：sole 模式对新写入关闭（400），只保留 enhancer（与其它智能体或团队搭配使用）。旧 sole 绑定继续解析（forward-only 律）。
3. **issue 右上角、因果图图标旁**新增指示图标：标识该 issue 已启用本插件。
4. 具体使用方式按 Multica 真实任务流优化，确保实际可用。

## 现状地基（0.3.31+ 已有，本次只修不重建）

| 资产 | 位置 | 状态 |
|---|---|---|
| `mythos_run.mode/target_assignee/extension_agent_ids/self_optimization_enabled/convergence_history/coda_conclusions` | mig 156/157 列 | ✅ 全在 |
| 三段引擎 prelude→loop(收敛)→coda + supervise + reaper | `service/mythos/runner.go`(983) + `supervise.go`(461) + `reaper.go` | ✅ 0.5.87 加固过 |
| LabPicker 二级模式 tab（sole/enhancer，**默认 sole**） | `packages/views/issues/components/pickers/lab-picker.tsx` | ⚠️ 默认值要翻 |
| run 进度卡（GET /api/issues/{id}/mythos-runs，current_loop/迭代数） | `lab-progress-card.tsx` | ✅ 复用 |
| 因果图图标挂点 | `issue-detail.tsx:2371` `<IssueCausalGraphIcon>` | ✅ 旁边加新图标 |
| self-opt 服务（扫已完成 issue→建议 edit→人工 Apply/Reject） | `service/agent_self_optimization/` | ✅ 智慧回流管道，保持人工门 |

## 可用性缺口（本次要修的核心，按严重度）

- **G1 蒸馏结果不送达 target（致命）**：`coda_conclusions`/reflections 只躺在 `mythos_run` 里，target 的 claim 简报（`daemon.go::ClaimTaskByRuntime`）不注入——"增强"从未真正喂给执行者。**修法**：claim 简报注入 `## OpenMythos 策略` 段（coda 摘要 + 最新 reflection），仿 0.5.85 causal claim_brief 模式（≤4KB、flag-gated、每条错误路径静默降级、无新迁移）；同时 coda 结论回写根 issue 评论（镜像 pythia/timesfm 0.5.86 回写律）。
- **G2 触发链断裂**：绑定 enhancer 后还得去 labs 页手动 `POST /api/experimental/mythos-swarm/run`。**修法**：新图标 popover 内"启动外循环"按钮（problem 预填 issue 标题+描述，target 默认当前 assignee，可改选 agent/squad）。**不自动触发**（与 pythia/timesfm 手动口径一致，成本可控）。
- **G2b 时序竞态**：绑定即走普通 assignee 入队，target 可能在 coda 完成前就开跑。**修法**：图标启动的 enhancer run 由 run 生命周期接管交付——coda 完成后经现有 child-done mention 通道唤醒 target 重查（若 target 已终态则 supervise 直接收敛，策略评论留档）。
- **G3 子 issue 污染**：prelude/loop/coda 每轮在用户工作区铺顶层子 issue（0.5.86 "no multica pollution" 未竟事项）。**修法（零迁移）**：run 的子 issue 全部挂 `parent_issue_id` = 根 issue（列已存在），列表收纳为嵌套；彻底治理（hidden_at 列 + 列表过滤 + 完成后归档）顺延 0.5.91 mig 285。
- **G5 品牌化**：catalog Title/Description（En "OpenMythos" / Zh "OpenMythos 外循环"）、manifest 侧边栏、settings 徽章、LabPicker 文案、4 locale 全量 OpenMythos 化；描述改写为"搭配模式外循环"定位。

## 实施步骤（按依赖序）

1. **后端门**（`issue.go` ~2499）：`lab_mode='sole'` + `lab_source='mythos_swarm'` 的新写入 → 400 "OpenMythos 暂仅支持搭配模式"；enhancer 路径校验 target 存在性（validateAssigneePair 已覆盖，补测试 pin）。assigneeLabLockError 对 mythos 增加模式条件：sole 维持互斥（存量）、enhancer 不锁 leader（target 自由选）。
2. **runner 交付链**（`runner.go` runCoda + Run 尾部）：子 issue 挂 parent；enhancer 下 coda 摘要回写根 issue 评论（AuthorType=agent，coda agent 署名）；coda 后对 target 发 mention 唤醒。
3. **简报注入**（`daemon.go::ClaimTaskByRuntime`，仿 claim_brief.go 模式 + `claim_brief` 同款 200ms deadline）：issue.lab_source=mythos_swarm 且存在 supervising/completed run → 注入策略段。新增 DB-less 单测（仿 `claim_brief_test.go`）。
4. **UI**：新组件 `IssueOpenMythosIcon`（挂在 `issue-detail.tsx:2371` 因果图标旁；仅当 `issue.lab_source==='mythos_swarm'` 渲染——ICP-5 被动律；lucide `Orbit` 图标；popover = 运行状态 + 启动/重跑 + target 徽章 + supervise 阶段 + run 详情链接）。LabPicker：mythos 砍 sole tab、默认 enhancer、target 用现成 assignee picker。
5. **品牌化**：catalog/manifest/locale 四语言；delegate inspect 输出同步。
6. **验证**：Go 单测（sole 拒绝门、简报注入、runner 父化/回写）；live API 全循环（建 issue 带 assignee → enhancer 绑定 → 图标启动 run → 断言 target claim 简报含 OpenMythos 段 → target 完成 → run supervised→completed → 根 issue 收到 coda 评论）；Playwright UI 三断言（图标出现时机、popover 启动、LabPicker 无 sole）。

## 验证清单

- [ ] `go test ./internal/... -count=1` 全绿（DATABASE_URL 导出、verification server 停机）
- [ ] `pnpm typecheck` 6/6；views/desktop vitest 无新增失败（stash 鉴别基线）
- [ ] live 全循环 23→N 断言含简报注入新断言
- [ ] 旧 sole 绑定 issue 仍可解析、run 历史可查（forward-only）
- [ ] delegation briefing 仍跳过 mythos（无 leader，与新口径天然一致——断言防回归）

## 风险与回滚

- sole 拒绝只 gate 新写入：回滚 = revert issue.go 门 + LabPicker tab，存量数据无迁移不可逆点。
- 简报注入超预算：硬 cap 4KB + 200ms deadline + 静默降级（claim_brief 同款三保险）。
- 唤醒重复：mention 通道幂等（0.5.88 已验证 idempotent 边）。

## 不做的事（明确边界）

- 不改 flag key、不做 swarm_topology 解冻（successor 仍指 mythos_swarm）。
- 不自动触发 run、不自动 Apply self-opt edit（人工门保持）。
- 不动 145 条历史 mythos 锁行（roster 复用属 0.5.91）。
- 嵌入收敛（pgvector）、自适应早退、reflection→建议 edit 自动起草、causal 记忆写入 → 0.5.91。

## 0.5.91 预告（OpenMythos 深化）

G4 收敛升级（嵌入余弦 + 词袋回退 + 早退）、G3 彻底治理（mig 285 hidden_at）、self-opt 深接线、roster 复用（固定基础 roster + 技能适配器，对齐 LoRA/MoE 原则）、与 WS5 causal memory 同流沉淀。
