# 0.5.8 — Anthropic SDK "empty / malformed response" 分类修复 (2026-08-03)

## TL;DR

修复 agent task 因 Anthropic SDK 报 `API Error: API returned an empty or malformed
response (HTTP 200) — check for a proxy or gateway intercepting the request`
而被错误归类为 `agent_error.unknown` 的问题。`taskfailure.Classify` 扩展了对
Anthropic SDK EmptyResponseError / APIConnectionError 的识别,让它落入上游已有的
`ReasonAgentProviderServerError` retry allowlist,task queue 能正常 re-enqueue。

## 触发场景

- 用户启动 desktop app,在 dashboard 里把 issue 分配给 agent
- daemon spawn `claude` CLI / Codex 子进程,子进程调用 Anthropic API
- 链路任一跳(gateway / CDN / 代理 / VPN)返回 HTTP 200 + 非 JSON body(HTML login
  page / CDN truncated / 透明代理拦截),Anthropic SDK 在子进程内抛 EmptyResponseError
- daemon 透传 stderr 到 UI,UI 显示
  `API Error: API returned an empty or malformed response (HTTP 200) — check for
  a proxy or gateway intercepting the request`
- **修复前**:taskfailure.Classify 没匹配这条文案,落 `agent_error.unknown`,task queue
  不重试,UI 反复弹出同一条错误直到用户手动重试
- **修复后**:taskfailure.Classify 命中 `empty or malformed response` / `emptyresponseerror`
  / `apiconnectionerror` 三个变体,归类为 `agent_error.provider_server_error`,走
  上游 retry 路径

## Diff 范围

最小修复 — 仅 1 个生产文件 + 1 个测试文件:

| 文件 | 变化 |
|---|---|
| `server/pkg/taskfailure/classify.go` | case 6 新增 3 个关键词 + 注释说明 SDK 错误源 |
| `server/pkg/taskfailure/classify_test.go` | case 6 新增 3 条 fixture(全消息 / class name / 中英混合) |

## 不在本次范围内(已 audit,等下次)

- **`server/pkg/redact` 包恢复**:fork 当前源码缺失此包,但 `internal/handler/daemon.go:29`
  + `internal/service/task.go:25` 都 import 它。已从 upstream 重新拷贝回源码并通过
  `go build ./...` + `go test -race ./pkg/redact/` 验证。**注意**:这不是本次修复的一部分,
  只是顺手把缺失的源码补回来——fork 之前能 build 是因为 0.5.6 拆解遗留,现在必须让
  handler/service 重新能编译。本 release notes 把这个恢复也记下来,避免混淆。
- **`server/internal/selfexec` 包移植**:upstream 有,当前 fork 无源码且无 import,本次
  不动。如果未来 fork 想加 daemon 自解析可执行路径,再从 upstream 拉。
- **daemon prepareTimeout sentinel / errgroup / singleflight** 等并发原语:upstream 后期
  引入,跟本错误无关,不移植(避免引入与 fork 现有 heartbeat 逻辑冲突的回归)。
- **pkg/agent ExecOptions IdleWatchdogTimeout / HandshakeTimeout / ResumeExpected**:
  upstream 后加,跟本错误间接相关(子进程被网关关连接后僵死),但加这些字段需要重写
  claude/codex backend 的 stdin/stdout 协议层,scope 超出 0.5.8 的小修。留 0.5.9 处理。

## 验证

```
cd server && go build ./...                                # 通过
cd server && go test -race -count=1 -timeout 60s \
    -run TestClassify ./pkg/taskfailure/                   # 通过 (3 个新增 case 全过)
cd server && go test -race -count=1 ./pkg/taskfailure/ ./pkg/redact/  # 全部通过
```

## ship chain

按 `make ship-mac`(canonical) 走:
1. `bash ~/.multica/scripts/pre-update-snapshot.sh`
2. `cd server && go run ./cmd/migrate up`(本版本无 SQL 变更,run 也是 idempotent 的 noop)
3. `pnpm --filter @multica/desktop bundle-cli`
4. `pnpm --filter @multica/desktop build`
5. `pnpm exec electron-builder --mac --dir`
6. `cp -R dist/mac-arm64/Multica.app /Applications/`
7. `bash scripts/desktop-sign-nested-binaries.sh /Applications/Multica.app`
8. `bash ~/.multica/scripts/verify-desktop-cold-start.sh`(三检查通过 + row parity)

## 风险

- **低**:`taskfailure.Classify` 是纯字符串分类器,新增 3 个关键词不会改变其他规则的
  优先级(顺序排在 case 6 的 containsAny 里,case 6 已经在 context_overflow / auth /
  quota / capacity 之后),不会触发已有 80+ 条 fixture 的回归。已 `go test -race` 全过。
- **中**:`server/pkg/redact` 恢复属于本次修复的"前置条件"——如果不补,daemon 子进程
  任何 stderr 内容(含 API key / Bearer token / home path)直传到 DB / WS broadcast,
  与 0.5.6 拆解时的"消除实验 flag 直通"原则冲突。已 audit:上游 redact 包的 16 个
  secret 正则 + 嵌套 InputMap 与 fork 当前 import 它的两个调用点语义一致,无回归。