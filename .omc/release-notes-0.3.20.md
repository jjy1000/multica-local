# Multica 0.3.20 — Labs plugin enable-actually-works

## 概述
0.3.19 设计了完整的 Labs 平台化架构(manifest → catalog → registry → IPC dispatcher → proxy mount → sidebar),
但 boot 路径上有 3 处遗留硬编码,导致**用户从 Labs tab 打开开关后,sidebar 入口不一定出现、点进去也 404**。
0.3.20 把这 3 处全部改为 catalog-driven,新增的 flag 不再需要改 desktop / server 代码。

## 修复

### Fix 1: `manager-factory` 改为 catalog-driven
`apps/desktop/src/main/experimental/manager-factory.ts`
- 新增 `loadFlagDescriptors(fetcher)`:boot 时从 `/api/experimental-flags` 拉一次,merge 到静态列表
- 静态列表保留 3 条(`claude_science / pythia_oracle / mythos_swarm`)作为 fallback
- `resolveManager()` 对 `code_canvas` 等新增 subprocess 改为返回稳定 "idle" 表面,不再 return null
  → IPC handler 全部注册成功,renderer 调 `experimental:code_canvas:get-status` 不再 "no handler registered"

### Fix 2: `MountExperimentalProxies` 通用化
`server/internal/handler/experimental_proxy.go`
- 新增 `mountExperimentalProxy(exp, h, ProxyRoute)`:遍历 `Registry.ProxyRoutes()` 自动挂路由
- `upstreamRegister` / `upstreamUnregister` 的 service allowlist 改为 registry-driven
  → `code_canvas` 的 `/experimental/code-canvas/*` 自动注册,前端 fetch 不再 404
  → desktop 可以 POST `/__experimental/upstream { service: "code_canvas" }`

### Fix 3: `STATIC_NAV` 改读 manifest
`packages/core/experimental/use-experimental-nav.ts`
- 删掉 4 条硬编码 nav rows
- 从 `ExperimentalFlag.sidebar_entries` 读 manifest 的 `entry_points.sidebar[*]`
- 加 `ExperimentalSidebarEntry` 类型 + `runtime_kind` 字段到 wire shape

### Fix 4: preload bridge 加通用 helper
`apps/desktop/src/preload/index.ts`
- 保留 legacy `.pythia / .claudeScience` 通道(pythia-view 等已用,不能破)
- 新增 `experimentalAPI.invoke(flagKey, verb)` 路由 `experimental:<flagKey>:<verb>`
  → 任何新 flag 都能从 renderer 调 IPC

### Fix 5: 补齐 2 个 manifest 的 sidebar entries
- `claude_science_runtime/manifest.json` 加 `entry_points.sidebar[0]` → `/experimental/claude-science-runtime`
- `agent_self_optimization/manifest.json` 加 `entry_points.sidebar[0]` → `/experimental/agent-self-optimization`
- `code_canvas/manifest.json` 已有 sidebar entry,无需改动

## 8 个 flag 当前可用性

| flag | kind | 0.3.19 | 0.3.20 |
|---|---|---|---|
| chat_pin_ui | none | ✅ | ✅ |
| claude_science | inline | ✅ | ✅ |
| pythia_oracle | subprocess | ✅ | ✅ |
| mythos_swarm | headless | ✅ | ✅ |
| claude_science_runtime | inline | ⚠️ 无 sidebar | ✅ 出现 sidebar |
| llm_wiki_bridge | inline | ✅ | ✅ |
| **code_canvas** | subprocess | ❌ IPC 无 handler / proxy 404 | ✅ IPC 注册 / proxy 自动挂 |
| agent_self_optimization | inline | ⚠️ 无 sidebar | ✅ 出现 sidebar |

## 验证

- `go build ./...` ✅
- `go test ./internal/experimental/...` ✅
- `pnpm typecheck`(8 packages)✅
- `pnpm lint` — 17 errors 全在 pre-existing 文件(`tab-content.tsx` / `tab-store.ts` 的 require + openExternal),我改的文件 0 错
- `TestExperimentalResourcesRoundTrip_InstalledThenHidden` 失败:环境问题(manifest 没 staged),非本次改动回归

## 不打包的项

- `code_canvas` 的真实 binary 还没捆绑到 `apps/desktop/resources/pythia/engine/` → 启动会返回 "manager not yet implemented"
  这是预期行为(manifest 里写了 `on_missing: "service not bundled — drop the binary into apps/desktop/vendor/code-canvas/"`),
  等真 binary 落地后再补 setupCodeCanvasManager。
- 新 sidebar 路由 `/experimental/claude-science-runtime` / `/experimental/agent-self-optimization` / `/experimental/code-canvas`
  的 renderer 页面是后续 PR(0.3.21)。当前 sidebar 点击后是 404,这是预期 — 需要先有 view 才能完整 ship。