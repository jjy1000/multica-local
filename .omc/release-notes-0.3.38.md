---
name: 0.3.38 release notes
created: 2026-07-17T17:57:00Z
updated: 2026-07-17T17:57:00Z
---

# 0.3.38 — 主面板实验室任务集成 + 实验室 chat 删除

## 用户视角修复(核心需求)

1. **实验室任务在主面板可见**
   - 创建 `lab_source=claude_science_lab`(或任意 lab flag)的 issue 后,issue 现在出现在主面板的 issue 列表 / 看板 / swimlane。
   - 主 issue 列表 / 看板 / swimlane 默认包含 lab-tagged issue(`exclude_lab=false` 默认值)。
   - 主 issue 详情已渲染 `LabBadge` / `LabPicker` / `IssueLabsSection`,可在那里改 lab / 改 mode / 看 plan+forecast+artifacts 入口。

2. **删除双向同步**(同一行,一次删除)
   - 主面板删除 lab issue 与"实验室计划任务"是同一行,删除即级联。
   - API 路径 `DELETE /api/issues/:id` 走 workspace 守卫,无特殊处理 — lab issue 与普通 issue 同路径。

3. **从实验室 view 跳到主 issue 详情**
   - Claude Lab `Plan` tab 行标题点击 → `router.push(paths.workspace(slug).issueDetail(id))` 跳主问题详情,而不是 `/experimental/claude-lab?issue=<id>`。
   - 主 issue 详情右上保留"打开实验面板"链接(要看 plan/forecast/artifacts 时用)。

4. **创建即 dispatch**
   - 创建 `claude_science_lab` issue → 自动绑定 `research` agent → 写入 `agent_task_queue`(0.3.34 已就绪的 `assignDefaultLabAgent` 链路)。
   - `constitution_agent` → `constitution_leader` 同上。

5. **实验室 chat 集成到主任务对话流**
   - 删除 Claude Lab 内的 `Chat` tab(原 `ExperimentalChatPane`)。
   - 在 `Plan` tab 每个 issue 行右侧加 "💬 打开对话" 按钮 → 跳主 issue 详情(主 issue 详情的 chat 入口就是该 issue 的对话流,`chat_input_task_id` 关联)。
   - 删除 `packages/views/experimental/components/experimental-chat-pane.tsx`(整个文件)。
   - 删除 4 locales 的 `tab_chat` / `title_chat` 键;新增 `open_chat_button`(4 locales 翻译:Open chat / 打开对话 / チャットを開く / 대화 열기)。

## P0 bug 修复(隐藏的正确性问题)

6. **mythos-view setter 缺失**
   - `apps/desktop/src/renderer/src/pages/mythos-view.tsx` line ~193:`useState` 只解构 value 不取 setter → 切 issue 时 `rootIssueId` 不更新,mutation body 送 stale 值。
   - 修复:加 setter + `useEffect` 同步 `initialIssueId`/`urlIssueId`。

7. **MythosEnhancerSupervisePanel 父级无条件 render**
   - `packages/views/issues/components/issue-labs-section.tsx`:父组件无条件渲染 supervise panel → sole-mode issue 仍请求 `/api/issues/:id/mythos-runs`。
   - 修复:加 `&& issueLabMode === 'enhancer'` 守卫;`IssueDetail` 父级传 `issueLabMode={issue.lab_mode ?? null}`。

8. **pythia-manager proxyRateLimit 写死 identity**
   - `apps/desktop/src/main/pythia-manager.ts:407-410`:`proxyRateLimit(identity)` 写死 `"renderer"` → `proxyBuckets` Map 永远只有一个 key。
   - **CLAUDE.md 写的 "30/min/renderer" 是假的,实际是 30/min 全局单桶**。
   - 修复:`PythiaProxyRequest` 加可选 `identity?: string`;IPC handler 用 `req.identity ?? String(_event.sender.id)`。webContents-id 区分桶,30/min 真正按 caller 计。

9. **preload claudeScience IPC 死代码**
   - `apps/desktop/src/preload/index.ts:372-381`:`experimentalAPI.claudeScience.{ensureUp,stop,getStatus,getURL}` 4 IPC 暴露但 main 端 0 handler(0.3.22 lab 合并后已迁移到 `experimental:<flagKey>:<verb>` dispatcher)。
   - 修复:删除整个 `claudeScience` 对象 + `preload/index.d.ts:242-247` 类型声明。

10. **pythia-view PythiaStatusPanel 死代码**
    - `apps/desktop/src/renderer/src/pages/pythia-view.tsx:165-199`:flag-off 时 url 永远 null,flag-on 时早返回 → 组件 100% 不可达。
    - 修复:删除整个 `PythiaStatusPanel` 函数 + 渲染分支;flag-off placeholder 保留。

11. **lab-workspace-panel 全删**
    - `packages/views/issues/components/lab-workspace-panel.tsx` + `lab-workspace-panel.test.tsx`:0 caller(grep 仅命中自身 + issue-detail.tsx 注释)。
    - 修复:删 2 文件 + `issue-detail.tsx` 的 import/JSX + `issue-detail-page.tsx` 的 `labRouteSuffix` 调用 + `IssueDetailProps.labRouteSuffix` 死 prop + `pickLabRouteSuffix` 死 helper。

## 验证

- `pnpm typecheck`(清 turbo 缓存后真实跑):6 packages, 0 error
- `pnpm test`:141 文件, 1275 tests,全过
- `cd server && go build ./...`:0 error
- pre-update snapshot:0.3.37 .app 767M + PG 13 tables + 37 config + 810 KB docs → 备份至 `~/.multica/backups/pre-update-20260717-175711`

## 文件改动清单

**修改**:
- `apps/desktop/package.json`(version bump)
- `apps/desktop/src/main/pythia-manager.ts`(PythiaProxyRequest + identity)
- `apps/desktop/src/preload/index.ts`(删 claudeScience)
- `apps/desktop/src/preload/index.d.ts`(删类型)
- `apps/desktop/src/renderer/src/pages/claude-lab-view.tsx`(删 ChatTab / 加 OpenChatButton / 改 PlanTab 行跳转)
- `apps/desktop/src/renderer/src/pages/issue-detail-page.tsx`(删 pickLabRouteSuffix 死 helper)
- `apps/desktop/src/renderer/src/pages/mythos-view.tsx`(setter + useEffect)
- `apps/desktop/src/renderer/src/pages/pythia-view.tsx`(删 PythiaStatusPanel)
- `packages/core/api/client.ts`(`exclude_lab` 默认值翻 false)
- `packages/core/types/api.ts`(JSDoc)
- `packages/views/issues/components/issue-detail.tsx`(删 LabWorkspacePanel import/JSX + issueLabMode prop)
- `packages/views/issues/components/issue-labs-section.tsx`(supervise panel 守卫 + issueLabMode prop)
- `packages/views/experimental/components/index.ts`(删 ExperimentalChatPane export)
- `packages/views/experimental/index.ts`(删 ExperimentalChatPane export)
- `packages/views/locales/{en,zh-Hans,ja,ko}/claude-lab.json`(删 tab_chat/title_chat,加 open_chat_button)

**删除**:
- `packages/views/experimental/components/experimental-chat-pane.tsx`
- `packages/views/issues/components/lab-workspace-panel.tsx`
- `packages/views/issues/components/lab-workspace-panel.test.tsx`

## 已知遗留(后续 PR)

- **Forecast/Artifact 改 comment 流**:Claude Lab Forecast tab 仍走 SSE `/api/experimental/claude-science-lab/forecast/stream`,未迁到读 issue `comment WHERE type='forecast'`。需要后端 SSE handler 改为 `INSERT comment`,renderer 改读 comment 流。本 PR 范围外。
- **CLAUDE.md "30/min/renderer" 描述**:已与代码同步(改 `req.identity ?? webContents.id`),但 CLAUDE.md 仍是旧描述,后续 PR 更新。
- **i18n 残留 key**:`loopback_url`/`health_probe`/`try_from_agent` 在 4 locales 仍存在(仅 PythiaStatusPanel 用),本 PR 删除组件后 key 变孤儿,后续清理。