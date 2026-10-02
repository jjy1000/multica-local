# 0.5.134 (2026-10-03) — 实验室页闲置大脑 hero + ICU 复数死串修复(截图反馈批)

一个 FE-only 修复提交(`a34570dca`),零迁移零 wire 零 Go。用户截图 0.5.133 装机后的实验室页:**页面上没有任何视觉**,且计划列表头裸显 `{count, plural, other {# 条}}`。两个独立 bug:

## ①大脑画布根本不在实验室页上

大脑只挂在工作台(`LabWorkbenchSection`)里,而工作台**选中 Issue 前恒 null**——不选中就整页无视觉。修复:未选中 Issue 时,页面 Intro 下方直接渲染**闲置大脑 hero**(`issueId=""` 使画布保持闲置态:六节点呼吸+边慢漂+中枢轨道;工作区 AgentTaskSnapshot 本就是共享缓存,产物查询保持 disabled,零额外网络);选中 Issue 后 hero 让位给工作台里的话题级大脑,同屏只有一只。

## ②六个 ICU 复数键从未渲染过

`plan_run_count` / `plan_audit_count` / `artifact_sessions_count` / `code_sessions_count` / `plan_timeline_predictions_header` / `plan_lab_seq_label` 在**全部四语言**里值都写成 ICU 语法 `{count, plural, other {…}}`——i18next 从不处理这种语法,所以键诞生以来计划页每一行都在裸显花括号。转换为 i18next 约定:en `_one/_other` 键对,中日韩 `_other`(仓库既有复数法则);`plan_lab_seq_label` 调用侧传的是 `{seq}` 非 count,转为普通 `{{seq}}` 插值单键。

## 门禁

`pnpm typecheck` 6/6;`pnpm lint` 8/8;views claude-lab 32 + desktop **381** vitest 绿。
