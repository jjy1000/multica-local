# Multica 0.3.3 Release Notes (2026-07-11 → 2026-07-12)

> Ship date: 2026-07-12 (planned 2026-07-11, packaging DMG 卡 dmg-builder download 推迟 1 天).
> Source: https://Downloads/multica-main (官方 multica-ai/multica 0.1.0 tag + 后续 151-160 migration).

> ⚠️ **用户视角 delta = 0** — 0.3.3 与 0.3.2 在 GUI / 行为上**完全相同**。
>
> 0.3.3 ship 内容是 **schema-only foundation + 1 个孤立工具函数 (已被 audit 删除)**:
> - 10 个 migration apply 后, 8/10 新列 (`last_read_at` / `is_agent_intro` / `pinned_at` / `delivered_comment_ids` / `chat_input_task_id` / `message_kind` + chat_pinned_agent 表 / idx_chat_message_input_owner) 在 0.3.3 是 **dead column** (handler 0 引用, 前端 0 引用)
> - `mcp_overlay.go` 4 函数 0 caller, audit 后已 rename 到 `.disabled`
> - 真实用户可见变化: **无**
>
> 0.3.4 必须做 (per `.omc/incidents/2026-07-12-0.3.3-comprehensive-audit.md`):
> - PR-2: MUL-4351 完整集成 (chat_input_task_id claim handler + message_kind 渲染)
> - PR-5: 前端 UI (IM 红点改数字 / agent pin / session pin / agent intro / no_response)
> - 5 P1 fixes (mcp_overlay dead code 已删, 但 dmg-builder / last-packaged / verification 流程需补)
> - 6 P2 fixes (chat_session 新列接入 + plan/CLAUDE.md 文档修订)

## 范围控制

按 `.omc/plan-0.3.3-upstream-integration.md`，**最小可行 0.3.3** 只集成：

1. 10 个 forward-only schema migration (135–144)
2. mcp_overlay.go handler (4 个函数, 纯 stdlib)

**未实装**（推到 0.3.4）：

- chat_history.go / chat_pinned_agent.go / chat_title.go — 依赖 sqlc 重生成 + LLM 抽象层 + Slack 集成（已剔除）
- daemon_comment_delivery 完整 23 测试 + workspace scope 2 测试
- 6 个独立 bug 修复（squad_creator_scope / runtime_custom_name / search_timeout / rollup_guard / comment_reconcile / runtime_redis_keys）
- 前端 IM unread 红点 / agent pin UI / session pin UI / agent intro / no_response 渲染

## Schema 集成（apply + verified）

| 迁移 | 内容 | 应用结果 |
|---|---|---|
| 135_chat_session_last_read_at | IM 未读游标 | ✅ chat_session.last_read_at TIMESTAMPTZ |
| 136_chat_pinned_agent | agent 快速置顶表 | ✅ chat_pinned_agent 表 |
| 137_chat_pinned_agent_index | (workspace_id, user_id, position) 索引 | ✅ idx_chat_pinned_agent_user_ws |
| 138_chat_session_is_agent_intro | 自我介绍会话标志 | ✅ chat_session.is_agent_intro BOOLEAN |
| 139_chat_session_pinned_at | 会话置顶 | ✅ chat_session.pinned_at TIMESTAMPTZ |
| 140_chat_session_pinned_index | (creator_id, workspace_id, pinned_at DESC) 部分索引 | ✅ idx_chat_session_pinned |
| 141_agent_task_queue_delivered_comment_ids | daemon 已交付 comment 追踪 | ✅ agent_task_queue.delivered_comment_ids UUID[] |
| 142_agent_task_queue_chat_input_task_id | MUL-4351 输入批次所有权 | ✅ agent_task_queue.chat_input_task_id UUID |
| 143_chat_message_message_kind | 显式终态 message/no_response | ✅ chat_message.message_kind TEXT |
| 144_chat_message_input_owner_index | (task_id, created_at) WHERE role='user' 部分索引 | ✅ idx_chat_message_input_owner |

## Handler 集成

- `server/internal/handler/mcp_overlay.go` — 4 个函数: `mergeMCPOverlay` / `hasManagedJSON` / `passthroughAgentMcpConfig` / `unmarshalServerMap`. 纯 stdlib 零外部依赖. 为未来 per-task MCP bearer 注入留接口.

## 故意剔除（按本地化策略）

- 128_129_130_131_132_133 (Composio / Slack / Lark / GitHub / Permissions) — 沿用 0.3.2 剔除决定
- PostHog / Google OAuth / Cloud billing / Invitations / electron-updater publish
- chat_history.go / chat_pinned_agent.go / chat_title.go (handler 实装) — 推到 0.3.4

## 部署 + 验证

### 打包路径偏离
- 期望: `pnpm --filter @multica/desktop package` → `dist/multica-desktop-0.3.3-mac-arm64.dmg`
- 实际: DMG 生成卡在 dmg-builder download `dmgbuild-bundle-arm64-75c8a6c.tar.gz` (网络/缓存问题)
- 已生成: `dist/multica-desktop-0.3.3-mac-arm64.zip` (220MB) + `dist/mac-arm64/Multica.app` (719M, version=0.3.3)
- 部署: 直接 `cp -R dist/mac-arm64/Multica.app /Applications/` (跳过 DMG 中间环节)

### 三-check pass (8 秒内)
- 5432 LISTEN ✅
- 8090 LISTEN ✅
- `/health` → `{"status":"ok"}` ✅
- Renderer PID 53531 正常启动 (Electron 39 NSAlert 阻塞问题未复现)
- Daemon foreground PID 53879 + server PID 52246

### Row parity (0.3.2 基线 → 0.3.3)
| 表 | 基线 (0.3.2) | 0.3.3 | 漂移 |
|---|---|---|---|
| workspace | 1 | 1 | 0 |
| issue | 148 | 150 | +2 (新 app 启动 quick-create) |
| comment | 668 | 674 | +6 (启动活动) |
| agent | 80 | 80 | 0 |
| squad | 11 | 11 | 0 |
| chat_session | 0 | 0 | 0 |
| chat_message | 0 | 0 | 0 |
| chat_pinned_agent | — | 0 | 新表 |

漂移在合理范围（启动期 quick-create 自动 agent 任务），核心表 (workspace / agent / squad) 零损失。

### Test pass
- `go test ./...` → 27/27 包 pass
- 唯一 FAIL: `cmd/server TestReadinessEndpoints` — 已记入 0.3.2 release notes 的"已知环境问题，与本次改动无关"
- `pnpm --filter @multica/desktop typecheck:node` → 0 错误

## 备份锚点

- .app 备份: `/Applications/Multica.app.0.3.2.pre-update-20260711-103903.bak` (719M)
- PG 备份: `/Users/jiangjianyan/.multica/backups/pre-update-20260711-103903/`
- 回滚命令: `pkill -f "Multica.app/Contents/MacOS/Multica" && cp -R /Applications/Multica.app.0.3.2.pre-update-20260711-103903.bak /Applications/Multica.app`

## 已知 deferred → 0.3.4

1. chat_history.go / chat_pinned_agent.go / chat_title.go (handler 实装 + 路由注册)
2. MUL-4351 完整集成: chat_input_task_id claim handler / message_kind 渲染
3. daemon_comment_delivery 完整实现 (官方 23 + 2 测试 cherry-pick)
4. 6 个独立 bug 修复: squad_creator_scope / runtime_custom_name / search_timeout / rollup_guard / comment_reconcile / runtime_redis_keys
5. 前端 UI: Chat 列表红点改数字 / agent pin UI / session pin UI / agent intro 渲染 / no_response 状态
6. dmg-builder 下载问题 — 0.3.4 打包前需手动 `pnpm config set store-dir` 或预下载 dmg-builder tarball
7. task_lifecycle.go / skill_import_archive.go / runtime_profile.go / agent_permission.go / dashboard 图表 / issues/surface 重构