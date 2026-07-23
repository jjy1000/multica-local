---
name: release-notes-0.3.61
created: 2026-07-23T01:30:00Z
updated: 2026-07-23T01:35:00Z
status: complete
---

# Multica 0.3.61 — squad-as-subscriber / squad-as-recipient schema fix

修复 fork 实测中发现的 latent schema bug：当 `issue.assignee_type='squad'`
时，三条事件路径会向 `issue_subscriber` / `inbox_item` / `agent_task_queue`
写入 `squad` 类型或触发"both accountable/originator must be set and equal"
约束，全部 23514（CHECK violation）。0.3.60 ship 后首次 squad 任务
(`c18fe11b-...` × squad `ff1ee606-...`) 显式报错。

根因：上游 0.4.0 官方版本同款 latent bug（已与
`/Users/jiangjianyan/Downloads/multica-main` 三文件 diff = 0 验证），schema
漏扩 `squad` 受体，且 `accountable_user_id` 列从 0.3.x 起就无 Go 写入
路径但约束仍把它强制为非空当 originator 出现时。

## Changes

### 1. Migration 167 — `server/migrations/167_squad_subscriber_inbox_constraints.{up,down}.sql`
- `issue_subscriber_user_type_check`: `'member','agent'` → `'member','agent','squad'`
- `inbox_item_recipient_type_check`: `'member','agent'` → `'member','agent','squad'`
- `agent_task_queue_accountable_matches_originator`:
  原谓词 `((originator IS NULL) OR ((accountable IS NOT NULL) AND (accountable = originator)))`
  → 新谓词 `((originator IS NULL) OR (accountable IS NULL) OR (accountable = originator))`
  即两列独立 nullable，只在都非空时才要求相等

### 2. Wire-up 路径（未改）
- `server/cmd/server/subscriber_listeners.go:36,61` — `addSubscriber(*issue.AssigneeType, *issue.AssigneeID, "assignee")` 继续直传
- `server/cmd/server/notification_listeners.go:564,618,630,836,876` — `notifyDirect(*issue.AssigneeType, *issue.AssigneeID, ...)` 继续直传
- `server/internal/service/task.go:541 enqueueMentionTask` — squad leader briefing 走 `CreateAgentTask` 不写 `accountable_user_id`

不再有 schema-side 阻断；handler 端逻辑保持与官方一致。

## 已知边界 / 未包含
- 仅修 schema CHECK 约束，**不**修 handler filter（`subscriber_listeners.go` 仍订阅 squad assignee —— 现 schema 允许，效果：squad 收到 issue:created 时被加为 subscriber；后续 inbox 通知也照常发）。如需 squad 不收个人化 inbox，可后续在 `notifyDirect` 加 `recipient_type != "squad"` short-circuit —— 不在本版范围。
- `MergeCommentIntoPendingTask` 写 `originator_user_id`（member 评论时）继续正常，原约束不阻断。新约束允许 `originator IS NOT NULL AND accountable IS NULL`（即 squad leader task 接收 member 评论触发合并）。

## Ship 前置（已完成 2026-07-23）

`apps/desktop/package.json` → `0.3.61`；migration 167 已在生产 PG 应用
（schema_migrations 已收录）；`/Applications/Multica.app` 替换为 0.3.61；
cold-start 三检查（5432+8090 /health）+ row parity（1/224/1371/92）通过。
详见 `.omc/0.3.61-ship-2026-07-23.md`。