---
name: release-notes-0.3.60
created: 2026-07-22T15:30:00Z
updated: 2026-07-23T01:25:00Z
status: complete
---

# Multica 0.3.60 — 用户插件运行时闭环 + 容器式环境 + 内置数据库

用户自建实验室插件（`user_*`）从"能创建"补齐到"能运行、能产出、能渲染、能存状态"。
核心是给每个实验室一个**按需执行、无 Docker、可持久化的容器式环境**，并保证三样地基：
**数据库、交互性、渲染**。具体实验室装什么/怎么跑/怎么测由用户在 Multica 内自行完成，
平台只负责这三样基础能力。

## Changes

### 1. 执行运行时 — `server/internal/handler/user_plugin_runtime.go`（新增）
- `POST /api/user-plugins/{slug}/run` 关闭运行闭环，按 `runtime_kind` 列分派：
  - `inline` → 运行 `python3 -I entry.py`（镜像 `claude_science_runtime.go`）
  - `none` → 400；`subprocess` → 501（预留升级位）；未知 → 400
  - 缺插件 → 404；非 `active` → 409
- 复用同包函数，**不重复定义**：`probePython3` / `kindFromName` / `mimeForKind` /
  产物 index 助手 / 常量（64 KiB 代码、30s 默认 / 120s 上限超时）。
- 代码来源优先级：请求体 `code` → `manifest.runtime.entry_code` → 既有 `env/entry.py`。
- 运行历史：`~/.multica/plugins/<slug>/runs.json`（原子 tmp+rename，保留最近 50 条）。
- `manifest.runtime = {kind, entry_code?, timeout_ms?}` 为纯附加约定；`runtime_kind` 列仍是权威源。

### 2. 容器式环境（按需、无 Docker）— 同文件
- 每插件持久环境 `~/.multica/plugins/<slug>/env/`：进程跑完即退（非常驻容器），
  但目录持久，给实验室私有有状态工作区。Multica 独立安装包，**无外挂 Docker 依赖**。
- 进程注入平台契约环境变量（`pluginRuntimeEnv`）：
  - `MULTICA_PLUGIN_SLUG` / `MULTICA_PLUGIN_ENV`(=cwd) / `MULTICA_PLUGIN_DB`(=`env/data.db`)
- **数据库** = Python 内置 `sqlite3` 打开 `MULTICA_PLUGIN_DB`：零安装、按插件隔离、
  跨运行累积状态（如"按键图谱"先存后渲染的场景）。
- `env/` 同时是未来 `subprocess`/真容器升级的挂载点，平滑演进。

### 3. 产物 / 私有数据分离 — `isIngestableName`（新增）
- 运行后按 mtime diff 采集变更文件为产物：png/svg → image、html → html、其余 → file。
- **排除清单**（永不摄取为产物）：`entry.py`、`data.db` 及 `-wal`/`-shm`/`-journal`
  sidecar、`.sqlite` / `.sqlite3` 及其 sidecar、`.pyc`、隐藏文件。
- 效果：数据库等私有持久状态不会每次 run 都污染面板"产物"Tab，只有真实交付物出现。

### 4. 交互性与渲染（已具备，本版明确保证）
- HTML 产物在面板 `sandbox="allow-scripts"` iframe 内渲染，可跑内嵌 JS 实现交互。
- 前端产物渲染器支持 7 类：image / chart / table / html / code / text / file；
  运行时自动摄取只产 image / html / file，`chart`/`table` 内联数据类型仍走 `POST /artifacts` 上传。

### 5. 路由 — `server/cmd/server/router.go`
- 认证块内注册 `r.Post("/api/user-plugins/{slug}/run", h.RunUserPlugin)`，与 artifacts 路由同组。

### 6. 测试 — `server/internal/handler/user_plugin_runtime_test.go`（新增）
- `TestUserPluginRuntime_IngestEmittedArtifacts` — 产物摄取与类型映射。
- `TestUserPluginRuntime_RunHistory` — runs.json 原子写 + 50 条上限。
- `TestUserPluginRuntime_TypeMapping` — 扩展名 → 类型映射。
- `TestUserPluginRuntime_DatabasePersistsNotIngested` — 跑两次证明 SQLite 跨运行累积
  （`runs=2`）、每次仅 `index.html` 成产物、`data.db` 不被摄取。
- `TestUserPluginRuntime_IngestExcludesPrivateData` — 钉死排除清单。
- 全 handler 包 `go test` 通过；`gofmt` clean；`go vet` clean。

### 7. 文档 / 技能
- `CLAUDE.md` — 新增"容器式环境（按需、无 Docker）"段落（env 持久化、环境变量、SQLite、排除清单、iframe 交互）。
- `server/internal/service/builtin_skills/multica-lab-builder/SKILL.md` — Step 7 补充 DB 与环境变量说明。
- `.../multica-lab-builder/references/runtime-example.md` — 新增"有状态实验室 — 内置 SQLite 数据库"示例（按键图谱模式）。

## 已知边界 / 未包含
- `subprocess` 真容器隔离仍为 501 预留升级位（`env/` 已作为未来挂载点）。
- 仅摄取 `env/` 顶层变更文件（子目录不递归）。
- `inline` 运行 `python3 -I` 无文件系统沙箱（与既有 claude 运行时同构；沙箱/隔离随 subprocess 升级引入）。
- 产物 index 并发写存在与既有 upload 端点相同的竞态（非本版引入）。
- 智能体"创建实验室"为描述软路由（`[实验室创建]` 提示 + 技能 description 匹配），无服务端硬注入 —— 按用户要求保持不强制。

## Ship 前置（已完成 2026-07-23）

`apps/desktop/package.json` → `0.3.60`；`/Applications/Multica.app` 替换为 0.3.60；
cold-start 三检查（5432+8090 /health）+ row parity（1/222/1354/92）通过。详见
`.omc/0.3.60-ship-2026-07-23.md`。
