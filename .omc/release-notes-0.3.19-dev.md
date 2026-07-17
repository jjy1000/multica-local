# Multica 0.3.19-dev — 实验平台:Claude Science Runtime + LLM Wiki Bridge

## TL;DR

0.3.19-dev 在 0.3.18 Labs 安全网之上落地两个新的**完全独立**的实验性功能,每个自带 Labs flag,默认关闭,LLM 全部走 Multica provider,完全本地化(不打包,等用户决定 ship 时机):

1. **`claude_science_runtime`** — 让 claude_science 智能体能在隔离沙箱跑 Python,捕获 stdout,把 PNG/SVG/HTML/JSON/CSV/MD 产物渲染到工作台面板上
2. **`llm_wiki_bridge`** — 让 Multica 智能体桥接到 `/Applications/LLM Wiki.app`:读走桌面 API (vector search / file read / graph),写直接落本地 vault 目录由用户手动向量化

两个 flag 都满足 0.3.6 hard rule #1:**flag=off 完全 bypass**,`experimental.DefaultFor(key)` 单一 chokepoint 通过 0.3.18 安全网联动。

## 数据流

```
用户开启 flag → Settings → Labs
   ↓
catalog.DefaultFor(key) → true → register routes + IPC + 渲染 entry
   ↓
skill adapter 通过 CLI/HTTP 调后端
   ↓
handler 写入 experiment_*_session 表 → status / artifacts
   ↓
前端 artifact viewer (Render) → 缓存 / 5min TTL → 渲染 PNG/SVG/HTML
   ↓
后台 GC goroutine (每 6h) → expires_at < now() → archive/<YYYY-MM>/<uuid>/

LLM Wiki:
   ↓
读了:http://127.0.0.1:19828/api/v1/{health,projects,files,read,search,graph}
   ↓
写了:直落 /Users/jiangjianyan/Documents/llm wiki/<vault_path> → Sources Rescan 触发向量化
```

## 改动清单(只新增 + 必要编辑)

### 后端 Go
- `server/internal/experimental/catalog.go` — 新增 2 个 Flag literal(已被 linter 重构)
- `server/migrations/151_experimental_claude_runtime.up.sql` / `.down.sql` — forward-only
- `server/pkg/db/queries/experimental_claude_runtime.sql` — 9 个新查询
- `server/pkg/db/generated/experimental_claude_runtime.sql.go` — sqlc 生成
- `server/internal/handler/claude_science_runtime.go` — 新增 6 端点
- `server/internal/handler/llm_wiki_bridge.go` — 新增 8 端点
- `server/internal/handler/handler.go` — 加 2 个字段 + llmwiki import
- `server/internal/llmwiki/client.go` + `writer.go` — 直连桌面 API + 文件 vault writer
- `server/internal/experimental/runtime_gc.go` — 后台 GC,30/90/120 天梯
- `server/internal/experimental/experiments/{claude_science_runtime,llm_wiki_bridge}/manifest.json` — placeholder 给 0.3.19 P1
- `server/cmd/server/router.go` — 接 2 个 `Register*Routes` in flag-gated if blocks
- `server/cmd/multica/cmd_experimental.go` — 11 个子命令 CLI dispatcher

### 前端 TS
- `apps/desktop/src/renderer/src/components/experimental-artifact-view.tsx` — flag-gated artifact viewer
- `apps/desktop/src/renderer/src/pages/claude-science-view.tsx` — 顶部接入

### Skill
- `server/internal/service/builtin_skills/multica-claude-science-runtime/SKILL.md`
- `server/internal/service/builtin_skills/multica-llm-wiki/SKILL.md`

## 关键决策

| 决策 | 原因 |
|---|---|
| runtime 用系统 python3,不打 bundled venv | 用户需求:本地化、最小变更;bundled venv +60MB DMG,等 ship 决定 |
| `python3 -I`(isolated mode)| 等同 pythia pre-flight,启动前探测 |
| LLM Wiki 走 HTTP 不是 stdio MCP | mcp-go 不必要;LLM Wiki MCP 内部就是这层 HTTP 转发 |
| LLM Wiki 写走文件系统而非 desktop API | desktop API 不暴露 write 端点;user 反正要手动向量化 |
| 0.3.18 safety net 不动 | panic/5xx burst/init timeout 三个 recover 都已串联两个 flag |

## 验证(都已跑通)

```bash
cd /Users/jiangjianyan/jjy/multica-main

# TypeScript
pnpm typecheck                                # 6/6 PASS

# Go
go build ./...                                # 0 error
go vet ./...                                  # 0 error
go test -race -count=1 ./internal/llmwiki/ ./internal/experimental/  # ok

# CLI dispatcher (无需 server 启动)
go build -o /tmp/multica-new ./cmd/multica/
/tmp/multica-new experimental --help           # 列出所有子命令
/tmp/multica-new experimental claude-science-runtime --help
/tmp/multica-new experimental llm-wiki --help
```

## 端到端 smoke(任务 #16 待跑 — 需 server 在 5432 + 8090 跑起来)

不打包,所以**不需要**走 DMG 流程(避开了 0.3.10 ENOENT + Electron 39 NSAlert + dmg-builder hang 三道老坑)。

## 已知限制 / 后续 PR

- LLM Wiki 写没有"删除"端点 — desktop API 不暴露,503-style 错误响应
- runtime GC 的 tar.gz 是 placeholder,真实 streaming tar.gz 在后续 milestone(memory: next-code-environment-library)
- catalog `ManifestPath` 字段对两个新 flag 是占位 JSON,真正结构化 manifest 等 0.3.19 labs 蓝图 P1 落地
- 实验 PDF / Excel / PPTX 没在 artifact kind 里 — 需要时加 migration + sqlc

## 不在 0.3.19-dev 范围

- 不打包(用户要求)
- 不改 0.3.18 safety net(单独 ship 周期)
- 不改 catalog 既有 flag
- 不引 mcp-go 依赖
- 不动 PostHog / Lark / cloud 任何代码
