# 0.5.127 (2026-09-29) — 默认 CLI 可切换 + 存量智能体批量迁移 + 实验室委托发现断链修复

三批改动，零迁移，零 wire 破坏性变更。

## ① 实验室委托发现断链（server，fix）

**现象**：agent 在 issue 里被要求委托科研实验室时"看不见"实验室——claim 时注入的
"Available Labs (delegation)" 简报在默认安装上恒为空，目录技能还说"需用户绑定/CLI 已废弃"。

**根因**（两处叠加）：
1. 委托简报的 enabled 集合来自 `ListEnabledFlagKeys`——**pref-only 投影**，只看
   `experimental_pref` 表里手动拨过开关的行。而 `claude_science_lab`/`pythia_oracle`
   自 0.5.114 起 `DefaultVal=true` 默认开启，用户从未拨过开关 → 无 pref 行 → 简报恒空。
   Labs UI 用 `pickEnabled(DefaultVal, override)` 合并语义所以面板显示正常——两处语义不一致。
2. `multica-labs` 目录技能明说 claude_science_lab "⚠️ 需用户绑定 / 不要试图用 CLI 驱动"，
   与事实完全相反（0.5.125 pythia 事故的翻版）。

**修复**：
- `experimental.EffectiveEnabledKeys(prefs)`：pickEnabled 合并语义（pref 行覆盖默认值，
  缺行回落默认值），候选全集 = catalog + user plugins，输出排序。配套 SQL `ListAllExperimentalPrefs`。
- `BuildDelegateBrief` 改用合并语义；过滤契约抽成纯函数 `selectDelegateLabs` 并钉测试
  （assignee-model / 非 frozen / auto-dispatch / leader 可解析）。
- `issue create` / `issue update` 新增 `--lab-source`：一步绑定实验室并派发，替换掉
  技能里"手写 curl PATCH"的脆弱路径。客户端快速失败（未知/冻结实验室 400 前置），
  `--lab-source ""` 走服务端 explicit-null 解绑契约。AutoDispatch=false 实验室允许绑定但告警。
- 技能文案反转：`multica-labs` 科研实验室行改 ✅ 可自主委托（`multica lab delegate
  --parent <id> claude_science_lab "<task>"` / `issue update --lab-source`）；
  `multica-claude-science` Step 1 重写为三条原生命令入口，curl 全删；
  pythia 一节明确"推演只走 issue-forecast，`lab delegate pythia_oracle` 快速失败是设计使然"。
- `multica-labs` 纳入 `skill_cli_contract` 契约测试（每条命令逐 flag 对活 cobra 树）。
- 旧 DB 钉 `TestDelegationBriefListsEnabledAssigneeLabs` 把 bug 当基线钉着
  （断言"无 pref 行 = 无简报"），反向重写为新契约："无 pref 行也必须广告 claude_science_lab；
  显式 enabled=false 必须消失"。事故类认知钉：技能正文再出现"需用户绑定/CLI 已废弃"即红。

## ② 默认 CLI 运行时（FE，feat）

"默认都是 Claude"不是硬编码：运行时列表 `ORDER BY created_at ASC`（Claude 先注册排第一）
+ 新建 agent 表单 RuntimePicker 种子选"第一个可用项"的涌现结果。服务端原本没有默认运行时概念。

- 存储：`workspace.settings.default_runtime_id`（append-only 键，走既有 workspace settings
  通道，零迁移）。core 纯函数 `defaultRuntimeIdFromSettings` / `withDefaultRuntimeId`
  （settings 漂移安全，输入不变异）。
- 运行时页：每行 ⋯ 菜单「设为默认运行时 / 取消默认运行时」（owner/admin），默认行 ⭐「默认」
  徽章（tooltip 说明），设置/取消 toast。指向已删除运行时的残值优雅降级（不显示徽章、表单回落）。
- RuntimePicker：空选择种子优先工作区默认（须在当前筛选集内且对调用者可用——他人私有运行时
  不会种出 Create 必 403 的选择）；筛选切换同样尊重默认。只影响**新建**的 agent。
- i18n 四语言（zh-Hans/en/ja/ko）。

## ③ 存量智能体批量迁移（server + FE，feat）

默认设置只影响新建；存量 agent 各绑着创建时的运行时。本批补上"整批切换/切回"通路。

- 服务端 `POST /api/agents/bulk-move-runtime`（owner/admin；字面路由注册在 `/{id}` 之前，
  Active Contract #2）：`from_runtime_id` → `to_runtime_id` 整批搬运，默认含已归档
  （`include_archived: false` 排除），事务内逐 agent 走与单更新 runtime-switch **完全相同**
  的语义——已知 provider 不兼容 model 清空（MUL-3341）、字面非法 thinking 清空
  （唯一刻意放宽：单更新 400 让人处理，批量清空继续——批量卡在第 37 个 agent 上就失去意义）；
  opencode 属未知模型族，模型串按契约保留；目标运行时私有性校验同单更新（403）。
  响应计数（moved/cleared_model/cleared_thinking + agent_ids），逐 agent 广播 `agent:status`。
- 前端：运行时行 ⋯ 菜单「迁移智能体到其他运行时…」→ `MigrateAgentsDialog`：目标列表
  （排除源、带在线点）、受影响 agent 数、不兼容字段重置提示；确认后 invalidate agents 查询。
  **切回 = 在新运行时行上反向迁移**，无单独回滚机制。FE 走 `parseWithFallback`（API 兼容契约）。
- i18n 四语言（migrate 家族，count 走 `_one/_other` 复数键）。

## 测试与门禁

- 新增：`TestEffectiveEnabledKeys_PickEnabledSemantics`（catalog 派生夹具）、
  `TestSelectDelegateLabs`（活 catalog 过滤契约）、`TestLabsCatalogSkillAdvertisesClaudeScienceDelegation`、
  `TestRunIssueCreateSendsLabSource` / `TestRunIssueCreateRejectsUnknownLabSource` /
  `TestRunIssueUpdateLabSourceBindAndClear`、core `default.test.ts`（7）、
  create-agent-dialog 默认种子（2）、runtime-row-menu 默认/迁移菜单项（4）、
  migrate-agents-dialog（4）、`TestBulkMoveAgentRuntime_*`（8，DB 实测：迁移+清字段/切回/
  归档含与排除/同源 400/未知目标 400/空源 no-op/plain member 403）。
- 门禁：`go test ./internal/... ./pkg/agent/... ./cmd/multica/` 40 包全绿；
  `cmd/server` 仅在案 2 条 comment-trigger baseline 红（与本批无关）；
  `pnpm typecheck` 6/6；core 965 + views 1905 vitest 绿（33 skipped 在案基线）；
  `node scripts/check-agents-docs-sync.mjs` ✓。

## 运营说明（装后）

- 运行时页把 Opencode 设为默认：行 ⋯ →「设为默认运行时」。之后**新建**的智能体默认落 Opencode。
- 存量切换：Claude 行 ⋯ →「迁移智能体到其他运行时…」→ 选 Opencode → 迁移；切回反向操作。
  与目标 CLI 不兼容的模型/thinking 会被重置为默认（对话框有提示）。
- agent 委托科研实验室：直接 `multica lab delegate --parent <issue-id> claude_science_lab "<task>"`，
  或 `multica issue update <id> --lab-source claude_science_lab`。
