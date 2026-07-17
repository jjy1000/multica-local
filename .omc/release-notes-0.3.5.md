# Multica 0.3.5 P1 Ship (2026-07-12)

> Status: ship with 1 real P1 fix + 5 deferred / already-fixed items audited
> Version: apps/desktop/package.json 0.3.4 → 0.3.5
> Binary deployed to /Applications/Multica.app (server PID 39459 etime 14s fresh)

## TL;DR

0.3.5 ship 完成了 0.3.4 plan 列出的 6 个独立 bug 修复中的 **1 个真实可重现的 bug**（squad_creator_scope）+ 1 个审计修复（search_timeout），其余 4 个在当前 fork 代码中**已经不存在**（audit 中误报或已被历史 migration 修复）。DMG 240MB 真打 ship，5432/8090 双 listen <12s，row parity 核心表零损失。

## 真实修复 (2 项)

### 1. squad_creator_scope — `server/internal/handler/squad.go`

**Bug**: `UpdateSquad` / `DeleteSquad` / `AddSquadMember` / `RemoveSquadMember` / `UpdateSquadMemberRole` 5 个 mutation handler 只过 `requireWorkspaceRole(..., "owner", "admin")`，**任何 workspace admin 都能修改/归档/改 membership 别人创建的 squad**。`squad.creator_id` 列早已存在（migration 127）但 5 个 handler 都没读它。

**修复**:
- 加 `canMutateSquad(member, squad)` helper (squad.go:135)
  - workspace owner → always allowed
  - squad creator (squad.creator_id == member.user_id) → allowed
  - legacy zero-UUID creator → fall back to admin (pre-127 数据)
  - 其他 admin/member → forbidden
- 5 个 handler 加 `if !h.canMutateSquad(...) { 403 }` gate
- 加 `isZeroUUID(pgtype.UUID)` helper 处理 NULL UUID + 全零字节两种 legacy 形态

**新增测试** (`server/internal/handler/squad_creator_scope_test.go`):
- `TestCanMutateSquad` — 6 case table-driven: owner / creator / non-creator admin / non-creator member / legacy zero-uuid admin / legacy zero-uuid member
- `TestIsZeroUUID` — 3 case: 零 UUID / 真 UUID / NULL UUID
- `TestUpdateSquad_NonCreatorAdminForbidden` — 集成测试，新建 squad by owner, 非 creator admin 调 PUT /api/squads/{id} 应得 403，DB 验证 name 未被改
- `TestUpdateSquad_CreatorCanMutate` — 集成测试，creator 调 PUT 应得 200，name 真正被改

### 2. search_timeout — `server/internal/handler/issue.go:598`

**Bug**: `SearchIssues` 用 `r.Context()` 直接传给 `h.DB.Query()`，没有任何 timeout 限制。一次 runaway LIKE/ILIKE 搜索（大 workspace 复杂 term）会让 HTTP 连接 hang 整个 server.readTimeout，触发 client retry loop。

**修复**:
- 包一层 `context.WithTimeout(parentCtx, 5*time.Second)`
- `defer cancel()` 确保不泄漏
- 在 `errors.Is(err, context.DeadlineExceeded)` 分支返回 `504 Gateway Timeout` + "search took too long; please narrow your query"
- 保留 happy path 行为不变

**新增测试** (`server/internal/handler/search_timeout_test.go`):
- `TestSearchIssues_DeadlineAlreadyPassed` — 用已过期 deadline context 调 SearchIssues，必须得到 504 或 500 (不能 hang)
- `TestSearchIssues_HappyPathIsFast` — 普通查询 <2s 完成 (远低于 5s wrapper)，response shape 正确含 "issues" 数组

## Deferred / 已修复审计 (4 项)

| 项 | 当前状态 | 备注 |
|---|---|---|
| runtime_custom_name | schema 无 custom_name 列 | 本 fork 没引入 per-user custom_name 概念，audit 提到的 upstream MUL-XXXX 在本 fork 不存在 |
| rollup_guard | 已修 | `rollup_task_usage_hourly()` 已用 `pg_try_advisory_lock(4246)`，`rollup_task_usage_dashboard_daily()` 用 4244，`rollup_task_usage_daily_window()` 在 migration 103 被 drop |
| comment_reconcile | 已修 | migration 018 加 `parent_id ON DELETE CASCADE`，删 root 自动级联删 replies |
| comment_reply_authz | 已修 | `CreateComment` 走 `loadIssueForUser` 已 gate workspace membership |
| runtime_redis_keys | 已 namespace | 所有 runtime redis keys 用 `runtimeID` 作 suffix（`mul:runtime:hb:{id}` / `mul:local_skill:list:pending:{id}`），不存在 cross-runtime 泄漏 |

**为什么不"顺手修"已不存在的 bug**: 稳定性优先 + 最小修改原则。凭空写代码可能引入 regression 而没有真实 bug 解决。审计目标 = 区分"audit 误报 vs 真实 bug"，本轮 4 项 = 误报或上游历史 migration 已修。

## 数据

| 项 | 数 |
|---|---|
| 改动 backend 文件 | 2 (`internal/handler/squad.go` +50 行, `internal/handler/issue.go` +14 行) |
| 新增 backend 文件 | 2 (`internal/handler/squad_creator_scope_test.go`, `internal/handler/search_timeout_test.go`) |
| 新增 helper | `canMutateSquad` + `isZeroUUID` (squad.go) |
| go test 全包 | **28/28 packages PASS** (含 7 个新测试 case) |
| go vet | clean |
| gofmt | clean |
| TypeScript check | (frontend 未改) |
| DMG | `dist/multica-desktop-0.3.5-mac-arm64.dmg` 240MB |
| 部署 | /Applications/Multica.app 0.3.5 — server PID 39459 etime 14s fresh |
| 三-check pass | 5432 LISTEN + 8090 LISTEN + /health `{"status":"ok"}` <12s |
| row parity | workspace=1/issue=150/agent=80/squad=11/comment=676 核心表零损失 |
| schema_migrations | 181/181 完整 |

## 数据安全边界 (data-safety 维持)

- PG data: `~/Library/Application Support/Multica/pgdata/` **完全未触碰**（0.3.4 → 0.3.5 同 schema, forward-only 维持）
- backup: `/Users/jiangjianyan/.multica/backups/pre-update-20260712-231118/` (0.3.4 baseline)
- snapshot: `/Applications/Multica.app.0.3.4.pre-update-20260712-231118.bak` (0.3.4 .app, 719MB)

## 历史经验对照

| 历史 bug | 0.3.5 状态 |
|---|---|
| 0.3.0 destructive-migration | ✅ schema 0 改动 |
| 0.3.0 JWT recovery | ✅ 未触动 |
| 0.3.2 P0 task:completed | ✅ 0.3.4 PR-3 已修 |
| 0.3.3 P1-3 last-packaged drift | ✅ 本轮准备同步 |
| 0.3.3 P1-4 dmg-builder 下载卡死 | ✅ CUSTOM_DMGBUILD_PATH env var 仍工作 |
| 0.3.3 P1-5 8090 server 旧进程 | ✅ verify-desktop-cold-start 模式仍遵循 |
| 0.2.97 daemon auto-start 回归 | ✅ App.tsx / daemon-manager.ts 未触动 |
| **新: squad_creator_scope** | ✅ 修复并 ship |
| **新: search_timeout** | ✅ 修复并 ship |

## 0.3.6 P1 候选 (剩余)

- chat_history.go + chat_pinned_agent.go 拆分 (3 文件 + sqlc regen)
- daemon_comment_delivery 完整 23+2 测试 cherry-pick
- 前端 session list 按 pinned_at DESC 排序 (pinned 集置顶)
- scripts/package.mjs 加 CUSTOM_DMGBUILD_PATH 默认值
- skill_import_archive + runtime_profile + agent_permission + dashboard + issues/surface 重构

## 关联

- `.omc/release-notes-0.3.4-pr6.md` — 0.3.4 PR-6 (PinChatSession)
- `.omc/release-notes-0.3.4-pr5.md` — 0.3.4 PR-5 (intro session)
- `.omc/release-notes-0.3.4-pr4.md` — 0.3.4 PR-4 (no_response)
- `.omc/release-notes-0.3.4-final.md` — 0.3.4 final (MUL-4351 claim)
- `memory/0.3.4-chat-mul4351-and-ui-2026-07-12.md` — 0.3.4 经验沉淀
- `memory/0.3.3-audit-and-fixes-2026-07-12.md` — 0.3.3 audit 报告