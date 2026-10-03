# 0.5.136 — 三实验室画布原型级环境层补全（2026-10-03）

一句话:0.5.132–0.5.135 把三个实验室画布（科研大脑 / 推演议会 / 因果星图）的**数据与挂载**建完了,但对照批准的原型（`.omc/prototypes/*.html`）,**视觉环境层**在移植时被丢掉了——画布悬在一张素卡片上,缺少原型被认可的"活着的空间感"。本批把原型有的六类层一次性补齐,零数据改动、零迁移、零 i18n 键。

## 背景与诊断

用户反馈"三个功能面板动画还没有完成建好"。逐面核查:

- 挂载点**无缺**——claude lab 页两分支（idle hero + workbench）、issue 三嵌入、pythia 监控页 hero（chamber 查询走 `loadIssueForUser`,不需要 `workspace_id`,与 embed 同链路）、causal 两路由（FocusedGraph + WorkspaceGraph）全部在位。
- 取数链路**无断**——`issue/runs` handler 按 issue 授权,monitor hero 不带 `workspace_id` 也是正确调用（0.5.115 教训只适用于 monitor 端点本身,那个已带）。
- 差的是**视觉完成度**:原型每张画布底下有主题色环境辉光（radial-gradient wash）+ 径向网格;脑图运行边有彗星粒子、核心有热辉光、运行图标浮动、失败节点抖动、待机随机 ping 脉冲;星图有背景星空与焦点星辉光。这些全部没有进产品。

## 改动

### ClaudeBrainCanvas（packages/views/experimental/components/claude-lab/）

- **环境辉光**:舞台背景从单一 `--brain-grid` 洗升级为 `--brain-glow`（翡翠绿,亮 .06 / 暗 .05）radial wash + 网格,双主题。
- **边彗星**:`BrainEdge` 重构为 `<g>`（testid 挪到 g 上）——运行边满强度彗星（animateMotion 1.15s）,与议会/星图同款。
- **待机 ping 脉冲**:setTimeout 链（首拍 1.2s,节奏 3.4–4.9s 随机）从非运行节点里随机点亮一条边 1.6s（`.brain-edge-ping` 快速虚线流 + 柔彗星）——装饰性,不改节点状态;节点数组走 ref,5s 快照轮询不会重置节奏;零 rAF。
- **核心热辉光**:运行时核心圆挂 `.brain-core-hot`（drop-shadow 翡翠绿）。
- **图标浮动**:运行节点图标 1.6s bob（translateY −2px;独立包装 g,避开属性 transform 与 motion 的 style.transform 双坑）。
- **失败抖动**:failed/cancelled 节点内容包一层 `.brain-shake`（0.4s 单次）——同样独立包装层。

### PythiaCouncilCanvas（…/pythia/）

- **环境辉光**:舞台加 `--council-glow`（紫,亮 .07 / 暗 .06）+ `--council-grid` 径向网格。
- **运行刻度辉光**:running 时共识进度弧挂 `.council-arc-hot`（drop-shadow 紫）。
- **双向待机环**:standby 态在原顺时针外环（30s）内侧新增 r−16 逆时针环（44s,opacity .25）——"等待中"读作活仪表。

### CausalConstellationCanvas（…/experimental/components/）

- **环境辉光**:舞台加 `--const-glow`（青,亮 .08 / 暗 .06）+ `--const-grid`。
- **背景星空**:FNV-1a 种子（focus id,无 focus 时首个节点 id）→ xorshift 流生成 26 颗确定性星（位置/半径/相位/基础亮度),`.const-twinkle` 3.6s 呼吸闪烁、per-star animationDelay;种子只随 focus 变——图谱轮询零抖动;空库状态同样有星空。
- **焦点星辉光**:`.const-focus-glow`（drop-shadow 青 .7）;影响锥点亮的星挂 `.const-star-lit`。

### 共同纪律

全部新层只做呈现,不碰任何数据/状态;CSS keyframe 循环 + 单一 `prefers-reduced-motion` 闸（新类逐个入闸）;`Math.random` 仅用于装饰性 ping 取边节奏（Mimosa 标记为"加密弱随机"系误报——非加密用途）。

## 测试

- `claude-brain-canvas.test.tsx`:edge class 断言改指 `<g>` 内的 `<path>`（0.5.136 结构变化）;新增 ambience 钉——运行边挂 animateMotion 彗星、live 核心挂 `.brain-core-hot`、stage style 含 `var(--brain-glow)`、样式表含 ping/bob/shake 三类。
- `pythia-council-canvas.test.tsx`:新增——stage 含紫辉光、running 弧挂辉光类、standby 双环（.council-standby + .council-standby-rev）都在。
- `causal-constellation-canvas.test.tsx`:影响锥测试补 `.const-star-lit` 断言;新增——星空层 >10 颗、焦点星辉光、青辉光 wash、**确定性**（同 focus 两次渲染 innerHTML 全等）。

门禁:`pnpm typecheck` 6/6 绿;views vitest 201 文件 1979 通过（33 skipped 在案）;go `./internal/... ./pkg/agent/...` 见 ship 当次记录（本批零 Go 改动）。

## 不变

- 挂载矩阵、取数链路、i18n 键、Go 侧——全部零改动。
- 上批全部契约（idle-presence、props-driven、零查询星图、单 SSE 议会）原样。
