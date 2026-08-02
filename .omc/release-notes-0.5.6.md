# 0.5.6 Release Notes — 实验 → 产品 全面拆解

**日期**: 2026-08-02
**版本**: 0.5.6

## 拆解

`agent_creation_studio` + `agent_self_optimization` 经过 0.5.5/0.5.5.1/0.5.5.2/0.5.5.3 累积的 6 处遗漏,0.5.6 完整拆解:

| 拆解内容 | 状态 |
|---|---|
| catalog Flag literal | 删除 |
| install handler | 删除 |
| self-opt 运行时 flag gate | stub 化(总是可用) |
| self-opt view 页面 + 路由 | 删除 |
| RecentLabsPanel + view state | 删除 |
| HIDDEN_LAB_KEYS / PRODUCT_LEVEL_LAB_KEYS 黑名单 | 删除(catalog 是 source of truth) |
| 实验性 lock/visibility 行 | migration 237 清理 |

## 用户视角

| 之前 | 0.5.6 |
|---|---|
| Labs tab 启用 `agent_creation_studio` flag | 无 toggle(catalog 已删) |
| `multica experimental install ...` | 404(router 不再注册) |
| LabPicker 选 智能体创建 | 入口已删(AssigneePicker 直接选 leader) |
| `/experimental/agent-self-optimization` view | 404 页面(view 已删) |
| 智能体自优化 view 顶 banner "本功能已升格为产品内置" | banner 没了(view 没了) |
| `/autopilots` 改 4 条 self-opt autopilot | ✓ 仍可用 |
| AssigneePicker 选 `agent_creation_expert` / `智能体优化专家` | ✓ 仍可用 |

## 数据完整性

**0 损失**。验证后行级:
- workspace=1 ✓
- agent=97(含 `agent_creation_expert` + `智能体优化专家` 仍在)
- autopilot=16(4 条 self-opt autopilot 全 active)
- issue=252(未动)
- comment=1518(未动)
- migration 237 清理:`experimental_resource_lock` 2 行 → 0 行

## 0.5.6 改动的文件

**后端 (Go)** 9 个文件
**Migration** 1 个(migration 237)
**前端 (FE)** 5 个文件(含 3 个 view/route 删)
**Ship chain** 2 个文件(package.json + CLAUDE.md)

## 验证

- `go build ./...` + `go test ./internal/...`: 全过(visibility_test.go + panic_context_test.go 改 0.5.6 contract)
- `pnpm typecheck`: 6/6
- `lab-picker.test.tsx`: 8/8(0.5.6 contract 重写 3 个 case)
- 冷启动三查:5432/8090 LISTEN、/health ok、Info.plist 0.5.6
- Migration 237 跑成功(`up 237_product_level_cleanup` + lock/visibility 清干净)
- Boot log:`agent-self-opt: scheduler started workspaces=1 tickers=1` + `boot provision product labs complete agent=agent_creation_expert`

## multica 稳定性

本次 0.5.6 拆解,6 类核心资源 100% 保留,**0 数据损失**。0.5.5.3 ship 的 252 issue / 1518 comment / 97 agent / 16 autopilot / 4 self-opt autopilot 全部不动。
