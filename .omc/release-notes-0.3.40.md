---
name: 0.3.40 release notes
created: 2026-07-17T12:39:21Z
updated: 2026-07-17T12:39:21Z
---

# 0.3.40 — Claude Lab 完整工作台 v1 (Plan timeline + LabChatPanel)

## 用户视角修复

1. **Claude Lab 工作台** — 当用户在 lab 里选中一个 lab issue 后,主面板下方出现新的 **workbench strip**:
   - **左侧 timeline**:该 issue 的所有 agent_task_queue 历史(最多 20 条),按时间倒序;完成 / 失败 / 取消的 task 显示状态徽章 + 耗时 + 第一句报告摘要;in-flight task(running/queued/preparing)显示在底部独立分组。
   - **右侧 chat panel** (`LabChatPanel`):绑定 issue + agent 的常驻对话面板;首次发送消息时自动创建 chat session(走 chat_input_task_id 链路),后续消息实时拉取(3s 轮询)。可 ⌘↩ 发送。
   - **顶部 issue context bar**:显示 issue 标题(点击跳主 issue 详情)+ 状态徽章 + lab_seq 计数器(`# runs so far`)。
   - 不破坏 0.3.38 Plan tab(行点击跳主详情 + OpenChatButton 还在);新增 workbench strip 是 Plan tab 之外的独立 strip。

2. **完整的 agent run 历史可见** — 之前 lab agent 任务失败只能看主 issue 评论流;现在 workbench timeline 一目了然(失败的 task 显示 `error` 字段,完成的显示 `result_summary`)。你刚才那条 `随机测试一个实验科研项目` issue 一共 16 个 task,lab_seq=15 次完成/失败,完整可见。

3. **agent 输出摘要可见** — `result_summary` 是 agent `result.output` jsonb 字段的前 200 字符(truncated 沿 UTF-8 边界)。失败 task 显示 `error.slice(0, 200)`。无摘要显示 "(no summary)"。

4. **实时刷新** — workbench strip 每 5s 自动重新拉 `/lab-context`,chat panel 每 3s 拉消息。无需手动 F5。

## API 新增(1 个 endpoint)

- `GET /api/experimental/claude-science-lab/issues/{id}/context?workspace_id=<uuid>` — 单次拉齐 issue + 关联 agent + 该 issue 的最近 20 个 agent task + 该 issue 的 agent 评论(最多 50 条) + chat_session_id + lab_seq + server_time。
- 挂 `experimental.DefaultFor("claude_science_lab")` flag gate,flag off 时路由不存在。
- 复用现有 sqlc queries(`GetIssueInWorkspace` / `ListAgentTasks` / `ListCommentsForIssue` / `GetChatSession` / `GetAgentInWorkspace`),**不引入新 sqlc 生成**。
- 端到端验证:HTTP 200,真实 issue 返回 16 tasks + 14 comments + lab_seq=15,字段全部对齐。

## 文件改动

### 新增
- `server/internal/handler/lab.go`(355 行:handler + helpers + extractResultSummary + truncateUTF8 + trimLeadingWhitespace)
- `server/internal/handler/lab_test.go`(230 行:6 helper tests + 3 endpoint tests)
- `packages/views/experimental/components/lab-chat-panel.tsx`(220 行:LabChatPanel + labChatPanelPropsFromContext + pickLatestChatSessionId helpers)
- `.omc/plans/0.3.40-claude-lab-v1-api.md`(API wire shape 设计文档)

### 修改
- `server/cmd/server/router.go`(注册 1 个新 endpoint,挂在现有 `claude_science_lab` flag gate 下)
- `packages/core/types/api.ts`(新增 `LabContext` / `LabIssueBrief` / `LabAgentBrief` / `LabTaskBrief` / `LabCommentBrief` interfaces)
- `packages/core/api/client.ts`(新增 `getLabContext()` method)
- `packages/views/experimental/components/index.ts`(导出 `LabChatPanel` + helpers)
- `packages/views/experimental/index.ts`(re-export)
- `packages/views/locales/{en,zh-Hans,ja,ko}/claude-lab.json`(每个 locale +20 个 i18n key:lab chat panel 标题/loading/empty/input/send + plan timeline header/empty/status × 5/run label/trigger/duration/no-summary/lab_seq label/issue context aria/status)
- `apps/desktop/src/renderer/src/pages/claude-lab-view.tsx`(新增 `LabWorkbenchSection` + `LabChatPanelContainer` + `useLabWorkbenchContext` + `IssueContextBar` + `PlanTimeline` + `statusBadgeClass` + `formatMs`,不动现有 5 tab 结构)

### 修改(bump 版本)
- `apps/desktop/package.json` → `0.3.40`

## 验证

- `go test -race -count=1 ./internal/handler/` — 9 lab tests PASS(6 helper + 3 endpoint)
- `pnpm typecheck`(清 turbo 缓存后真实跑) — 6 packages, 0 error, 0 cached
- `pnpm test` — 8 packages, 141 test files, 1275 tests PASS
- `go build ./...` — 0 error
- pre-update snapshot — `0.3.39 .app 767M + PG 13 tables + 37 config + 810 KB docs` → 备份至 `~/.multica/backups/pre-update-20260717-203641`
- Cold start — `/Applications/Multica.app` 0.3.40,5432+8090 LISTEN,`/health` 返回 `{"status":"ok"}`
- 端到端 — e2e 调 `/lab-context?issue=8c068c3d...&workspace_id=283d3de3...` 返回 200 + 16 tasks + 14 comments + lab_seq=15,字段全部对齐 wire shape

## 兼容性

- **0.3.38 mainboard-integration 保持不变**:Plan tab 行点击仍跳主 issue 详情,OpenChatButton 仍工作,issue 删除仍双向同步。
- **flag-off 完全 bypass**:`{flagEnabled ? <ClaudeLabView /> : null}` 包整个 view,workbench strip 在 flag-on 时才挂载。
- **chat_input_task_id 链路 (MUL-4351) 复用**:chat panel 直接用现有 `createChatSession` + `sendChatMessage` + `listChatMessages` API,无新 IPC / 新 store。
- **forward-only migration**:无新表 / 新列;handler 实时从 `agent_task_queue.result` jsonb 提取 `result_summary`。
- **i18n arrow-only selector** (2026-07-14 incident):所有新代码用 `($) => $.key` 形式,无 block body。

## 已知遗留(后续 PR)

- **Artifact / Forecast / Code / Knowledge 4 tab 仍接 sandbox session,未接 agent task 输出** — 留 v2 (0.3.41) 处理。0.3.40 v1 范围只把 Plan tab 升级成 agent task timeline,完整 lab 工作台体验闭环。
- **chat panel 暂不支持附件上传** — 现有 `sendChatMessage` API 支持 `attachment_ids`,但 LabChatPanel 没暴露附件按钮。后续 PR 加。
- **workbench polling 间隔硬编码 (5s context / 3s messages)** — 后续可抽 hook 配置化。
- **CLAUDE.md 没更新** — 描述 lab 现状的章节需要补 v1 新组件,留 release notes 文档。

## 后续 v2 计划(0.3.41)

- Artifact / Forecast / Code / Knowledge 4 tab 全部接 agent output(`result.attachments[]` / `comment WHERE type='forecast'` / `result.code_blocks[]` / task citations)
- 引导 agent 在 prompt 中 emit `result.predictions[]` / `result.artifacts[]` 结构化字段
- 4 个 tab 接 agent output 之后,lab 工作台真正"科研实验台"语义闭环