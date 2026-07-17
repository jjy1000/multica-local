# Multica 0.3.4 PR-5 Incremental Notes (2026-07-12)

> Status: 0.3.4 final incremental hotpatch, no version bump
> Binary deployed to /Applications, server PID 76448 fresh cold start
> Memory: `0.3.4-chat-mul4351-and-ui-2026-07-12.md`

## TL;DR

0.3.4 final + PR-3 (claim) + PR-4 (no_response) 之后, 立即补 PR-5: **agent-create 路径自动创建 chat_session + 调 SetChatSessionAgentIntro**。这是 0.3.4 release notes 标注的 "0.3.5 P1-1" 提前完成 — UI badge 已经在 0.3.4 final ship, server 端创建 intro session 的路径必须配套, 否则用户看到的永远只是空列表。

## PR-5: CreateAgent → maybeEnqueueAgentIntro

### 改动

**`server/internal/handler/agent.go`**:

1. **CreateAgent 末尾** (line 866 `writeJSON` 前): 当 `runtime.Status == "online"` 时调 `h.maybeEnqueueAgentIntro(ctx, workspaceID, created, ownerID)`. 失败仅 log 不 block 创建响应 (best-effort).

2. **新增 `maybeEnqueueAgentIntro` helper** (line 904): 
   - `CreateChatSession` 创建 session, title = `"Intro · " + agent.Name`
   - `SetChatSessionAgentIntro` 标记 migration 138 字段 `is_agent_intro = TRUE`
   - `TaskService.EnqueueChatTask` 复用 0.3.4 service 层 MUL-4351 绑定 (chat_input_task_id = task.id, last_read_at set)

### 2 个新单元测试 (`server/internal/handler/agent_test.go` 末尾)

1. **TestCreateAgent_AutoEnqueuesIntroChatSession** — 主线. CreateAgent + online runtime → chat_session created with `is_agent_intro=true` + chat task queued with `chat_input_task_id = task.id` (MUL-4351) + `last_read_at` set.
2. **TestCreateAgent_NoIntroWhenRuntimeOffline** — 边界. Runtime offline → agent still created (201), 但 **不创建** intro session (避免 dead thread).

**测试结果**: 2/2 PASS, 全套 27/27 packages pass (除已知 TestReadinessEndpoints 环境问题).

## 数据

| 项 | 数 |
|---|---|
| 改动 handler 文件 | 1 (`agent.go` +60 行) |
| 新增 unit test | 2（全部 PASS） |
| 部署 | Multica.app 0.3.4 — server PID 76448 etime 0s fresh |
| row parity | workspace=1/issue=150/agent=80/squad=11/chat_session=0 核心表零损失 |
| schema_migrations | 181/181 完整 |

## 0.3.4 完整功能链 (final + 4 PRs 完整 ship)

| PR | 内容 | Status |
|---|---|---|
| 0.3.4 main | 10 个 forward-only migration + 7 个新 sqlc query + mcp_overlay 删除 | ✅ |
| 0.3.4 UI | chat_session / chat_message 类型 + unread_count / agent_intro / no_response 渲染 | ✅ |
| PR-3 | daemon claim handler 接 chat_input_task_id caller (MUL-4351) | ✅ |
| PR-4 | CompleteTask 写 message_kind='no_response' | ✅ |
| PR-5 (this) | CreateAgent → maybeEnqueueAgentIntro (migration 138) | ✅ |

**用户视角 0.3.4 final vs 0.3.2**:

| 功能 | 0.3.2 | 0.3.4 final + 4 PRs |
|---|---|---|
| Chat 列表未读 | 红点 (boolean) | **数字 (unread_count)** |
| 新建 agent | 看不到 intro | **自动有 intro 会话 + badge "Intro"** |
| 直聊 agent 沉默 | 看起来死掉 | **占位气泡 "no text reply this turn"** |
| 直聊并发 race | trailing selector 容易误读 | **chat_input_task_id 严格只读 bound batch** |
| MarkRead | 清 unread_since flag | **推进 last_read_at cursor (IM-style)** |

## 0.3.5 剩余 P1 清单 (2 项)

1. **PinChatSession / UnpinChatSession HTTP route + UI button** (sqlc query 已落, handler + UI 路径待补)
2. **6 个独立 bug 修复** (squad_creator_scope / runtime_custom_name / search_timeout / rollup_guard / comment_reconcile / runtime_redis_keys)

## 0.3.5 P2 清单

- chat_history.go + chat_pinned_agent.go (3 文件 + sqlc regen)
- daemon_comment_delivery 完整 23+2 测试 cherry-pick
- 前端 agent pin UI button
- scripts/package.mjs 加 CUSTOM_DMGBUILD_PATH 默认值
- skill_import_archive + runtime_profile + agent_permission + dashboard + issues/surface 重构

## 关联

- `.omc/release-notes-0.3.4-pr4.md` — PR-4 (no_response)
- `.omc/release-notes-0.3.4-final.md` — PR-3 (claim)
- `.omc/release-notes-0.3.4.md` — 0.3.4 主 release notes
- `memory/0.3.4-chat-mul4351-and-ui-2026-07-12.md` — 0.3.4 经验沉淀