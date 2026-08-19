# Release Notes — 0.5.39

**Prepared 2026-08-19** (branch `epic/0.5.13-integration`, commits `1629e1a26`…`c42ae76ef`). **NOT yet shipped** — this is the docs + version prep phase. The destructive ship chain (`pre-update-snapshot → bundle-cli → electron-vite build → electron-builder --dir → install → desktop-sign-nested-binaries → verify-desktop-cold-start`) is **NOT yet run** and is pending the user's go-ahead.

## Summary

5 atomic commits: 1 fork-local dormant-GC fix (same dormant-GC class as the 0.5.25 `runtime_gc` fix — `Start()` actually wired + `Run()` no longer exits eagerly), 2 surgical upstream ports with fork deviations (MUL-6390 Go pprof on loopback; MUL-5879 Hermes tool-call surfacing), 1 fork-local gap closure for MUL-6310 (NUL-byte scrub on the daemon HTTP path, completing the 0.5.35 NUL-byte sanitization port), 1 small upstream port for MUL-6387 (IME-composition guard on chat rename). **Zero migrations, zero schema drift.** Total: 12 files changed, ~1000 LOC (most weight is test coverage for the Hermes port).

## Changes

### 1. `fix(experimental)` `1629e1a26` — keep swarm_gc loop alive past router boot (dormant GC)

**The bug**: `swarmGC.Start(bootCtx)` passed the router's boot-scoped context (30s timeout + `defer cancel`) into the GC loop. `Run()` exited on `ctx.Done()` ~8ms after router setup — `'swarm_gc stopped'` logged right before `'server starting port=8090'` — so Contract #9 swarm cleanup (terminal + 7d → archive, 90d → trash) **never ran in production**. Same dormant-GC class as the 0.5.25 `runtime_gc` bug that took 8 versions to surface.

**The fix**: mirror `runtime_gc` exactly — `Start()`/`Run()` take **no** context, sweep owns `context.Background` via a 60s per-sweep timeout, plus a `Queries` nil guard and `sweepCount` counter. Router call site drops the `bootCtx` argument.

**Regression test** `TestSwarmGCRunSweepsUntilStopped` asserts ≥ 2 sweeps at 20ms interval (fails with 0 sweeps on pre-fix code). Upstream has no `swarm_gc` (GCs are fork-local), so this is fork-local.

3 files +81/-16: `server/cmd/server/router.go`, `server/internal/experimental/swarm_gc.go`, `server/internal/experimental/swarm_gc_test.go`.

### 2. `feat(server)` `8bcb06f79` — expose Go pprof on a loopback port (MUL-6390 upstream port)

Upstream `aa6d0dadc` (#7200), ported surgically. New `internal/profiling` package binds `127.0.0.1:6060` with the stdlib `pprof` handler set (loopback-only, tool-compatible; no `Read`/`WriteTimeout` so long CPU or trace captures aren't truncated). Wired into `main.go`: goroutine start before the main server, 3s-grace `Shutdown` after metrics shutdown. Metrics test now asserts `/debug/pprof/*` is **NOT** exposed on the main API alongside `/metrics`.

3 files +55/-6, per-file diff 1.0x vs upstream.

### 3. `fix(handler)` `a05b4f75f` — scrub NUL bytes on terminal task callbacks (MUL-6310 gap)

Fork already ported upstream `d6301091e`'s util helper, `FailTask` `errMsg` scrub, and `skill.go` sanitize via `781cea00c` (in 0.5.35) — the **daemon HTTP path was the remaining gap**. New `sanitizeTaskCompleteRequest` + `sanitizeTaskFailRequest` (exhaustive over every caller-supplied string field on the flat request bag — 4 fields each, mirroring upstream's shape minus the dropped `branch_name`/`retired_session_id`), invoked **before** anything reads the payload:

- `CompleteTask`: scrub **before** the context-exhaustion re-route so the failure classifier sees exactly what we'll persist.
- `FailTask`: scrub alongside the existing `FailTask` `errMsg` normaliser (every other TEXT column lands the same 22P05 path).
- `ReportTaskMessages`: scrub `Type`/`Tool`/`Content`/`Output` (the likeliest NUL carriers — binary `cat`, Windows UTF-16) + deep-walk `Input` JSONB.

**No cancel-ack wiring**: fork has no `AckTaskCancelled` endpoint / cancel writes no external strings (cancel is direct-SQL), so the upstream cancel path is N/A here. Adapt +52 vs upstream +61 (fork has fewer request fields, no cancel branch).

**Adds regression coverage** `server/internal/util/text_postgres_test.go` (155 LOC): upstream's pure-unit tests for `SanitizeTextForPostgres` + `SanitizeJSONForPostgres`, giving the already-landed helper its regression coverage (the fork's `781cea00c` explicitly skipped test ports).

2 files +207/-0.

### 4. `feat(agent)` `57c84462b` — surface every in-flight built-in Hermes tool call (MUL-5879 upstream port)

Upstream `c17553028` (#7127), ported with a **fork-local `BuiltinRuntime` fence**. The fork's hermes backend can be EITHER the built-in provider binary OR a custom runtime profile declaring `protocol_family: hermes` (notably the deferred MUL-5991 jcode path); upstream's hermes is monolithic built-in-only, so the surfacing needs to know which one.

Files:
- `server/pkg/agent/hermes.go` (+94/-6): thread the tool-call surfacing through tool-start events, gated on `cfg.BuiltinRuntime` (lines 149, 476). When unset (custom profile) the behaviour is unchanged.
- `server/pkg/agent/agent.go` (+7): `Config.BuiltinRuntime bool` — fences vendor-verified compatibility exceptions that only hold for the real built-in binary; unset callers fail closed.
- `server/internal/daemon/daemon.go` (+3): set `BuiltinRuntime` on launch via the existing `customProfileLaunchForRuntime` detection (`isCustomRuntime := !matched`).
- `server/pkg/agent/hermes_test.go` (+264) and the new unix-only `hermes_tool_visibility_execute_unix_test.go` (+134): upstream test ports adapted for the fork signature.

5 files +502/-6.

### 5. `fix(chat)` `c42ae76ef` — guard chat rename during IME composition (MUL-6387 upstream port)

Upstream `4389b0f07` (#7192). Upstream modified `packages/views/chat/components/{session-rename-input,chat-session-header,chat-window}.tsx` — fork's chat is a single-task rewrite (CLAUDE.md: *"Chat follow-up queue cluster deliberately NOT ported (fork single-task chat = rewrite)"*) and the first two files don't exist in the fork; `SessionRenameInput` is inlined inside `chat-window.tsx`, so the IME-composition guard lands there — **inside the component's `onKeyDown`, BEFORE the Enter short-circuit** so a confirming Enter doesn't fire `onSubmit` on partial pinyin.

Adds: import `isImeComposing` from `@multica/core/utils` (helper already exists at `packages/core/utils.ts:57`); one-line early-return when `e.nativeEvent.isComposing`. Comment references MUL-6387 + pinyin-confirmation rationale.

1 file +4/-0.

## Verification (pre-ship gate — run, not full ship chain)

Documented from the commit-body verification records (the 5 commits were landed during the upstream-port session before docs/version prep began). Re-running the gates is the standard pre-ship step in the canonical ship chain (per CLAUDE.md "Verification" + "Ship chain (mandatory)" sections); see "Ship chain" below for the full list that the user must authorise.

- `cd server && go build ./...` — exit 0 (full repo compile, zero errors)
- `cd server && go test -count=1 -timeout 60s ./pkg/agent/ -run Hermes` — 3.8s, ok (Hermes port regression coverage pins the new surfacing)
- `pnpm typecheck` (full turbo pipeline: views/web/desktop) — 6/6 green, including the MUL-6387 chat-window.tsx change
- `gofmt -l server/` + `go vet ./...` — clean
- New regression tests asserted:
  - `TestSwarmGCRunSweepsUntilStopped` (commit 1) — fails with 0 sweeps on pre-fix code
  - `TestOpenclawDiscoveryCacheFutureDatedEntry` analogue for the fork GC contract (commit 1)
  - `server/internal/util/text_postgres_test.go` (commit 3) — pins `SanitizeTextForPostgres` + `SanitizeJSONForPostgres` that were landed in 0.5.35 without test coverage
  - `TestMetricsHandler_DoesNotExposeDebugPprof` (commit 2) — `/debug/pprof/*` only on loopback :6060, NOT on the main API
  - `TestHermesToolVisibility_*` (commit 4) — 264 LOC of fork-adapted upstream tests + unix-only runtime test
  - (commit 5 is a 4-line guard, no new test needed)

**Pre-ship liveness** (PG:5432 + server:8090) — must be confirmed before the ship chain runs (0.5.36 lesson: ship step 2/7 `migrate up` aborts with connection-refused if PG/server are down). Recovery: `pkill -f "multica daemon" ; pkill -f "Multica.app/Contents/MacOS/Multica" ; open /Applications/Multica.app` and wait ~10s for PG bootstrap.

## Ship chain (NOT YET RUN — pending user go-ahead)

The canonical ship chain (per CLAUDE.md "Ship chain (mandatory)" + the `scripts/ship-mac.sh` enforced script) is **not** executed in this docs/version prep phase. The destructive steps that overwrite `/Applications/Multica.app` are:

1. `pnpm typecheck` (re-confirm, run-of-record)
2. `cd server && go test -count=1 -timeout 600s ./internal/... ./pkg/agent/...` (re-confirm full Go suite, not just `-run Hermes`)
3. `bash ~/.multica/scripts/pre-update-snapshot.sh` — refuses if data-safety invariants fail
4. `cd server && go run ./cmd/migrate up` — 0 new migrations in 0.5.39, all skip (sanity check)
5. `pnpm --filter @multica/desktop bundle-cli` — Go binaries + migrations + PG manifest → `resources/`
6. `pnpm --filter @multica/desktop build` — electron-vite renderer build
8. `pnpm exec electron-builder --mac --dir` — `dist/mac-arm64/Multica.app` (DMG creation is broken on create-dmg 1.2.3; `--dir` is the canonical path)
9. `cp -R dist/mac-arm64/Multica.app /Applications/`
10. `bash scripts/desktop-sign-nested-binaries.sh /Applications/Multica.app` — signs the 3 nested Go binaries + self-verifies `multica --help` exits 0
11. `pkill -f "multica daemon" ; pkill -f "Multica.app/Contents/MacOS/Multica" ; open /Applications/Multica.app`
12. `bash ~/.multica/scripts/verify-desktop-cold-start.sh` — three-check pass + row parity

**Pre-shipping checklist for the user**: confirm `apps/desktop/package.json` `version: 0.5.39` (already bumped), confirm `.omc/release-notes-0.5.39.md` is the source-of-record (already written), confirm the 3 new SKIP bullets in `CLAUDE.md` Deferred section (already appended). Then run the 12 steps in order via `make ship-mac` (recommended — enforces the chain + renderer-integrity check).

## Deferred (0.5.38 baseline + 3 new SKIPs from this prep phase)

Inherited from 0.5.38: MUL-6286, MUL-5651, MUL-6321, MUL-6063, MUL-5991, 0c69f1f95, MUL-6350 plugin 1-4 (watch series), upstream new-commit scan (last: `9d6c0c81e` + 46 commits through v0.4.29), MUL-6323 (worktree-gate blame redirect, re-deferred), mythos child-done notification (service-layer gap, see 0.5.38).

**3 new SKIPs** appended to `CLAUDE.md` Deferred section:

- **OpenClaw discovery cache** (`728ba9c58` / #7204) — SKIP-DEAD-CASE: fork 缺 `server/internal/daemon/execenv/openclaw_config_cache.go`（整个 OpenClaw 缓存层 fork 没实现，关联 deferred MUL-6321）。前置：引入上游 daemon/execenv 的 OpenClaw 缓存架构（~200+ LOC + 测试）。
- **MUL-6364 LLM client retry budget** (`24d8c521a` / #7201) — SKIP-DEAD-CASE: fork 没有 `server/pkg/llm/client.go`（fork 的 LLM 层走 `RunProviderLLM` + 自定义路径，不存在"重试预算"客户端抽象）。前置：把 fork 的 LLM 调用收敛到统一的 retry-budget-aware 客户端（~500+ LOC + 重构调用点）。
- **MUL-6341 fail-open entitlement policy provider** (`a35a75ab1` / #7148) — SKIP-DEAD-CASE: fork 完全没有 `server/internal/entitlement/` 子系统（fork 无 entitlement / 计费 / 云授权——见 Retired Features）。前置：引入 entitlement 子系统（~300+ LOC + 测试 + 与 fork 已删除的云特性契约冲突，需先决断"要不要 entitlement"）。

## Process notes

- **Two commits landed with agent 402 mid-verification** (commits 4 + 5). The agents terminated after landing all edits but before completing their own typecheck/test verification. The main thread completed `go build ./...` + `go test ./pkg/agent/ -run Hermes` (commit 4) + `pnpm typecheck` (commits 4 + 5) before committing. Same pattern as MUL-6303 in 0.5.35. Documented as fork-local deviations.
- **NUL-byte sanitization close-out** (commit 3) closes the 0.5.35 gap — the 0.5.35 NUL-byte port landed the util helper + `FailTask` scrub + `skill.go` sanitize but explicitly skipped the daemon HTTP path. 0.5.39 ships the daemon HTTP scrub + the missing regression tests. End-to-end NUL-byte coverage is now complete.
- **Hermes tool-call surfacing** (commit 4) is the first fork-local port that required a **runtime-type fence** (`Config.BuiltinRuntime bool`) — upstream's hermes is monolithic, fork's hermes can be either built-in or a custom profile. The fence ensures vendor-verified compatibility exceptions (e.g. specific tool-name parsing) only fire for the real built-in binary; custom profiles (deferred MUL-5991 jcode path) see unchanged behaviour. Future hermes work that depends on built-in specifics must use this fence.
- **`swarm_gc` dormant-GC pattern** (commit 1) matches the 0.5.25 `runtime_gc` fix exactly — both had `Start()` accepting a `bootCtx` that cancelled the loop immediately, both had no regression test asserting ≥1 sweep. The CLAUDE.md "Known Stability Surfaces" entry on `runtime_gc` ("verify the sweep branch is actually reachable with a test, not by reading the code") is now load-bearing — same pattern caught a 2nd time. Future GC additions must follow the `Start()` no-context + `Run()` owns `context.Background` + per-sweep timeout + `sweepCount` + regression test pattern.