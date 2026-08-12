---
name: upstream-integration-0.5.0-proposal-rev2
created: 2026-07-31
updated: 2026-08-12T13:05:44Z
type: project
status: complete
---

# Multica 0.5.0 Fork 集成 — Proposal rev2(2026-07-31)

> 配套 v1 → v2 的关键修正:Downloads 是**晚于 fork wave 2** 的上游 snapshot,大量
> migration 已被 fork wave 2 cherry-pick 覆盖。Wave 1 实际可做内容**从 11 缩减到 2**。

## TL;DR — 关键修正

| 序号 | 内容 | fork prod DB 现状 | Wave 1 决策 |
|---|---|---|---|
| 200 | `inbox_archived_listing_index` | **已 ship**(fork wave 2) | ❌ 跳过 |
| 201 | `inbox_active_by_issue_index` | **已 ship**(fork wave 2) | ❌ 跳过 |
| 202 | `runtime_profile_add_qwen` | **已有** qwen 在 `protocol_family` CHECK | ❌ 跳过 |
| 203 | `issue_workspace_assignee_index` | **已 ship**(fork wave 2 = 218) | ❌ 跳过 |
| 204 | `issue_workspace_parent_index` | **已 ship**(fork wave 2 = 219) | ❌ 跳过 |
| 205 | `issue_workspace_position_index` | **已 ship**(fork wave 2 = 220) | ❌ 跳过 |
| 206 | `agent_disabled_runtime_skills` | **已 ship**(fork wave 2 = 223) | ❌ 跳过 |
| 207-211 | **`client_usage_daily` 新表 + 5 索引** | **不存在** | ✅ Wave 1 重点 |
| 212 | `agent_service_tier` | 部分存在(fork wave 2 = ?) | ⏸ Wave 2 |
| **213** | **`task_usage_authoritative_cost` 列加** | **不存在 cost 列** | ✅ Wave 1 |
| 214-215 | `chat_session_project` | **已 ship**(fork wave 2 = 224-225) | ❌ 跳过 |
| 216-240 | VCS / channel_media / quick_actions | fork 没 | ⏸ Wave 3+ |

**Wave 1 实际可做 = 2 个 module,8 migration,5-7 commit**。

## 0.5.0 总体 Wave 划分(rev2)

| Wave | Module | Migration | Commit | 预计工作量 |
|---|---|---|---|---|
| **Phase 0** | cloud 拒收 grep verify | 0(只 grep) | 0 | 0.5 天 |
| **Wave 1** | **client_usage_daily + task_usage_authoritative_cost** | **8** | **5-7** | **1-2 周** |
| Wave 2 | agent_service_tier + chat_session_project 残余 | 4-6 | 4-5 | 1-2 周 |
| Wave 3 | VCS 集成(216-223) | 8 | 10-15 | 2-3 周(云风险高) |
| Wave 4 | channel_media + agent_task_queue 索引(224-234) | 11 | 8-10 | 2 周 |
| Wave 5 | chat_quick_actions(235-240) | 6 | 6-8 | 1-2 周 |
| Wave 6+ | comment + search trgm + chat pinned(135-145 + 149-160+) | ~60 | 30-40 | 4-6 周 |
| **总计** | | **~100 migration** | **~70 commit** | **3-4 月** |

## Wave 1 详细(本 session 试点)

### Module 1: `client_usage_daily` 新表(5 migration)

**Downloads migrations**:
- `207_client_usage_daily.up.sql` — CREATE TABLE 7 列(user_id, client_type, install_id, activity_date, workspace_id, client_version, os, first_active_at, last_active_at, runtime_probed_at, probe_result, runtime_count, provider_summary, online_count, offline_count, created_at, updated_at + 3 CHECK constraints)
- `208_client_usage_daily_unique_index.up.sql` — `client_usage_daily_identity_date_uidx` UNIQUE on (user_id, client_type, install_id, activity_date)
- `209_client_usage_daily_primary_key.up.sql` — ALTER TABLE ADD PRIMARY KEY USING INDEX
- `210_client_usage_daily_query_index.up.sql` — `client_usage_daily_activity_client_user_idx` on (activity_date, client_type, user_id)
- `211_client_usage_daily_workspace_index.up.sql` — `client_usage_daily_workspace_idx` on (workspace_id) WHERE workspace_id IS NOT NULL

**Downloads 应用层文件**(grep 'client_usage' -r):
- `server/internal/handler/client_usage.go` + test
- `server/pkg/db/generated/client_usage.sql.go`(sqlc regen)
- `server/pkg/db/queries/client_usage.sql`
- 也许 `server/cmd/server/router.go` 路由注册
- 也许 `packages/core/api/client.ts` TS client
- 也许 `packages/views/` 一些 usage stats UI(可能 fork 不需要)

**Commit 序列(3 commit)**:
1. `feat(server): client_usage_daily table + 5 indexes` — copy 5 .up.sql + 5 .down.sql to `server/migrations/fork-2xx_*` (用 207+ offset 防 number 冲突),run `migrate up` 验证
2. `feat(server): client_usage handler + sqlc regen` — copy `server/internal/handler/client_usage.go` + test, copy `server/pkg/db/queries/client_usage.sql`,run `make sqlc`,patch `server/cmd/server/router.go` if needed
3. `test(server): client_usage + verify` — run go test,verify row parity

### Module 2: `task_usage_authoritative_cost` 列加(1 migration)

**Downloads migration 213**:`ALTER TABLE task_usage` 加列(`cost_usd_ticks BIGINT`,`cost_source TEXT`,`cost_recorded_at TIMESTAMPTZ`,`uncosted_input_tokens BIGINT`,`uncosted_output_tokens BIGINT`,`uncosted_cache_read_tokens BIGINT`,`uncosted_cache_write_tokens BIGINT`)

**fork prod DB 现状**:`task_usage` 表 10 列(无 cost/uncosted 字段)

**Downloads 应用层**(grep 'cost' / 'task_usage' / 'authoritative'):
- `server/internal/scheduler/jobs_task_usage.go` — 调度任务
- `server/pkg/db/generated/task_usage.sql.go` — sqlc regen
- `server/pkg/db/queries/task_usage.sql` — query 定义
- 也许 `server/internal/service/` 一些 cost 计算

**Commit 序列(2-3 commit)**:
1. `feat(server): task_usage_authoritative_cost columns` — copy 1 .up.sql + .down.sql to `server/migrations/fork-213_*`,run `migrate up` 验证
2. `feat(server): authoritative_cost rollup + scheduler` — copy `server/internal/scheduler/jobs_task_usage.go`,copy query, run `make sqlc`
3. `test(server): authoritative_cost + go test` — run go test + verify

### Wave 1 验证闸门

每个 commit 后必跑:
- `pnpm typecheck` 6/6 PASS(无 TS 改动时 cached)
- `cd server && go build ./...` PASS
- `cd server && go test -count=1 -timeout 180s ./internal/...` PASS
- `pnpm test` 8/8 PASS(无 TS 改动时 cached)
- `grep -rE 'posthog|electron-updater|cloud-billing|cloud-runtime|contact-sales|GoogleLogin|autoUpdate' server packages apps | grep -v node_modules` 应只命中 stub
- `node scripts/check-agents-docs-sync.mjs` PASS
- prod DB 行 parity(workspace=1 / issue=244 / comment=1510 / agent=96 / user_plugin=0 + 新表行数)

### Wave 1 风险评估

| 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|
| `client_usage_daily` PK 重建在 prod DB 失败 | 低 | 中(IF NOT EXISTS 守卫) | dev DB 试跑 |
| Downloads sqlc regen 后 type 命名与 fork 冲突 | 中 | 中 | `make sqlc` 后 typecheck 必过 |
| Downloads handler 与 fork `internal/handler/inbox.go` 命名冲突 | 中 | 中 | 改 client_usage 路径,选 fork 优先 |
| Downloads router 路由 `/api/client_usage*` 与 fork 路由前缀冲突 | 低 | 中 | 路由命名 check |
| task_usage 新列加触发 `ADD COLUMN` 默认值计算慢 | 低 | 低 | `BIGINT` 默认 0,微秒级 |

### Wave 1 不做

- VCS / channel_media / quick_actions(Wave 2+)
- Downloads 212 `agent_service_tier`(改 agent dispatch,Wave 2)
- Downloads 135-145 + 149-160+(comment + search trgm + chat pinned,Wave 6+)
- 6 cloud migration(永久拒收)
- `127_issue_pull_request_reference_only`(collision,fork 已有 `127_task_squad_id`)

## 3 路径 merge strategy

| 路径 | 何时用 | Wave 1 怎么用 |
|---|---|---|
| **Fork 优先** | Downloads 改 fork 已有内容 | 不动 Downloads sql 文件,只 cherry-pick Downloads 应用层 + 用 fork 已有 schema 改 |
| **上游优先** | Downloads 新内容(新表、新列) | 直接复制 Downloads `.up.sql` 到 fork `server/migrations/fork-XXX_*`(XXX = Downloads 编号 + 0 offset) |
| **冲突手工** | 两边都改 | agent 报冲突,用户拍板 |

## 决策点(等用户批准)

| 决策 | 选项 |
|---|---|
| **范围** | 仅 Wave 1(推荐,1-2 周) / Wave 1+2(2-3 周) / 全部 6 wave(3-4 月) |
| **节奏** | 每 wave 单独 ship(推荐) / 整批 ship / 仅 git baseline |
| **网络** | 修代理 7890 fetch 真最新 / 不动,基于 Downloads |
| **应用层** | 全 cherry-pick(推荐) / 选 bug fix only / 重写 |
| **Release** | 0.5.0 fork release ship / 仅 git / 不标 version |
| **Worktree** | 在 `wave1-upstream-2026-07-31` worktree 试 / 新建 worktree |

## 给 jyf 的具体建议

**最小风险路径**:仅做 Wave 1(2 个 module,5-7 commit,1-2 周):
1. Module 1 `client_usage_daily` — 全新表,无冲突风险,纯增量
2. Module 2 `task_usage_authoritative_cost` — 列加,fork task_usage 已存在,只加列

**不要**直接"全量集成 Downloads 110 migrations" — 月级 任务,agent 一波跑会破 fork。

**Wave 1 完成后**:
- 出 ship 报告
- 决定 Wave 2+ 是否继续
- 决定 release ship 策略

## 下一步(等用户批准)

1. 用户审本 proposal rev2
2. 拍板 Wave 1 范围 + 节奏 + worktree
3. 在 `wave1-upstream-2026-07-31` worktree 开始 Wave 1 Module 1
4. Module 1 完成后 Module 2
5. 任何 commit 失败 → 立即停 + 报失败输出 + 不继续
6. Wave 1 完成 → 出 ship 报告
