---
name: 0.3.39 release notes (draft)
created: 2026-07-17T11:43:32Z
updated: 2026-07-17T11:43:32Z
---

# 0.3.39 — Claude CLI 2.1.211 MCP strict-mode 修复

## 用户视角修复

1. **实验室任务 dispatch 100% 失败修复**
   - 0.3.38 ship 后,所有 lab agent(由 `install_claude_science` / `install_mythos` /
     `install_constitution_agent` / `install_agent_self_opt` / `install_code_canvas` /
     `install_llm_wiki_bridge` 创建)派发的任务 100% 失败,stderr 报:
     ```
     Error: Invalid MCP configuration:
     mcpServers: Invalid input: expected record, received undefined
     ```
   - 根因:这些 installer 给新 agent 的 `mcp_config` 默认值是 `{}`(空对象,表示
     strict-mode 但无 managed server)。Claude CLI 2.1.211+ 用 zod schema 严格校验
     MCP 配置文件,要求顶层必须含 `mcpServers` 键,否则立即 exit 1。
   - 修复:`server/pkg/agent/claude.go::writeMcpConfigToTemp` 在写入 temp file 前
     做 normalize — `{}` → `{"mcpServers":{}}`。其他值原样写入。

2. **回归保护测试**
   - `TestWriteMcpConfigToTempNormalizesEmptyObject`:5 个 case 覆盖 `{}` / 带空格的 `{}` /
     `{"mcpServers":{}}` / 已有 server / 非 mcpServers 键 的 normalize 行为。

## 受影响路径

- `pkg/agent/claude.go::Execute` (claude 后端)
- `pkg/agent/codebuddy.go::Execute` (codebuddy 后端,共享 `writeMcpConfigToTemp`)
- Codex / OpenClaw / cursor / opencode / kimi / kiro / hermes / qoder 不受影响(各自有独立
  的 MCP 处理路径,codex 走 `hasManagedCodexMcpConfig` 路径,openclaw / cursor 写 .mcp.json,
  opencode 走 `buildOpenCodeMCPConfigContent`,ACP 系列走 `buildACPMcpServers` — 都没有
  `{}` → zod 拒绝的兼容性问题,因为它们各自 schema 容忍空对象)。

## 验证

- `go test -race -count=1 ./pkg/agent/`:10.996s PASS
- `TestWriteMcpConfigToTemp`:原 1 个 case + 新增 5 case(全部 PASS)
- 端到端:写 `{"mcpServers":{}}` 给 claude CLI 2.1.211 + `--strict-mcp-config` → 接受,正常返回

## 文件改动

- 修改:`server/pkg/agent/claude.go`(+ 38 行,新增 `isEmptyJSONObject` + `writeMcpConfigToTemp` normalize)
- 修改:`server/pkg/agent/claude_test.go`(+ 74 行,新增 `TestWriteMcpConfigToTempNormalizesEmptyObject`)

## 跟 0.3.38 的关系

不是 0.3.38 回归,是 claude CLI 升级(用户机器上是 2.1.211,2026-07-xx 发布)带来的
新校验。0.3.37 时期 claude CLI 接受 `{}`,所以这个 bug 一直没暴露。lab installer
也一直没改 `{}` 默认值(因为之前 work)。

是否 ship 0.3.39 = 是否接受这个修复作为单独 patch 版本(只是单点 backend 修复,
不影响 schema/UI/i18n)。