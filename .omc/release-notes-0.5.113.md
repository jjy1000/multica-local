# 0.5.113 (2026-09-23) — pythia_oracle issue 优先嵌入: 主任务区动画 + 状态徽章 + 被动监控台 + 终止闭环 4-tab 改造

Feature commit: `18212c285` (11 files, +463/−151). 用户驱动的第二轮 pythia 交互重构: 把实时动画与报告从属性面板搬到主任务区评论之下, 标题区增加运行指示 pill, monitor 400 修, 属性面板 4-tab→3-tab.

## 用户可见

- **状态徽章挂标题右**: 打开任一 pythia 绑定的 issue, 标题右上角出现运行徽章 (跟 agent run pill 同一位置), 显示 "Pythia · 第 N 轮" / "已完成 N 轮" / "已中止" / "已失败".
- **主任务区动画**: 派发推演后, 评论流下方 / 输入框之上展开紫色推演条 (折叠默认); 轮次实时填入 + council 票据 + 概率轨迹曲线; 报告完成后切换到 markdown 完整报告渲染.
- **属性面板 3-tab**: `继续推演` / `历史·回放` / `追问` (live + report tab 删除, 已迁到主区).
- **监控台 fix**: `/experimental/pythia` 不再报 "pythia monitor 400".

## 工程

- 新组件 `PythiaIssueEmbed` (`packages/views/experimental/components/pythia/pythia-issue-embed.tsx`): 主任务区评论之下, 输入框之上挂载; 共享 `usePythiaIssueLab` hook (SSE bus + cache); 无 run 时折叠到 `null` 不占空间.
- 新组件 `PythiaHeaderPill` (`pythia-header-pill.tsx`): 标题行右侧, 仿 agent run pill; 仅当 `issue.lab_source === "pythia_oracle"` 才挂; 共享 hook 同步反应 SSE round frames.
- 服务端 `pythiaForecastMonitor` handler-local header fallback (`X-Workspace-ID`): `/experimental/...` 实验路由组下 `ctxWorkspaceID` 永远空, 旧代码 400. 单点改动不增加 workspace-membership DB hit.
- 客户端 monitor 错误不再静默退化成空 list; `parseWithFallback` 的 `[]` fallback 仅在 `r.status === 404` 时生效, 400/500 抛到 `runsQuery.isError`.
- `PythiaPanel` (属性面板) 从 4-tab 简化到 3-tab; live/report tab 删除.
- i18n: 4 语言 `experimental.json#pythia_lab` 加 10 个 key (embed_title/running/empty/history_toggle, pill_running/done/aborted/failed/idle, tab_continue).
- 测试: `lab-output-panel.test.tsx` pythia 5 条用例改为打开 history tab 验证; retry 用例只验证错误消失 + 调用计数.

## Ship 结果

(待补)
