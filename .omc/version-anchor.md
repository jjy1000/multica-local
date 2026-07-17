---
name: multica-version-anchor-2026-07-02
created: 2026-07-02T21:51:00Z
updated: 2026-07-11T01:54:00Z
type: project
---

# Multica 版本号 source-of-truth（2026-07-02）

## 4 个不同的"版本号"含义

| 维度 | 值 | 实证位置 | 谁负责更新 |
|---|---|---|---|
| **当前 working app** | **0.2.96** | `/Applications/Multica.app/Contents/Info.plist` (`CFBundleShortVersionString` + `CFBundleVersion`) | Apple Installer (DMG 重打) |
| **Source tree 版本声明** | 0.2.97 | `apps/desktop/package.json` 字段 `version` | `bundle-cli.mjs` 读它作为 DMG 内嵌版本号 |
| **最近成功打包 DMG** | 0.2.96 | `~/.multica/last-packaged.json` | `pnpm --filter @multica/desktop package` 完成后写 |
| **Plan 未来 PR 目标** | PR 1 → 0.2.97 / PR 2 → 0.2.98 / PR 3 → 0.3.0 | `.claude/plans/cached-greeting-coral.md` §9 | PR commit 时改 source + DMG 一起 |

## Why this doc exists

CLAUDE.md 与 memory 之前多处把 "running 0.2.97" 当事实陈述，但 `Info.plist` 实证是 **0.2.96**。
两处 bug 的根因是 source 声明 0.2.97 与实际 shipped DMG 之间没有 force-sync。

**本文件是 single source of truth**。后续 PR bump 时**唯一可改的版本号** = `apps/desktop/package.json` 的 `version` 字段；
打完 DMG 完成后必须 update `last-packaged.json` 的 `version` 字段。两者必须一致。

## 强制同步检查（任何 PR 必跑）

```bash
# 1. 读取 source 声明
SOURCE_VER=$(node -p "require('/Users/jiangjianyan/jjy/multica-main/apps/desktop/package.json').version")

# 2. 读取 installed app 版本
INSTALLED_VER=$(defaults read /Applications/Multica.app/Contents/Info.plist CFBundleShortVersionString)

# 3. PR commit 前必须: SOURCE_VER == INSTALLED_VER
#    PR commit 后 (打完 DMG 替换): SOURCE_VER == INSTALLED_VER == last-packaged.version
```

不通过 = **block commit**。

## 回滚锚点

- 任何 PR 引发回归 → `cp -R ~/.multica/backups/pre-standalone-cd-2026-07-02/app-snapshot/Multica.app.0.2.96.snapshot-20260702-215110.bak /Applications/Multica.app`
- 备份细节：见 `~/.multica/backups/pre-standalone-cd-2026-07-02/README.md`

## 历史 release notes 目录

- `.omc/release-notes-0.2.97.md` — 已存在（内容是 0.2.96 的稳定性修复记录，文件名是规划性命名冲突，**不是已 ship 的 0.2.97 release notes**）
- `.omc/release-notes-0.2.96.md` — 不存在（0.2.96 的 release notes 应该在 release-time 创建时直接命名 0.2.96，不是 0.2.97）

## 下次 PR bump 流程（不变）

1. PR commit 前：`apps/desktop/package.json` `version` = 计划版本号
2. `make`/`pnpm` 跑完测试，DMG 打包: `pnpm --filter @multica/desktop package`
3. 替换: `hdiutil attach ... && cp -R ... && hdiutil detach ...`
4. 验证: `defaults read /Applications/Multica.app/Contents/Info.plist CFBundleShortVersionString` == 计划版本号
5. 写 `~/.multica/last-packaged.json` (脚本应自动)
6. 写 `.omc/release-notes-<version>.md` (人工, en + zh-CN)
7. 在 MEMORY.md 加一行 + 关键变更写独立 memory 文件

## 仍然未 ship 的 planned version

- **v0.2.97** — Stage C (brew PG 兜底 dialog)。PR 1 待实施。
- **v0.2.98** — Stage D-1 (native PG init/start/stop, 用户手动放 binary)。PR 2 待实施。
- **v0.3.0** — Stage D-2 (自动下载 PG binary + 数据迁移 + 删 Docker 路径)。**已 ship 2026-07-02** (memory `multica-0.3.0-standalone-2026-07-02.md`)
- **v0.3.1** — 稳定性修复 (in-flight coalescing + stopping flag + probeMulticaPg 4 字段 + sentinel 原子化). **已 ship 2026-07-03** (memory `multica-0.3.1-stability-fix-2026-07-03.md`)
- **v0.3.2** — 智能体连续运行 P0 + Path D modulePreload + asarUnpack 完整化. **已 ship 2026-07-05** (memory `multica-0.3.2-stability-fix-2026-07-06.md`)
- **v0.3.3** — 上游 chat 子系统集成 (MUL-4351 + IM unread + agent pin + session pin + intro session + comment delivery) + mcp_overlay handler + 6 个 bug 修复. **已 ship 2026-07-12** (memory `0.3.3-audit-and-fixes-2026-07-12.md`). Comprehensive audit 发现 5 P1 + 6 P2; 5 P1 在 0.3.4 修复 + 6 P2 文档化推到 0.3.5+. Plan: `.omc/plan-0.3.3-upstream-integration.md`.
- **v0.3.4** — MUL-4351 backend (EnqueueChatTask 绑 chat_input_task_id + IM unread count + last_read_at cursor) + 前端 UI (unread_count badge + agent_intro badge + no_response 气泡). **已 ship 2026-07-12** (memory `0.3.4-chat-mul4351-and-ui-2026-07-12.md`). DMG 240MB 通过 CUSTOM_DMGBUILD_PATH env var 跳过 dmg-builder socket hang.

详见 `/Users/jiangjianyan/.claude/plans/cached-greeting-coral.md` §9。