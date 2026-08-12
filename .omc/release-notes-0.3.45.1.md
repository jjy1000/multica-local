---
name: 0.3.45.1 release notes
created: 2026-07-18T15:00:00Z
updated: 2026-08-12T13:05:44Z
status: complete
---

# 0.3.45.1 — agent_self_optimization 真正接入 (调度器 + 学习流水线 + 实验室历史)

## Why this ship

0.3.17 + 0.3.27 B4 把 `agent_self_optimization` 的"插件化 shell"做完了 — catalog literal / install handler / visibility seed / autopilot cron 占位 / placeholder view。但**没有真正的学习流水线**: autopilot 调度器调度 2 条占位 autopilot (SkillOpt 每日 + 每 3 工作日批量) 但 agent 没有执行逻辑。0.3.45.1 把这条缺失的执行链接通:

- 用户原文任务：「让它定时可以根据 multica 的任务问题（已完结的任务）来进行自我学习和优化相关智能体」
- 频次：每四天一次 + 工作日 10:00 (本地时区)
- 创建的自动化任务显示在主面板, 标注实验室图标
- 任务结束后自动归档 (lab_source='agent_self_optimization' + exclude_lab=true)
- 优化记录在实验室该功能中显示
- 学习数据写入外置 KB

## What changed

### 1. Service 三件套 (`server/internal/service/agent_self_optimization/`)

5 个新文件 (~970 LOC):

- **`scheduler.go`** (150 LOC) — `NextTrigger(lastSuccess, now, loc)` 纯函数, 实现"上次成功后 ≥96h + 工作日 10:00 (本地时区)"双门控. `time.Hour` 精度, 0 I/O, table-driven 测试覆盖所有 weekday/weekend 边界.
- **`source.go`** (160 LOC) — `SourceFilter` 派生"非实验 agent"名单. 复合来源:
  1. `experimental.HiddenResourceIDsByFlag()` 对每个 flag × HideAgent 查询 visibility 表
  2. 硬编兜底: mythos_*/claude_science*/pythia_oracle/agent-optimizer-expert 名字
- **`runner.go`** (330 LOC) — 单 run 流水线: `Collect → Learn(启发式) → CreateIssue(lab_source='agent_self_optimization') → KBWrite`. 启发式建议按 agent 聚类 issue 标题, top-3 关键词频次, 置信度按 issue 数 0.4/0.65/0.8 三档. **MVP 不调 LLM** — 0.3.46+ 接 `/api/runtime/llm-call` 换 rationale.
- **`service.go`** (250 LOC) — per-workspace Service. `Start(workspaceIDs)` 起 ticker, `Resume()` 扫 pending 行, `Stop()` cancel all. 复制 mythos Service 模式 (`Service.Set` map + cancel func).
- **`kb_stub.go`** (90 LOC) — mock `KBWriter` 写 `~/.multica/learning-vault/<wsId>/<timestamp>-<rand>.md`. 0 IPC, 0 新依赖. 0.3.46+ 换实现接 llm-wiki/write 或新 IPC `kb:append-learning`.
- **`flag.go`** (40 LOC) — `flagOnExperimental()` 单一 gate, 通过 `experimental.DefaultFor("agent_self_optimization")` 检查. Service 顶层 `if !flagOn() { return }` 完全 bypass.

### 2. 迁移 159 + sqlc queries

**`server/migrations/159_agent_self_opt_run.up.sql`** (40 LOC):
- `agent_self_opt_run` 表 (id / workspace_id / status / trigger_kind / started_at / finished_at / source_issue_count / prompt_suggestions JSONB / report_md / kb_appendix_path / error_message / created_issue_id)
- 2 索引 (workspace_id+started_at DESC, partial status IN ('pending','running'))
- forward-only: `CREATE TABLE IF NOT EXISTS` + `CREATE INDEX IF NOT EXISTS`. **不加 issue.archived_at 列** — `exclude_lab=true` 自动隐藏带 lab_source 的 issue.

**`server/pkg/db/queries/agent_self_optimization.sql`** (8 query): `CreateAgentSelfOptRun` / `GetAgentSelfOptRun` / `ListAgentSelfOptRunsByWorkspace` / `ListPendingAgentSelfOptRuns` / `UpdateAgentSelfOptRunStatus` / `UpdateAgentSelfOptRunResult` / `LastSuccessfulAgentSelfOptRun` / `ListDoneIssuesForSelfOpt` / `LockAgentSelfOptRun`. sqlc generate 产 9 个 method.

### 3. HTTP surface (`server/internal/handler/agent_self_optimization.go`)

4 endpoint, 全 flag-gated (返回 404 when flag off):

```
GET  /api/experimental/self-opt/runs?workspace_id=<uuid>&limit=20&offset=0
POST /api/experimental/self-opt/runs             body: {workspace_id}
GET  /api/experimental/self-opt/runs/{id}
POST /api/experimental/self-opt/runs/{id}/cancel
```

绑到 `router.go` 实验路由组, handler 全部用 `util.ParseUUID` 验证 + `writeError` 4xx 响应 + 写 JSON 响应.

### 4. Service 启动 + Resume 接线 (`router.go:597-635`)

镜像 mythos supervise 块的模式:

```go
{
    optSvc := selfoptsvc.NewService(h.Queries)
    h.SelfOptService = optSvc
    // Resume + Start, flag-off 时 no-op
    if pool != nil { ... }
}
```

`handler.go` 加 `SelfOptService *selfoptsvc.Service` 字段 + import.

### 5. 前端 self-opt-history view

- **`apps/desktop/src/renderer/src/pages/self-opt-history-view.tsx`** (340 LOC) — run list + 详情 (md 渲染 + prompt suggestions 列表) + 「立即运行」按钮. 用 `api.rawRequest` (CLAUDE.md "Experimental tab network calls" 0.3.30 约定) + 60s polling + 404 → 空态 fallback.
- 路由 `/experimental/self-opt-history` (routes.tsx). 原 `/experimental/agent-self-optimization` (127 行 placeholder) 保留作为 flag 状态说明页.
- 4 locales (`en/zh-Hans/ja/ko`) `experimental.self_opt_history_view.*` 21 keys.

### 6. Manifest 加 sidebar 入口 (`agent_self_optimization/manifest.json`)

加 `entry_points.sidebar[1]` 指向 `/experimental/self-opt-history`, 让用户从侧栏"试验性功能"直接跳转历史页.

## Constraints honored

- ✅ Forward-only migration: 加表/索引, 不动 issue 表
- ✅ Flag-off 完全 bypass: `Service.Start()` 顶部 `if !flagOn() { return nil }`
- ✅ 不创 reserved workspace: install 复用 caller 的 wsId
- ✅ i18n arrow-only: 所有 selector 用 `($) => $.experimental.self_opt_history_view.xxx`
- ✅ 不引入新依赖: 仅 stdlib + 现有 sqlc + 现有 zod
- ✅ `Source*` 常量已存在 (`SourceAgentSelfOptimization`), 不动 lock.go
- ✅ 实验性 agent 排除: SourceFilter 双层 (visibility 表 + hardcoded names)
- ✅ 自优化 issue 自动归档: `lab_source='agent_self_optimization'` + `exclude_lab=true` 自动从主面板隐藏
- ✅ LabBadge 自动可见: 现有 `LabBadge` + `IssueLabsSection` 已识别 `agent_self_optimization` flag key, 实验室图标自动出现

## Verification

| Check | Result |
|---|---|
| `pnpm typecheck` | 6/6 PASS |
| `pnpm turbo test --filter=@multica/views --filter=@multica/core --filter=@multica/desktop` | views 1278 PASS (其余 cache hit) |
| `cd server && go build ./...` | PASS |
| `cd server && go vet ./...` | clean |
| `cd server && go test -count=1 -timeout 120s ./internal/handler/` | PASS (历史 flake TestRollupTaskUsageHourlyCapsWindowAtOneDay 单独跑 OK, 与本改动无关) |
| `make sqlc` | 9 method generated, no drift |

## NOT packaged in this commit

Per 0.3.45 same convention — release notes ship ahead of packaging. The `bundle-cli` + `electron-vite build` + `electron-builder --mac --dir` + cold-start 3-check pass run as a separate step after this commit lands.

## 0.3.46+ deferred

- LLM-driven rationale via `/api/runtime/llm-call` (replaces the heuristic token-clustering)
- Apply prompt_suggestion 写回 `agent.prompt` + `agent_prompt_history` 表 + audit log + 回滚
- 增量扫描 (本次只扫上次 run 之后的 issue)
- 真 KB 集成 (接 `llm_wiki_bridge` 或新 IPC `kb:append-learning`)
- 失败重试 + 退避 + sidebar badge 报警
- E2E (Playwright: 开 flag → 触发 → 看 issue → 看实验室页)