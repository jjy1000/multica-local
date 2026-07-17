# 0.3.30.1 — Labs rawRequest 切换 + 路由 gate 重构

**Date**: 2026-07-16
**Build**: `/Applications/Multica.app` 0.3.30.1(替换 0.3.30)
**Pre-update snapshot**: `pre-update-20260716-194444`(已归档于 `~/.multica/backups/`)

---

## 改动范围(4 文件)

### Server

- **`server/cmd/server/router.go`** — Labs 路由门控方式统一从 `experimental.DefaultFor(...)` 包级判断,改为 `RequireExperimentalFlag(key)` 中间件 + `r.Group(...)`。涉及 4 个 flag:
  - `claude_science_lab`(`RegisterClaudeScienceRuntimeRoutes` + `RegisterClaudeLabForecastRoutes`)
  - `llm_wiki_bridge`(`RegisterLLMWikiBridgeRoutes`)
  - `mythos_swarm`(`/api/experimental/mythos-swarm/run`)
  - `pythia_oracle`(`RegisterPythiaIssueForecastRoutes`,从 0.3.29 死代码复活)

  net diff: 删 ~60 行旧 gate,加 ~30 行新 group。

  **好处**:
  - 行为一致:所有 lab 路由统一走 `RequireExperimentalFlag` 中间件(flag off → 404)
  - `DefaultFor` chokepoint + blacklist 读取路径不变(中间件内部仍读 `DefaultFor`)
  - 中间件级 trace 注入点统一

### Frontend(渲染端)

所有 Labs 视图/组件的网络调用从裸 `fetch()` 切到 `api.rawRequest()`,触发原因为:
- 桌面 packaged 时 renderer origin = `file://`,后端在 `localhost:8090`,裸 `/api/...` 永远到不了
- 桌面 token mode 没 cookie,`credentials: "include"` 失效
- `rawRequest` 注入 `baseUrl` + `authHeaders()`,且不 throw-on-error / decode,保留 SSE / 204 处理

| 文件 | 替换数 |
|---|---|
| `apps/desktop/src/renderer/src/pages/claude-lab-view.tsx` | 6(`fetch → rawRequest`) |
| `apps/desktop/src/renderer/src/pages/mythos-view.tsx` | 1(`/api/experimental/mythos-swarm/run`) |
| `apps/desktop/src/renderer/src/components/pythia/pythia-report-surface.tsx` | 1(`/api/experimental/pythia-oracle/forecast/issue`) |

**例外**:`apps/desktop/src/renderer/src/components/pythia/use-pythia-sse.ts:149` 仍用裸 `fetch()` — 其 URL 来自 `window.experimentalAPI.pythia.getURL()` 指向 loopback Python 引擎(`http://127.0.0.1:<port>`),不能被 `rawRequest` 的 `baseUrl` 前缀污染。已在文件加注释,CLAUDE.md §"Experimental tab network calls (0.3.30)" 显式标记为例外。

**未改动但已正确**:`llm-wiki-bridge-view.tsx:45` 已是 `rawRequest`(此前已切换);`pythia-view.tsx`、`code-canvas-view.tsx`、`agent-self-optimization-view.tsx`、`constitution-agent-view.tsx` 无网络调用;`experimental-artifact-view.tsx` 6 处 `rawRequest` + 注释说明 native resource (`<img>`/`<iframe>`/`<a download>`) 限制是 follow-up。

### CLAUDE.md

- §"Experimental tab network calls (0.3.30)" 新增章节(L430-442):阐述 origin + auth 两层失败原因,列举已转换的 6 个 tab + 2 个例外(`use-pythia-sse` + native resource)
- §"Adding a new experiment" 第 5 步更新(L452):新增约束"Any network call from that view must use `api.rawRequest`, never a bare `fetch`"

---

## Cold start 验证(待 ship 后填)

- [ ] `lsof -nP -iTCP:5432 -sTCP:LISTEN` 6s 内 LISTEN
- [ ] `lsof -nP -iTCP:8090 -sTCP:LISTEN` 6s 内 LISTEN
- [ ] `curl http://localhost:8090/health` → `{"status":"ok"}`
- [ ] row parity:`workspace=1 / issue≈170 / comment≈977 / agent=85`(基线 0.3.29)
- [ ] Labs 路由 smoke:开启 `claude_science_lab`,访问 `/experimental/claude-lab`;关闭后 404

---

## 已知未修复项(本次 ship 不涉及)

- `llm_wiki_bridge` catalog `RuntimeKind` split(catalog `inline` vs manifest/desktop `subprocess`)
- 8 个 manifest 缺 `installable` + `resources` 字段
- `agent.go:602` 用 `AllFlagKeys()` 遍历 HideAgent,但 `autopilot.go` / `skill.go` 仍硬编
- `lab_section.*` 8 keys + `lab.picker_none` 在 4 个 locales `issues.json` 缺失
- `settings.json` 缺 `broken_flags` / `labs.toast_failed`
- CLI 无 `experimental install|rollback|gc` 子命令(CLAUDE.md 提及但未实现)
- `SourceClaudeScience` 是否窄化为 `LegacySources`
- `vendor/pythia-src/runs/ledger.jsonl` 被打包到 DMG
- `__pycache__/`(3.12 + 3.14)随 engine 递归 cp 到 resources
- bundle-cli fixture manifest(旧 `schema_version:1`)应归档到 testdata
- 实验视图 native resource load (`<img src>` / `<iframe src>` / `<a href download>`) 不能携带 Bearer,需要 blob objectURL 或签名 URL