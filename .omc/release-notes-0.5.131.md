# 0.5.131 — 实验室三系统完善批(修复 + 参考源移植)

日期: 2026-10-02 · 分支: `epic/0.5.72-followups` · 零迁移、零 wire 变更(除 preload 类型字面量)

三个实验室(科研实验室 / 事件推演 / 因果决策)的 issue 结合面完善。分两批:修复批(探索发现的真缺陷)+ 参考源移植批(open-science / MiroFish / semantica 的 UI 与交互模式)。**三个实验室代码均为 fork 自研,参考源只提供模式**。

## 批 1 — 修复批

### 事件推演 (pythia)

- **监控页健康条修活**:健康条自 0.5.112 起轮询引擎从未定义的 `/status` 路由(allowlist 还为该幻影路径放行),三条健康芯片永远 pending。改走引擎真实的 `/links` 端点;从 allowlist 移除 `/status` 并留注释。preload 的 `pythia.proxy` path 字面量联合同步(`index.ts` + `index.d.ts` 两处)。
- **引擎 `oracle.health()` 诚实化**:桌面契约(MULTICA_REQUIRED=1)下原来恒返回 False(残留的"vestigial signal"),`/links` 的 oracle 芯片在健康安装上也显示"关"。现改为探测 `MULTICA_AGENT_RUNTIME_URL/health` 真实可达性(URL/token 缺失 → False)。vendor 与 resources 镜像双写同步。
- **概率轨迹图接线**:`PythiaTrajectory`(0.5.111 写好、从未被任何界面引用)接入 issue 内嵌实时图(≥2 轮淡入,reduced-motion 门控)与历史折叠展开态;顺带修掉实时图里无 children 的空 `motion.div` 动画槽。
- **停止按钮文案**:可见文本从错用的 `t(embed_running,{round:0})`("推演中 · 第 0 轮")改为真正的停止标签;**续推表单**运行中真正禁用(原硬编码 `disabled={false}`),start 提交带 busy 态。
- **SSE 断流重连**:reader 错误原被吞且永不重挂;现 1s→15s 指数退避重连(服务端 snapshot 帧保证不丢轮、重复轮按 index 幂等),404/410 弃跟。
- **计划轮数持久化**(服务端):`pythia_forecast_run.rounds` 语义改为计划总轮数(创建时写入,进度写不再压成已落地数)——重载页面后"待定轮"占位卡终于可见。新 Go 钉子 `TestPythiaForecastPlannedRoundsPersistMidRun`(2 轮 + 5s 轮间隔窗口内确定性采样)。
- start 后闪屏修复(乐观信任 stream.status)、历史折叠时间戳按 locale 本地化(原原始 RFC3339)。

### 科研实验室 (claude lab)

- **invalidation 键修复**:「立即开始研究」成功后失效的 `["claude-lab-context", issueId]` 是无人持有的键,刷新承诺从未生效;改为正确的 workbench 键 + agentTaskSnapshot 失效(issue 侧 pill/embed 同步加速)。
- **错误浮出**:任务快照/产物请求失败原渲染得与"没有运行"一致(pill 消失、embed 空提示);`useClaudeLabIssue` 暴露 isError + refetch,embed 渲染错误条 + 重试。
- **硬编码中文清零**:共享产物视图约 17 处(实验产物/加载产物/删除 session/图表状态等)全部 i18n 化,新 `runtime_*` 键族 ×4 语言。
- 不安全的 `(tLab as (k:string)=>string)(\`tab_${key}\`)` 强转改为逐键箭头选择器。

### 因果决策 (causal graph)

- **建议队列 wsId 修复**:聚焦模式下原从 `nodes[0]?.workspace_id` 推导,无节点 issue 上队列静默失效;改从路由上下文直传。
- **边过滤条(新)**:服务端 `type/status/min_confidence` 过滤参数 0.5.83 起就支持(i18n 键都埋好)但无客户端发送;工作区图新增过滤控件,过滤时客户端裁剪孤立节点,缓存键随过滤变化。
- 空态文案纠正(零节点 vs 过滤无结果区分)、队列脚注与 "by {agent}" 徽章 i18n 化。

## 批 2 — 参考源移植批

- **因果路径追溯**(semantica pathPulse/路径智能卡移植):`useCausalGraphPath`(0.5.83 起存在于 core、零 UI 消费)接线。画布新增 `pathHighlight`(顺序点亮 70ms/边 + `causal-path-flow` 流动虚线 + 链外 0.15 调暗 + 起点虚线环/终点实线环);工作区图两击选点 + PathTraceCard(距离带 direct/near/mid/distant、最弱环节置信度红黄绿条、全图度数瓶颈节点、反转/清除)。纯函数 `causal-path-summary.ts` + 6 测试。
- **全员问卷**(MiroFish Step5 批量访谈移植):追问 tab 双模式(单聊/全员问卷),一问并行扇出 Oracle+4 persona(纯客户端复用 /chat 端点,零后端),答案卡错峰入场,单人设失败只红该卡。
- **council 动作卡**(MiroFish Step3 类型卡移植):议政票表展开为动作卡——相对 oracle 基线的立场徽章(≥+5pp 倾向更高 / ≤-5pp 倾向更低 / 其余接近基线;无基线不造立场),错峰入场;阈值整数化比较防浮点漏判(测试钉住)。
- **run 状态动画条**(open-science session-card 模式):科研实验室 embed 头部下新增状态条——运行中 shimmer 扫描、排队琥珀脉冲、完成祖母绿填充 + 对勾弹入;CSS-only + prefers-reduced-motion 媒体查询门控。
- **终端输出视图**(open-science JobTerminalOutput 模式):沙箱会话 stdout/stderr 改红绿灯头 + 暗色 mono 终端块(stderr 红系)。
- **死动画接线**:桌面 `globals.css` 的 `pythia-pulse-ring`(0.5.112 起零消费者)接进监控页 live 心跳点。

## 测试与门禁

新增/扩展 9 个测试文件面:pythia embed(4 例:零态收缩/轨迹+待定轮/乐观闪屏/历史本地化)、followup-chat 问卷(2)、round-view 立场(4)、claude embed 错误态+状态条、lab-output-panel 停止钮/表单禁用、causal path-summary(6)、minimap 路径覆盖层(3)、causal-graph-queries 过滤参数(1)、Go 计划轮数钉子(1)。

门禁(全绿):`pnpm typecheck`(6/6)、`pnpm lint`(8/8)、views vitest 1940 通过、core 969 通过、desktop 381 通过(33 skipped 在案)、`go test ./internal/... ./pkg/agent/...` 全绿(DATABASE_URL 从 repo root 导出,新 Go 测试实跑非跳过)。

测试期修掉两类坑:共享 QueryClient 需先 `cleanup()` 再 `clear()`(否则后台 refetch 泄调用数);FE `CausalEdge` 类型无 `evidence_comment_id`/`updated_at`(DB 列 ≠ wire 类型)。

## 参考源研究结论(存档)

- **aipoch/open-science**(科研实验室参考):Electron 研究工作台,可复现性领先(artifact 溯源版本链/复现回执/.science 会话包/文献子系统/PDF 图表抽取)。后续候选:composer 消息队列(直击 JYF-490)、分页报告纸面视图、token 用量 popover、provenance 面板(需后端列)。
- **666ghj/MiroFish**(事件推演参考):Vue3 群体模拟引擎;**无概率数值/Brier/回放——我们定量面全面领先**。后续候选:persona 档案卡、报告工具调用观测栏、模拟时钟轴。
- **semantica-agi/semantica**(因果决策参考):sigma+graphology;后续候选:zoom 分层 LOD、时间轴 scrubber(需 valid_from/until)、邻域透镜、审批链建模。
