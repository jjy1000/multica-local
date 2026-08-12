# `.omc/_legacy/` — 归档的历史项目文档

## `0.3.x-ship-logs/`

2026-08-11 备份协议落地时同步归档的 16 个 0.3.x 时代文档：

- 13 个 `0.3.56 → 0.3.69` ship log
- 3 个 `0.3.29` 时期 impl/status/plan 文档

**为什么归档**：
- 0.3.x 是 pre-fork 时代（2026-07-22 前），已与当前 fork-local 维护脱节
- 0.4.0 起项目从 upstream 切到本地维护（`fork-migration-verify-2026-07-31`）
- 当前活跃版本线是 0.5.x（最新 0.5.15），0.3.x 的 ship log 不再是"近期参考"

**保留原因**（不删除）：
- 历史决策与契约（CLAUDE.md 引用 0.3.31/0.3.45/0.3.46 等关键 ship 的 Active Contracts）
- 故障链排查（如 0.3.62 codesign nested binary、0.3.63 pluginRuntimeEnv 等事故）
- git blame / search 时仍需可追溯

**未来归档条件**（触发即移动到这里，不删）：
- 0.4.x 文档被 0.5.x 完全替代时
- 单文件 / 单目录的临时调研产物（如 `0.3.3-audit-and-fixes-2026-07-12.md` 这类）

## 协议

详见 `.omc/backups/README.md` 的「定期清理」节。清理原则：

- 归档而不是删除（除非明确说要删）
- 保留 git history（用 `git mv`）
- 子目录命名 `<branch>-<era>` 或 `<era>-<topic>`
