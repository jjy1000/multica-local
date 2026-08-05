# Release Notes — 0.5.9 (2026-08-05)

## Summary
Zero-migration cherry-pick integration of upstream `multica-ai/multica` 0.4.18 window
safety / perf fixes. Five server-only Go patches. No schema change, no version
bump on the running app until you ship this version.

## Changes

### Cherry-picked from upstream 0.4.18 window (4 PRs)
- **#6222 — fix(realtime): bound inbound WebSocket message size** (`MUL-5569`)
  gorilla/websocket buffered a whole inbound message before any application
  check, so a fragmented frame with interleaved pongs kept the read deadline
  alive and grew the buffer without bound — a single connection could OOM the
  process. Set `SetReadLimit(64 KiB)` immediately after upgrade (covers the
  pre-auth token frame), distinguish `ErrReadLimit` closes from ordinary churn
  via a new `inbound_too_large_total` counter.

- **#6279 — fix(daemon): honour HTTP(S)_PROXY when dialing the wakeup WebSocket**
  Hand-built `websocket.Dialer` left `Proxy == nil`, so behind a corporate
  egress proxy the daemon's control handshake always failed and silently
  degraded to 30s HTTP polling. Set `Proxy: http.ProxyFromEnvironment`; with no
  proxy vars set, behaviour is byte-for-byte the direct dial.

- **#6175 (broadcast half) — fix(server): stop double-broadcasting descriptions on `issue:updated`**
  `prev_description` / `prev_title` existed only for in-process listeners
  (mentions diffing, title-change activity), but the WS forwarder reused the
  producer's payload verbatim. Every debounced autosave broadcast TWO full
  copies of the description to every connection in the workspace (including
  users who hadn't opened the issue). Added `projectOutbound` / declarative
  `internalOnlyPayloadKeys` table so the boundary is reviewable.

- **#6175 (timeline-cap half) — fix(timeline): cap the issue timeline at the newest end**
  `ListCommentsForIssue` and `ListActivitiesForIssue` used `ORDER BY created_at ASC LIMIT N`,
  so once an issue had more than N comments/activities the cap discarded the
  NEWEST rows with no indication anything was missing. Activity is machine-paced
  (description autosave, agent runs, status/assignee), so this was reachable in
  normal use. Inner DESC + outer ASC re-sort — same shape as upstream but no
  thread-completion refactor, no migration. Sorted by existing `idx_*_keyset`.

### Fork-specific adaptation
- **`runTaskWakeupConnection` signature**: upstream returns `(time.Duration, error)`,
  fork returns `error` (single-error form already chosen by an earlier fork revision).
  Child-half test adapted accordingly; the proxy fix itself is identical.

- **`/tmp` upstream fetch unavailable during this session** (network unstable to
  github.com). Patches generated via `git format-patch` against the local
  `/tmp/multica-upstream` clone; go.work was rewritten to `use ./server` (relative)
  so worktrees build their own module instead of accidentally inheriting main's.

## Explicitly NOT cherry-picked (with reason)

- **#6309 (coalesced fallback)** — fork has no `CoalescedCommentIDs` branch in
  `prompt.go`; the comment-reading pointers were already extracted into
  `execenv/reply_instructions.go` (`BuildNewCommentsHint`/`BuildColdCommentsHint`).
  Both are bounded reads (`--thread --tail 30`, `--recent 10`). The bug B17
  fixed at upstream does not exist in this fork's prompt architecture.

- **#6230 (workspace deletion perf)** — requires 18 new sqlc queries +
  `workspace_delete.sql` + migrations 242-247. Migration 242 extends
  `runtime_profile.protocol_family` CHECK with runtime names this fork has no
  adapter for (`qoderclicn` etc) and migrates the trigger guards. Pulling these
  would change packaged-app cold-start behaviour (the user requirement was
  zero impact on the running app).

- **#6152 (solid-tone tokens full migration)** — fork's `tokens.css` already
  defines `--faint-foreground` (0.5.1 type-scale port). The remaining 195 alpha
  call-sites are progressive polish with non-trivial visual-regression risk;
  left for a dedicated UI pass.

- **#6187 / #6223 / other UI cherry-picks** — touch files/directories fork has
  already deleted (`packages/views/issues/surface/`, etc) or rely on a
  0.4.x-only component that's not in this fork.

## Migration & schema
**None.** Zero new migrations, zero `ALTER TABLE`, zero `CREATE INDEX`.
Verified: `git diff 62ce9bd..HEAD --stat -- server/migrations/` is empty.

## Packaging impact
- `apps/desktop/package.json` version: **0.5.8 → 0.5.9** (patch bump).
- Everything else (electron-builder config, asar layout, signing) unchanged.
- `desktop-sign-nested-binaries.sh` is the only post-`electron-builder --dir`
  step that actually touches the produced `.app` (mandatory per 0.3.62 lesson).

## Verification
- `cd server && go build ./...` — OK
- `cd server && go test ./...` — 39-40 packages OK (one `TestQuickCreateIssueParentTrustBoundary`
  flake confirmed pre-existing via 3× isolated runs and `git diff 62ce9bd..HEAD -- server/internal/handler/` empty)
- `pnpm typecheck` — 6/6 packages OK
- 5 new tests added: `#6222 TestHandleWebSocket_RejectsOversizedFrameBeforeAuth` +
  `TestReadPump_AcceptsFrameUnderReadLimit`, `#6279 TestTaskWakeupDialUsesEnvironmentProxy`,
  `#6175 TestIssueUpdatedBroadcast_OmitsFullPreviousDescription` +
  `TestProjectOutbound_DoesNotMutateProducerPayload` +
  `TestProjectOutbound_PassesThroughUnlistedEvents`.

## Commits (since 0.5.8)
```
4880c92  chore(release): bump 0.5.8 → 0.5.9 — 4 upstream cherry-picks + go.work fix
89cfa99  fix(timeline): cap the issue timeline at the newest end, not the oldest
2d94062  cherry(server): #6175 — stop double-broadcasting descriptions on issue:updated
0b2d5be  cherry(daemon): #6279 — honour HTTP(S)_PROXY when dialing the wakeup WebSocket
58b48b1  cherry(realtime): #6222 — bound inbound WS message size (MUL-5569)
324a3c3  chore(go): make go.work use relative path so worktrees build their own server
```
