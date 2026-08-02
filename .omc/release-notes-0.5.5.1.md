# 0.5.5.1 Release Notes — 智能体自进化循环彻底脱离实验域

**日期**: 2026-08-02
**版本**: 0.5.5.1

## 改动:self-opt 完全产品化

0.5.5 阶段把 `agent_self_optimization` 的 **install 路径**移到了 server boot(leader agent 已经在 boot provision),但**控制面仍在 Labs tab**:
- catalog `DefaultVal=false`(用户要手动 enable)
- self-opt service `flagOnForUser` 读 `experimental_pref` 行作为调度 gate
- 9 个 HTTP handler(self-opt edits + agent trust)在 flag off 时返回 404
- install handler 仍在 router 注册

**0.5.5.1** 进一步:**控制面完全移到 autopilot 自身的 `enabled` 字段**。

## 用户视角

| 之前 | 0.5.5.1 |
|---|---|
| Labs tab 启用 `agent_self_optimization` | 总是启用(无 toggle) |
| `multica experimental install agent_self_optimization` 触发 install | server boot 自动 wire(0.3.45.1) |
| 修改 cadence 只能去 Labs tab toggle flag | `multica autopilot update --disabled` 改每条 autopilot |
| 关闭整个 self-opt:删除 pref row | 关闭每条 autopilot 单独控制(更细粒度) |

## 自动化工程控制

self-opt 现在由 2 条普通 autopilot 表达,跟其他 autopilot 平级:

| autopilot | cadence | user 关闭方式 |
|---|---|---|
| `SkillOpt-Multica · 每日 00:00 自进化循环` | 每日 00:00 UTC | `multica autopilot update <id> --disabled` |
| `智能体工程师团队 · 每3工作日批量优化` | 工作日 02:00 UTC | 同上 |

用户能在 `自动化工程` 页(autopilot list)直接看、改、删这两条 autopilot,**完全无需经过 Labs tab**。

## 内部改动

- `catalog.go`: `agent_self_optimization` DefaultVal `false → true`
- `flag.go`: `flagOnForUser` + `flagOnExperimental` 改为 stub 永远返回 true
- `service.go`: `tickerKey` 删 UserID 字段;`Start()` 不再调 `ListOptedInUsers`,直接对 `workspaceIDs` 起 ticker;`runScheduler` / `maybeFire` / `TriggerManualRun` 删 flag gate
- `self_opt_edits.go` + `agent_trust.go`: 9 个 `experimentalFlagEnabled` gate 用 `false &&` 短路(保留调用,改 `true` 即恢复 gate)
- `router.go`: `RegisterInstallHandler(agent_self_optimization)` 注释补"0.5.5.1 保留为 legacy compat"

## 验证

- `go build ./...`: 全过
- `go test ./internal/service/agent_self_optimization/`: 全过(0.404s,4 个 case 钉死 stub 契约)
- `pnpm typecheck`: 6/6 子包过
- `pnpm test packages/views/issues/components/pickers/`: 23/23 过
- 冷启动三查: Info.plist 0.5.5.1 = package.json 0.5.5.1、5432/8090 LISTEN、/health ok
- boot log: `agent-self-opt: scheduler started workspaces=1 tickers=1`(0.5.5 是 N×M,0.5.5.1 是 1×workspace)
- self-opt 2 条 autopilot `status=active`,用户可直接 `multica autopilot update` 改

## 已知 + 后续 0.5.6 待办

- 0.5.5.1 是 patch,无 schema 变更,无新 flag
- `ListOptedInUsers` sqlc query 仍生成但 unused —— 留作未来"per-user 暂停 self-opt"功能
- 后续 0.5.6:
  - catalog 完全移除两个 flag 条目
  - 删 `install_agent_*` handler
  - 删 `ListOptedInUsers` query
  - migration 236 清理 stale lock
  - 删 `RecentLabsPanel`(已不在 picker)
