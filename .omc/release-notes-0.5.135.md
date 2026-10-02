# 0.5.135 (2026-10-03) — 星图补上未绑定全图视图 + Pythia 监控页议会 hero(截图反馈第二批)

一个 FE-only 修复提交(`48ede8457`),零迁移零 wire 零 Go。用户第二批截图:①因果决策**未绑定 issue 的默认路由**(全图力导向视图,100 节点)没有星图——0.5.132 只把星图挂进了**绑定路由的 FocusedGraph**,默认路由漏了;②Pythia 监控页只有运行列表,没有议会。

## 修复

- **causal WorkspaceGraph**:星图 hero 补挂到未绑定全图视图标题行上方(`visibleNodes`/`edges`,焦点跟随选中,默认最高度数节点)——现在无论是否绑定 issue,进页面第一眼都是星图。
- **pythia 监控页**:健康条与运行列表之间新增**议会 hero**。监控列表按设计不带 envelopes(PythiaMonitorRun),故新查一个查询走 issue-runs 端点取**最新一条 run 的 envelopes**(同一宽松 schema);无 run=固定名单待命席,fetch 失败降级为同一待命视图(装饰性 hero)。`useQuery` 位于早退 return 之前(rules-of-hooks,lint 抓的)。
- pythia-view 测试:单查询钉子升级为**双查询契约**(按 queryKey 找 monitor/chamber),新增 chamber 空列表不 fetch 断言。

## 门禁

`pnpm typecheck` 6/6;`pnpm lint` 8/8;desktop vitest **382 passed**。
