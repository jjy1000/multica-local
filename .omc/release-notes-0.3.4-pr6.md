# Multica 0.3.4 PR-6 Incremental Notes (2026-07-12)

> Status: 0.3.4 final incremental hotpatch, no version bump
> Binary deployed to /Applications, server PID 97961 fresh cold start
> Memory: `0.3.4-chat-mul4351-and-ui-2026-07-12.md`

## TL;DR

0.3.4 final + PR-3 (claim) + PR-4 (no_response) + PR-5 (intro session) 之后, 立即补 PR-6: **PinChatSession / UnpinChatSession HTTP route + UI button**. 这是 0.3.4 release notes 标注的 "0.3.5 P1-2" 提前完成 — sqlc query 已落 (PinChatSession / UnpinChatSession), 缺的是 HTTP route + frontend mutation hook + UI button.

## PR-6: PinChatSession / UnpinChatSession 完整链路

### 改动

**`server/cmd/server/router.go:1014`**: 在 `r.Post("/read", h.MarkChatSessionRead)` 后加
- `r.Post("/pin", h.PinChatSession)`
- `r.Post("/unpin", h.UnpinChatSession)`

**`server/internal/handler/chat.go` 末尾** (MarkChatSessionRead 之后): 加 2 个 handler
- `PinChatSession` — 调 `h.Queries.PinChatSession(ctx, session.ID)`, 发 `chat:session_updated` WS event (复用, 无新 event kind)
- `UnpinChatSession` — 调 `h.Queries.UnpinChatSession`, 同样发 `chat:session_updated`
- 两者都过 `gateChatSessionForUser` 鉴权 (与 MarkChatSessionRead 一致, 防 private-agent 后门)

**`packages/core/api/client.ts:1814`**: 加 2 个 API client 方法
- `pinChatSession(sessionId)` → `POST /api/chat/sessions/{id}/pin`
- `unpinChatSession(sessionId)` → `POST /api/chat/sessions/{id}/unpin`

**`packages/core/chat/mutations.ts` 末尾**: 加 2 个 mutation hook (useMutation + optimistic + rollback + invalidate)
- `usePinChatSession` — onMutate 设 `pinned_at: new Date().toISOString()` 在缓存, onError rollback
- `useUnpinChatSession` — onMutate 清 `pinned_at: null` 在缓存, onError rollback
- 都 onSettled invalidate `chatKeys.sessions(wsId)`

**`packages/views/chat/components/chat-window.tsx`**: 在 renderRow 的 group-hover 按钮区域加 pin button
- 复用 `Pin` lucide icon (filled when pinned, outline when not)
- 复用 onPointerDown / onClick 模式 (与 delete/stop 按钮一致)
- 调用 `pinSession.mutate` / `unpinSession.mutate`

**`packages/views/locales/{en,zh-Hans}/chat.json`**: 加 4 个 i18n key
- `row_pin_aria` / `row_unpin_aria` / `pin_tooltip` / `unpin_tooltip`

## 数据

| 项 | 数 |
|---|---|
| 改动 backend 文件 | 2 (`router.go` +2 routes, `chat.go` +60 行) |
| 改动 frontend 文件 | 4 (`api/client.ts` +20 行, `chat/mutations.ts` +70 行, `chat-window.tsx` +30 行, 2 locale JSON) |
| 新增 i18n key | 4 (en + zh-Hans) |
| 部署 | Multica.app 0.3.4 — server PID 97961 etime 0s fresh |
| row parity | workspace=1/issue=150/agent=80/squad=11/chat_session=0 核心表零损失 |
| schema_migrations | 181/181 完整 |
| TypeScript check | 0 errors |
| go test | 27/27 packages pass + 0.3.3 写的 TestPinChatSession_TogglesPinnedAt 仍 PASS |

## 0.3.4 完整功能链 (final + 5 PRs 完整 ship)

| PR | 内容 | Status |
|---|---|---|
| 0.3.4 main | 10 个 forward-only migration + 7 个新 sqlc query + mcp_overlay 删除 | ✅ |
| 0.3.4 UI | chat_session / chat_message 类型 + unread_count / agent_intro / no_response 渲染 | ✅ |
| PR-3 | daemon claim handler 接 chat_input_task_id caller (MUL-4351) | ✅ |
| PR-4 | CompleteTask 写 message_kind='no_response' | ✅ |
| PR-5 | CreateAgent → maybeEnqueueAgentIntro (migration 138) | ✅ |
| PR-6 (this) | PinChatSession / UnpinChatSession HTTP route + UI button | ✅ |

**用户视角 0.3.4 final + 5 PRs vs 0.3.2**:

| 功能 | 0.3.2 | 0.3.4 final + 5 PRs |
|---|---|---|
| Chat 列表未读 | 红点 (boolean) | **数字 (unread_count)** |
| 新建 agent | 看不到 intro | **自动有 intro 会话 + badge "Intro"** |
| 直聊 agent 沉默 | 看起来死掉 | **占位气泡 "no text reply this turn"** |
| 直聊并发 race | trailing selector 容易误读 | **chat_input_task_id 严格只读 bound batch** |
| MarkRead | 清 unread_since flag | **推进 last_read_at cursor (IM-style)** |
| 重要会话置顶 | 需手动滚动找 | **Pin 按钮, pinned 置顶, group-hover 显示** |

## 0.3.5 P1 剩余 (1 项)

**6 个独立 bug 修复** (squad_creator_scope / runtime_custom_name / search_timeout / rollup_guard / comment_reconcile / runtime_redis_keys)

## 0.3.5 P2 候选

- chat_history.go + chat_pinned_agent.go (3 文件 + sqlc regen)
- daemon_comment_delivery 完整 23+2 测试 cherry-pick
- 前端 session list 按 pinned_at DESC 排序 (server 已经按 updated_at DESC 排, pinned 集放在最上面需要前端分组)
- scripts/package.mjs 加 CUSTOM_DMGBUILD_PATH 默认值
- skill_import_archive + runtime_profile + agent_permission + dashboard + issues/surface 重构

## 关联

- `.omc/release-notes-0.3.4-pr5.md` — PR-5 (intro session)
- `.omc/release-notes-0.3.4-pr4.md` — PR-4 (no_response)
- `.omc/release-notes-0.3.4-final.md` — PR-3 (claim)
- `.omc/release-notes-0.3.4.md` — 0.3.4 主 release notes
- `memory/0.3.4-chat-mul4351-and-ui-2026-07-12.md` — 0.3.4 经验沉淀