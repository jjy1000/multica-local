# Multica 0.3.2 Release Notes (2026-07-05)

## 核心修复：智能体连续运行（memory multica-0.3.2）

### P0 — 任务完成时 issue 缓存不刷新
`packages/core/realtime/use-realtime-sync.ts` 的 `task:completed` handler 早返回（`if (!payload.chat_session_id) return`），注释说 "issue tasks handled elsewhere" 但全代码库 438 文件无对应处理器。修复：去掉早返回，对 issue-bound 任务 invalidate `issueKeys.detail(wsId, payload.issue_id)` + `issueKeys.tasksAll()`。同步修 `task:failed`。

### P1 — Squad leader 收到纯文本追问后无下文
`server/internal/handler/squad_briefing.go` 的 Operating Protocol 追加第 6 条：member 纯文本追问（"完成了吗"、"继续吧"）时，leader 必须 re-evaluate：worker 已完成且交付物正确 → `multica issue update <id> --status done` + 简短总结；交付物缺失/不正确 → 重新派给 worker。

### P2 — Server log 0 字节
`apps/desktop/src/main/server-manager.ts` 在 spawn server 之前写入一行 boot marker + `logFd.sync()`，保证 server.log 至少有一行可观测信号。

## 稳定性 + 打包风险修复

### Path D modulePreload 修复（防 NSAlert）
`apps/desktop/electron.vite.config.ts` 加 `main.build.modulePreload: false`，关闭 ~70 个 dynamic chunk 的同步 preload 链，消除 Electron 39 BrowserHungDetector 1.5s NSAlert 触发条件（memory 0.2.89.5.1）。

### asarUnpack 完整化（防 packaged 启动崩）
`apps/desktop/electron-builder.yml` 在 `resources/**` 基础上补 `resources/pg/**`、`resources/bin/**`、`node_modules/embedded-postgres/**`、`node_modules/@embedded-postgres/darwin-arm64/**`（memory 0.2.89.5.1 asar unpack failure）。

### F7 before-quit 合并
`apps/desktop/src/main/index.ts` 与 `daemon-manager.ts` 两个 `before-quit` 监听器 race。统一到 index.ts：顺序 `stopServerManager → stopDaemon → app.quit()`，`stopPolling/stopLogTail` 移入 `stopDaemon` 内部避免 fd 泄漏。

### M9 pre-update TABLES 补全
`~/.multica/scripts/pre-update-snapshot.sh` TABLES 数组补 `comment / inbox_item / squad / skill` 4 表，与 `pg-bootstrap.ts` MIGRATION_TABLES 同步。

## Schema 集成

| Migration | 状态 | 备注 |
|-----------|------|------|
| 123_issue_stage | 保留（fork 早 apply）| issue.stage ordinal |
| 124_task_prepare_lease | 保留 | agent_task_queue.prepare_lease_expires_at |
| 125_dispatched_prepare_index | 保留 | 性能索引 |
| 128_runtime_mcp_overlay | apply | 任务级 MCP 覆盖层 schema |
| 128_autopilot_collaborator | apply | autopilot 协作成员表 |
| 128_comment_routing_escalation | apply | comment escalation 字段 |
| 130_agent_invocation_permission | **不 apply** | 依赖 129（Composio allowlist）— 按本地化策略剔除 |
| 134_runtime_profile_add_qoder | apply | protocol_family 白名单扩 qoder |

## UI 动效

`packages/views/agents/components/agent-presence-indicator.tsx` 加 motion/react 在线脉动（`scale + opacity`，compositor-only），守门 `useReducedMotion()` — 仅 online agent 触发，offline/unstable 静态保持可区分。

## 上游 cherry-pick（精简后采纳）

- `server/internal/handler/mcp_overlay.go` + test — 任务级 MCP 覆盖合并逻辑（纯 stdlib 依赖）

## 故意剔除

- 128_129_132 Composio 链路（schema 全部不 apply）
- 130_agent_invocation_permission（依赖 129）
- 131_issue_origin_slack_chat（Slack）
- 133_github_installation_multi_workspace（GitHub）
- 122_lark / 124_channel_generalization（Lark/Feishu）
- PostHog / Google OAuth / Cloud billing / Invitations / electron-updater publish（CLAUDE.md "Localized Fork"）

## 验证

- `go test ./...` 全绿（除 cmd/server TestReadinessEndpoints 自身环境问题，与本次改动无关）
- `pnpm --filter @multica/desktop typecheck:node` 0 错误
- `pnpm --filter @multica/views typecheck` 0 错误
- `pnpm --filter @multica/core typecheck` 0 错误

## 已知 deferred → 0.3.3

- issues/surface 重构（11 文件，独立功能，0.3.3 cherry-pick）
- task-transcript dialog + transcript button
- runtime profile UI（catalog / dialog / heatmap）
- chat 整套 FAB/window/input
- issues/surface 重构的 cache-coordinator 274 行
- dashboard 图表（7 weekly + 4 daily + activity-heatmap）
- CLI 子命令扩展（squad / runtime profile / skill import / chat history / issue rerun / metadata / label / user profile / attachment download）
- task_lifecycle.go（RecoverOrphanedTasks）— 需 sqlc 重生成
- skill_import_archive.go — 需 cherry-pick skill.go:1974 finishSkillImport
- runtime_profile.go + agent_permission.go — Composio epic 联动