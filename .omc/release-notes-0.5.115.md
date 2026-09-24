# 0.5.115 (2026-09-24)

主题:pythia monitor 400 hotfix — 实验室监控台(/experimental/pythia)恢复可用。单 commit,无迁移。

## 根因(三层)
- monitor 路由 `GET /api/experimental/pythia-oracle/forecast/monitor` 注册在**无 workspace 中间件**的 authed 路由组,`ctxWorkspaceID` 永远空。
- 0.5.113 的修复 fallback 读 `X-Workspace-ID` 头,但 `api.rawRequest` 的 authHeaders 只发 `X-Workspace-Slug`,从不发该头(它只在 mat_ 任务 token 路径由 auth 中间件写入)→ fallback 永远落空。
- 服务端日志全史:该端点 4 次请求全部 400、零 200。0.5.112 客户端静默吞错(页面空白),0.5.113 起错误抛到 runsQuery.isError 才变成用户可见横幅。

## 修复
- 服务端 `pythiaForecastMonitor`:显式 `workspace_id` 查询参数优先(镜像 GetClaudeLabContext / claude artifacts 端点契约),ctx → header 保留为降级 fallback;`h.workspaceMember` membership 闸前置到 stale sweep + listing 之前(调用方提供的 UUID 先验证再动 DB)。
- 客户端 `pythia-view.tsx`:请求 URL 显式携带 `workspace_id`(页面本就持有 getCurrentWsId(),enabled 闸保证非空)。
- 注释纠偏:handler 注释不再宣称 rawRequest 发送 X-Workspace-ID。

## 钉子
- `TestPythiaForecastMonitorWorkspaceResolution`(HTTP 层首次覆盖 monitor):query param 200 / 双源空 400 / 非 UUID 400 / header 单独可用。注意 newRequest 夹具自带 X-Workspace-ID 头,双源空用例需显式剥离。
- `pythia-view.test.tsx` ×2:URL 携带 workspace_id;空 wsId 时 query disabled。
- 回归:go test -run TestPythia 全绿;desktop vitest 2/2;gofmt/vet/build OK。
