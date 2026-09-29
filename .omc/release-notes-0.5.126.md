# 0.5.126 (2026-09-29) — 审计事实修正批: 冷启动门禁的数据对账首次真正生效 + 指南三处错误命令修正

Commits: `da3b6a89d` (docs 批, 4 文件 +18/−6), `27c46a4d2` (catalog 注释)。repo 内全部是 docs + 注释: 零行为变更 / 零迁移 / 零 wire 改动。repo 外另有 1 个运维脚本修复（无版本历史, 已记忆备份）。

## 起因

2026-09-29 审计报告列出 4 个「全部有实测证据」的问题, 逐条核实全部属实:

1. **冷启动校验的数据对账 SQL 是坏的（最重）** — `~/.multica/scripts/verify-desktop-cold-start.sh:120`
   的 `SELECT 'workspace=' || COUNT(*) FROM workspace || ' issue=' || ...` 跨 FROM 拼接字符串, 恒语法错误;
   `2>/dev/null || echo "psql-unavailable"` 吞掉 exit 1, 第 123 行无条件 PASS。
   **ship 门禁的数据维度自脚本诞生起从未执行过一次**, 历次 ship 都打印
   `Row parity: psql-unavailable` 照样 PASS。psql 本身没问题（`select 1` 成功）。
2. **CLAUDE.md silent-skip 段教的是它自己警告的陷阱** — `export $(grep -E '^DATABASE_URL=' .env | xargs)`
   紧跟 `cd server && go test` 语境, 但 `.env` 在 repo root, `server/.env` 不存在 →
   `DATABASE_URL` 仍为空 → DB 测试全 skip → 15 秒假绿 exit 0。当天首次执行即踩中。
3. **数据完整性验证命令用不了** — 文档教 `docker exec multica-postgres-1 psql ...`,
   本机连 docker CLI 都没有; 5432 是 app 自带 native PG 17.4
   （`~/Library/Application Support/Multica/pgdata`）。同一 Docker 错误还出现在数据路径表
   （`Docker volume multica_pgdata`）。
4. **daemon 日志分叉无文档** — `~/.multica/daemon.log` 停在 09-25 02:01
   （结尾是 401 re-auth 警告）; live 日志是 `~/.multica/profiles/desktop-localhost-8090/daemon.log`
   （代码侧 `daemon-manager.ts` `profileLogPath()` 确认）。照根目录那个查 auth 会得出完全错误结论。

## 修复

### repo 外: verify-desktop-cold-start.sh row parity 重写

- SQL 改标量子查询拼 counts, 实测 `workspace=8 issue=499 agent=143 squad=22`;
- 探测失败 **exit 4 硬失败**（header exit code 表已补记; ship-mac.sh 以
  `bash "$COLD_START" || die` 直调、无管道, 退出码可达 die）;
- 端口自动探测 5432→5433（对齐 `pre-update-snapshot.sh`）;
- 错误进失败信息, 不再吞。
- 双场景验证: 活 PG 打出真实行数; 死端口（5999）变异场景 exit 4。
  **自本次 ship 起数据对账真正生效。**

### repo 内: 2 commits

- `da3b6a89d` **docs: correct machine-false commands in guides** — CLAUDE.md + AGENTS.md 同步:
  silent-skip 段写明 `.env` 在 repo root、必须 `cd server` 之前导出; 数据完整性验证改 native PG
  实测命令 + 5433 回退; 数据路径表 Docker 字样改正; apps/desktop 指南新增 daemon 日志分叉条目
  （live = per-profile, 根目录文件是冻结遗留）。
- `27c46a4d2` **docs(experimental): correct AutoDispatch opt-out examples** — catalog.go 字段注释 +
  `AutoDispatch()` doc 两处: claude_science_lab 0.5.81 已翻回自动派工, opt-out 现行集 =
  `pythia_oracle` + `causal_graph`, 记录 0.5.22–0.5.80 历史防再引错。

## 门禁

- `pnpm typecheck` 6/6 绿。
- `go test -count=1 ./internal/... ./pkg/agent/...`（DATABASE_URL 从 repo root 导出, 无 skip）全绿。
- `node scripts/check-agents-docs-sync.mjs` 绿（含三处镜像同步）。
- ship 链含本次修复后的冷启动验证（row parity 首次真实执行）。

## 未动 / 残留

- jyf 工作区测试 agent 残留: 真凶未定位, 不盲改 live 数据。
- `cmd/server` 2 条 baseline 红维持原判（0.5.121 在案, 与本批无关）。
