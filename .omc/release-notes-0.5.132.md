# 0.5.132 (2026-10-03) — 三实验室专属动态图全部落地(大脑/议会/星图),全接真实数据

三批 FE-only 改动(5 feat/docs 提交),零迁移、零 wire 改动、零 Go 改动。三实验室各得一个专属视觉隐喻,不再共用通用进度条:**科研实验室=大脑在运转,事件推演=议会在收敛,因果决策=星图在传播**。全部经独立 HTML 原型(存档 `.omc/prototypes/`,docs `5d1dcadec`)先经用户过目再移植。

## ① claude-lab:科研大脑(feat `cecc3822d` + docs `4ec3cd348`)

`ClaudeBrainCanvas`(`packages/views/experimental/components/claude-lab/claude-brain-canvas.tsx` + 纯映射层 `claude-brain-derive.ts`):中枢核心(呼吸环+旋转虚线环+当前相位/计时)+六智能体轨道(research/critique/ml/physics/biology/write,按装包 manifest 真实阵容)。

- **真实数据**: `agentListOptions` 按 name 解析 roster 槽位 + `useClaudeLabIssue` 的 AgentTaskSnapshot(5s 轮询)驱动节点状态;dispatched/waiting_local_directory 折叠为 queued;**不造进度**——AgentTask 无百分比字段,运行中=不定弧+实时时长。
- 相位流水线(问题解析→文献检索→实验复现→同行评审→报告撰写)=运行角色的呈现(critique→同行评审 对应 0.5.114 起真实自动派发),代码注释明示"呈现非事实,后端相位事件是后续契约"。
- 交互:点击节点固定详情卡(角色描述/触发摘要/时长/重试/错误);边随运行流动+完成 ✓弹出+running→终态中枢波纹。
- 双宿主:ClaudeIssueEmbed 状态条下(紧凑可折叠,运行默认展开)+桌面 lab 工作台 LatestResultPanel 上方(完整:近期运行条+产物计数)。

## ② pythia:推演议会(feat `20efeab2e`)

`PythiaCouncilCanvas`(`pythia-council-canvas.tsx` + `pythia-council-derive.ts`):共识罗盘(指针缓动+分歧带+轮次徽章)+引擎真实四人格席位(swarm.py PERSONAS:Strategist/Economist/Naturalist/Skeptic)+每轮共识轨迹条。

- **数据面**: council 票据(votes/consensus/spread/split)在 PythiaForecastEnvelope.council 里是真实的(mig 290 全议会契约)。座位=最新 council 保留每人格跨轮最近票(newest-first 首见即胜;probability 0 是 zod 默认=未投票);无 council 轮轨迹回落 oracle 概率;**split 服务端标记优先**(_SPLIT_TRACK 0.30 兜底);verdict 分歧>方向。
- **props-driven 刻意设计**: usePythiaIssueLab 持唯一 SSE 订阅,画布吃父组件 envelopes——二次订阅=第二条流。
- 挂载:PythiaIssueEmbed live 块顶部(轮卡之上)+历史折叠内(默认收起)。运行中席位脉冲+投票彗星;完成锁定环+判定文案。

## ③ causal:因果星图(feat `7fdfdb7a7`)

`CausalConstellationCanvas`(`causal-constellation-canvas.tsx` + `causal-constellation-derive.ts`):焦点星居中,左弧上游成因/右弧下游影响(1 跳邻域按方向切,每侧度数上限),**边色直接骑现有 `--causal-edge-*` tokens**(明暗两套在 tokens.css,零组件级配色)。

- active 边按置信定速流因果粒子(方向 成因→焦点→影响);suggested 边虚线+?脉动(≤0.5 法则在 tier chip tooltip);rejected 幽灵化保留(墓碑语义)。
- **决策光锥**:BFS 逐层波纹(仅 active 边、方向无关、2 跳),影响面计数;信任阶梯 chips 按真实边状态计数(suggested=Tier D,rejected 自成桶)。
- **零查询组件**:页面持有 useCausalWorkspaceGraph,确认/驳回仍在 SuggestedQueue,画布只显示待确认计数。挂载:桌面 causal-graph-view 全图上方 hero,焦点跟选中(默认最高度数节点)。

## 共同纪律(三画布一致)

- 动画:CSS 关键帧循环+单一 `prefers-reduced-motion` 闸门;入场走 motion/react+`useReducedMotion`;**逻辑零依赖 requestAnimationFrame**(IAB 类宿主 visible 却永不回调帧——原型阶段实测教训)。
- 主题:组件级 `--brain-*/--council-*/--const-*` CSS 变量+`.dark` 覆盖(Shiki 同款纯 CSS 契约),chrome 走语义 token。
- i18n:动态键一律显式 switch helper(TFunction<ns> 收窄,模板字面量键破坏箭头表达式 proxy 契约——0.5.131 tabLabel 教训);新增 31 brain_* + 8 pythia_lab.council_* + 9 constellation_* 键 ×4 语言。
- Flag-off 面不变:组件只挂载在已闸表面内部,flag-off 完全绕过。

## 门禁

`pnpm typecheck` 6/6;`pnpm lint` 8/8;views vitest **1975 passed**(33 skipped 在案;新增 14+10+11=35 测试:derive 纯函数+canvas 渲染/交互/主题契约)。Go 零改动,发版前仍跑全量 go test 门禁。发版未包含:原型 HTML 已入库(5d1dcadec)供溯源。

## 已知残留

- Mimosa commit hook 三次报 scanner_enobufs 放行,完整安全审计仍待跑(0.5.131 起累积)。
- 相位流水线是"运行角色"呈现;真实相位事件需后端契约(已在 derive 注释立此存照)。
- 原型阶段两个宿主教训已入记忆:严格模式未声明赋值=ReferenceError 杀全脚本;TS 非空断言 `!.` 在浏览器 JS=SyntaxError。
