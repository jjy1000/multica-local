# 0.5.92 Release Notes — MCP 同步：把 Claude Code 的 MCP 复刻进 Multica，用量页新增 MCP 调用量

> **状态：已发布（shipped 2026-08-31）。** `/Applications/Multica.app` = **0.5.92**，冷启动验证 PASS（~6s）。Ship 链 7/7（快照 `.bak` 已按偏好移至 `~/.multica/backups/`，pg_dump 保留；migration 285 已应用）。Ship log：[`.omc/0.5.92-ship-2026-08-31.md`](0.5.92-ship-2026-08-31.md)。

## 新功能一：MCP 管理（设置 → MCP）

Multica 现在会把你 **Claude Code 里的 MCP server 自动同步进来**，智能体任务可以直接使用同一套 MCP，无需手工复制配置。

### 工作方式

- **来源**：`~/.claude.json` 的 user-scope `mcpServers`（本机实况 15 个 server）。服务端同步 worker 随启动拉起，每 60 秒检查一次；设置页可"立即同步"。
- **只读镜像，Multica 侧不可删**：列表只提供查看与刷新，没有任何编辑/删除入口。增改删以 Claude Code 为准——你在 Claude Code 里 `claude mcp add/remove`，一分钟内镜像跟随；源里删掉的条目会标记"源中已移除"并停止对智能体生效，但保留可查。
- **变更检测**：只对 `mcpServers` 子树做规范化 hash（`~/.claude.json` 每次启动都会被 Claude Code 重写，整文件检测会误报）。源文件缺失或损坏时**保留最近一次好的镜像**，错误显示在设置页，绝不误删。
- **生效方式**：claude provider 的智能体在领取任务时自动获得 合并配置 = 智能体手动 MCP 配置 + 同步镜像（**同名时手动配置优先**）。Claude CLI 本就以 `--strict-mcp-config` 运行，所以智能体实际可用的工具面就是这份合并集。
- **密钥安全**：设置页只显示 env/headers 的**键名**，值一律由服务端打码为 `********`，明文只存在本地库用于下发，不跨 API 边界。
- 入口：**设置 → 工作区 → MCP**（`?tab=mcp-sync`），含同步时间、server 数量、类型/命令、状态徽标。

### 边界（v1）

- 仅 claude provider 生效（codex/cursor 等后续逐个放行）；仅 user-scope；项目级 `.mcp.json` 不在范围。
- 暂无 per-agent"不继承"开关——同步来的 MCP 工具定义会进入每个 claude 智能体的运行上下文（轻微 token 开销）。
- 对 Claude Code 本身零影响：同步器对 `~/.claude.json` 纯只读，不写不锁，交互式会话不受任何干扰。

## 新功能二：用量页"MCP 调用"KPI

用量页（Dashboard）新增第 5 个 KPI 卡 **"MCP 调用 · ND"**：统计智能体运行中 MCP 工具被调用的次数。

- daemon 在流式解析时按 `mcp__<server>__` 前缀计数，走现有 usage 上报通道——**任务失败/阻塞也计入**；会话续跑重试的两次调用求和（真实发生即计数）。
- 数据落在 `agent_task_queue.mcp_calls`，按任务完成时间做日切片（查看者时区口径与其他 KPI 卡一致）。旧版本任务该值为 0，不回填。

## 升级与兼容

- migration 285：新增 `mcp_sync_server`、`mcp_sync_state` 两表 + `agent_task_queue.mcp_calls` 列（forward-only，均为新增，不动既有列）。
- 对既有链路零替换：agent 手动 `mcp_config` 行为不变；其他 provider 不受影响；旧 daemon 不上报 `mcp_calls` 时服务端不清零既有值。
- 首次启动后同步 worker 自动工作，无需任何配置；`MULTICA_MCP_SYNC_SOURCE` / `MULTICA_MCP_SYNC_INTERVAL` 可覆盖来源路径与轮询间隔。

## 验证

typecheck 6/6；全量 `go test -p 1 ./...`（DB 真跑）30 包 0 FAIL；新增 mcpsync 单测 + 3 个 DB-backed handler 测试（脱敏红线、mcp_calls 持久化、dashboard 聚合）全绿；真实冒烟：本机 15 个 server 镜像成功、二次同步 hash 快路径生效、同名手动优先验证通过；安装版核验：worker 自启、镜像实时、asar/二进制特性存在性检查通过。
