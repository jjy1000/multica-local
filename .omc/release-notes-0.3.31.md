
# 0.3.31 — Mythos Swarm 双模式 + Enhancer 监督

## 一句话

`mythos_swarm` flag 升级为双模式。独立模式(sole)保持 0.3.30 的单 agent 自闭环行为；新增增强模式(enhancer)让 Mythos 蜂群先开会规划、再把你选的 agent/squad 推到执行位、并 30s 轮询监督完成性。

## 变更统计

- 15 个后端文件(6 新 9 改) + 5 个前端文件 + 4 语言 i18n
- ~1800 LOC 增量
- 8 个新 sqlc 查询 + 1 个 migration(157)

## 新增功能

### Mythos 双模式(LabPicker)

在 issue detail 的 Lab picker 里选 `mythos_swarm` 后,展开 2 个 tab:

- **蜂群独立(sole)** — 吞掉 assignee,mythos 5-agent RDT runner 跑完即结束
- **蜂群增强(enhancer)** — 保留你的 assignee,蜂群先 prelude 规划 → loop 收敛 → coda 综合,完成后切 `supervising` 状态,后台 30s 轮询追踪 assignee 执行进度

### 增强模式监督

- coda 结束后自动启动 `supervise goroutine`,写入 `mythos_run.supervision_state`
- 30s ticker + 24h max-lifetime 兜底
- IssueLabsSection 新增 supervise 面板：phase 进度条 + 子任务进度 + 最近 reflection + "立即检查"按钮
- `POST /api/experimental/mythos-swarm/supervise/{runID}/tick` 触发即时评估

### daemon 重启恢复

daemon 启动时扫描 `mythos_run.status='supervising'`,自动恢复所有未完成的 enhancer-mode supervise goroutine

### 蜂群不可见(独立功能)

`mythos_swarm` 关闭时:5 个 mythos_* agent + Mythos Swarm squad 从 workspace 的 agent/squad picker 中完全隐藏。`experimental_resource_visibility` 表新支持 `squad` 类型,install handler 会在安装时自动 seed 隐藏行。

## 补完的 mig 156 半成品

| 字段 | 0.3.29 状态 | 0.3.31 |
|---|---|---|
| `mythos_run.extension_agent_ids` | JSONB 列,runner 不读 | runner 读入 effective loop pool,轮转迭代 |
| `mythos_run.self_optimization_enabled` | BOOLEAN 列,不写 | runner 写 + coda 后 reflection 批量写 |
| `mythos_members.reflection` | TEXT 列,无人写 | loop 每轮迭代后写入,supervise panel 渲染 |
| `mythos_members.reflection_iter` | 不存在 | 新加 INT 列,同批写入 |

## 数据库变更(157)

```sql
ALTER TABLE issue ADD COLUMN lab_mode TEXT CHECK ...;  -- 'sole'|'enhancer'
ALTER TABLE mythos_run ADD COLUMN mode TEXT NOT NULL DEFAULT 'sole' ...;
ALTER TABLE mythos_run ADD COLUMN target_assignee JSONB;
ALTER TABLE mythos_run ADD COLUMN supervision_state JSONB DEFAULT '{}'::jsonb;
-- mythos_run.status CHECK 扩 'supervising'
ALTER TABLE mythos_members ADD COLUMN reflection_iter INT;
-- experimental_resource_visibility CHECK 扩 'squad'
```

## 关键约束保持

- ❌ 不动 `runtime.go` LLM 调用路径(监督走 issue 事件流)
- ❌ flag-off 完全 bypass(supervise goroutine 在 flag 关闭时被 Stop 取消)
- ❌ 不重命名 `mythos_swarm` flag key
- ❌ 不引入 OpenMythos PyPI 包(该包不是 agent 框架)
- ❌ 不在 workspace 里注册 agent/squad 到普通 picker

## 测试

- `go test -race ./internal/service/mythos/...` PASS(6 新测试)
- `go test -race ./internal/experimental/...` PASS
- `go test -race ./internal/handler/` PASS(所有改动相关测试)
- `go vet ./...` 0 警告

## 部署

1. `cd server && go run ./cmd/migrate up`(mig 157 前向)
2. `pnpm --filter @multica/desktop bundle-cli`(bake-in 新 mig)
3. `pnpm --filter @multica/desktop build`
4. cp .app → cold start 5432+8090 + /health OK
