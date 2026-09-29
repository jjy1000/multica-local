# 0.5.128 (2026-09-29) — 设默认即一键迁移 + 运行时删除 tombstone 化

两批改动，零迁移。承接 0.5.127 的默认 CLI + 批量迁移，按用户反馈补两个缺口：
"点击默认后就开始迁移，无需再手动点迁移" 与 "误删运行时后重加回来不能丢智能体/用量"。

## ① 设默认即一键迁移（FE）

- 运行时行菜单「设为默认运行时」升级为「**设为默认并迁移智能体**」（menu item 带 hint
  tooltip 说明）。点击后：写 `workspace.settings.default_runtime_id` → 自动
  `bulk-move` **旧默认**上的全部智能体（含归档）到新默认 → toast 报告迁移数
  （"已设为默认，并迁移了 N 个智能体"）。
- 反向点击即切回（Opencode→Claude 同一道理）。首次设默认（无旧默认）只设不迁。
- 迁移失败（如目标为他人私有运行时 403）时**默认仍生效**，toast.warning 说明
  "默认已生效，但存量迁移失败：<原因>"——不静默把舰队留下。
- 其他运行时上的智能体不动；任意运行时对之间仍可用「迁移智能体到其他运行时…」菜单。
- ⭐默认徽章 tooltip 更新，说明设默认会顺带迁移。

## ② 运行时删除 tombstone 化（server，修复"误删重加丢智能体/用量"）

**旧行为的两个破坏点**（都是删除路径的真实行为，非假设）：
1. `DeleteAgentRuntime`（轻量）与 `ArchiveAgentsAndDeleteRuntime`（级联）最终
   **硬删归档 agents + runtime 行**——删除 Opencode 后 daemon 重注册会铸一个
   **新 id** 的运行时行，之前的智能体（已硬删）永远回不来："智能体消失"。
2. 用量表（task_usage_hourly 等，runtime_id 无 FK）行不删但成**孤儿**——
   新 id 接不上，token 消耗历史从 UI 消失。

**新行为（tombstone）**：
- 两个删除端点不再删任何行/agent：改为在 `agent_runtime.metadata` 打
  `deleted_at` 标记（级联端点同时记录 `deleted_archived_agent_ids` =
  本次删除归档的 agents）。级联的 archive/cancel-tasks/pause-autopilots
  保留；squads 清理与 agents 硬删移除（不再需要——没有任何行被删）。
- 列表查询（`ListAgentRuntimes` / `ListAgentRuntimesByOwner`）过滤
  tombstone → UI 上"已删除"。
- **复活**：`UpsertAgentRuntime` 的 `UNIQUE (workspace_id, daemon_id, provider)`
  仲裁器意味着 daemon 重注册**复用同一行 id**。`DaemonRegister` 内置分支在
  upsert **前**快照 tombstone 记录的 ids（upsert 的
  `metadata = EXCLUDED.metadata` 覆盖正是清标记动作），upsert 后调用
  `RestoreAgentsArchivedByRuntimeDeletion` 恢复这批 agents 并逐个广播
  `agent:restored`。删除时归档的智能体自动复活；删除前就归档的不受影响。
- agent 绑定（FK RESTRICT）从未断开、用量行从未被碰 → **token 消耗等历史
  完整保留且直接挂在复活的同一行上**。
- 复活以 daemon 实际重注册为准：CLI 卸了/daemon 没了 → 行保持删除状态，
  不会凭空复活。
- 删除的是**默认运行时**时，FE 清 `default_runtime_id` 键（tombstone 行
  保留给复活，但不应静默续任默认）。
- 自定义 profile 运行时的级联（runtime_profile.go）保持原语义未动；
  legacy daemon 合并（mergeLegacyRuntimes）不受影响。

## 测试与门禁

- 反向重写旧行为钉：`TestArchiveAgentsAndDeleteRuntime_HappyPath`（旧钉断言
  行删+agent 删）、`TestDeleteAgentRuntime_RemovesArchivedSquadsLedByArchivedAgents`
  与 `NoSquadsRegression`（旧钉断言硬删）→ 全部改为 tombstone 契约
  （行保留+标记、agent 保留归档、task_usage_hourly 保留、列表不可见）。
- 新增：`TestDeleteAgentRuntime_LightPathTombstones`（轻量路径 tombstone +
  既有归档 agents 不复活）、`TestRuntimeTombstoneRevival`（快照→upsert 同 id
  复用→恢复→标记清除→双恢复 no-op）、FE 行菜单删默认清键（2）。
- 门禁：go 40 包全绿；cmd/server 仅在案 2 条 comment-trigger baseline 红；
  `pnpm typecheck` 6/6；core 965 + views 1907 vitest 绿（33 skipped 在案）。

## 运营说明（装后）

- 一键切换：Opencode 行 ⋯ →「设为默认并迁移智能体」——Claude 上的智能体整体
  搬过来；切回就在 Claude 行上点同一个菜单。
- 误删恢复：删除 Opencode 后重启 daemon（或它自动重注册）→ 同一行复活，
  之前删除时归档的智能体自动恢复，用量历史无损。
