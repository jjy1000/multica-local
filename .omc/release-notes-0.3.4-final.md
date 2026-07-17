# Multica 0.3.4 (Final) — daemon claim 接 MUL-4351 caller (2026-07-12)

> Status: 0.3.5 incremental hotpatch, no version bump
> 0.3.4 binary already deployed to /Applications (PID 23774 fresh)
> Memory: `0.3.4-chat-mul4351-and-ui-2026-07-12.md`

## TL;DR

0.3.4 完整 ship 后, 当晚 22:14 补 PR-3: daemon claim handler 接 `ListChatInputMessages` + `chat_input_task_id` caller。这是 0.3.2 P0 task:completed 修复的**真正底层** — 之前 P0 fix 是在 use-realtime-sync 缓存层手动 invalidate, 0.3.5 让 daemon claim 通过 chat_input_task_id 严格只读自己的 input batch, **从 schema 层面消除 race condition**。

## PR-3: daemon claim handler 接 MUL-4351 caller

### 改动

**`server/internal/handler/daemon.go:1601` (ClaimTaskByRuntime handler)**

之前: 不管 `task.chat_input_task_id`, 总是用 `ListChatMessages(session.ID) + trailingUserMessages()` 取消息 (race-prone)。

现在: **优先** `task.ChatInputTaskID.Valid` → `ListChatInputMessages(task.ChatInputTaskID)`, 严格只读绑定的 user message 批次; **fallback** `chat_input_task_id IS NULL` → 老 trailing selector (兼容 pre-142 + Slack/Lark channel tasks)。

```go
if len(resp.ChatMessage) == 0 {
    if task.ChatInputTaskID.Valid {
        // MUL-4351 path: 144 partial index 命中, 严格 batch 边界
        batch, _ := h.Queries.ListChatInputMessages(r.Context(), task.ChatInputTaskID)
        // ... parts + attachments
        resp.ChatMessage = strings.Join(parts, "\n\n")
    } else {
        // Pre-142 + channel task fallback
        msgs, _ := h.Queries.ListChatMessages(r.Context(), cs.ID)
        unanswered := trailingUserMessages(msgs)
        // ... existing logic
    }
}
```

### 3 个新单元测试

**`server/internal/handler/daemon_test.go` 末尾新增 3 个 TestClaimTask_MUL4351_* 测试**:

1. **TestClaimTask_MUL4351_LoadsBoundInputBatch** — 主线. 创建 task 通过真 `TaskService.EnqueueChatTask` (自动绑 `chat_input_task_id = task.id`), 插入 2 条 bound messages + 1 条 racing NULL-bounded message. 断言 claim 只返回 bound batch.
2. **TestClaimTask_MUL4351_NullInputOwnerFallsBackToTrailing** — fallback. 模拟 pre-142 row (`chat_input_task_id IS NULL`), 断言走 trailing selector.
3. **TestClaimTask_MUL4351_RetryChildInheritsInputOwner** — 关键边界. Parent failed, retry child 复用 parent 的 `chat_input_task_id`. 验证 retry 只重读 parent 的 input batch, 不读"parent failed 之后才到的 racing message".

**测试结果**: 3/3 PASS.

## 历史问题最终闭环

| 0.3.2 P0 问题 | 0.3.3 | 0.3.4 (前) | **0.3.4 (final)** |
|---|---|---|---|
| task:completed issue 缓存不刷新 | use-realtime-sync 加 invalidate (workaround) | 同 0.3.2 fix | **claim handler 读 chat_input_task_id (schema-level fix)** ✅ |
| 8090 server 是 0.3.2 旧进程 | audit 发现 | verify script | **0.3.4 PID 23774 etime 2s fresh** ✅ |
| schema_migrations 4 行 0.3.2 缺失 | audit backfill | 维持 | **维持 181/181 完整** ✅ |
| dmg-builder 下载卡死 | audit 记录 | CUSTOM_DMGBUILD_PATH 绕过 | **CACHED dmgbuild 重复利用** ✅ |
| last-packaged.json 漂移 | audit 修 | 0.3.4 同步 | **维持三方同步** ✅ |

## 0.3.4 Final 部署状态

| 项 | 值 |
|---|---|
| `/Applications/Multica.app` version | 0.3.4 |
| Server PID | 23774 (etime 2s, fresh cold start after claim handler 改动) |
| `/health` | `{"status":"ok"}` |
| schema_migrations | 181 rows == 181 .up.sql files |
| row parity | workspace=1 / issue=150 / agent=80 / squad=11 / chat_session=0 (核心表零漂移) |
| DMG size | 240MB (`dist/multica-desktop-0.3.4-mac-arm64.dmg`) |
| ZIP size | 220MB (`dist/multica-desktop-0.3.4-mac-arm64.zip`) |

## 0.3.5 候选清单（按 release-notes-0.3.4.md）

**P1 (5 项)**:
1. CompleteTask 路径写 `message_kind='no_response'` (0.3.4 UI 支持, server 写入路径待补)
2. agent-create 路径调 `SetChatSessionAgentIntro` (UI badge 已加, daemon 路径待补)
3. PinChatSession / UnpinChatSession HTTP route + UI button
4. 6 个独立 bug 修复
5. bundle-cli.mjs 加 last-packaged.json 写钩子

**P2 (5+ 项)**:
6. chat_history.go + chat_pinned_agent.go (3 文件 + sqlc regen)
7. daemon_comment_delivery 完整 23+2 测试 cherry-pick
8. 前端 agent pin UI button
9. scripts/package.mjs 加 CUSTOM_DMGBUILD_PATH 默认值
10. skill_import_archive + runtime_profile + agent_permission + dashboard + issues/surface 重构

## 给 0.3.5+ 的预防 contract (from memory 0.3.3 audit)

1. **schema_migrations 同步检查** — ship 前必跑:
   ```bash
   diff <(ls server/migrations/*.up.sql | xargs -n1 basename | sed 's/\.up\.sql$//' | sort) \
        <(PGPASSWORD=multica psql ... -tAc "SELECT version FROM schema_migrations" | sort)
   ```
2. **last-packaged.json 强制同步** — bundle-cli.mjs 加 post-write 钩子
3. **verification 必须 pkill + cold restart** — 不能用 in-flight server PID 误导
4. **dmg-builder 预下载** — pre-package 阶段 curl dmgbuild tarball, 或用 `CUSTOM_DMGBUILD_PATH` env var
5. **release-notes 显式标注 user-visible delta** — 不能只说"加了 X feature", 必须说"用户能否感知"

## 关联

- `.omc/release-notes-0.3.4.md` — 0.3.4 主 release notes
- `memory/0.3.4-chat-mul4351-and-ui-2026-07-12.md` — 0.3.4 主要 ship 记录
- `memory/0.3.3-audit-and-fixes-2026-07-12.md` — 0.3.3 audit 经验
- `.omc/incidents/2026-07-12-0.3.3-comprehensive-audit.md` — 完整 audit report