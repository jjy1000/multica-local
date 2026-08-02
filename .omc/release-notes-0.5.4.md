# 0.5.4 Release Notes — 智能体创建 Click-Through 修复

**日期**: 2026-08-02
**版本**: 0.5.4

## 修复:智能体创建 Lab Picker UX

**问题**: 0.5.4 之前(以及 0.5.4.studio 中间版本),点 "智能体创建" 弹 RecentLabsPanel 只读预览,需要关闭弹窗或点"返回实验插件"才能让 lab 真正生效。这违反"智能体工程创建就是团队,直接开始"的预期。

**修复**: 改为"flag on → 一步到位,flag off → 弹 panel 引导启用"的 click-through 语义。

| flag 状态 | 行为 |
|---|---|
| **on** | 点 chip → 直接 `PATCH issue.lab_source='agent_creation_studio'`,server 0.3.46 P0#4 契约改写 assignee 为 `agent_creation_expert`,**一步派单** |
| **off** | 点 chip → 弹 RecentLabsPanel(leader 未装,直接派会被 server 400),文案引导用户在 Labs 设置里启用 flag |

## 文件改动

- `packages/views/issues/components/pickers/lab-picker.tsx`: `onClick` 分支 + `entries` 注释 + 头部 comment
- `packages/views/issues/components/pickers/lab-picker.test.tsx`: 3 个 case 0.5.4 → 0.5.4.x
- `packages/views/issues/components/issue-detail.tsx`: 头部注释同步 0.5.4.x 语义
- `packages/views/locales/{en,zh-Hans,ja,ko}/issues.json`: `recent_panel_hint` 文案重写

## 验证

- `pnpm typecheck`: 6/6 子包过
- `lab-picker.test.tsx`: 9/9 + `recent-labs-panel.test.tsx`: 4/4
- 冷启动三查:5432/8090 LISTEN、/health ok、Info.plist 0.5.4 = package.json 0.5.4
- row parity: workspace=1 / issue=252 / comment=1518 / agent=96(workspace 不动,其余漂移同 0.5.2 以来累积)

## 已知问题

- `app-builder-bin@5.0.0-alpha.13` 仍然含 fork 0.3.63 之前的 stale `multica-main` 路径硬编码,触发 `ENOENT stat` 不可修复。本次走 **manual asar repack fallback**(0.3.63 ship 验证过)完成 ship。
- 后续 ship 建议:升级 `app-builder-bin` 到 5.x stable,或直接弃用 `electron-builder` 改用全 manual asar repack。
