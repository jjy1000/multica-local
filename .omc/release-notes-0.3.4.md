# Multica 0.3.4 Release Notes (2026-07-12)

> Ship date: 2026-07-12 (build session started ~21:00, packaged DMG by ~21:58)
> Source: 0.3.3 + PR-2 (MUL-4351 backend) + PR-5 (frontend UI)
> Plan: `.omc/plan-0.3.3-upstream-integration.md` + this notes file

## 范围

按 `.omc/plan-0.3.3-upstream-integration.md`，0.3.4 完成 plan 中**PR-2 (MUL-4351)** 和 **PR-5 (前端 UI)**。PR-1/3/4 已在 0.3.3 完成或推迟。

| 阶段 | 内容 | 状态 |
|---|---|---|
| PR-2 (MUL-4351) | chat_input_task_id 在 EnqueueChatTask 中绑定；IM unread count 派生；MarkChatSessionRead 同步 last_read_at | ✅ |
| PR-5 (前端 UI) | chat_session / chat_message 类型加 unread_count / last_read_at / pinned_at / is_agent_intro / message_kind；fab 红点用 unread_count 派生；is_agent_intro badge；no_response 占位气泡 | ✅ |
| mcp_overlay.go | 已 disabled (0.3.3 audit P1-2 删除) | n/a |

## Schema 改动

**无新增 migration**。0.3.4 复用 0.3.3 已 apply 的 135–144 schema：
- `chat_session.last_read_at` / `is_agent_intro` / `pinned_at` / `unread_since`
- `agent_task_queue.chat_input_task_id`
- `chat_message.message_kind`
- `chat_pinned_agent` 表

**新增 sqlc query**（`server/pkg/db/queries/chat_input_ownership.sql`，不影响 `chat.sql`）：
1. `CreateChatTaskWithInputOwner` — 创建 task 同时设 chat_input_task_id
2. `ListChatInputMessages` — 按 chat_input_task_id 查 user 消息批次
3. `MarkChatMessageNoResponse` — message_kind='no_response'
4. `TouchChatSessionLastRead` — IM cursor 推进
5. `ListChatSessionsByCreatorWithUnreadCount` — IM 红点改数字
6. `PinChatSession` / `UnpinChatSession`
7. `SetChatSessionAgentIntro`

## 后端改动

**`server/internal/service/task.go`**：EnqueueChatTask 改用 `CreateChatTaskWithInputOwner`，让 `chat_input_task_id = task.id`，**避免重复消费 user message 批次**（MUL-4351）。

**`server/internal/handler/chat.go`**：
- `ChatSessionResponse` 加 `unread_count / last_read_at / is_agent_intro / pinned_at` 字段
- `ListChatSessions` 改用 `ListChatSessionsByCreatorWithUnreadCount`，返回 IM 数字
- `MarkChatSessionRead` 同步调 `TouchChatSessionLastRead`，推进 IM cursor

**新增 `server/internal/handler/chat_input_ownership_test.go`**：7 个测试覆盖：
1. EnqueueChatTask 绑定 chat_input_task_id = task.id
2. TouchChatSessionLastRead 推进 cursor
3. Unread count 数 cursor 后的 assistant 消息
4. Fallback 到 unread_since（last_read_at 早于 unread_since）
5. Fresh session 返回 0
6. PinChatSession / UnpinChatSession 切换 pinned_at
7. SetChatSessionAgentIntro 标记 intro session

**测试结果**：`go test ./...` 27/27 packages pass（除已知 TestReadinessEndpoints 环境问题）。

## 前端改动

**`packages/core/types/chat.ts`**：
- `ChatSession` 加 `unread_count?: number / last_read_at?: string | null / is_agent_intro?: boolean / pinned_at?: string | null`
- `ChatMessage` 加 `message_kind?: "message" | "no_response"`

**`packages/views/chat/components/chat-fab.tsx`**：
- `unreadSessionCount` 从 `unread_count` 派生（降级到 `has_unread`）

**`packages/views/chat/components/chat-window.tsx`**：
- 3 处 `has_unread` 派生逻辑改用 `unread_count > 0 || has_unread`
- session row 加 `is_agent_intro` badge（"自我介绍" chip）

**`packages/views/chat/components/chat-message-list.tsx`**：
- `AssistantMessage` 加 `message_kind === "no_response"` 分支，渲染"本轮智能体未回复文本"占位气泡

**`packages/views/locales/{en,zh-Hans}/chat.json`**：加 `window.agent_intro_badge` + `window.no_text_reply` 翻译。

**前端 typecheck**：`pnpm --filter @multica/views typecheck` 0 错误。

## 部署

- ✅ DMG `dist/multica-desktop-0.3.4-mac-arm64.dmg` (240MB) — 通过 `CUSTOM_DMGBUILD_PATH` env var 跳过 dmg-builder 下载 hang
- ✅ ZIP `dist/multica-desktop-0.3.4-mac-arm64.zip` (220MB)
- ✅ Multica.app 0.3.4 (719MB) 部署到 `/Applications/`
- ✅ 三-check：5432 + 8090 listen <12s, /health ok, server PID etime 10s (fresh 0.3.4)
- ✅ Row parity：workspace=1 / issue=150 / agent=80 / squad=11 / chat_session=0 (核心表零损失)

## DMG 打包解法（**P1-4 历史问题 + 0.3.4 新解**）

`electron-builder` 26.8.1 + `dmg-builder` 26.8.1 在这台机器上 `socket hang up` 在 dmgbuild tarball 下载阶段。0.3.3 时 dmg-builder 彻底卡死。

**0.3.4 解法**：
1. 跑 `pre-download-dmg-builder.sh` 预下载 dmgbuild tarball 到 `~/Library/Caches/electron-builder/dmg-builder@1.2.0/dmgbuild-bundle-arm64-75c8a6c-{hash}/`
2. 跑 `CUSTOM_DMGBUILD_PATH=/path/to/dmgbuild pnpm --filter @multica/desktop package` — dmg-builder 检测到 env var 直接用本地路径，**完全跳过下载机制**

**0.3.5+ 改进方向**：把 `CUSTOM_DMGBUILD_PATH` 写进 `scripts/package.mjs` 默认参数 + 把 dmgbuild 路径 pinning 到固定目录，**未来打包零手工干预**。

## 故意剔除（推到 0.3.5+）

- 6 个独立 bug 修复（squad_creator_scope / runtime_custom_name / search_timeout / rollup_guard / comment_reconcile / runtime_redis_keys）
- chat_history.go (Slack/Lark 集成) / chat_pinned_agent.go (前端 UI) — 需要 sqlc regen 12 个新 query + agent 权限守卫
- daemon_comment_delivery 完整 23+2 测试 cherry-pick
- no_response handler 写入路径（`message_kind` schema 已落地，但 CompleteTask 路径还没写 — 0.3.4 UI 渲染支持了，但**没有 task complete 触发** no_response message_kind）
- 前端 agent pin UI（migration 136 schema + sqlc query 已落地，handler 路径 0.3.5）

## 关联

- `.omc/plan-0.3.3-upstream-integration.md` — 原始 5-PR 计划
- `.omc/release-notes-0.3.3.md` — 上一版本
- `.omc/incidents/2026-07-12-0.3.3-comprehensive-audit.md` — 触发本次 0.3.4 修复的 audit
- `memory/0.3.3-audit-and-fixes-2026-07-12.md` — 经验沉淀