---
name: multica-fork-vs-upstream-runtime-catalog-2026-08-05
description: Runtime catalog 比对:local fork server/pkg/agent/ 实际有的 runtime vs upstream HEAD 同名目录新增 runtime;本次为 Sprint 0 评估产物。
metadata:
  type: project
---

# Fork Runtime Catalog (2026-08-05)

Local fork `62ce9bd` (server/pkg/agent,30 非测试 .go) vs upstream HEAD `2bb1366`(43 非测试 .go)。

## Fork 已有 (16 个 runtime)
claude · codex · codebuddy · copilot · cursor · antigravity · hermes · kimi · kiro · openclaw · opencode · pi · qoder

(剔除工具文件:`agent.go` / `models.go` / `thinking.go` / `version.go` / `proc_*` / `stderr_tail` / `opencode_mcp` / 各 `invocation_other/windows` 三件套。)

## Upstream 独有 (新增 6 个 / 多 7 个分支)

| Adapter | Fork 状态 | Upstream 状态 | Cherry-pick 路径 |
|---|---|---|---|
| **qwenpaw** | ❌ 无 | ✅ #5986 MUL-5355 | ~150 行 + test — 体积中 |
| **qwen** | ❌ 无 | ✅ | ~150 行 + test |
| **reasonix** | ❌ 无 | ✅ #6370 MUL-5604 | ~150 行 + test |
| **traecli** | ❌ 无 | ✅ | ~150 行 + test |
| **deveco** | ❌ 无 | ✅ | 含 `deveco_models.go` 双文件 |
| **grok** | ❌ 无 | ✅ | 含 `grok_integration_test.go` |
| `openclaw_stdout.go` | ❌ 无 | ✅ | 极小(~50 行),与 fork openclaw 兼容 |
| `stream_json_result.go` + `stream_scanner.go` | ❌ 无 | ✅ | 共享工具,fork 应直接接 |
| `browser_mcp_config.go` | ❌ 无 | ✅ | 共享 |
| `mcp_config.go` | ❌ 无 | ✅ | 共享 |
| `acp_deliverable.go` | ❌ 无 | ✅ | 共享 |

## Cherry-pick 决策

**Sprint 5 (Runtime catalog)** 三块候选:
- **A. 6 个 runtime 直搬(qwenpaw / qwen / reasonix / traecli / deveco / grok)**:工作量 ~6×250 行 = 1500 行 + ~6 个 catalog entry + 各自的 manifest。⚠️ 此为最大块。
- **B. 仅共享工具先接**:`stream_json_result.go` + `stream_scanner.go` + `openclaw_stdout.go` + `mcp_config.go` + `browser_mcp_config.go` + `acp_deliverable.go` — ~300 行,fork 后续 runtime 接续需要。
- **C. 不接 runtime,只接运行时修复**:看 Sprint 1/2 BUG 修复中是否触及 openclaw / kimi / traecli 修补。

## 备注

`#5951` 多 backend drain 修补 + `#6320` ErrWaitDelay regression fix 大概率影响 `acp_deliverable.go` 等共享层 — 即使不接新 runtime,**共享层 cherry-pick 几乎是必接的**(否则上游 openclaw stdio 反转 / coze 行为变更会越积越难同步)。

**Why:** Sprint 5 plan 落地前的依赖输入,确认 fork 现在落后 6 个 runtime + 6 个共享工具类,共享工具直接接续,新 runtime 要等用户单独 decision。
**How to apply:** Sprints 启动前先看一眼;acp_deliverable / openclaw_stdout 是后续 0.4.18+ 修复前后必查项目。
