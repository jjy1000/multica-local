---
name: release-notes-0.3.55
created: 2026-07-20T05:49:40Z
updated: 2026-07-20T05:49:40Z
status: complete
version: 0.3.55
---

# 0.3.55 — 实验室可用性契约:自动派单 + 结果持久化 + 审计修复

上一次提交是 0.3.53。本次 ship 合并了两段未提交工作(0.3.54 自动派单 + 0.3.55 结果可见),
并落地一次全面的实验室审计与修复。对照用户契约四条:
**① 选 lab 自动开始 · ② 过程 issue 可见 · ③ 结果 lab view 可见 · ④ 自动化 lab 不可选。**

## 审计发现并修复的契约 bug(7 处)

| 位置 | 问题 → 修复 |
|---|---|
| `experimental/catalog.go` | `constitution_agent` 漏进 issue LabPicker → 加 `HideFromIssueLabPicker: true`(契约 ④) |
| `service/issue.go` + `handler/issue.go` | leader 名 `constitution_leader` ≠ 实装名 `宪法智能体`,自动派单永远 no-op → 两表对齐(契约 ①) |
| `service/issue.go` | 注释称 pythia/code_canvas「需人工选」,与 0.3.54 矛盾 → 更正 |
| `issue-detail.tsx` | enhancer 模式仍锁 assignee(与契约相反)→ 加 `lab_mode !== "enhancer"` 门 |
| `create-issue.tsx` + `core/api.ts` | 创建时丢 `lab_mode` → enhancer 静默变 sole,触发服务端 mutex 400 → 全链路补 `lab_mode` |
| `create-issue.tsx` | assignee 对所有 lab 上锁 → 收窄到 mythos-sole |
| `issue-detail.tsx` | 内联面板链接丢 `?issue=` 作用域 → 补上 |

## 0.3.54 段:A 类 lab 自动派单全覆盖 + issue 状态全生命周期

- `defaultLeaderAgentForLab` / `defaultLabLeaderForKey` 扩到 4 个 A 类 lab
  (claude_science_lab→research / pythia_oracle→pythia_runtime / code_canvas→code_canvas_worker / constitution_agent→宪法智能体);mythos_swarm 仍走自己的 5-agent squad(故意 no-op)。
- 新增 `install_pythia.go` / `install_code_canvas.go`:upsert leader agent + `experimental.Claim` + visibility 行 + rebind online runtime(防 AssigneePicker 泄漏,复用 0.3.53 模式)。
- `router.go` 注册两个新 install handler。
- Migration 163:`experimental_resource_lock` CHECK 加 `code_canvas` + `constitution_agent`。
- `IssueLabsSection` 状态指示从 running/queued 扩到 **running/queued/failed/cancelled** 四态。
- pythia/mythos/code-canvas 三个 view 加 onboarding hint。
- 3 个新 dispatch test(`TestUpdateIssueLabSourcePythiaOracle/CodeCanvas/MythosSoleModeNoAutoAssign`)。

## 0.3.55 段:结果在 lab view 可见(契约 ③)

### V1 — Pythia 持久化(最重缺口,全栈)
- Migration 164 `pythia_forecast_run` 表(id/workspace_id/issue_id/rounds/source/envelopes JSONB)+ sqlc 3 查询。
- `forecast_issue.go`:SSE 循环累积 envelope,`defer` 落库(完成/中断/掉线都写);detached context 保证写库不受流取消影响;source 折叠成 `oracle/synthetic/synthetic_oracle_failover/mixed`。
- 新 endpoint `GET /api/experimental/pythia-oracle/forecast/issue/runs?issue_id=`(literal 路径,避开 chi 参数路由顺序坑)。
- `pythia-report-surface.tsx` 加「历史推演」面板:按 issue 列过往 run,点击重现 10 轮结果。关掉页面不再丢。

### V2 — Mythos 完成结果
- `MythosRunSummary` 扩 problem/iterations/coda_conclusions/final_issue_id/completed_at(复用已有 `GET /api/issues/{id}/mythos-runs`,无新查询、加性字段不破坏监督面板)。
- `mythos-view.tsx` 加「历史运行」面板:完成 run 的 coda 结论 + 落点 issue 可见。

### F2 — B 类后台提示(契约 ④)
- Labs 设置页给启用的 `hide_from_issue_lab_picker` flag(llm_wiki_bridge / agent_self_optimization / constitution_agent)加「自动后台运行」徽章。

### V3 — Claude Lab Summary(不做)
审计确认 Claude Lab 已达标(用户举的正面例子):Artifact tab 按 issue 出产物 + Plan 状态徽章 + timeline `result_summary`。Summary tab 是增强不是缺口,不改 2104 行文件。

## 验证(cold start 三项 + DB)
- pre-update snapshot ✅(`pre-update-20260720-134150`)。
- bundle-cli + electron-vite build + electron-builder --dir ✅;asar 含新 renderer 代码(`forecast/issue/runs`)+ migration 164 unpacked。
- cold start:5432 + 8090 监听 ✅,`/health` = `{"status":"ok"}` ✅。
- migration 164 自动应用 ✅,`pythia_forecast_run` 表存在 ✅。
- row parity `1/209/1174/91`(workspace/issue/agent 稳定,comment +6 为正常活动)。
- `go test -race ./internal/handler/` 全过(12.1s);views + desktop typecheck 全绿;gofmt/vet 干净。

## 遗留(0.3.56,下个 ship)
- `is_installed` marker 列 + Restore 契约收紧。
- lab flag on→off 自动 cancel running tasks。
- Playwright e2e 覆盖 5 个 lab onboarding。
- ref-only `scholar-evaluation` skill 集成(claude-science skills 目前 19 个,无此项)。
