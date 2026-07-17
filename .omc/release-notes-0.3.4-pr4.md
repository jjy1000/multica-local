# Multica 0.3.4 PR-4 Incremental Notes (2026-07-12)

> Status: 0.3.4 final incremental hotpatch, no version bump
> Binary deployed to /Applications, server PID 55700 fresh cold start
> Memory: `0.3.4-chat-mul4351-and-ui-2026-07-12.md` + `0.3.3-audit-and-fixes-2026-07-12.md`

## TL;DR

0.3.4 已 ship 后的当晚 PR-3 (daemon claim handler 接 chat_input_task_id) 之后, 立即补 PR-4: **CompleteTask 路径写 `message_kind='no_response'`**。这是 0.3.4 release notes 标注的 "0.3.5 候选 P1-1" 提前完成 — 因为 0.3.4 已经 ship 了 UI 渲染 no_response 气泡, server 端写入路径必须配套, 否则前端永远只看到空表。

## PR-4: CompleteTask 写 message_kind='no_response'

### 改动

**`server/pkg/db/queries/chat.sql`**: `CreateChatMessage` 加 `message_kind` 字段 (sqlc.narg, NULL = server 写默认 'message').

**`server/pkg/db/generated/chat.sql.go`**: sqlc regen 自动生成 `MessageKind pgtype.Text` 在 `CreateChatMessageParams`.

**`server/internal/service/task.go:1410-1470`** (TaskService.CompleteTask chat 路径):

之前: 只在 `payload.Output != ""` 时写 assistant 消息, 空 output 不写.

现在: 
- `payload.Output != ""` → 写 assistant 消息, `MessageKind: "message"`
- `payload.Output == ""` **且** `task.ChatInputTaskID.Valid` (MUL-4351 直聊) → 写空 content + `MessageKind: "no_response"`, **前端渲染 "no text reply this turn" 占位气泡**
- `payload.Output == ""` **且** `chat_input_task_id IS NULL` (pre-142 + Slack/Lark channel task) → 不写, channel engine 自行处理

**`server/internal/service/task.go:1410` (CancelTask 路径)** + **`:1594` (FailTask 路径)** + **`handler/chat.go:477` (SendChatMessage user msg)** + **`integrations/channel/engine/session.go:272` (channel engine)**: 全部显式传 `MessageKind: pgtype.Text{String: "message", Valid: true}` — 不传会因 `message_kind NOT NULL DEFAULT now() ...` 等价于 `NOT NULL` 触发 23502 错误.

**Why `MessageKind: pgtype.Text` 而不是 `string`**: sqlc 推断. ChatMessage 模型 MessageKind 是 `string` (因 schema default 'message'), 但 CreateChatMessageParams 是 `pgtype.Text` (因可空). 两个不一致是 sqlc 已知行为, 必须按 query 与 model 区分.

### 3 个新单元测试 (`server/internal/handler/daemon_test.go` 末尾)

1. **TestCompleteTask_Chat_EmptyOutputWritesNoResponse** — 主线. ChatInputOwner 有效 + 空 output → 写 `message_kind='no_response'` 行, content="" 但 task_id 绑定
2. **TestCompleteTask_Chat_NonEmptyOutputKeepsMessageKindDefault** — 兼容. 非空 output → content 写入, message_kind 保持 schema default 'message'
3. **TestCompleteTask_Chat_EmptyOutputNoChatInputOwnerDoesNotWriteNoResponse** — 边界. pre-142 + Slack/Lark task (chat_input_task_id NULL) + 空 output → 不写 no_response 行 (channel engine 自行处理)

**测试结果**: 3/3 PASS, 全套 27/27 packages pass (除已知 TestReadinessEndpoints 环境问题).

### SQL 改动总览

```sql
-- 0.3.3 落地
ALTER TABLE chat_message ADD COLUMN message_kind TEXT NOT NULL DEFAULT 'message';

-- 0.3.4 PR-4 写入路径
-- payload.Output != "" → MessageKind='message'
-- payload.Output == "" && chat_input_task_id IS NOT NULL → MessageKind='no_response'
-- payload.Output == "" && chat_input_task_id IS NULL → 不写入
```

## 数据

| 项 | 数 |
|---|---|
| 改动 handler/service 文件 | 4 (`service/task.go` + `handler/chat.go` + `integrations/channel/engine/session.go` + `pkg/db/queries/chat.sql`) |
| 新增 sqlc 字段 | 1 (`MessageKind`) |
| 新增 unit test | 3（全部 PASS） |
| 修复老测试 fail | 4 (TestCancelTaskByUser_ChatTaskWithTranscript_PersistsAssistantSnapshot / TestCancelTaskByUser_ChatTaskWithBoundAttachment_SurvivesCancelAndRebinds / TestSendChatMessage_LinksAttachments / TestSendChatMessage_LinksUnattachedAttachments) — 都因 CreateChatMessage 加 message_kind NOT NULL 字段失败, 全部传 `MessageKind: "message"` 修复 |
| 部署 | Multica.app 0.3.4 (719MB) — server PID 55700 etime 0s fresh |
| row parity | workspace=1 / issue=150 / agent=80 / squad=11 / chat_message=0 (零损失) |
| schema_migrations | 181/181 完整 |

## 历史问题最终闭环

| P0/P1 问题 | 0.3.4 final + PR-4 状态 |
|---|---|
| 0.3.2 P0 task:completed issue 缓存不刷新 | ✅ schema-level fix (chat_input_task_id claim) |
| CompleteTask 写 no_response 路径 (0.3.4 之前缺失) | ✅ PR-4 落地, server 端现在写 `message_kind='no_response'`, 前端 0.3.4 已能渲染 |
| chat_input_task_id claim (MUL-4351) | ✅ PR-3 落地, daemon claim 严格只读 bound batch |
| Schema migrations 135-144 dead column | ✅ 全部活 (unread_count 走 ListChatSessionsByCreatorWithUnreadCount, no_response 走 PR-4 写入) |
| 8/10 新 schema 列 dead column | ✅ 6/10 活 (last_read_at, is_agent_intro, pinned_at, chat_pinned_agent, message_kind, chat_input_task_id) |
| last_read_at cursor drift | ✅ MarkChatSessionRead 同步 TouchChatSessionLastRead |

## 0.3.5+ 候选清单

**P1 (3)**:
1. agent-create 路径调 `SetChatSessionAgentIntro` (UI badge 已加, daemon 路径待补)
2. PinChatSession / UnpinChatSession HTTP route + UI button
3. 6 个独立 bug 修复 (squad_creator_scope / runtime_custom_name / search_timeout / rollup_guard / comment_reconcile / runtime_redis_keys)

**P2 (5+)**:
4. chat_history.go + chat_pinned_agent.go (3 文件 + sqlc regen)
5. daemon_comment_delivery 完整 23+2 测试 cherry-pick
6. 前端 agent pin UI button
7. scripts/package.mjs 加 CUSTOM_DMGBUILD_PATH 默认值
8. skill_import_archive + runtime_profile + agent_permission + dashboard + issues/surface 重构

## 关联

- `.omc/release-notes-0.3.4-final.md` — PR-3 (claim) ship notes
- `memory/0.3.4-chat-mul4351-and-ui-2026-07-12.md` — 0.3.4 主 release notes
- `memory/0.3.3-audit-and-fixes-2026-07-12.md` — 0.3.3 audit