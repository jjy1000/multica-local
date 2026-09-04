# 0.5.97 (2026-09-03) — 实验室结果渲染全面优化（结果优先 + 双动作分工）

按用户两张截图的方向做的实验室 UX 改版（参考 Claude Science 结果优先布局），
TS-only 周期：无迁移、无 Go 改动。

## 实验室面板页（Claude 科研实验室）

- **删掉行上「打开对话」按钮**：行标题/历史本身已具备跳转对话能力，按钮冗余
  （`open_chat_button` 字符串保留——`lab-chat-panel` 仍在用）。选中行为不变：
  仍负责把该 Issue 装进右侧工作台。
- **新增「实验结果」面板（LatestResultPanel）**：左栏重排为 Issue 上下文 →
  实验结果 → 运行时间线。自动展示最近一次有结构化产出的运行
  （图表/预测/代码内联渲染，带状态徽标、耗时、进行中提示），
  testid `claude-lab-latest-result`。运行时间线每行改用共享渲染器。

## Issue 详情「科研实验室结果」卡片

- 原先两个按钮都跳同一页，现在**分工明确**：
  - **查看结果渲染**——原地展开本卡片，内联渲染该次运行的结构化产出
    （interactive-chart 图表 + figcaption、SVG/图片、预测概率条、代码块），
    不发生任何跳转。仅对「已终止 且 有结构化产出」的运行出现
    （testid `lab-deliverable-result-toggle`，aria-expanded 可访问）。
  - **在实验室查看完整记录**——保留的 run 级深链
    （`/experimental/claude-lab?issue=…&run=…`，0.5.81 ICP-3 契约），
    跳到实验室面板并定位该次运行。
- 无 scope 的「在实验室打开」链接删除，`open_in_lab` 字符串从 4 语言包移除；
  空任务卡片仍给出一条不带 run 的入口链接。

## 共享化重构（packages/views/experimental/components/）

- **lab-task-result-view.tsx**：`LabTaskResultView` +
  `labTaskHasStructuredDeliverables`——一次运行的结构化产出的唯一渲染器，
  桌面时间线行、实验结果面板、Issue 卡片三面共用；代码块默认折叠 2 段，
  图表先行（Claude Science 式结果优先）。
- **interactive-chart-envelope.tsx**：从桌面页提升为共享组件；顺手修掉
  `LabOutputPanel` 的遗留 TODO——`interactive-chart` 附件此前退化成 JSON
  代码块，现在渲染真正的 recharts 图表（line/bar/scatter，防御式解析，
  空/畸形包络显示占位符不抛错）。
- **lab-attachment-sanitize.ts**：XSS 消毒 helpers（safeSvgMarkup /
  safeImageSrc / safeHrefUrl）从桌面页移出，Issue 侧渲染执行与桌面完全
  相同的策略（views 的 noUncheckedIndexedAccess 下正则捕获组需 `?.[1]`）。

## i18n 与测试

- 新增 9 个 `lab_output_panel` 键（view_result_render /
  hide_result_render / no_output / show_more_code / chart_empty /
  chart_unsupported_type / image_no_source / download）+ `claude-lab`
  的 `latest_result_header`，zh-Hans/en/ja/ko 四语对齐（192/192/192/192、
  74×4 键校验过）。views 的 `i18next/no-literal-string` 比桌面严格，
  桌面原样搬来的字面量全部改走 i18n。
- `issue-detail.test.tsx`：原卡片测试断言改为 run 级深链
  （`?issue=issue-1&run=task-1`）+ 仅摘要运行不出现 toggle；新增
  「toggle 展开内联渲染图表 figcaption + 预测行」交互测试。

## 验证

typecheck 6/6、lint 8/8、views vitest 1832 通过 / 33 跳过（MUL-6632 停放）、
5 个相关测试文件 96 用例全绿；desktop 无引用改动文件的测试。本周期无 Go/
迁移改动，ship step-2 migrate 预期 no-op。
