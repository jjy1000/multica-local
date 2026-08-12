# `.omc/backups/` — 增量历史备份协议

## 何时备份

满足任一：

- **版本发布**: 新版本 ship 时（如 `0.5.16`）
- **重大重构**: 单分支 > 20 文件 或 > 3 commit 且有用户可见行为变化
- **schema/migration**: 任何新增或修改 `server/migrations/*.sql` 的改动
- **data-shape**: 任何对持久化数据结构有破坏性风险的改动（即便未发 release）
- **用户明确**: 用户说"备份一下" / "存一份" / "留个快照"

不触发（噪声）：

- 单文件 typo / 注释修正
- 1-2 文件的小 bugfix
- 文档 typo

## 备份位置

```
.omc/backups/<TS>/<reason>/
.omc/backups/                       ← 活跃（最近 30 份）
.omc/backups/_archive/<TS>/<reason>/ ← 归档（超 30 份 / >90 天的老备份）
```

`<TS>` = `YYYY-MM-DD-HHMM`（UTC 24h，避免时区歧义）
`<reason>` = kebab-case slug

例：`.omc/backups/2026-08-11-1430/0.5.15-ship/`

## 备份内容

每个备份目录里：

```
.omc/backups/<TS>/<reason>/
├── manifest.json         # 见 _template/manifest.json.template
├── diff.patch            # git diff --binary (相对 base 或 HEAD~N)
├── status.txt            # git status --short 快照
├── log.txt               # git log --oneline -N 输出
└── notes.md              # 可选：ship 上下文 / 决策理由 / 验证证据
```

**只存 diff + 元数据**，不复制源码（已有 git 还原）。

## 创建方式

用 `scripts/backup.sh`（项目根）：

```bash
# 备份当前 HEAD 的 release
bash scripts/backup.sh --reason 0.5.16-ship --trigger release

# 备份一组未提交改动
bash scripts/backup.sh --reason schema-mig-239 --trigger schema-migration --base HEAD

# 手动触发，可选 notes
bash scripts/backup.sh --reason epic-rewrite --trigger manual
```

脚本行为：

1. 确认是 git 仓库 + 当前分支
2. 生成 `<TS>` + 验证 `<reason>` 合法
3. 创建目录
4. 写 manifest.json（含 git status / log / diff stat）
5. 写 diff.patch
6. 写 status.txt + log.txt
7. 检查总备份数：> 30 → 把最老的移到 `_archive/`
8. 输出路径 + 摘要

## 保留策略

- **活跃** `.omc/backups/`: 最近 30 份
- **归档** `.omc/backups/_archive/`: 超量入归档；> 90 天的老备份可手动清理
- **清理触发**: 用户说"清理" / 备份数 > 30 时自动 archive / 每月手动一次
- **永不自动 rm**: archive 永远不删，只有人工决定

## 与 .gitignore 关系

`.omc/backups/.gitignore` 已配：**只忽略实际备份内容**，`_template/` 仍 tracked。

## 与 ship-mac.sh 集成

`s/scripts/ship-mac.sh` 是 canonical 桌面 ship 流程。未来可在 step "Postinstall 验证" 后自动调 `scripts/backup.sh --reason <ver>-ship --trigger release`，作为 ship gate 一环。

## 失败恢复

要从某个备份还原源码：

```bash
# 看 manifest.json 找 base_commit / head_commit
git checkout <head_commit>
git apply .omc/backups/<TS>/<reason>/diff.patch
```

或直接 `git reset --hard <head_commit>`（如有 dirty 状态先用 `git stash`）。
