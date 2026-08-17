# Release Notes — 0.5.25

**Ship date:** 2026-08-17
**Branch:** `epic/0.5.13-integration`
**Baseline:** 0.5.24 (commit `d54fa0c0e`, packaged 2026-08-16)

---

## Headline: Experimental runtime GC bug actually fixed (was dormant for 8 versions)

While re-investigating lab integration stability, an audit-verification pass surfaced that the **2026-07-28 audit's "RuntimeGC defer bug" claim was aspirational** — no fix commit landed in any version from 0.3.33 through 0.5.24. The 0.5.25 ship delivers the real fix.

3 atomic commits on top of 0.5.24 baseline:
1. `3592725d4` fix(experimental): RuntimeGC never wired + Run() defer bug + regression pin
2. `5fc80ea81` fix(test): TestQuickCreateIssueParentTrustBoundary test isolation (Handler.RuntimeOnlineOverride hook)
3. `b8b362184` docs(root): RuntimeGC entry — bug actually landed 0.5.25, prior claim was aspirational

---

## What's new

### 🔴 P0 — Experimental runtime GC finally works

Three problems combined to make the experimental runtime GC completely non-functional since 0.3.33:

1. **`runtime_gc.go::Run()` eager close** — line 136 called `g.stopOne.Do(func() { close(g.stopped) })` *before* the `for { select { ... } }` loop. The first `select` evaluated `<-g.stopped` immediately and returned. The GC exited without ever ticking. (The "GC never swept" bug from the 2026-07-28 audit.)
2. **`RuntimeGC.Start()` never wired at boot** — `cmd/server/router.go:758` even has a comment "Launch swarm_gc (parallel to runtime_gc.Start pattern)" but only `swarm_gc.Start()` was actually called. The `RuntimeGC` struct was orphaned. Migration 151 documented the 30/90/120-day retention ladder, but the ladder was dormant.
3. **No regression test verified sweep execution** — `runtime_gc_test.go` had 4 tests that smoke-tested or called `sweep()` directly; none verified the actual `Run()` loop fires the ticker and calls `sweep()`.

**Impact**: every `experimental_claude_runtime_session` row (from claude_science_lab research runs) accumulated indefinitely past its 30-day `expires_at`. The fork's local install 0.5.24 had this bug for the entire 0.3.33+ era.

**Fix (commit `3592725d4`)**:
- Drop eager `stopOne.Do(close(stopped))` from `Run()`. `Stop()` owns the close-once contract; `Run()` only reads.
- Wire `RuntimeGC.Start()` in `router.go` alongside `swarm_gc.Start()`. Store on `Handler.RuntimeGC`; `Stop()` in `main.go` shutdown.
- Add `sweepCount atomic.Uint64` to `RuntimeGC` (test observability).
- Add `TestRuntimeGC_RunSweepsBeforeExit` — asserts `sweepCount >= 2` within 60ms at 20ms Interval. **Verified**: test FAILS on pre-fix code ("got 0 sweep invocations"), PASSES with fix.

### 🟡 Test isolation fix (commit `5fc80ea81`)

`TestQuickCreateIssueParentTrustBoundary` was failing 4 subtests in the full handler sweep because `agent_test.go:1051` flips the shared `testRuntimeID` to `'offline'` and its `t.Cleanup` restore can land AFTER this test reads the runtime.

**Fix**: add a `Handler.RuntimeOnlineOverride *bool` test hook. When non-nil, `isRuntimeOnline()` returns the dereferenced value without querying the DB. The test sets it to `true` for its duration and clears it in `t.Cleanup`. Production: always nil.

**Verified**: 5/5 full handler sweeps green (was 1/3 with the defensive-UPDATE attempt).

### Docs (commit `b8b362184`)

CLAUDE.md "Known Stability Surfaces" RuntimeGC entry rewritten to reflect that the 2026-07-28 audit's claim was aspirational, the 0.5.25 commit is the actual fix, and the lesson is to `git log --grep` when an audit says "fixed".

---

## Verification

```
pnpm typecheck                                              6/6 ok
cd server && go test -count=1 -timeout 600s \
  ./internal/... ./pkg/agent/...                             all green (30 packages)
go build ./...                                              exit 0
```

---

## Migration contract

**Zero migrations** — runtime_gc operates on the existing `experimental_claude_runtime_session` table from migration 151.

---

## Risks and follow-ups

1. **First GC tick after deploy** — within 6 hours of first launch, the GC will sweep for the first time. Any expired sessions (>30 days from creation) get archived. A fresh install has nothing to sweep.
2. **Test isolation pattern** — `RuntimeOnlineOverride` is the third test hook on Handler (`HeartbeatScheduler`, `LivenessStore`, `RuntimeOnlineOverride`). A future shared-fixture test should consider per-test runtime fixtures instead of a test-only hook, but the hook is the minimum surgical fix.
3. **Audit verification everywhere** — this bug proved the 0.5.17 lesson ("Audit verification before fix") applies in the inverse direction too: do not believe "fixed" without verification. Memory file `0.5.25-runtimegc-fix-2026-08-17.md` documents the full investigation.