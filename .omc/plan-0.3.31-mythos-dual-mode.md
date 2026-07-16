---
name: plan-0.3.31-mythos-dual-mode
created: 2026-07-16T13:45:00Z
updated: 2026-07-16T13:45:00Z
---

# 0.3.31 Mythos 双模式改造 (mythos_swarm sole + enhancer)

## 一句话总结

把现成的 `mythos_swarm` flag **升级为双模式**——保留唯一 flag key，加 `lab_mode` 列做二态分流。`sole`(独立模式,5-agent 自闭合)走老路径不变;`enhancer`(增强模式,补完 mig 156 半成品 + 新增 supervise 后台轮询)让 mythos 先开会规划、再把子任务派给原 assignee,盯着它干完。

## 关键约束(吸收自现状调研 + CLAUDE.md 0.3.30.3+)

| 约束 | 来源 | 实施约束 |
|---|---|---|
| `mythos_swarm` 已是 production flag(0.3.29 ship) | memory `0.3.29-labs-closure-goal-2026-07-15.md` | 改 flag key 违反"flag-off 完全 bypass",**保留 key** |
| `runtime.go` 949 行 0 hits for `experimental` | 实测 | 监督走 issue 事件流,**不碰 LLM 调用路径** |
| `issue.go:2153` 互斥校验 | 实测 | 仅放宽例外到 `lab_mode='enhancer'` |
| 8 labs flags 已在 catalog | 实测 | 不动 catalog.go |
| mig 156 已加 `extension_agent_ids` / `self_optimization_enabled` / `coda_conclusions` / `reflection`,但 runner.go **不读** | 实测 grep 0 hits | 一次性补完 |
| Pythia 0.3.30.3+ 已有 oracle + per-issue forecast,占用 `pythia_oracle` 名字 | CLAUDE.md §Pythia engine | 不冲突,mythos 仍独占 `mythos_swarm` |
| `runner.Run()` 同步阻塞,waitFn 注入 | runner.go:120 | supervise 必须是**独立 goroutine**,不塞进 Run |
| 单 PR 全做 | 用户拍板 | 0.3.31 一个 PR ship |
| LabPicker 已经能展示 flag 列表 | lab-picker.tsx:89-100 | 加 `labMode` prop + 4 tab UI |

## 实施步骤(按依赖顺序)

### Step 1 — 数据库迁移(migration 157)

**文件**:`server/migrations/157_mythos_dual_mode.up.sql`(新)+ `.down.sql`

```sql
-- (1) issue.lab_mode: 二态分流,与 lab_source 配对存在
--     NULL = 没走 mythos; 'sole' = 独立; 'enhancer' = 增强
--     与 issue.lab_source (mig 155) 1:1,允许独立查询
ALTER TABLE issue
    ADD COLUMN lab_mode TEXT
        CHECK (lab_mode IS NULL OR lab_mode IN ('sole', 'enhancer'));

-- 索引: 0.3.31 全部 enhancer 模式 issue 数量极少,只为未来
-- "列出本工作区所有 enhancer 增强任务"查询加速
CREATE INDEX idx_issue_lab_mode_enhancer
    ON issue(workspace_id, updated_at DESC)
    WHERE lab_mode = 'enhancer';

-- (2) mythos_run: 监督状态 + target 字段 + 收敛模式扩展
--     target_assignee: enhancer 模式下由用户选定的 assignee
--     supervision_state: 后台 supervise goroutine 写入的进度快照
--     mode: 'sole' | 'enhancer' 同步,落地进 run 行方便历史查询
ALTER TABLE mythos_run
    ADD COLUMN mode TEXT NOT NULL DEFAULT 'sole'
        CHECK (mode IN ('sole', 'enhancer')),
    ADD COLUMN target_assignee JSONB,
    ADD COLUMN supervision_state JSONB NOT NULL DEFAULT '{}'::jsonb;

-- target_assignee 结构: {type: 'agent'|'squad', id: uuid}
-- supervision_state 结构: {phase, last_check_at, sub_tasks_total, sub_tasks_done,
--                          assigned_issue_id, reflection_iter}
COMMENT ON COLUMN mythos_run.target_assignee IS
    'Enhancer-mode target. JSONB {type: agent|squad, id: uuid}. NULL for sole mode.';
COMMENT ON COLUMN mythos_run.supervision_state IS
    'Supervise goroutine state snapshot. JSONB {phase, last_check_at, sub_tasks_*, assigned_issue_id, reflection_iter}. Empty {} when not supervising.';

-- (3) mythos_members.reflection 已有(mig 156),但类型只是 TEXT,
--     0.3.31 显式加 coda 反思迭代号,方便 supervise 跟踪
--     (这一列只是文档/查询优化,nullable)
ALTER TABLE mythos_members
    ADD COLUMN reflection_iter INT;
```

**反向迁移**:列全部 `DROP COLUMN` + `DROP INDEX`,无数据丢失风险(列都是 nullable / 默认值)。

### Step 2 — schema 重生成

```bash
cd server && go run github.com/sqlc-dev/sqlc/cmd/sqlc generate
```

新增 sqlc 方法(根据新列生成):

- `UpdateIssueLabMode(ctx, UpdateIssueLabModeParams{ID, LabMode})`
- `CreateMythosRunEnhancer(ctx, ...)` — 走 `CreateMythosRun`,sqlc 会自动用 `mode` 列填充
- `SetMythosRunSupervisionState(ctx, ...)`
- `SetMythosRunTargetAssignee(ctx, ...)`
- `SetMythosMemberReflectionIter(ctx, ...)`

### Step 3 — runner.go 改造(`server/internal/service/mythos/runner.go`)

**变更**:
1. `Config` 加 `Mode string`、`TargetAssignee *TargetAssignee`、`ExtensionAgentIDs []pgtype.UUID`
2. `Run` 入口:
   - 计算 effective loop agent pool = `cfg.LoopAgentIDs + cfg.ExtensionAgentIDs`
   - 同步执行 prelude + loop + coda,行为不变(对 sole 路径完全向后兼容)
   - **enhancer 模式**:coda 完成后**不**返回,启动 supervise goroutine(在 Service 上存 map)
3. 加 `reflection` 写入:loop iteration 完成后,coda 写一个 reflection 到 `mythos_members.reflection` + `reflection_iter`(用 `cfg.SelfOptimizationEnabled` 控制,mig 156 字段)
4. 加 `extension_agent_ids` 读取:loop agent pool = 5 个固定 + 用户勾选的额外成员(轮转选)

**新增文件**:`server/internal/service/mythos/supervise.go`

```go
// superviseLoop 是 enhancer 模式专属,Run 完成后启动。
// 用 30s ticker + issue 事件钩子(评论 + 状态变更)双驱动。
// 通过 Service.superviseSet[runID] 存活的 supervise 句柄,
// Service.Stop() 时遍历 cancel。daemon 重启后从 mythos_run.mode='enhancer'
// AND status='supervising' 恢复。
func (s *Service) superviseLoop(ctx context.Context, cfg Config, runID pgtype.UUID, assignedIssueID pgtype.UUID) {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            if err := s.tickSupervision(ctx, runID, assignedIssueID); err != nil {
                slog.Warn("mythos supervise tick failed", "run", runID, "err", err)
                // 失败不退出,继续 tick;运行期错误写 supervision_state.phase='degraded'
            }
            // 终止条件: supervision_state.phase='done' 或 'aborted'
            state, _ := s.queries.GetMythosRunSupervisionState(ctx, runID)
            if isTerminalSupervision(state) {
                return
            }
        }
    }
}
```

**关键设计**:`tickSupervision` 通过 `GetIssueComments(issueID)` 取最新评论 + `GetIssueByID(assignedIssueID)` 取状态/进度,任何 sub-task 进展都触发一次 mini-loop(只调 `mythos_loop_analyst`,不开新 sub-issue,直接把 reflection 写 `mythos_members.reflection`)。

### Step 4 — issue.go 放宽互斥

**文件**:`server/internal/handler/issue.go:2153` 附近

原校验(CLAUDE.md 460 行引用):
```go
// 拒 lab_source + manual assignee
if req.LabSource != "" && req.AssigneeID != nil {
    return httpError(400, "lab_source and assignee are mutually exclusive")
}
```

**改为**:
```go
if req.LabSource != "" && req.AssigneeID != nil {
    // Enhancer-mode 是唯一例外:必须同时有 assignee
    if req.LabMode == "enhancer" {
        // enhancer 模式额外校验:assignee 必须是该工作区可见的 agent 或 squad
        if !isValidAssigneeForWorkspace(ctx, h, req.AssigneeType, req.AssigneeID, workspaceID) {
            return httpError(400, "enhancer mode requires a valid agent or squad in this workspace")
        }
    } else {
        return httpError(400, "lab_source and assignee are mutually exclusive")
    }
}
```

**反向校验**:如果 `lab_mode='enhancer'` 但 `lab_source != 'mythos_swarm'` → 422。
如果 `lab_mode='sole'` 且有 assignee → 422(原行为保留)。

### Step 5 — 新增 API endpoint

**文件**:`server/cmd/server/router.go`

```go
// 0.3.31: POST /api/experimental/mythos-swarm/supervise/{runID}/tick
// 手动触发一次 supervise tick (UI "立即检查" 按钮用)
experimental.Route(r, "/mythos-swarm", func(r chi.Router) {
    r.Post("/supervise/{runID}/tick", h.PostMythosSuperviseTick)
})
```

`PostMythosSuperviseTick` handler:
- 校验 `experimental.DefaultFor("mythos_swarm")`
- 查 `mythos_run.mode = 'enhancer'`
- 同步调 `Service.tickSupervision`
- 返回 `{ok: true, supervision_state: {...}}`

### Step 6 — daemon 启动 supervise 恢复

**文件**:`server/cmd/server/router.go` daemon bootstrap

启动时扫一次:
```sql
SELECT id, workspace_id FROM mythos_run
WHERE mode = 'enhancer' AND status = 'supervising'
  AND completed_at IS NULL
```

逐条重启 supervise goroutine,断电恢复。

### Step 7 — 状态机:`status` 加 `'supervising'`

**文件**:mig 157 内 ALTER:
```sql
ALTER TABLE mythos_run
    DROP CONSTRAINT mythos_run_status_check,
    ADD CONSTRAINT mythos_run_status_check
        CHECK (status IN ('running','completed','aborted','failed','supervising'));
```

sole 模式: `'running' → 'completed'`(原行为)
enhancer 模式: `'running' → 'supervising' → 'completed'`

runner.go `markFailed` 不动,新增 `markSupervising`。

### Step 8 — 前端 LabPicker 4 tab UI

**文件**:`packages/views/issues/components/pickers/lab-picker.tsx`

新增 props:
```ts
interface LabPickerProps {
  // ...existing
  labMode?: 'sole' | 'enhancer' | null;
  onUpdateMode?: (next: { lab_mode: 'sole' | 'enhancer' | null }) => void;
}
```

UI:lab 选中后展开第二个 tabs:
```
[不使用] [蜂群独立(sole)] [蜂群增强(enhancer)]
```

- 选 sole → 清空 assignee(disabled assign picker)
- 选 enhancer → 必填 assignee(显示 `<AssigneePicker>`),可选勾 extension agents
- 选不使用 → 清 lab_source + lab_mode + assignee(disabled assign picker)

**chrome**:沿用 `PropertyPicker`,2 个 tabs(`tabs-mode` + `tabs-extension`)。

### Step 9 — IssueLabsSection 渲染 supervise 状态

**文件**:`packages/views/issues/components/issue-labs-section.tsx`(已有 0.3.29)

新增渲染分支:
- `labSource='mythos_swarm' && labMode='enhancer'` → 渲染 supervise 面板:
  - phase: preparing / planning / supervising / done / degraded
  - sub_tasks_total / sub_tasks_done 进度条
  - 最近 reflection(取 `mythos_members.reflection` 最近一条)
  - "立即检查"按钮 → POST supervise/tick

### Step 10 — i18n 4 语言

**文件**:
- `packages/views/locales/en/issues.json`
- `packages/views/locales/zh-Hans/issues.json`
- `packages/views/locales/ja/issues.json`
- `packages/views/locales/ko/issues.json`

新键(`pickers.lab.*` namespace):
```json
{
  "pickers": {
    "lab": {
      "mode_sole": "蜂群独立",
      "mode_enhancer": "蜂群增强",
      "mode_sole_hint": "Mythos 自己完成,清空 assignee",
      "mode_enhancer_hint": "Mythos 先开会规划,再交给选中的 assignee 执行并监督",
      "enhancer_requires_assignee": "增强模式必须选择 agent 或 squad",
      "sole_clears_assignee": "独立模式将清空现有 assignee",
      "extension_loop_hint": "可选勾 0~3 个额外 agent 加入 loop"
    }
  },
  "labs": {
    "mythos_enhancer_phase_preparing": "准备中",
    "mythos_enhancer_phase_planning": "蜂群规划中",
    "mythos_enhancer_phase_supervising": "监督执行中",
    "mythos_enhancer_phase_done": "已完成",
    "mythos_enhancer_phase_degraded": "监督降级",
    "mythos_enhancer_check_now": "立即检查",
    "mythos_enhancer_subtask_progress": "子任务 {{done}}/{{total}}"
  }
}
```

4 语言全量补;zh-Hans 为权威,en/ja/ko 暂以 zh-Hans fallback。

## 改动清单(最终)

| # | 文件 | 类型 | LOC(估) |
|---|---|---|---|
| 1 | `server/migrations/157_mythos_dual_mode.up.sql` | 新 | 50 |
| 2 | `server/migrations/157_mythos_dual_mode.down.sql` | 新 | 20 |
| 3 | `server/internal/service/mythos/runner.go` | 改 | +120 |
| 4 | `server/internal/service/mythos/supervise.go` | 新 | 280 |
| 5 | `server/internal/service/mythos/supervise_test.go` | 新 | 200 |
| 6 | `server/internal/handler/issue.go` | 改 | +30 |
| 7 | `server/internal/handler/mythos_supervise.go` | 新 | 80 |
| 8 | `server/cmd/server/router.go` | 改 | +25 |
| 9 | `apps/desktop/package.json` | 改 | bump to 0.3.31 |
| 10 | `packages/views/issues/components/pickers/lab-picker.tsx` | 改 | +120 |
| 11 | `packages/views/issues/components/issue-labs-section.tsx` | 改 | +90 |
| 12 | `packages/views/issues/components/issue-detail.tsx` | 改 | +15 (传 prop) |
| 13 | `packages/core/types/issue.ts` | 改 | +5 |
| 14 | `packages/views/locales/{en,zh-Hans,ja,ko}/issues.json` | 改 | 4 × 30 |
| 15 | `.omc/release-notes-0.3.31.md` | 新 | 100 |

**总计**:约 1450 LOC 增量,其中 6 个新文件、9 个修改文件。

## 验证清单

按 CLAUDE.md "Verification" 段 + Go 测试规则(golang/testing.md,table-driven,`-race`):

- [ ] `cd server && go test -race ./internal/service/mythos/...` 全过
  - 新增 supervise_test 至少 3 个 case:enhancer 模式启动 / 终止条件触发 / daemon 重启恢复
  - 既有 sole 模式测试**全部不破**
- [ ] `cd server && go vet ./...` 0 警告
- [ ] `cd server && make sqlc` 无 diff(新列已生成)
- [ ] `cd server && go run ./cmd/migrate up` 本地 PG 跑通
- [ ] `pnpm typecheck` 0 错
- [ ] `pnpm lint` 0 错(`lab-picker.tsx` 新增 4 tab 用 arrow selector,符合 2026-07-14 incident 规则)
- [ ] `pnpm test` 全过
- [ ] **手动集成验证**(必做,CLAUDE.md "Desktop self-contained backend smoke"):
  1. `pnpm --filter @multica/desktop bundle-cli`(新迁移已 baked-in)
  2. `pnpm --filter @multica/desktop build`
  3. cp .app 到 /Applications/
  4. cold start 5432+8090 OK
  5. row parity baseline 不漂移
  6. **GUI 路径**(必截图):
     - issue detail 选 `mythos_swarm` → labMode=sole → assignee 清空 → 任务跑完无 supervise 面板
     - issue detail 选 `mythos_swarm` → labMode=enhancer → 必选 assignee → 提交 → 30s 内 supervise 面板出现 → phase 从 preparing→planning→supervising→done
     - daemon 重启 → supervise 自动恢复(supervision_state.phase 持续推进)

## 风险与回滚

| 风险 | 缓解 |
|---|---|
| supervise goroutine 泄漏 | `Service.Stop()` 遍历 cancel;daemon 重启时扫 `mode='enhancer' AND status='supervising'` 恢复;最长存活 1 个 issue 的生命周期 |
| enhancer 模式 assignee 选错(其他工作区 agent) | `isValidAssigneeForWorkspace` 校验 + 前端 picker 只列当前工作区 agents |
| supervisor 轮询 30s 太频繁 | 30s 是保守值;小工作区 30s ≈ 1728 次/天,DB 都是主键查询,QPS 远低于 daemon 其他服务 |
| `mythos_swarm` flag 开关切换时旧 run 残留 | supervise goroutine 检测 `DefaultFor` 翻 false → 优雅退出,写 `supervision_state.phase='aborted'` |
| 用户在 enhancer 模式中途改 assignee | supervise 锁定目标(第一次启动时快照);UI 隐藏 assignee picker,直到 issue 关闭 |

**回滚**:migration 157 down 文件 + reverting commit。Supervise 不影响 LLM 调用路径,数据可安全丢弃。

## 不做的事(明确边界)

- ❌ 不重命名 `mythos_swarm` flag key
- ❌ 不加 per-agent enhancer 字段(那是 v0.4 范畴)
- ❌ 不动 `runtime.go` LLM 调用路径
- ❌ 不动 catalog.go(`mythos_swarm` 描述小幅更新可以,放到 Step 0.5 顺手)
- ❌ 不引入 OpenMythos PyPI 包(用户认知偏差,本次只做 Multica-side 双模式)
- ❌ 不做 codepath 互斥之外的额外权限扩展
- ❌ 不在 0.3.31 ship `chat_pin_ui` 等其他 lab 收尾(0.3.29 audit 已标记,留 0.3.32)

## 后续(0.3.32+ backlog)

- `extension_agent_ids` UI 渲染:在 issue detail 显示 "loop 成员 + 扩展成员" 头像组
- `coda_conclusions` JSONB 在 Mythos view 渲染结构化卡片
- `in_mythos_supervision` issue 状态锁(本 PR 不加,留作后续,理由:监督通过 status='supervising' run 行而非 issue 状态实现,issue 状态保持原样便于用户手动操作)
- per-agent enhancer 架构(用户原话"给某些智能体增加蜂群性能"):需要新 catalog 概念 + `agent.experimental_capabilities JSONB`,独立 PR
- CLI `multica mythos run --mode enhancer --assignee <id>` 入口(0.3.32+)