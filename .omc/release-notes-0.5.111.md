# 0.5.111(2026-09-23)— pythia_oracle 续推重构: issue 内推演闭环 + 面板全可视化

修复 Pythia 集成发现的两个问题: ① issue 推演无续推循环(报告交付后就断了);
② 实验室面板无过程可视化(用户不知道实验室在做什么)。设计对齐用户的
SocialSim-explorer(MiroFish 本地化, **只借设计未搬 AGPL 代码**); council
投票机制取自 vendor 内 MIT 血统 jangles-byte/Pythia。跨层契约见记忆
`pythia-forecast-continuity-contract.md`。

## 用户可见

- **推演报告交付在 issue 对话里**: 轮次跑完后引擎一次 LLM 合成中文结题报告
  (共识结论/多视角立场/关键关切/风险/建议), 以 pythia_runtime 评论写回当前
  issue——此前报告只在面板里闪现。合成失败回退机械摘要, 来源标签(synthetic/
  failover)不丢。
- **续推循环**: 报告交付后, 面板「继续推演」变量框注入新方案修正 → 基于
  原问题 + 全部历史轮次 + 新变量继续推演(默认 6 轮, 上限 10), 新报告继续
  写回 issue, 可无限循环。issue 正文写「推演N轮」即按 N 轮执行(服务端解析)。
- **推演过程实时可见**(对齐 SocialSim): 面板 4-tab——实时(轮次时间线 + 每轮
  4 视角投票/共识/分歧面板 + 概率/置信轨迹图 + 停止按钮)、报告、历史·回放
  (播放器: 播放/暂停/倍速/步进/滑杆)、追问(persona 聊天)。推演中途刷新
  页面会自动重发现 running run 并续上。
- ** council 每轮全开**: 策略/经济/自然/怀疑 4 视角并发投票, Brier 加权软
  投票出 consensus 作 headline, spread≥0.30 打分歧旗标; 每轮同时保留
  oracle base 概率供对比。

## 工程

- **异步 run 架构**: run 行开跑前创建(status='running') → detached goroutine
  逐轮执行+落库 → 内存总线 fan-out → SSE(snapshot 先放历史再接直播, round 帧
  按 index 幂等); 取消端点 + >15min 僵尸 running 清扫。migration 290:
  `pythia_forecast_run` + parent_run_id/run_kind/variables/status/report。
- **引擎 v2**: `/forecast/issue` 收 history/variables/council/total_rounds;
  新增 `/forecast/issue/report` 结题报告合成; `swarm.deliberate_issue()`
  4 persona 并发投票。vendor 改动, bundle-cli 时刷进 resources/。
- **面板重写**: 旧 PythiaPanel/PythiaFrames 删除, 新 4-tab 面板 +
  `use-pythia-issue-lab.ts`(5s live/60s idle 轮询); 触发器返回 run_id,
  新 start best-effort 取消同 issue 上一 run。
- 测试: 引擎 import 冒烟、Go 单测+DB 端到端(start→逐轮落库→评论写回/
  续推校验/SSE 快照)、hook reducer+SSE 解析 8 测; typecheck 6/6、lint 8/8、
  pnpm test 8/8、go test ./internal/... ./pkg/agent/... 全绿。

## Ship 结果

(待补)
