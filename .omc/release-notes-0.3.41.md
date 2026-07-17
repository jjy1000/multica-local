---
name: 0.3.41 release notes
created: 2026-07-17T13:24:40Z
updated: 2026-07-17T13:24:40Z
---

# 0.3.41 — Claude Lab Workbench v2 (agent 结构化 emit + 渲染)

## 用户视角修复

1. **实验室任务结果现在渲染图表 / 预测曲线 / 代码块** — 之前 lab agent 跑完任务,结果只是 markdown 文字(`## 研究完成 ...`)。这次更新后,Claude Lab workbench timeline 每一行任务现在能渲染:
   - **interactive-chart**(Recharts line / bar / scatter) — 数据科学 / 统计图
   - **svg** — 内联 SVG 决策树 / 流程图 / 几何图
   - **png** — base64 data 或 `/api/uploads/<id>` URL
   - **html** — sandbox iframe
   - **md / csv / json / txt / log** — 折叠 / 滚动文本
   - **predictions[]** — 一条 SVG polyline 显示每轮预测概率
   - **code_blocks[]** — 带语法高亮的 fenced code 块(可滚动)
   - **markdown 报告** — 始终作为 timeline 行底部 footer 出现,保持可读性

2. **agent 自动 emit 协议** — lab 的 `research` leader agent 现在拿到一份 `Result Envelope` 约定,告诉它在最终输出里 emit `{output, attachments[], predictions[], code_blocks[]}` JSON envelope。SKILL.md(`multica-claude-science`)也加了相同段落,让 chat agent 在 delegating 到 research 时引导它。

3. **fall-through 路径(关键 UX 兜底)** — 我们在测试中发现,agent 的最终 message 经常被 claude stream-json 协议"重写"成 markdown 总结,但 agent 会同时把结构化 envelope **作为 comment 发到 issue**。所以 server 端 `lab-context` 现在在 `result.jsonb` 不带 attachments 时,自动扫描该 issue 最新一条 agent comment,parse 出 envelope 字段。这个 fall-through 让**老的 + 新的** agent 都自动能渲染,不需要 agent 严格遵循 prompt。

## API 新增/修改

### 修改(无新 endpoint)
- `GET /api/experimental/claude-science-lab/issues/{id}/context` 响应 `LabTaskBrief` 增加 3 个字段:`result_attachments[]` / `result_predictions[]` / `result_code_blocks[]`(omitempty)
- `POST /api/daemon/tasks/{id}/complete` server 端 handler 现在 parse `req.output` 为 JSON envelope;如果包含 `attachments` / `predictions` / `code_blocks`,promote 到 `result.jsonb` 顶层

### 字段 shape

```typescript
interface LabAttachment {
  kind: "interactive-chart" | "png" | "svg" | "html" | "md" | "csv" | "json" | "txt" | "log";
  name?: string;
  mime?: string;
  data?: unknown;  // inline payload (string for text/md, object for chart)
  url?: string;    // server-stored reference
  bytes?: number;
}
interface LabPrediction {
  round: number;
  scenario: string;
  narrative?: string;
  probability: number;
  confidence?: number;
  horizon?: string;
  persona?: string;
}
interface LabCodeBlock {
  language: string;
  filename?: string;
  code: string;
}
```

`interactive-chart.data` 形状:
```json
{
  "schema": {"type": "scatter", "x": {"field": "train_pkd"}, "y": {"field": "test_pkd"}},
  "data": [{"train_pkd": 5.2, "test_pkd": 5.1}, ...]
}
```
(`type` ∈ `line` / `bar` / `scatter`)

## 文件改动

### 修改
- `server/internal/handler/daemon.go`(`buildTaskResultJSON` + 3 个新 TestBuildTaskResultJSON tests + ResultEnvelope keys)
- `server/internal/handler/lab.go`(`extractResultDeliverables` + `scanAgentCommentsForEnvelope` + LabAttachment/Prediction/CodeBlock struct + `wsID` parameter pass-through)
- `server/internal/handler/daemon_test.go`(+ 3 BuildTaskResultJSON tests)
- `apps/desktop/resources/claude-science/agents/research.txt`(append 68 行 Result Envelope 章节)
- `server/internal/service/builtin_skills/multica-claude-science/SKILL.md`(append Result envelope 段落)
- DB direct update `research` agent instructions(把新 prompt 推给已创建的 agent,因为 install 只在首次装时填 instructions)
- `packages/core/types/api.ts`(+ LabAttachment / LabPrediction / LabCodeBlock interface)
- `packages/views/locales/{en,zh-Hans,ja,ko}/claude-lab.json`(+ `plan_timeline_predictions_header` key)
- `apps/desktop/src/renderer/src/pages/claude-lab-view.tsx`(`renderResultBody` + `AttachmentStrip` + `AttachmentCard` + `PredictionsStrip` + `CodeStrip` + `InteractiveChartEnvelope` Recharts 渲染)

### 修改(bump 版本)
- `apps/desktop/package.json` → `0.3.41`

## 验证

- 6 packages typecheck PASS(0 cached)
- Go tests:`TestBuildTaskResultJSON_MarkdownPreserved` / `TestBuildTaskResultJSON_EnvelopePromoted` / `TestBuildTaskResultJSON_OnlyPromotedKeysWhenPresent` + 6 之前的 lab tests 全部 PASS
- 端到端:用户测试 issue `随机测试一个实验科研项目` 的最新 task `09bf1d1a-...` 现在返回:
  - `result_attachments`: 2 (interactive-chart `ks-train-vs-test` + svg `decision-boundary.svg`)
  - `result_predictions`: 3 (baseline 0.42 / sensitivity 0.58 / replication 0.33)
  - `result_code_blocks`: 2 (Python KS 检验 + JavaScript render-chart)
- server log 证实 fall-through 路径:`lab: scanAgentCommentsForEnvelope rows=2 ... lab: fall-through populated attachments=2 predictions=3 code_blocks=2`
- pre-update snapshot — 0.3.40 备份在 `~/.multica/backups/pre-update-20260717-212421`

## 兼容性

- **0.3.40 mainboard-integration 保持不变**:Plan tab 跳主详情、OpenChatButton、删除同步。
- **flag-off 完全 bypass**:`{flagEnabled ? <ClaudeLabView /> : null}` 包整个 view。
- **chat_input_task_id 链路 (MUL-4351) 复用**:chat panel 仍用现有 chat API。
- **forward-only migration**:无新表 / 新列;新增字段全部 `omitempty`。
- **i18n arrow-only selector** (2026-07-14 incident):全部新代码用 arrow form。
- **fall-through 兼容老 task**:旧的(0.3.40 之前)task 没有 attachments,predict etc,timeline 渲染走 `result_summary` 旧路径,不破坏任何已有 UI。

## 已知遗留(后续 PR)

- **chart 渲染只支持 line / bar / scatter** — `heatmap` schema 已定义但渲染函数未实现(返回 "unsupported chart type")。后续 PR 加。
- **recharts 按需 require** — InteractiveChartEnvelope 用 `require("recharts")` 动态加载,避免 desktop 包大小膨胀。后续可换成 named imports + code-splitting。
- **fall-through 边界 case**:如果 issue 有 ≥2 条 agent comment 都带 envelope,我们只取最新一条。如果用户在多轮迭代中想看历史 envelope,需要单独 endpoint(后续 PR)。
- **CLAUDE.md 描述** 未更新 — 实验室 v1/v2 描述仍 0.3.19 时代的旧说明,后续文档 PR。

## 体验闭环

这次 ship 完成了 Claude Lab 从"装饰品"到"科研工作台"的语义闭环:

| 之前 | 现在 |
|---|---|
| 任务完成 → 主 issue 看 markdown 报告 | 任务完成 → lab workbench timeline 直接看到 chart / prediction curve / code block |
| 5 个 tab 大部分是装饰 | Plan tab 显示完整 timeline,其余 4 tab 仍装饰(v2 处理) |
| agent 没约定 emit 格式 | research.txt + SKILL.md 都加了 envelope 约定 |
| 老 task 没有结构化数据 | fall-through 自动从 agent comment 提 envelope,无需新 agent 重跑 |

用户现在打开 Claude Lab,选中 issue,workbench strip 直接渲染交付物 — 真正的"科研工作台"语义达成。