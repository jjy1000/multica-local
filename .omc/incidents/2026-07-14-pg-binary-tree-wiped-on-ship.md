---
name: 0.3.21 ship 后 PG binary tree 被清,fetcher 卡死,login 500 误导
created: 2026-07-14T13:51:06Z
updated: 2026-07-14T13:51:06Z
status: closed
severity: P0
affected: local desktop fork 0.3.21 / /Applications/Multica.app
scope: apps/desktop/src/main/pg-bootstrap.ts, server/internal/handler/auth.go, ship 链
---

# 0.3.21 ship 后 PG binary tree 被清,fetcher 卡死,login 500 误导

## 现象

0.3.21 ship 后,启动 `/Applications/Multica.app` → GUI `POST /auth/login {"name":"jyf"}` 返回 `500 "failed to lookup user"`。

体检链路:

- `~/.multica/pg/17.4/` **binary tree 不存在**(`bin/lib/share/include` 全空)
- `pg-bootstrap.ts` 的 `startNativePg` 在 `await ensureMulticaDb()` (`pg-bootstrap.ts:533`) 之前已抛错,因为 `~/.multica/pg/17.4/VERSION` 文件不存在 → PG 启动失败
- `downloadAndExtractPg` (`pg-bootstrap.ts:708`) 试图从远端重下 DMG,**`fetch()` 在 `pg-bootstrap.ts:783` 卡死**,DMG 永远下不完,导致 native PG 路径永久阻塞
- `~/.multica/server-guard.log` 只 poll `GET /health` 200,误判 server 正常 → 反复拉起 PIDs `55374 / 57448 / 59714 / 83516 / 34180 / 59932`,每个新进程都因为 PG 连不上在 login 路径上 500
- 最终存活 PID `69862` 进程在,但 5432 端口无人监听 → `GetUserByEmail` 拿到 `wrapped ECONNREFUSED`
- `errors.Is(err, pgx.ErrNoRows) == false`(`server/internal/handler/auth.go:312`) → 走 `err != nil && !isNew` 分支(`auth.go:313`) → 返回 `"failed to lookup user"`(**错误信息误导**:实际是 DB 没连,不是用户不存在)

数据完整性:R1 修复前确认 `schema_migrations=153`,`workspace=2`,`issue=164`,`agent=85`,user `jyf` 完好,仅 PG binary tree 缺失。

## 根因(2 段)

### 根因 A:ship 链误删 `~/.multica/pg/17.4/`

0.3.20 → 0.3.21 的 ship 链里某一步把 `~/.multica/pg/17.4/` 的 binary tree 清掉。具体哪一步尚未定位(候选:`make start` 的 stop 钩子误触 / `apps/desktop/scripts/bundle-cli.mjs` 的 `wipe-and-copy` 步骤扩了范围 / 用户手动清理过 cache)。**根因未锁死,待 R2 排查**。当前 ship 链的设计边界:

- `bundle-cli` 只 stage `apps/desktop/resources/pg/`(打包进 asarUnpack),**不应**触碰 `~/Library/Application Support/Multica/pg/`
- 但是 `~/.multica/pg/17.4/` 在 `~/Library/Application Support/Multica/pg/` 之外,走的是另一个路径,推测是 launchd watchdog 或 cleanup 脚本误删

### 根因 B:`downloadAndExtractPg` 的 `fetch()` 卡死

`pg-bootstrap.ts:783` 的 `await fetch(url, { redirect: "follow", signal })` 没有 timeout fallback,sandbox/代理/DNS 任一环节卡住都无重试:

- `signal` 是 `AbortSignal`,但调用链上层没有超时包装
- 没有 download mirror / checksum 二次校验前的退避
- 没有 "DMG 下载失败 → 回退到外部 PG" 的代码路径

→ 永久等待 → 用户侧表现为"应用启动后无响应",实际 PG 仍未就绪 → login 500。

### 根因 C:server-guard 弱验(助燃)

`~/.multica/server-guard.log` 只 poll `GET /health`(`server/internal/handler/health.go` 返回 `{"status":"ok"}`),**不验 DB 连通性**。PG down 时 server 进程仍能启动到 listen 状态,健康检查通过 → guard 反复 spawn 新进程 → 资源耗尽但用户感知不到真正的根因。

## 错误信息误导链

```
PG down (5432 ECONNREFUSED)
  → server 启动但 GetUserByEmail 抛 wrapped error
  → errors.Is(err, pgx.ErrNoRows) == false  (auth.go:312)
  → 落 500 "failed to lookup user"            (auth.go:314)
```

正确诊断需要日志里有 `connection refused` / `pgx` / `ECONNREFUSED` 关键字,纯看 HTTP body 永远误导成"用户不存在"或"DB schema 错"。

## 修法(本次采用)

### 1. brew PG 17.6 symlink 替身(临时)

绕过 fetcher 重下路径,用本机已有 brew PG 17.6 的二进制伪造 `~/.multica/pg/17.4/` 目录:

```bash
BREW_PG="$(brew --prefix postgresql@17)/"   # /opt/homebrew/opt/postgresql@17/
mkdir -p ~/.multica/pg/17.4
for d in bin lib share include; do
  ln -sfn "${BREW_PG}${d}" ~/.multica/pg/17.4/${d}
done
echo "17.4" > ~/.multica/pg/17.4/VERSION    # VERSION 文件 = ngate
```

`pg-bootstrap.ts:533` 上游的 `pgVersionFile()` 检测到 `VERSION` 后,跳过 fetcher,直接走 native `pg_ctl`,PG 正常起。`schema_migrations=153` 完整,login 200 OK。

> **警告**:这是绕过 fetch 死锁的临时修法,不是 ship 链根因的修复。

## 未解事项

1. **ship 链根因未锁死** — `~/.multica/pg/17.4/` 何时被删、谁删的、需要 R2 排查 ship 链每一步的副作用。
2. **fetch() 卡死无超时** — `pg-bootstrap.ts:783` 仍无 `AbortSignal.timeout()` 包装,下次 ship 后同一网络条件下会复发。
3. **错误信息误导未修** — `auth.go:314` 的 `"failed to lookup user"` 在 PG down 时仍然误导,需要把 wrapped err 写进 slog 并在 body 里加 `cause: "db_unreachable"` 之类 hint。
4. **server-guard 弱验** — `~/.multica/server-guard.log` 只 poll HTTP,不验 DB,误判 green。
5. **cache 命中策略** — 没有 "DMG 已下载 + sha256 通过 → 跳过下载" 的强一致校验,manifest 一变就可能重下。

## 预防 contract

```text
[2026-07-14 P0] PG binary tree may be wiped on ship; fetcher can hang.

DO NOT:
- 在 ship 链任一步(含 launchd watchdog / cleanup / pre-update snapshot)
  删除 ~/.multica/pg/17.4/ 下的 bin/lib/share/include。
- 信任 server-guard 的 HTTP /health 200 当作 "DB 正常",PG down 时它也 200。

MUST:
- 触碰 pg-bootstrap.ts / ship 链 / server-guard 前,先读本 incident
  + memory pg-binary-tree-ship-wipe-2026-07-14.md。
- 任何 DMG 下载失败必须可观测(进度 + abort + timeout),不能让 fetch() 静默卡住。
- 修 fetch() 卡死 + 加 download mirror / cache 命中校验 + server-guard 验 DB
  是 0.3.22 ship 的硬要求。
- login 路径的 500 必须把 wrapped err 透到 slog,用户/调试侧能从日志
  一眼看出 "DB 没连" 而不是 "用户不存在"。
```

## 引用

- `apps/desktop/src/main/pg-bootstrap.ts:533` — `await ensureMulticaDb()`(polling 后)
- `apps/desktop/src/main/pg-bootstrap.ts:783` — `await fetch(url, { redirect: "follow", signal })`(fetcher 卡死点)
- `apps/desktop/src/main/pg-bootstrap.ts:708` — `downloadAndExtractPg`(无超时包装)
- `server/internal/handler/auth.go:309-316` — `UsernameLogin` 错误分类
- `server/internal/handler/auth.go:312` — `isNotFound(err)` 判定
- `server/internal/handler/auth.go:314` — 误导文案 `"failed to lookup user"`
- Memory: `~/.claude/projects/-Users-jiangjianyan-jjy-multica-main/memory/pg-binary-tree-ship-wipe-2026-07-14.md`
