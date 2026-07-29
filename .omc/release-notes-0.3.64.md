# release-notes-0.3.64 (labs audit fixes — SHIPPED 2026-07-28)

> Status: **SHIPPED 2026-07-28** — `/Applications/Multica.app` 0.3.64
> cold-start verified (three-check pass + exact row parity vs the
> pre-update backup). Ship log: `.omc/0.3.64-ship-2026-07-28.md`.
> Working tree changes on top of `0.3.63` (HEAD `ef7ed4b`). Version
> bumped to 0.3.64 at ship time — this round included one new
> migration (168), so the ship chain ran `migrate up` before
> `bundle-cli`.

Origin: a full read-only audit of the Labs/plugin subsystem
(「全面检查实验室插件功能情况」) graded 9 high-severity, 14
medium-severity findings. This round fixes all 9 high-severity
items. The 14 medium items are documented in the audit report and
deliberately deferred.

## Changes

### P0-A. Lab ↔ assignee mutex realigned to the 0.3.33 narrowed semantics

The narrowed contract (mutex = `mythos_swarm` only; enhancer
REQUIRES assignee; all other labs allow manual assignee + get
leader auto-rewrite) was live on CreateIssue/UpdateIssue but two
surfaces still enforced the pre-0.3.33 any-lab mutex:

- `server/internal/handler/issue.go::BatchUpdateIssues` — replaced
  the broad gate with the narrowed one. Post-state
  (lab/assignee) is computed by overlaying batch fields on
  `prevIssue`; enhancer-ness comes from the persisted
  `prevIssue.LabMode` (batch cannot set `lab_mode`). Violations
  `continue` (per-issue skip) per the batch contract — never 400
  the whole batch.
- `packages/views/issues/components/pickers/lab-picker.tsx` —
  `onClearAssignee` now fires ONLY for mythos_swarm sole (including
  mode-tab switch back to sole). Picking any other lab keeps the
  user's assignee.
- **Batch leader-rewrite parity**: the batch path now runs the
  Active Contract #2 leader rewrite (`resolveLabLeader`, which
  falls through to `experimental.UserPluginLeader` for
  `user_<slug>` keys). Explicit `assignee_type`/`assignee_id` in
  the same batch body wins over the rewrite
  (`batchTouchedType`/`batchTouchedID` guard).

### P0-B. Experimental runtime GC never swept + tarGz stub

`server/internal/experimental/runtime_gc.go`:

- `Run()` called `g.stopOne.Do(func() { close(g.stopped) })`
  eagerly at loop entry instead of deferring it — the first
  `select` hit `<-g.stopped` immediately and the GC exited without
  ever sweeping. Now `defer`red.
- `tarGz` was a placeholder (`os.WriteFile(dst, []byte("placeholder"))`).
  Now a real streaming tar.gz writer: entries relative to the
  session dir's parent, symlinks/irregular files skipped, written
  via tmp + rename so a crash never leaves a half archive that
  `trashSweep` would treat as success.

### P1-1. Panic flag attribution survived LIFO defer unwind

`server/internal/experimental/panic_context.go::WithPanicFlagContext`
cleared the slot with `defer panicFlagContext.Store(nil)` — defers
run innermost-first during unwind, so the slot was wiped BEFORE the
outer recover sentinel could `PopPanicFlagContext`, and blacklist
attribution was always empty. Now the slot is cleared only on
normal return; the sentinel's Pop clears it on the panic path.

### P1-2. Runtime pipe-hang hardening (user plugin + claude science)

`user_plugin_runtime.go` + `claude_science_runtime.go`: a grandchild
of `python3 -I entry.py` inheriting the stdout/stderr pipes could
keep `cmd.Run()` (and the HTTP handler) blocked forever after the
context kill. Both exec sites now set `cmd.WaitDelay = 10 *
time.Second` (project-standard "final backstop", mirrors
claude.go/codex.go) and call `configureRuntimeCmd(cmd)`:

- `runtime_proc_unix.go` (new, `//go:build !windows`) — `Setpgid`
  + `cmd.Cancel` that `syscall.Kill(-pid, SIGKILL)`s the whole
  process group, falling back to single-process kill.
- `runtime_proc_windows.go` (new, `//go:build windows`) — no-op
  (goreleaser builds windows targets).

### P1-3. Migration 168 — user_plugin slug reusable after soft-delete

`migrations/168_user_plugin_slug_partial_unique.{up,down}.sql`:
drops the column-level `user_plugin_slug_key` /
`user_plugin_flag_key_key` UNIQUE constraints and replaces them
with partial unique indexes `WHERE status != 'deleted'`
(`idx_user_plugin_slug_live` / `idx_user_plugin_flag_key_live`).
Previously a soft-deleted plugin permanently squatted its slug.
No code change needed: all `user_plugin.sql` queries already
filter `status != 'deleted'`, and the create handler's pre-check +
23505 → 409 mapping is index-compatible.

### P1-4. Mythos enhancer supervision never completed

`server/internal/service/mythos/supervise.go::tickSupervision` had
an empty if-body where completion should have been decided —
`SubTasksDone` was never written, so every supervised run polled
until the 24h cap. Now: when `run.FinalIssueID` is set and that
issue reaches a terminal status (`done`/`closed`/`cancelled`, new
`isTerminalIssueStatus` helper), the state snaps to `PhaseDone`
with `SubTasksDone = SubTasksTotal`. Read failure of the final
issue is a `slog.Warn`, never fatal to the tick.

### P1-5. Plugin-shell iframe tab sandboxed

`packages/views/experimental/components/plugin-shell-view.tsx`: the
manifest-driven `iframe` tab now carries
`sandbox="allow-scripts"` + `referrerPolicy="no-referrer"` (opaque
origin — plugin-authored content can no longer touch
localStorage/cookies/credential APIs). Matches the existing rule
for html artifacts.

## Tests pinning the old behavior (fixed alongside — 3 total)

1. `issue_lab_source_test.go::TestBatchUpdateIssuesRespectsLabMutex`
   — two subtests used `chat_pin_ui` + assignee to assert a mutex
   skip; post-narrowing that is a legal combination. Switched to
   `mythos_swarm`.
2. `panic_context_test.go` — asserted the slot is empty after a
   panic (the exact bug). Now asserts retention + sentinel pop.
3. `lab-picker.test.tsx` — asserted `onClearAssignee` fires for
   non-mythos labs. Now asserts it does NOT.

Lesson: when a contract changes, grep its tests for pinned
assertions before trusting a green run.

## Verified

- `go build ./...` + `go vet ./...` — clean.
- `go test -p 1 ./internal/experimental/... ./internal/service/mythos/...` — PASS.
- `go test -p 1 ./internal/handler/...` (DB-backed) — PASS.
- `pnpm vitest` — lab-picker (7) + modals (25) — PASS.
- `packages/views` `tsc --noEmit` — 0 errors.

## Files touched

```
server/internal/handler/issue.go                    batch mutex + leader rewrite
server/internal/handler/issue_lab_source_test.go    un-pin old mutex
server/internal/handler/user_plugin_runtime.go      WaitDelay + configureRuntimeCmd
server/internal/handler/claude_science_runtime.go   WaitDelay + configureRuntimeCmd
server/internal/handler/runtime_proc_unix.go        NEW — process-group kill
server/internal/handler/runtime_proc_windows.go     NEW — no-op
server/internal/experimental/runtime_gc.go          defer close + real tarGz
server/internal/experimental/panic_context.go       retain slot on panic
server/internal/experimental/panic_context_test.go  un-pin old semantics
server/internal/service/mythos/supervise.go         terminal-state completion
server/migrations/168_user_plugin_slug_partial_unique.{up,down}.sql  NEW
packages/views/issues/components/pickers/lab-picker.tsx       narrowed clear
packages/views/issues/components/pickers/lab-picker.test.tsx  un-pin old clear
packages/views/experimental/components/plugin-shell-view.tsx  iframe sandbox
```

## Ship checklist (when this round ships)

1. `bash ~/.multica/scripts/pre-update-snapshot.sh`
2. `cd server && go run ./cmd/migrate up` — **must apply 168** (new SQL this round)
3. Decide version bump (schema change → bump to 0.3.64 is warranted, unlike 0.3.63)
4. Standard chain: bundle-cli → build → electron-builder --dir (or manual asar
   repack per 0.3.63 fallback) → `cp -R` → codesign nested binaries → cold-start verify

## Deferred

14 medium-severity audit findings remain open (see the audit
report in the session record). None are data-loss or security
class; schedule as a follow-up round.
