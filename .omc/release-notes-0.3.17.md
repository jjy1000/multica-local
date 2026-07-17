# Multica 0.3.17 — 智能体自优化循环作为实验性功能

## TL;DR

0.3.17 把用户数据中已经存在的「智能体优化专家」智能体、其 2 条 autopilot(SkillOpt-Multica 每日 00:00 自进化循环 + 智能体工程师团队每3工作日批量优化)、以及 skillopt-multica 技能作为**实验性插件**抽出,默认 off,在 Labs 启用后才恢复显示与运行。

## 用户决策(原话)

> 修改它们智能体优化的,把智能体优化的功能抽离出来成为一个试验性功能。它本质上其实就是 multica 的机制可以做到的,只是我放到了实验室中启用或者停止,现在的智能体我组建了自动优化的,我打算给它们独立出来

→ 与 0.3.15 / 0.3.16 集成 Pythia / Mythos 的策略一致:**用 flag 控制可见性,数据保留**,符合 Labs 框架硬约束 + forward-only 数据安全。

## Catalog flag

```go
{
    Key: "agent_self_optimization",
    DefaultVal: false,
    Title: {En: "Agent Self-Optimization Loop", Zh: "智能体自优化循环"},
    Description: {
        En: "Exposes the 智能体优化专家 agent, its 2 autopilots (SkillOpt-Multica daily self-evolution loop + per-3-workday bulk optimization), and the skillopt-multica Skill as an opt-in plugin. Off by default — these features run autonomous edits across the workspace, so keep them hidden until you opt in.",
        Zh: "将「智能体优化专家」智能体与其 2 条 autopilot(SkillOpt-Multica 每日自进化循环 + 每3工作日批量优化)以及 skillopt-multica 技能作为可选用插件暴露。默认关闭 —— 这些功能会在工作区内自动修改智能体 / 技能 / 自动化,请在明确启用前保持隐藏。",
    },
},
```

## Migration 150

`experimental_resource_visibility` 表 + 4 条 seed row:

| resource_type | resource_id | name |
|---|---|---|
| agent | `6a647967-f56e-4661-ad39-774420b870d4` | 智能体优化专家 |
| autopilot | `f788217e-ef6a-4af0-a5a1-cbf85d8dbb8e` | 智能体工程师团队 · 每3工作日批量优化 |
| autopilot | `ab5de2d9-7af9-491d-a42f-2c9f87fbcdf3` | SkillOpt-Multica · 每日 00:00 自进化循环 |
| skill | `18edfaed-c493-4a61-89b4-8b6bff84d8fc` | skillopt-multica |

## 三层过滤

1. **autopilot scheduler** (`server/internal/service/autopilot.go::shouldSkipDispatch`) — 入口 hook,cached hidden-set,flag off 时直接 skip,reason = `"autopilot hidden by agent_self_optimization flag"`(稳定字符串,dashboard group by substring 不需改)
2. **ListAutopilots / ListAgents / ListSkills** — flag off 时从响应里 filter 掉
3. **squad_member 保留** — 智能体优化专家 仍在「智能体工程师团队」squad 下,只是 visible roster 隐藏

## 防呆 / 防漂移

- `HideableResource` 类型与 lock.go 的 `ResourceType` 区分,编译期拒误用
- 4 个 unit test 锁住: catalog flag 存在、ID 有效、enum 守卫、cached skip-set 一致
- fail-open on lookup error(best-effort,日志 warn,不阻塞业务)

## Verification(live)

```
/Applications/Multica.app = 0.3.17
5432 + 8090 LISTEN <30s
schema_migrations 150/149/148/147
visibility_seed: 4 rows
autopilot 总数: 19 → 17(flag off 时 list 返 17)
agent 总数: 81 → 80(智能体优化专家被隐藏)
workspace=1 / issue=163 / comment=855 / squad=11 / mythos_run=0
```

`pnpm typecheck` 6/6 pass。Go test 全部 PASS(26 个 package + 0.3.17 新加 5 个 test)。

## What can be done today

1. Labs tab → 看到 `智能体自优化循环` flag(off by default)
2. flag off → 自动化列表里看不到「SkillOpt-Multica 自进化」与「每3工作日批量优化」;agent roster 看不到「智能体优化专家」;skill 浏览器看不到 skillopt-multica
3. flag off → scheduler tick 自动跳过这 2 条 autopilot,不会派单,不会消耗 agent runtime
4. flag on → 全部恢复显示与调度

## What's NOT here(deferred to 0.3.18)

- **flag toggle hot reload**:当前用户切 flag 后 server 端要重启才生效。要做 hot toggle 需接 UserPrefProvider 替代 `experimental.DefaultFor` 调用
- **财务 squad 的「用户经济行为刻画定期优化」季度 autopilot** 不在隐藏范围 — 它是业务优化不是 self-optimization,默认保留

## Migration 注意事项(forward-only)

- 隐藏机制是元数据,不删源数据。flag off → agent / autopilot / skill 行仍在 DB,只是 list endpoint 不返 + scheduler 不跑
- enable flag 后无需 migrate,直接恢复
- 任何用户后续 toggle flag,数据完整性保持

## Critical files

1. `server/internal/experimental/catalog.go` — flag literal
2. `server/internal/experimental/visibility.go` (新) — HideableResource + helper
3. `server/internal/experimental/visibility_test.go` (新) — 4 tests
4. `server/internal/service/autopilot.go` — scheduler skip
5. `server/internal/service/autopilot_test.go` — skip-set cache test
6. `server/internal/handler/{autopilot,agent,skill}.go` — list filter
7. `server/migrations/150_experimental_resource_visibility.{up,down}.sql` (新)
8. `server/pkg/db/queries/experimental_resource_visibility.sql` (新)
9. `apps/desktop/package.json` — version bump
