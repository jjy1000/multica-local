# 0.5.93 Release Notes — MCP 管理提升为侧边栏一级入口

> **状态：已发布（shipped 2026-08-31）。** `/Applications/Multica.app` = **0.5.93**。Ship 链 7/7；ship log：[`.omc/0.5.93-ship-2026-08-31.md`](0.5.93-ship-2026-08-31.md)。

## 变更：MCP 管理入口移到侧边栏

0.5.92 把 MCP 管理放在了 **设置 → 工作区 → MCP** tab 里。发布当日用户反馈：作为常用配置面，放在设置页里太深。本版本将其提升为**侧边栏「配置」分组的一级页面**：

- 侧边栏位置：**配置 → 运行时 / Skills / MCP / 设置**（Skills 下面）。
- 原设置页的 `?tab=mcp-sync` tab 已移除（移动而非复制，避免双入口漂移）。
- 页面内容不变：只读镜像列表（15 个来自 Claude Code 的 server）、同步状态、"立即同步"。
- 新路由：`/{workspace}/mcp`（web 与桌面同步注册）。

仅前端导航/路由变更；0.5.92 的全部功能（同步 worker、claim 合并、用量页 MCP 调用 KPI）不受影响。
