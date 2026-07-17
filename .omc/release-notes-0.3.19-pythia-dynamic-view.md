# Multica 0.3.19 — Pythia 动态画面

## TL;DR

0.3.18 已经把 Pythia 引擎接进 Multica Labs(`pythia_oracle` flag + pythia-manager spawn + `/api/runtime/llm-call` 路由 LLM 调用 + `multica-pythia` Skill),但 renderer 那一侧只有 126 行的**信息壳**(loopback URL + CLI 用法),完全没渲染 Pythia 的可视化层。

0.3.19 补齐这块:**SVG 世界球 + 实时 SSE + 反事实面板**——四件 Phase,全部 flag-gated,不打包,代码层面 ship-ready。

| Phase | 改动 | 验证 |
|---|---|---|
| 1 | SVG 球 + 7 大陆轮廓 + graticule + 预测环 + pulse 动画 + sample data | typecheck/lint 0 错 |
| 2 | SSE 客户端(`/state/stream`,fetch+ReadableStream,自动 2s 重连)+ 头部 badge 显示连接状态 | 27 个单测全过 |
| 3 | IPC 代理 `pythia:proxy`(allowlist + 30/min 限流 + 503/502/504 错误码)+ WhatIf 面板 | 27 个单测全过 |
| Polish | All/None persona / Cmd⏎ 提交 / busy spinner / FreshnessTimestamp / WhatIf 历史 / 断线横幅 / persona 配色 toggle / selected 双层 halo / 错误码具体化(BINARY_NOT_BUNDLED/ENOENT/EADDRINUSE) | typecheck/lint 0 错 |

## 数据流

```
Labs flag "pythia_oracle" ON
    │
    ▼
pythia-view.tsx (lazy)
    │
    ├─ ensureUp() ───► IPC ───► pythia-manager.start() ───► uvicorn :8088
    │
    ├─ PythiaDashboard
    │    ├─ getURL() ───► IPC ───► loopback URL
    │    ├─ usePythiaSse(url, enabled)
    │    │    └─ fetch url+"/state/stream"
    │    │       └─ ReadableStream
    │    │          ├─ snapshot   → reducer { snapshot, connection: "open" }
    │    │          ├─ predictions → reducer merge
    │    │          ├─ world/run/deliberation → lastEventKind (Phase 2+)
    │    │          └─ : ping     → ignored
    │    │       └─ reconnect 2s on error/closed
    │    │
    │    └─ WhatIfPanel
    │         └─ submit ─► IPC pythia:proxy
    │              └─ main: allowlist /whatif /chat /predict
    │                 └─ 30/min rate limit (renderer identity)
    │                 └─ manager.start() on demand
    │                 └─ fetch manager.url()+path, 30s timeout
    │                    └─ { ok, status, body: { scenario, narrative, predictions[] } }
    │
    └─ StreamInterruptedBanner (live + connection≠open)
```

## 关键文件

```
新增
apps/desktop/src/renderer/src/components/pythia/
  ├── types.ts                  70 行  Prediction / AgentVote / WorldBrief 契约
  ├── sample-predictions.ts    105 行  5 条覆盖 4 horizons + 2 split
  ├── globe-svg.tsx            220 行  equirectangular SVG + 7 大陆 + pulse
  ├── swarm-vote-bars.tsx       95 行  4 persona 柱状图
  ├── pythia-dashboard.tsx     360 行  装配 + 5 个 sub-component
  ├── whatif-panel.tsx         210 行  textarea + persona pills + busy + IPC
  ├── use-pythia-sse.ts        180 行  fetch+ReadableStream SSE hook
  └── *.test.ts                230 行  27 个测试覆盖 4 个纯函数

改造
apps/desktop/src/renderer/src/pages/pythia-view.tsx
apps/desktop/src/renderer/src/globals.css           (pythia-pulse-ring keyframes)
apps/desktop/src/main/pythia-manager.ts              (setupPythiaProxyIPC + managerBootHint)
apps/desktop/src/main/index.ts                      (注册 setupPythiaProxyIPC)
apps/desktop/src/preload/index.ts                   (experimentalAPI.pythia.proxy)
apps/desktop/src/preload/index.d.ts                 (类型)

未打包(等 0.3.19 batch 一起 ship)。
```

## 边界 / 已知限制

1. **Osiris 未启**:Pythia `/world` 端点 404 → oracle 在空 brief 上推理 → `/whatif` 可用但 narrative 空泛 + 预测无依据。0.3.19 当前**不**解决这件事(需要 vendor mock Osiris 子集,工作量 ~1 周),仅在 UI 上明确告知"manager up · no runs yet"。
2. **flag-off bypass**:dashboard + whatif-panel + globe-svg 全部通过 `lazy()` 动态 import。flag-off 时不在初始 chunk 中,严格符合 0.3.6 Labs 框架约束。
3. **globe viewBox 硬编码** 720×360 + equirectangular 投影在两极有拉伸。可接受 — 状态面而非导航面。如需切换墨卡托或 globe.gl,后续 PR 处理。
4. **CSS keyframes**(`pythia-pulse-ring`) + `prefers-reduced-motion` 关停,无第三方动画库。
5. **MapLibre / 3D tile 故意未引入**:Electron 39 NSAlert 根因(0.2.89.4/0.2.90),保持 SVG 路线。

## 验证

```bash
# 单元测试
pnpm --filter @multica/desktop exec vitest run \
  src/main/pythia-manager.test.ts \
  src/renderer/src/components/pythia

# → Test Files  4 passed (4)
#   Tests       27 passed (27)

# 类型检查
pnpm --filter @multica/desktop run typecheck
# → 0 errors (node + web)

# Lint(pythia/* 新增)
pnpm --filter @multica/desktop run lint 2>&1 | grep -E 'pythia/'
# → (空)
```

## 可选后续(不在本 ship)

- **Phase 4** vendor mock Osiris 子集(GDELT / USGS / NWS alerts),让 oracle 跑在真 brief 上 → 预测质量显著提升。
- **WhatIf panel** 替换成 react-query mutation(乐观更新 + rollback),目前 fire-and-forget。
- **Persona chat**:Phase 3 已允许 `/chat`,但 dashboard 没暴露 speaker dropdown。
- **World brief banner**:SSE `world` 事件已路由到 `lastEventKind`,渲染层还没接。
- **DMG ship**:等 0.3.19 batch + main app 一起打包,前置依赖 `pre-update-snapshot.sh` 通过。

## 不打包理由

- 其他 agent 在开发共享代码(根据用户指示,本 session 不打包)。
- 代码层面 typecheck/lint/单测全过;ship 时只需 `pnpm --filter @multica/desktop package` 一次,跟其他 0.3.19 改动合并发布。