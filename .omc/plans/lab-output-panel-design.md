---
name: lab-output-panel-design
created: 2026-08-13
type: design
status: implemented
supersedes: 0.5.17 out-of-scope note (.omc/0.5.17-ship-2026-08-12.md)
---

# 阶段 4 设计：LabOutputPanel + iframe auth proxy

> **实现状态（2026-08-14）：M1–M5 已落地，ship gate 全绿（`pnpm typecheck` 6/6 + `go test -count=1 ./internal/... ./pkg/agent/...` 全 ok）。**
> 实现中发现两处 spec 与实际不符（0.5.17「audit 验证不能跳过」教训的重演），已按实测调整：
> - **M3 实际是前端-only**：读端点 `GET /api/experimental/pythia-oracle/forecast/issue/runs?issue_id=` 早已存在（migration 164 `pythia_forecast_run` 表 + `forecast_issue.go::pythiaIssueForecastRuns`），spec 里「需新增 `GET .../forecast/issue/{issueId}`」是过时信息。
> - **M4 面板形态**（用户定夺）：不是 spec 字面的「只读历史」，而是「自带渲染输入 + 历史」——新增 migration 239 `code_canvas_artifact` + `POST/GET /api/experimental/code-canvas/issues/{issueId}/artifacts`（渲染+落库 / 读历史）。
> 工作未提交（M1–M5 同批 uncommitted），release 记录 / 0.5.19 打包另行处理。

> 0.5.17 ship log（`.omc/0.5.17-ship-2026-08-12.md` §Out-of-scope）明确两项「真缺但无 spec」：
> - **LabOutputPanel**（4 个 issue-bound lab 缺统一输出面板）
> - **iframe auth proxy**（user-plugin 的 iframe tab + 原生资源加载无法带 Bearer 头）
>
> 本文档是这两项的 **design spec（只写设计、不写实现代码）**，供后续会话接棒实现。每个断言带 file:line 证据。

---

## 1. 现状盘点（证据）

### 1.1 四个 issue-bound（A 类 workbench）lab 的产出面

| Lab | 产出物 | 数据源端点（现状） | 当前展示位置 |
|---|---|---|---|
| `claude_science_lab` | attachments / predictions / code_blocks 结构化信封 + 运行摘要 | `GET /api/experimental/claude-science-lab/issues/{id}/context`（`server/internal/handler/lab.go:107-115` 返回 `LabContextResponse{Issue, Agent, Tasks, Comments, ChatSessionID, LabSeq}`；`lab.go:154-169` `LabTaskBrief` 带 `result_attachments/result_predictions/result_code_blocks`） | `claude-lab-view.tsx:83-85` 5 tab（plan/artifact/forecast/code/knowledge）；Artifact tab 用 `GET /api/experimental/claude-science-runtime/sessions/by-issue?workspace_id&issue_id&limit=20`（`claude-lab-view.tsx:1351-1354`） |
| `pythia_oracle` | per-issue 10 轮预测流（flat envelope） | `POST /api/experimental/pythia-oracle/forecast/issue`（`packages/views/modals/create-issue.tsx:598-610` 建 issue 后自动 `{issue_id, rounds:10}` 启动）；envelope：`PythiaReportEnvelope{id, round, issue_id?, scenario, narrative, probability, confidence, horizon, persona, lab_source, scenario_context?, created_at?}`（`apps/desktop/src/renderer/src/components/pythia/pythia-report-surface.tsx:39-52`） | 视图里无 per-issue 预测面板；预测只进 issue 的 IssueLabsSection（CLAUDE.md §Forecast SSE endpoint）；全走 `window.experimentalAPI.pythia.proxy`（`pythia-report-surface.tsx:14-22`） |
| `mythos_swarm` | `problem`（输入）+ `coda_conclusions[]` JSONB（coda 输出）+ supervision 状态 | `POST /api/experimental/mythos-swarm/run`（`apps/desktop/src/renderer/src/pages/mythos-view.tsx:247` rawRequest）；持久 run 行 `GET /api/issues/{id}/mythos-runs?workspace_id`（`mythos-view.tsx:519-520` → `problem`/`coda_conclusions[]`/`final_issue_id`，`mythos-view.tsx:167-170`）；supervise `GET|POST /api/experimental/mythos-swarm/supervise/{runID}`（`server/cmd/server/router.go:900-901`） | mythos-view 的 RunForm + IssueLabsSection supervise 面板；run 历史只有 mythos-runs 一种 |
| `code_canvas` | 渲染后的自包含 HTML | `POST /experimental/code-canvas/render`（`apps/desktop/src/renderer/src/pages/code-canvas-view.tsx:54-63` rawRequest → iframe `srcDoc` + `sandbox=""`，`code-canvas-view.tsx:149-154`） | code-canvas-view 内联预览；无 run 历史、无 issue 关联视图 |

### 1.2 共享输出面组件（可复用）

- `packages/views/issues/components/issue-labs-section.tsx` — issue 详情右栏：lab 徽章 + running/queued/failed/cancelled 指示（`issue-labs-section.tsx:177-220`）+「打开面板」链接（`labSourceRouteSuffix` → `/experimental/<slug>`，`issue-labs-section.tsx:155`）。**这是 LabOutputPanel 最自然的挂载点（对 pythia/mythos/code_canvas 目前是空白或简略）。**
- `packages/views/experimental/components/artifact-gallery.tsx` — 用户插件产物画廊（props 只有 `pluginSlug`，`artifact-gallery.tsx:40-42`；rawRequest 契约）。
- `packages/views/experimental/components/artifact-renderer.tsx` — 单产物渲染（`Artifact{id, type: image|chart|table|html|code|file|text, title, mime_type?, size?, data?, url?, created_at}`，`artifact-renderer.tsx:16-27`；`resolveUrl` 用 `api.getBaseUrl()` 前缀，`artifact-renderer.tsx:35-38`）。
- `packages/views/experimental/components/forecast-stream-view.tsx` — SSE 预测 ticker（flat `ForecastEnvelope{id, scenario, narrative, probability, confidence, horizon, persona, createdAt}`，`forecast-stream-view.tsx:26-35`；fetch+ReadableStream 带 auth，`forecast-stream-view.tsx:43+`）。
- `packages/views/experimental/components/lab-chat-panel.tsx` — 单 issue 对话（`chat_session_id` + 3s 轮询，`lab-chat-panel.tsx:29-34`）。

### 1.3 iframe / 原生资源鉴权缺口（iframe auth proxy 的问题本质）

- user-plugin `iframe` tab：`tab.src` 相对 API host → `${api.getBaseUrl()}${tab.src}`（`packages/views/experimental/components/plugin-shell-view.tsx:320`），`<iframe src>` 是**原生资源加载，不带 Bearer/CSRF 头** → token-mode desktop（`<iframe src>` 无法注入 `Authorization`）下任何需要鉴权的后端内容 401/404。
- 同理：产物 `<img src>` / `<a href download>`（CLAUDE.md 0.3.30 已记录为 follow-up）；`ServePluginArtifactRaw`（`server/internal/handler/user_plugin_artifacts.go:341-403`）用 `requireUserID`（Bearer 头）鉴权，iframe/img 里无法通过。
- claude-science 的 artifact-bytes 路由 `GET /artifacts/{artifactID}`（`server/internal/handler/claude_science_runtime.go:132`、handler `GetClaudeScienceRuntimeArtifactBytes` `:549`）同样依赖请求头鉴权。
- 现有沙箱规则：插件 iframe `sandbox="allow-scripts"` + `referrerPolicy="no-referrer"`（`plugin-shell-view.tsx:337-338`）— 代理方案必须维持 opaque origin，不得 `allow-same-origin`。

---

## 2. LabOutputPanel spec

### 2.1 目标与范围

一个**共享组件** `LabOutputPanel`，收敛 4 个 issue-bound lab 的「本 issue 运行结果」到一个统一面板，挂在 **issue 详情右栏（IssueLabsSection 内）**，让用户选中 lab 后能原地看到产出，而不是跳去各 lab 视图或翻评论。

范围：**只做 read-side 展示**（不新增写端点，不改变 lab 运行逻辑）。Claude Lab 已有多 tab workbench，本面板**不替代**它（claude 保持 5 tab，面板只做 issue 级摘要）；pythia/mythos/code_canvas 用面板补齐缺口。

### 2.2 组件 API（推荐）

```tsx
// packages/views/experimental/components/lab-output-panel.tsx
interface LabOutputPanelProps {
  wsId: string;
  issueId: string;
  labSource: string;            // claude_science_lab | pythia_oracle | mythos_swarm | code_canvas
  labMode?: "sole" | "enhancer"; // mythos 专用
}
```

- 内部按 `labSource` 分派到 4 个轻量取数/渲染子组件（见 2.3）。
- 挂载点：`issue-labs-section.tsx` 的 indicator 行下方（现状 indicator 只到 running/queued/failed/cancelled，`issue-labs-section.tsx:177-220`）；`lab_source` 存在时渲染。
- 空态：lab 已选但无任何 run → 显示「尚无运行」+ 一行引导（对应 0.3.54 计划的 onboarding hint）。

### 2.3 四个 lab 的产出映射（数据源 + wire shape + 渲染）

| Lab | 取数（全部 rawRequest，禁裸 fetch） | 渲染 | 轮询/SSE 策略 |
|---|---|---|---|
| claude_science_lab | 复用 `GET /api/experimental/claude-science-lab/issues/{id}/context`（lab.go:107-115）→ `Tasks[]` 里提取最新一条的 `result_attachments/result_predictions/result_code_blocks` + `result_summary`；`LabSeq` 做 run 计数徽章 | 复用 `ArtifactRenderer`（`artifact-renderer.tsx:16-27`）渲染 attachments；预测点进 `ForecastProbabilityChart`（claude-lab-view 已有）；code_blocks 进代码块列表 | 3–5s 轮询（对齐 lab-chat-panel 3s 模式 `lab-chat-panel.tsx:29-34`）；envelope 已在 result jsonb 内，无 SSE |
| pythia_oracle | 现状无「本 issue 预测」读端点（预测是 SSE POST 启动 + 落 IssueLabsSection）。**需新增 read 端点**：`GET /api/experimental/pythia-oracle/forecast/issue/{issueId}` 返回该 issue 最近一轮 10 帧；备选：面板复用 `forecast-stream-view.tsx` 连 SSE | `ForecastStreamView`（`forecast-stream-view.tsx:26-35`）flat envelope 直接兼容 | 若 read 端点：5s 轮询；若 SSE：fetch+ReadableStream（`forecast-stream-view.tsx:43+` 模式） |
| mythos_swarm | `GET /api/issues/{id}/mythos-runs?workspace_id`（mythos-view.tsx:519-520）→ 最近一条 `problem`（输入）+ `coda_conclusions[]`（coda 输出；free-text `coda_summary` 仅 ephemeral `POST /run` 响应，`mythos_run` 表无此列）；enhancer 模式叠加 `GET /api/experimental/mythos-swarm/supervise/{runID}`（router.go:900-901）→ `supervision_state` | problem 摘要卡片 + conclusions 键值表（mythos-view.tsx:582-584 现有渲染可抽组件）+ 监督状态徽章 | 5s 轮询；「立即检查」按钮 POST /supervise/{runID}/tick（router.go:901） |
| code_canvas | 现状无持久 run 记录（渲染是即时的，code-canvas-view.tsx:54-63）。**需新增 run 持久化**：issue 关联的渲染产物落 plugin artifact 风格条目 + 关联 issue | `<iframe srcDoc sandbox="">`（code-canvas-view.tsx:149-154 模式） | 3–5s 轮询产物列表 |

### 2.4 错误展示规范

- 取数失败：面板内联 error 条（`text-destructive`），带重试按钮；**不弹全局 toast**（面板是常驻只读面）。
- run 失败（task failed/cancelled）：indicator 已覆盖（issue-labs-section.tsx:211-220），面板补一行 `failure_reason`/`error` 摘要（lab.go `LabTaskBrief.Error`/`FailureReason`）。
- 空数据与 loading：`Skeleton`（复用 ui/skeleton）；空态文案走 i18n。
- 端点 404（lab flag 中途关闭）：面板显示「实验已关闭」态，不重复报错。

### 2.5 run 历史

- claude：`LabSeq`（lab.go 已完成 terminal-run 计数，`lab.go:312-347`）+ `Tasks[]` 时间线（fetchLabContextTasks 已返回 20 条）。
- mythos：`mythos-runs` 列表天然是历史。
- pythia/code_canvas：现状无持久历史 → **新增读端点/持久化时顺带保留 run 记录**（pythia 按 issue 存最近 N 轮；code_canvas 存 issue 关联产物列表）。
- 统一展示：面板顶部一个「运行次数」徽章 + 可折叠「历史运行」列表（id/时间/状态），点击展开看单次产出。

### 2.6 取数策略（统一约定）

1. 全部走 `api.rawRequest`（CLAUDE.md 0.3.30 契约，禁裸 fetch；Pythia loopback 例外走 `pythia.proxy`）。
2. 轮询用 `useQuery` + `refetchInterval`（对齐 `agentTaskSnapshotOptions` 5s 模式），SSE 用 fetch+ReadableStream（`forecast-stream-view.tsx:43+`），**不用 EventSource**（无法带 auth 头）。
3. 查询 key 必须含 `wsId`（packages/CLAUDE.md 规则）。
4. 组件间不共享 store；父层只传 `wsId/issueId/labSource`。

### 2.7 挂载点（唯一）

- `issue-labs-section.tsx`：indicator 行下方新增 `<LabOutputPanel …/>`，仅当 `labSource` ∈ 4 个 A 类 lab 时渲染。
- 不新增路由、不改变各 lab 视图；Claude Lab 视图保持现状（面板是 issue 级摘要，workbench 是全局工作台）。

### 2.8 推荐实现方案 vs 备选

**推荐（渐进式，按 lab 分 4 个 PR）：**
1. PR-1 组件骨架 + claude_science_lab 接入（复用 context 端点，零后端改动，最快闭环）。
2. PR-2 mythos_swarm 接入（复用 mythos-runs + supervise，零后端改动）。
3. PR-3 pythia_oracle 接入（需新增 read 端点 `GET .../forecast/issue/{issueId}`，纯查询，复用现有 10 轮落库数据）。
4. PR-4 code_canvas 接入（需新增 run 持久化 + issue 关联产物读端点）。

**备选 A（更重）**：把面板做成通用「lab run 汇总」，由后端统一出 `GET /api/experimental/labs/{labSource}/issues/{issueId}/output` 聚合端点——统一 wire 但需一次性后端改动 + 4 个 lab 的适配器，风险高于渐进式。
**备选 B（更轻）**：先只做 claude + mythos（零后端改动），pythia/code_canvas 的 read 端点留待各自 lab 后续版本——若想快速见效可选此路。

---

## 3. iframe auth proxy spec

### 3.1 目标

让 user-plugin 的 `iframe` tab 与产物 `<img src>/<a download>` 在 **token-mode desktop**（原生资源加载无法带 Bearer 头）能加载**当前用户有权限**的鉴权内容，且不破坏现有沙箱（opaque origin）与 `requireUserID` 鉴权。

### 3.2 endpoint 设计：短时签名 URL（推荐）vs 代理

**推荐：短时签名 URL（免 token 的 self-contained URL）**

- 形状：`GET /api/user-plugins/{slug}/artifacts/{id}/raw?sig=…&exp=…&uid=…`。
- 流程：renderer 用 `api.rawRequest` 调签发端点（带 Bearer）→ 后端校验权限 → 返回**签名 URL**（HMAC(server-secret, `{uid, slug, resource, exp}`)，exp ≤ 5min）→ renderer 把签名 URL 放进 `<iframe src>` / `<img src>`。
- 服务端：新 handler `POST /api/user-plugins/{slug}/artifacts/{id}/sign`（Bearer 签发）→ 返回 `{url, exp}`；raw 端点加 `?sig=&exp=&uid=` 分支——有签名则跳过 `requireUserID`（改验 HMAC），无签名则维持 Bearer 鉴权。
- 优点：后端改动小、无长连接、天然缓存友好；iframe/src 直接可用。
- 缺点：URL 会出现在日志/历史（exp 短 + 单资源缓解）；需防重放（exp + uid 绑定）。

**备选：后端反向代理端点**

- 形状：`GET /api/experimental/proxy/plugin/{slug}/{path}`，handler 内 rawRequest-style 带 Bearer 转发到目标，返回 200/stream。
- 优点：URL 无敏感参数。
- 缺点：每个资源一次服务端往返 + 头转发逻辑；大文件 stream 要 `io.Copy`；与现有 rawRequest 契约重复。
- 适用：仅当签名的 exp/重放模型不可接受时。

### 3.3 鉴权与 scoping

- **签名签发**（新端点，Bearer 鉴权）：`POST /api/user-plugins/{slug}/artifacts/{id}/sign` → `{url, exp}`。校验：`requireUserID`（user_plugin_artifacts.go:341-344 同款）+ slug 属于当前用户/workspace 可见 + 资源存在。
- **签名校验**（raw-signed 分支）：HMAC-SHA256(secret, `uid|slug|resource|exp`) 恒定时间比较 + `exp > now` + `uid` 匹配签发者；失败 → 403。secret 用 server 进程内随机值（重启失效 = 自动过期，可接受）。
- **按 plugin slug scoping**：签名只对 `{slug, artifactID}` 单一资源有效，不签发通配；杜绝跨插件访问。
- **维持 opaque origin**：iframe 保持 `sandbox="allow-scripts"`（plugin-shell-view.tsx:337-338），签名 URL 只是让**内容加载**通过鉴权，不授予 `allow-same-origin`——插件脚本仍无法触碰 app 凭据。
- **产物侧**：`ServePluginArtifactRaw`（user_plugin_artifacts.go:341-403）加 `?sig=` 分支；claude-science artifact-bytes（claude_science_runtime.go:132,549）如要支持 iframe 展示同样加（可选，claude artifact 视图已走 rawRequest 内联读取，非阻塞）。

### 3.4 renderer 如何解析 URL

- `plugin-shell-view.tsx:320` 的 iframe 分支改为：`tab.src` 相对路径先调签名端点换签名 URL 再设 `src`（保留 `api.getBaseUrl()` 前缀逻辑）。
- `artifact-renderer.tsx:35-38` 的 `resolveUrl`：对 `image/file/html` 类型产物，若 `url` 指向需鉴权的 raw 端点，先 `rawRequest` 取签名 URL 再返回给 `<img>/<a>`；`data:` 与绝对 http(s) 直通（artifact-renderer.tsx:36）。
- 组件内维护签名 URL 缓存（key = `slug|id`），exp 前刷新；组件卸载时无需撤销（短时自然过期）。

### 3.5 安全边界（清单）

- HMAC 恒定时间比较；secret 不出进程；重启自动失效。
- exp 上限 5min，签发限频（对齐 pythia proxy 30/min 思路）。
- 签名只含单资源；不签发目录/通配；拒绝 `..`/路径穿越（user_plugin_artifacts.go 已按 ID 索引查，天然防穿越）。
- iframe sandbox 不变；绝不加 `allow-same-origin`。
- 产物 Content-Type 保持存储值 + `X-Content-Type-Options: nosniff`（防 html 类产物执行脚本越权——虽然 sandbox 已挡）。

---

## 4. 边界与约束（跨项）

- 只出 read-side/签发侧，**不改变 4 个 lab 的运行逻辑与写端点**（pythia/code_canvas 的新增 read/持久化端点除外，它们是增量）。
- flag 关闭必须完全绕过实验代码（Labs 硬约束 #1，CLAUDE.md Labs Platform）。
- 迁移 forward-only；本设计**预期零迁移**（pythia per-issue 预测落库若缺字段，优先复用现有表/JSONB）。
- Labs 网络调用全走 rawRequest；i18n arrow-selector + 4 语言。
- 不做保留 workspace；资源仍隔离在安装者活跃 workspace。

---

## 5. 可执行任务清单（后续会话接棒）

### 里程碑 M1 — LabOutputPanel 骨架 + claude（后端零改动）
- [x] `packages/views/experimental/components/lab-output-panel.tsx` 骨架（2.2 props + 空态/错误/loading 态）
- [x] claude 子组件：rawRequest context 端点 → 提取最新 task 的 attachments/predictions/code_blocks → ArtifactRenderer + ForecastProbabilityChart 渲染
- [x] `issue-labs-section.tsx` indicator 下方挂载（仅 A 类 lab）
- [x] i18n 4 语言新 key + 单测（mock api.rawRequest）

### 里程碑 M2 — mythos 接入（后端零改动）
- [x] mythos 子组件：`mythos-runs` → coda 摘要 + conclusions 表；enhancer 加 supervise 状态 + 「立即检查」tick 按钮
- [x] 从 mythos-view.tsx:582-584 抽取可复用 conclusions 渲染（或直接复用组件）

### 里程碑 M3 — pythia 接入（实际前端-only，读端点早已存在）
- [x] 复用现有 `GET /api/experimental/pythia-oracle/forecast/issue/runs?issue_id=`（migration 164 + `forecast_issue.go::pythiaIssueForecastRuns`），**未新增后端**
- [x] pythia 子组件：5s 轮询 read 端点渲染最新 run 的 10 帧
- [x] zod schema/type + 4 语言 i18n + vitest

### 里程碑 M4 — code_canvas 接入（持久化 + 读端点 + 渲染输入面板）
- [x] migration 239 `code_canvas_artifact` + `POST /api/experimental/code-canvas/issues/{issueId}/artifacts`（渲染+落库）+ `GET .../artifacts`（读历史）
- [x] code_canvas 子组件：**自带渲染输入 + 历史**（用户定夺，非 spec 字面的只读历史），iframe srcDoc `sandbox=""` 渲染历史产物
- [x] flag-gated + `loadIssueForUser` membership + Go 单测 + vitest

### 里程碑 M5 — iframe auth proxy（签名 URL）
- [x] 后端：`POST /api/user-plugins/{slug}/artifacts/{artifactID}/sign`（Bearer 签发）+ raw 端点 `?sig=&exp=&uid=` 分支（HMAC 恒定时间校验，403 失败）
- [x] secret `crypto/rand` + `sync.Once` 进程级（重启失效）+ 测试（happy path / 过期 / 篡改 / 跨资源）
- [x] renderer：`useSignedArtifactUrl` hook + plugin-shell iframe 分支 + artifact-renderer 换签名 URL + 缓存/刷新
- [x] 签名分支跳过 Bearer、`X-Content-Type-Options: nosniff` 内联；非签名分支 attachment（F-006）不回退；sandbox `allow-scripts` 无 `allow-same-origin`

### 里程碑 M6 — 收尾
- [x] `pnpm typecheck` 6/6 + `cd server && go test -count=1 ./internal/... ./pkg/agent/...`
- [ ] 更新 CLAUDE.md 头部 release 记录 + 本 spec 状态 → shipped（待 commit + 打包，0.5.19）
- [ ] 阶段 4 完成，接棒 阶段 5（sec-first 6 项 HIGH vuln）
