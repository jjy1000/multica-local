# Multica 0.3.3 Plan — Upstream Integration (2026-07-11)

> 本 fork 不再 git repo。本 plan 用 `.omc/plan-*.md` 替代 PR description。

## 范围
官方 0.1.0+ 累计的 5 个 chat 子系统 feature + 1 个 mcp_overlay handler + 6 个独立 bug 修复
集成到本地 0.3.2 → 0.3.3。

**拒绝项**（按 `multica-fork-vs-upstream-divergence-map.md` §2.2）：
128_129_130_131_132_133 / Slack / Lark / GitHub / Composio / Cloud / OAuth / electron-updater / 邀请

> **Audit 修订 (2026-07-12)** — "拒绝项" 的真实含义是 **不引入新功能**，**不是物理删除磁盘代码**。
>
> 当前 fork 实际状态：
> - `server/internal/integrations/lark/` 代码存在 (10+ .go) — 但运行时 gate 在 `MULTICA_LARK_SECRET_KEY` env var, 未设置 = disabled
> - `server/internal/integrations/slack/` 代码存在 (8+ .go) — 同上, 未设置 = disabled
> - `server/internal/integrations/channel/` 通用 channel 框架存在 (无 Slack/Lark 专属)
> - `server/internal/integrations/composio/` **不存在** (Composio 完整剔除)
> - GitHub handler 0 grep (真正未引入)
> - Migrations 120 (autopilot_subscriber / github_pending_installation) / 122 (lark_chat_session_binding_thread_reply) / 124 (channel_generalization) / 128 (autopilot_collaborator / runtime_mcp_overlay / comment_routing_escalation) schema 已 apply — schema 列存在但**无 runtime writer**
>
> 这意味着：plan §"拒绝项"应理解为 **"不引入新功能 + 不接 caller"**，而不是 "磁盘物理删除"。下一次 cherry-pick 时如果看到 Lark/Slack/Autopilot 代码在 upstream 出现，不要 port caller，但**也不要删文件**（破坏 forward-only 兼容性）。

## Schema 迁移（按用户决策：从 135 顺序递增）

| 新号 | 原官方号 | 内容 | 风险 |
|---|---|---|---|
| 135 | 151 | chat_session.unread_since + last_read_at（IM 未读数）| 低 |
| 136 | 152 | chat_pinned_agent 表 | 低 |
| 137 | 153 | chat_pinned_agent 索引 | 低 |
| 138 | 154 | chat_session.is_agent_intro | 低 |
| 139 | 155 | chat_session.pinned_at | 低 |
| 140 | 156 | chat_session pinned 部分索引 | 低 |
| 141 | 157 | agent_task_queue.delivered_comment_ids | 低 |
| 142 | 158 | agent_task_queue.chat_input_task_id | 中（claim handler 集成） |
| 143 | 159 | chat_message.message_kind | 中（前端 schema 同步） |
| 144 | 160 | chat_message 输入批次索引 | 低 |

跳号 145-149：留给未来 hotfix / 紧急修复（避免下次 patch 时编号冲突）

## 5 个 PR 划分

### PR-1 — Schema 迁移 + IM unread (阶段 3+4 前半)
- migrations 135-137 + 139-141 (7 个新文件)
- schema_migrations 表 forward-only apply
- sqlc regen（chat_session / agent_task_queue / chat_message 表相关 query）
- handler: chat_unread.go (新) + chat_session_pin.go (新)
- **回滚点**：rollback 135-141 schema, 删除 handler 文件

### PR-2 — MUL-4351 输入批次所有权 (阶段 5+6)
- migrations 142 + 143 + 144
- handler 重构：chat.go (1027 行) 拆为 chat.go + chat_history.go + chat_input_ownership.go
- 测试：移植 chat_input_ownership_test.go (7) + chat_pending_tasks_test.go (7) + daemon_comment_delivery_test.go (23) + daemon_comment_workspace_scope_test.go (2)
- **回滚点**：rollback 142-144 schema, 还原 chat.go 单文件

### PR-3 — mcp_overlay handler (阶段 4 后半)
- 新增 mcp_overlay.go (4 个函数：mergeMCPOverlay / hasManagedJSON / passthroughAgentMcpConfig / unmarshalServerMap)
- **回滚点**：删除 1 个文件

### PR-4 — Bug 修复集合 (阶段 7)
- 6 个独立修改（squad_creator_scope / runtime_custom_name / search_timeout+search.go / rollup_guard / comment_reconcile+comment_reply_authz / runtime_redis_keys）
- 每个独立 commit，可单独 revert
- **回滚点**：6 个独立 git revert（实际无 git，用 Edit 还原）

### PR-5 — 前端 UI + 集成验证 + 打包 (阶段 8+9+10)
- packages/views/chat/ 红点改数字、agent pin UI、session pin UI
- packages/core/api/ schema 同步 + parseWithFallback
- 打包 + 替换 /Applications/Multica.app + 三-check 验证
- **回滚点**：cp 0.3.2 .app.bak 还原

## 强制同步检查（任何 PR 必跑）
```bash
SOURCE_VER=$(node -p "require('/Users/jiangjianyan/jjy/multica-main/apps/desktop/package.json').version")
# 必须 = 0.3.3
INSTALLED_VER=$(defaults read /Applications/Multica.app/Contents/Info.plist CFBundleShortVersionString)
# 升级前 = 0.3.2, 升级后 = 0.3.3
```

## 数据安全边界
- PG data: `~/Library/Application Support/Multica/pgdata/` 不动
- backup: `/Users/jiangjianyan/.multica/backups/pre-update-20260711-095239/` (0.3.2 基线)
- snapshot: `/Applications/Multica.app.0.3.2.pre-update-20260711-095239.bak` (0.3.2 .app, 719MB)

## 已知 deferred → 0.3.4（保留）
- task_lifecycle.go (RecoverOrphanedTasks) — 需 sqlc 重生成
- skill_import_archive.go — 需 cherry-pick skill.go:1974
- runtime_profile.go + agent_permission.go — Composio 联动
- dashboard 图表
- issues/surface 重构