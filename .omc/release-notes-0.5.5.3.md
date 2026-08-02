# 0.5.5.3 Release Notes — 智能体自优化循环页面入口补完

**日期**: 2026-08-02
**版本**: 0.5.5.3

## 修复:0.5.5.1 + 0.5.5.2 漏改的 self-opt 页面入口

0.5.5.1 + 0.5.5.2 已经把 self-opt 的控制面 + Labs tab 都搬到产品域,但**遗漏**了 2 处:
- `/experimental/agent-self-optimization` view 仍以"实验"口吻展示
- Issue detail 的"open panel"链接仍能跳到 self-opt view

## 用户视角

- **Issue detail**: 自优化 issue (`issue.lab_source='agent_self_optimization'`) 不再有"open panel"链接
- **`/experimental/agent-self-optimization` 页面**: 进页面后顶部看到绿色 "本功能已升格为产品内置" 提示 banner,说明控制方式(改 autopilot.enabled)
- **FlagOffPlaceholder 文案**: 改为"检查 FF_AGENT_SELF_OPTIMIZATION env 或 catalog 配置",不再误引导用户去 Labs 启用

## 改动

- `packages/views/issues/components/issue-labs-section.tsx`:
  - `FLAG_ROUTE_SUFFIX` 删 `agent_self_optimization` 条目
- `apps/desktop/src/renderer/src/pages/self-opt-view.tsx`:
  - 加 `<ProductLevelBanner />` 在 Intro 后(Sparkles 图标 + 绿底)
  - `useExperimentalFlag` default 改 true
  - `FlagOffPlaceholder` 文案重写(防御性)

## 验证

- `pnpm typecheck`: 6/6 子包过
- 冷启动三查: Info.plist 0.5.5.3 = package.json 0.5.5.3
- 纯 FE 修复,Go binary 未动

## 后续 0.5.6

完全删除 self-opt view + 路由 / RecentLabsPanel / install handler / HIDDEN_LAB_KEYS 集合
