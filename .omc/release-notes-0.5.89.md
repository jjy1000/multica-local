# Release notes — 0.5.89（2026-08-30，dev cycle，shipped-to-branch，未打包）

主题：**实验室对话化 + 插件资源隔离回收** —— 任意问题对话中的 agent 现在能创建/管理用户级实验室插件；插件自带的智能体/技能/自动化默认隐藏、删除即回收。设计文档：`.omc/plans/0.5.89-labs-conversational-and-memory-design.md`（WS1+WS2+WS3；WS4 委托体验、WS5 因果记忆+科研实验室留给 0.5.90/0.5.91）。

## 新能力

### 对话式创建与管理（WS1/WS3）
- **Claim briefing 新增 `## Lab Plugin Management`**：所有绑定非实验室 issue 的 agent 在领取任务时即得知可以用 `multica lab` 五动词为用户创建/管理插件。静态文本 ~670 字节，无 DB 查询；绑定实验室的 claim 不注入（agent 本身就是实验室，避免递归）。
- **`multica lab` 五动词**：`create` / `list` / `inspect` / `enable` / `disable` / `delete`。
  - create 支持 `--interaction-model`、`--leader`、`--skill`（可重复）、`--skills-visibility`、`--manifest-file`；在 agent 任务中自动盖章 `created_by_issue`/`created_by_task` 来源（daemon 新注入 `MULTICA_ISSUE_ID` env）。
  - enable|disable 在 agent 上下文中**拒绝内置实验室**（install/rollback 属于用户，Settings → Labs）。
  - delete 强制**两步协议**：无 `--confirm` 仅打印回收计划并退出 1（可先把计划贴到 issue 供用户过目）；`--dry-run` 纯查看。
  - never-disagree 法律扩展为五动词：`TestPluginManagementBriefVerbsMatchCLI` 把 briefing 里每条命令与 cobra 命令树 diff。

### 资源隔离与删除回收（WS2）
- **mig 284 回收账本 `user_plugin_resource`**：插件代建（provisioned）或声明（declared）的每项资源逐条登记，`reclaim_status` 状态机可重试。
- **manifest 内联代建**：`capabilities.agents_inline` / `skills_inline` 由服务端在插件创建/更新时原子建资源——自动隐藏（visibility 行 + lab_managed）、自动登记；声明型（原名单）资源登记为 declared，**永不回收**（属于用户）。manifest 更新时重跑代建 = 失败自愈。
- **删除即回收**：`DELETE /api/user-plugins/{slug}` 先做**活动绑定 409 护栏**（有非终态 issue 绑定则拒绝），然后按账本逐项回收：provisioned agent/squad 归档、autopilot 置 paused、skill 硬删、`~/.multica/plugins/<slug>/` 整目录移入 `.trash/`（30 天物理 GC 留待 GC sweep）；返回逐项回收报告（原 204 改 200）。`GET /reclaim-plan` + `POST /reclaim`（重试失败行）两个新端点。
- **skills_visibility**：manifest `capabilities.skills_visibility: global | lab_scoped`（缺省 global，存量插件行为不变）。lab_scoped 的技能只注入到该插件自身 issue 的 claim——实验室技能随实验室跑，不再全工作区扩散。

## 修复 / 校准
- 根 CLAUDE.md Active Contract #6 的过时表述更正：`claude_science_lab` 自 0.5.81 起 AutoDispatch 已回默认 true（可被委托）；现行 opt-out 集合为 pythia_oracle / timesfm / causal_graph。

## 验证
- 静态门（安静环境）：`go test -count=1 ./internal/... ./pkg/agent/...` 38 包 0 FAIL（DATABASE_URL 导出、验证服务器停止）；`pnpm typecheck` 6/6。
- 新增回归 pin：CLI 五动词 contract test、agent 上下文内置启停护栏、内联代建校验（重名/空名/超长）、skills_visibility 读写两侧、DB-backed 回收三件套（provision+reclaim / 409 护栏 / declared 保留）、lab_scoped 注入作用域。
- **Live API 全环（:8091，一次性工作区，23/23 PASS）**：注册 runtime→建 agent→建插件（agents_inline+skills_inline）→代建产物隐藏+登记→回收计划 3 项→绑定 issue 时 409→issue 终态后 200 回收 reclaimed×3→CLI 全动词→两步删除→briefing 实测注入（真实 claim 断言）→来源盖章→工作区级清理。

## 已知余留（0.5.90+ 账本）
- `.trash/` 30 天物理清除未接 GC sweep（move 即可逆步骤，风险为零）。
- `POST /reclaim` 重试端点未单测覆盖（与 delete 共用同一 reclaim 例程）。
- WS4 委托体验（DelegationCard、started 评论、LabProgressCard user_* 分支）与 WS5（causal recall/trace、LLM 抽取、Wiki 联动、科研实验室档案化）见设计文档分期。
