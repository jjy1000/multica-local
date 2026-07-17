# Multica 0.3.18 — Labs Safety Net

## TL;DR

0.3.18 给 Labs 实验性功能加了一个**保险机制**:flag 触发的代码路径出现异常时,系统自动把该 flag 加入**本地黑名单**,下次启动 Multica 时完全 bypass 该 flag 的代码,直到用户在 Labs → 「重新启用」按钮 + 重启 Multica。

| 异常类别 | 检测位置 | 写入原因 |
|---|---|---|
| panic | main.go recover sentinel | `panic` |
| 5xx burst(3 次 / 60 秒) | middleware | `5xx_burst` |
| init 超时(30s 默认) | main flag init hook | `init_timeout` |

黑名单存在 `~/.multica/experimental-blacklist.json`,与 Go server + TS main 双方互相同步。

## 数据流

```
runtime panic → main.go recover() → experimental.MarkBroken(flag_key, "panic")
                                       ↓
                              ~/.multica/experimental-blacklist.json (atomic write)
                                       ↓
                              next launch: experimental.DefaultFor(flag_key) = false
                                       ↓
                              所有引用 DefaultFor 的地方自动 bypass

5xx burst → middleware.ExperimentalFlagBurst → 3 次 5xx → MarkBroken("5xx_burst")
init timeout → experimental.RunWithTimeout → deadline 到 → MarkBroken("init_timeout")
```

## 关键文件

| 新文件 | 用途 |
|---|---|
| `server/internal/experimental/safety.go` | 黑名单 file format + Load/Save/MarkBroken/ClearBroken/IsBroken |
| `server/internal/experimental/safety_test.go` | 11 个 test:roundtrip / sort / atomic / concurrent / schema mismatch / JSON shape |
| `server/internal/experimental/panic_context.go` | SetPanicFlagContext / PopPanicFlagContext / WithPanicFlagContext |
| `server/internal/experimental/panic_context_test.go` | 5 个 test |
| `server/internal/experimental/init_watchdog.go` | RunWithTimeout(flag, name, hook) |
| `server/internal/experimental/init_watchdog_test.go` | 4 个 test |
| `server/internal/middleware/experimental_burst.go` | 5xx 累计 + flag context 识别 |
| `server/internal/middleware/experimental_burst_test.go` | 6 个 test |
| `apps/desktop/src/main/experimental-safety.ts` | TS 镜像:loadBlacklist / saveBlacklist / clearBroken / loadBrokenFlagEntries |
| `apps/desktop/src/main/experimental-safety.test.ts` | 8 个 vitest |

| 修改文件 | 改动 |
|---|---|
| `server/internal/experimental/catalog.go` | `DefaultFor` 加 blacklist check:IsBroken → false |
| `server/cmd/server/main.go` | panic recover sentinel 调 MarkBroken |
| `server/cmd/server/router.go` | `Use(middleware.ExperimentalFlagBurst(...))` |
| `apps/desktop/src/main/index.ts` | IPC `experimental-safety:list` + `:clear` |
| `apps/desktop/src/preload/index.ts` | `experimentalAPI.safety.list()` + `clear()` |
| `packages/views/settings/components/labs-tab.tsx` | broken badge + 「重新启用」按钮 + 自动 disable Switch |
| `packages/views/locales/{zh-Hans,en}/settings.json` | `labs.restore_button` + `labs.restored_toast` |
| `apps/desktop/package.json` | version 0.3.17 → 0.3.18 |

## 防呆 / 防漂移

1. **writeMu** 串行化 MarkBroken/ClearBroken/Save,防止并发写文件破坏。
2. **atomic write** (tmp + rename) 保证崩溃中写不会损坏文件。
3. **schema version** 字段:未来 schema 改可检测版本不匹配。
4. **fail-open on read error**:黑名单损坏不清空所有 flag,只是这一次的 read 返 false,后续操作继续。
5. **panic context 是 atomic.Pointer**:`WithPanicFlagContext` defer 清理 slot,避免跨 goroutine 残留。
6. **保存原子性 + UUID tmp 后缀**:并发 recover 不撞车。
7. **5xx burst 单次窗口内仅 MarkBroken 一次**(`notified` 字段),不污染黑名单文件。

## 不做的事(用户决策)

- **不自动恢复**:flag 被 break 后用户必须手动 Restore + 重启。原因:被 break 通常意味着同样的根因还在(网络、缺依赖、corrupted row),自动 reload 只会再次触发。
- **fail-open 不阻断业务**:黑名单读失败时只 log,业务继续。这是与「blacklist corrupted → disable everything」反着来的,符合稳定性优先。
- **不存 DB**:本地文件简单、不依赖 DB 可用性、跟 desktop 单进程模型一致。

## Verification (live DMG end-to-end)

```bash
$ defaults read /Applications/Multica.app/Contents/Info CFBundleShortVersionString
0.3.18

$ lsof -nP -iTCP:5432 -sTCP:LISTEN | tail -1
postgres  93471 ...  TCP 127.0.0.1:5432 (LISTEN)

$ lsof -nP -iTCP:8090 -sTCP:LISTEN | tail -1
server  96111 ...  TCP *:8090 (LISTEN)

$ curl -s http://localhost:8090/health
{"status":"ok"}

$ psql -c "SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 3"
150_experimental_resource_visibility
149_mythos_run
148_claude_science_experimental_lock

$ ls ~/.multica/experimental-blacklist.json
(no file — default empty blacklist)
```

Go tests: 全部 26 packages PASS,新增 26 个 safety 相关 test。`pnpm typecheck` 6/6 PASS。

## 用户行为

1. **flag 正常工作**:黑名单空文件,行为不变
2. **flag panic** → main 退出前 MarkBroken → 下次启动 flag off,UI 出现红色 badge + 「重新启用」按钮
3. **flag 连续 3 次 5xx** → middleware MarkBroken → 同上
4. **flag init 超时 30s** → watchdog MarkBroken → 同上
5. **用户点「重新启用」** → IPC clearBlacklist → toast「已恢复,请重启 Multica」

## Prevention contract additions

- **所有 MarkBroken 调用点必须原子**:并发调用必须经过 `writeMu`,测试 `TestConcurrentMarkBrokenSafe` 锁住
- **新增 SafetyReason 必须同时改 3 处**:catalog test、Reason enum、JSON wire shape
- **Panic context slot 必须 defer 清理**:用 `WithPanicFlagContext` 而非 `SetPanicFlagContext` 单独调用,避免 panic 后 slot 残留导致下次 panic 误标
- **5xx burst middleware 必须 dedupe 写**:每次请求都 MarkBroken 会写烂文件;`notified` 字段保证窗口内仅写一次
- **runtime panic → main recover → MarkBroken** 路径必须在 main 最顶层 defer 中,**不能在子 goroutine 内**(否则未捕获 panic 会绕过)

## 延期到 0.3.19

- **multi-profile safety isolation**:每个 profile 有自己的黑名单(目前所有 profile 共享 `~/.multica/experimental-blacklist.json`)
- **黑名单远程同步**:同一用户多设备间同步(目前只在本地)
- **更细粒度的 reason 分类**:如 `5xx_burst:database_timeout` / `5xx_burst:network_error`,便于根因分析
- **UI 引导诊断**:点 broken badge 不只 Restore,还显示 stack trace + 最近 N 条相关日志

## Critical files

1. `server/internal/experimental/safety.go` — 黑名单核心
2. `server/internal/experimental/panic_context.go` — panic 归属
3. `server/internal/experimental/init_watchdog.go` — init 超时
4. `server/internal/middleware/experimental_burst.go` — 5xx 累计
5. `server/cmd/server/main.go` — recover sentinel
6. `server/cmd/server/router.go` — burst middleware 挂载
7. `apps/desktop/src/main/experimental-safety.ts` — desktop 镜像
8. `packages/views/settings/components/labs-tab.tsx` — broken UI
