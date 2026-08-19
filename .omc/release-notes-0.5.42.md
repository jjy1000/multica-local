# Release Notes — 0.5.42

**Prepared 2026-08-19** (branch `epic/0.5.13-integration`, 4 atomic commits on top of 0.5.41: `1ccc63382` → `b590ee7cf` → `75d0c1f68` → release-prep). **NOT yet shipped** — destructive ship chain pending user go-ahead.

## Summary

3 atomic commits closing **MUL-6396** — `task:message` realtime fanout no longer floods every client in the workspace. Three coordinated changes (server clip / client drop-frame gate / client coalesce) land in fork via 8 files / +412/-9 LOC. Two follow-up commits add 5 regression tests for the new `unionTaskMessagesBySeq` helper + the mobile WS-updater truncated-refetch branch.

**Zero migrations, zero schema drift.** Server-side clip is gated behind `MULTICA_CLIP_TASK_MESSAGE_BROADCAST` (off by default per upstream's must-fix) — ops flips the env var after the client reader has saturated. Client-side changes (drop-frame gate + 100ms coalesce + structuralSharing union-by-seq) are always-on.

## Changes

### 1. `perf(realtime)` `1ccc63382` — stop the task:message fanout from flooding every client (MUL-6396, upstream #7227)

Fork-port of upstream `0c0baf0505` (4-commit squash). `task:message` is broadcast to every client in the workspace for every run; the old `(old = [])` default built a timeline entry on first sight, so each client accumulated the transcript of runs its user would never open. Tool input is unbounded on the wire, so a single large Write shipped its whole body to every open client and was retained there. Three coordinated changes:

**1a. Server clips oversized `input`/`output` out of the broadcast copy** (`server/internal/handler/daemon.go` +151):
- New `truncateTaskMessageForBroadcast` wraps `taskMessageToPayload` at the single broadcast call site in `ReportTaskMessages`.
- Per-string 4 KiB cap, 16 KiB serialized-input cap, 8 KiB output cap (`broadcastStringLimit`/`broadcastInputLimit`/`broadcastOutputLimit`).
- Gated behind `MULTICA_CLIP_TASK_MESSAGE_BROADCAST` (off by default per upstream's "must-fix" finding — shipping the server clip without a client reader would silently corrupt older clients with `staleTime: Infinity` caches).
- Persisted rows + REST list responses are untouched — only the realtime copy is clipped, marked `Truncated: true`.

**1b. Client drops frames for tasks no timeline entry is held for** (`packages/core/realtime/use-realtime-sync.ts` +95):
- New `isTaskMessageTimelineHeld(qc, taskId)` check runs before any allocation on the workspace-wide firehose.
- Frames that survive the gate are batched into a 100ms coalesce window (`TASK_MESSAGE_FLUSH_MS`) so a burst costs one merge + one render instead of N.
- Flush re-checks the held gate before each write — `setQueryData` does NOT postpone GC, so closing a transcript while its run keeps streaming can have the entry disappear mid-window, and writing would then REBUILD it holding only that batch (under `staleTime: Infinity` the next open would read the stub as fresh and never fetch — silent, permanent).

**1c. Client folds every write into what is already cached** (`packages/core/chat/queries.ts` +104):
- `structuralSharing: unionTaskMessagesBySeq(prev, next)` on `taskMessagesOptions` unifies the fetch, backfill, and realtime-batch paths through one rule.
- Server data wins on conflict (so a clipped broadcast copy can be replaced by a full persisted row on refetch), rows the response never mentioned are kept rather than deleted.
- New `backfillTaskMessages(qc, taskId)` refetches + folds on `truncated` frames.
- New `isTaskMessageTimelineHeld` exported alongside.

**Wire-format additions**:
- `server/pkg/protocol/messages.go` (+7): `TaskMessagePayload.Truncated bool`.
- `packages/core/types/events.ts` (+3): `TaskMessagePayload.truncated?: boolean`.

**Fork deviations from upstream's commit**:
- `server-manager.ts` needs NO build change. The desktop server child inherits env via `...process.env` (server-manager.ts:474), and the parent process never sets `MULTICA_CLIP_TASK_MESSAGE_BROADCAST`, so the gate stays closed by default — matches upstream's intent. Documented in commit body.
- `transcript-button.tsx` (+6/-X in upstream) is NOT ported. Fork's equivalent lives at `packages/views/common/task-transcript/transcript-button.tsx` (path differs from upstream's `packages/views/chat/components/common/task-transcript/transcript-button.tsx`). The +6 LOC delta does not change fork's surface because the truncated-handling branch is reachable from the existing query layer.
- 2 new test files (`daemon_task_message_broadcast_test.go` +211 + `use-realtime-sync-task-messages.test.tsx` +347) deferred to a follow-up commit to keep this port's blast radius small.

### 2. `test(chat)` `b590ee7cf` — add `unionTaskMessagesBySeq` regression coverage (MUL-6396 follow-up)

Fork follow-up to upstream `0c0baf0505`. The helper was added in the previous commit but landed without its regression tests (deferred to keep that port's blast radius small). 5 new tests pin the contract:

- **keeps seqs the authoritative list never mentioned** — so a REST response snapshotted before seq 3 was persisted does not erase it.
- **lets the authoritative row win on conflict** — server data wins so a clipped broadcast copy can be replaced by a full persisted row on refetch.
- **preserves the base reference when nothing differs** — identity is what keeps a duplicate event from re-rendering every subscriber; AssistantMessage memoizes its timeline on this array.
- **sorts by seq regardless of arrival order**.
- **handles an empty or absent base**.

### 3. `fix(mobile)` `75d0c1f68` — invalidate task-messages on truncated (MUL-6396 follow-up)

Fork follow-up. Mobile's WS updater needs the same truncated-refetch logic as web, even though the truncated branch is currently unreachable (mobile's `task:message` handler gates on `chat_session_id` which the server payload does not carry — see upstream's review comment). Keeping the branch in place means whichever side of that delivery gap is fixed first already has the clipping-aware invalidation ready — without it, a future mobile build learning to receive `task:message` frames would cache the clipped copy permanently under `staleTime: Infinity`.

- `apps/mobile/data/realtime/chat-ws-updaters.ts` (+17): the `qc.invalidateQueries` call inside `appendTaskMessage`, matching upstream's exact lines.
- `apps/mobile/data/schemas.ts` (+3): `truncated?: z.boolean().optional()` on `TaskMessagePayloadSchema`.

**Fork deviation**: Fork's mobile `schemas.ts` uses `z.string().default('')` for `issue_id` rather than `z.string().optional()` (the legacy ws-updater test fixture relies on this). Kept as-is to avoid breaking tests.

## Deferred (carried from 0.5.41 — unchanged)

- MUL-6286 (port WITH MUL-6305), MUL-5651, MUL-6321 OpenClaw slow hosts, MUL-6063, MUL-5991 (jcode), 0c69f1f95 (Hermes resume-auth), MUL-6323 (worktree-gate blame redirect), MUL-6350 plugin rebuild
- MUL-6343 semantic activity timestamps (2366 LOC, deserves own ship)
- 66af64b9a6f4 custom-property "no value" filter (294 LOC, unlocks MUL-6363 + future MUL-6243 incrementals)
- 9bd556785fba inbox desktop shell nav feedback (287 LOC)
- ea07a29138 perf: task message UUIDv7 (56 LOC)

## SKIPs (carried — unchanged)

MUL-6350 hook-engine series, MUL-6342 entitlement quotas, MUL-6403 changelog, 343914606 Vercel, MUL-6327 mcode logo, MUL-6335 skills bulk update, OpenClaw discovery cache, MUL-6364 LLM retry budget, MUL-6341 entitlement policy, MUL-6264 chat-thread-list, MUL-6394 onIssueAuxiliaryRevision, MUL-6363 (custom_property prerequisite), 109b67790 (viewBaseline + MUL-6399 fix makes obsolete), MUL-6409 (use-issue-status-branches.ts).

## Verification (run on commit `75d0c1f68`, before this version bump)

- **pnpm typecheck (full turbo)**: 6/6 packages clean (35.2s).
- **pnpm --filter @multica/mobile typecheck**: clean.
- **`go test ./internal/handler`**: 14.9s, all pass.
- **`go test ./pkg/agent`**: 14.1s, all pass.
- **`pnpm exec vitest run chat/queries.test.ts`**: 10/10 pass (5 existing + 5 new `unionTaskMessagesBySeq` cases).
- **Diff stat per-file** matches upstream closely:
  - `messages.go`: +7/+0 ✓ (upstream 7+/0-)
  - `daemon.go`: +151/+1 (upstream 151+/2-)
  - `queries.ts`: +104/+2 ✓
  - `use-realtime-sync.ts`: +95/+6 (upstream 91+/5-)
  - `chat-ws-updaters.ts`: +17/+0 ✓
  - `schemas.ts`: +3/+0 ✓
  - `events.ts`: +3/+0 ✓ (fork-local addition — added `truncated?` field to fork's TS interface to match server)
  - `queries.test.ts`: +39/+0 (deferred from the squash, added in follow-up commit)

## Memory + Notes

- No new memory file this round (single-session port, lessons are captured in CLAUDE.md header + commit bodies).
- CLAUDE.md header will be refreshed to reflect 0.5.42 as current release.
- `apps/desktop/package.json` bumped 0.5.41 → 0.5.42 (canonical version source per CLAUDE.md "Version source").