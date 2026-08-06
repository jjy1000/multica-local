# Release Notes — 0.5.11 (2026-08-06)

## Summary

Batch B of the upstream 0.4.19-window integration: **#6437** desktop tab
MRU restore, **#6440 + #6450** per-run token usage on the execution log,
**#6426 + #6464** issue thread navigator, **#6424 + #6435** image-sequence
preview navigation. Four upstream features semantically ported onto the
fork. Zero migrations, zero schema changes.

The upstream chat follow-up queue cluster (#6418/19/30/31 + #6444) was
**deliberately NOT ported** — the fork's single-task chat model is a
simplification the queue subsystem would rewrite, not cherry-pick (4
parallel agents confirmed the architecture mismatch; see
`.omc/0.5.11-ship-2026-08-06.md`).

## Changes (upstream cherry-picks, semantically adapted)

### #6437 — return to the last visited tab on close (MUL-5665)

Closing the active tab used to activate a positional neighbour — opening a
detail tab from a list and closing it dropped the user on whatever sat
next to it in the strip. Tab groups now carry an MRU activation order
(`recentTabIds`); closing the active tab returns to the most recently
visited surviving tab, falling back to the positional neighbour only when
that order is empty. The order is persisted (persist v3 → v4) and
re-validated on rehydration.

### #6440 + #6450 — per-run token usage (MUL-5762)

The execution log lists every agent run on an issue but never said what
any of them cost — `task_usage` has held per-task token counts since
migration 032, nothing surfaced them per run.

- Execution-log header carries the issue total ("2.1M · $4.92") and opens
  a breakdown dialog (`IssueUsageDialog`).
- Each row carries its own token figure (takes the slot the relative
  timestamp held — recency is already free; timestamp moves into the ⓘ
  tooltip).
- The transcript dialog gets the same figure in its header, with the
  input/output/cache split in the run-info popover.
- Backend `ListIssueTaskUsage` joins per-(task, provider, model) rows onto
  the existing task-runs response; cost reuses `estimateCost` so the issue
  and the workspace never disagree.
- No-usage stays distinguishable from zero end to end (omitted → undefined
  → null → em dash).
- **Fork adaptations**: dropped the MUL-4302 attribution hydration (#6440
  upstream consumed it, a separate workstream this fork never integrated);
  adapted test fixtures to fork-priced models (`claude-opus-4-8`,
  `gpt-5.4`); dropped the standalone "Token usage" sidebar section; kept
  `IssueLabsSection` (the fork's read-side lab surface, which upstream
  removed from issue detail).

### #6426 + #6464 — issue thread navigator (MUL-5785 / MUL-5786)

Two navigators over the same derivation — a right-edge rail (ThreadMinimap)
and a header panel (ThreadNavPanel) share one `minimapThreads` list, so
they can never disagree. `@me` threads, resolved/unresolved filters,
search, `Mod+Shift+O` shortcut (pinned state), 14px rail after #6464.

- **Fork dependencies ported**: `packages/core/shortcuts/{platform,store,
  index}.ts` (definitions.ts imported `./platform` but the file was absent);
  wired `configureShortcutPlatform/Runtime` into CoreProvider;
  `shortcut-keycaps.tsx`; the missing `thread_resolved_badge` locale key.
- **Fork adaptations**: dropped the absent in-page-find (`useInPageFind`/
  `FindBar`) dependency; `ActorAvatar` size number; dropped unused
  `pickerNavigationDirection` imports.

### #6424 + #6435 — image-sequence preview navigation (MUL-5752)

Opening any image in an issue or chat starts a sequence: "3 / 7" counter,
chevrons and Left/Right walk to neighbouring images, ends disable rather
than wrap. Images only — PDFs/video/audio/text keep single-file preview.

- `packages/core/attachments/image-sequence.ts`: the ordered sequence for
  `{content, attachments}` blocks, built from data not the DOM (both issue
  timeline and chat list are virtualized). Shared with mobile.
- `ImageSequenceProvider` hosts one viewer per surface and freezes the
  sequence on open, so arriving comments can't shift the index.
- A frame that fails to load is skipped in travel direction with a toast.
- Mobile keeps the semantics with horizontal-swipe paging + same counter.
- The re-sign hook moves to `hooks/use-inline-media-url.ts` so the modal
  can upgrade an auth-gated URL for a navigated-to image.
- **Fork dependencies ported**: `zoom-canvas.css`, `ApiClient
  .getAttachmentBlob`, `zoom-canvas.tsx` + `utils/zoom-transform.ts`.
- **Fork adaptations**: dropped `useLazyEditor`/`useEditorUpload`
  (separate upstream feature); dropped in-page-find block; fixed the
  fork-local CDN test fixture that tripped the new cross-origin re-sign.

## Migration & schema

**None.** Zero new migrations, zero `ALTER TABLE`, zero `CREATE INDEX`.
`migrate up` reports all migrations already applied (fork max 237).

## Packaging impact

- `apps/desktop/package.json` version: **0.5.10 → 0.5.11** (patch bump).
- Everything else (electron-builder config, asar layout, signing) unchanged.

## Verification

- `go build ./...` — OK; `go test ./internal/handler/` — OK.
- `pnpm --filter @multica/{core,views,desktop} typecheck` — OK.
- Mobile `npx tsc --noEmit` — OK (image-sequence port).
- 546 views tests pass (2 issue-detail scroll-to-comment tests are the
  documented pre-existing flakes, confirmed on a clean base).
- Desktop cold start: 0.5.11, ~2s; server PID 65929.
- Row parity: workspace 1 / agent 105 (baseline invariants), issue 294.
- Installed asar contains all Batch B frontend features (ImageSequence 32,
  usage_detail 55, thread_nav 56, recentTabIds 9); server binary contains
  all Batch A literals.

## Commits (since 0.5.10)

```
48fc1cfcb chore(release): bump 0.5.10 → 0.5.11 — Batch B upstream 0.4.19 cherry-picks
7e83e2729 cherry(editor): complete #6424 image sequence fork deps + adaptations
6d868dff9 fix(issues): add task-transcript.css missing from #6440 fork port
e322fe624 fix(editor): MUL-5752 follow-ups — header sequence navigation, snap re-fit (#6435)
bed7064d7 feat(editor): MUL-5752 image preview prev/next navigation (#6424)
204deb390 cherry(issues): complete #6426 thread navigator fork deps + adaptations
f1bab5536 fix(issues): loosen thread navigator spacing onto one 14px rail (MUL-5786) (#6464)
313aee5c3 feat(issues): add header thread navigator to issue detail (#6426)
a9b174763 fix(issues): keep the usage dialog's content inside the dialog (MUL-5762) (#6450)
28b5b6052 fix(issues): repair use-issue-subscribers.test.tsx types (unused import + never[]→IssueSubscriber[])
b39877e06 feat(issues): show per-run token usage on the execution log (MUL-5762) (#6440)
a1f612288 cherry(desktop): return to the last visited tab on close (MUL-5665)
```

## Not ported (deliberately)

- **Chat follow-up queue cluster** (#6418/19/30/31 + #6444, MUL-5750/5751/
  5760). The fork's chat is a deliberate single-task model: `ChatPendingTask`
  has 3 fields, no `supports_queue`/`queued_tasks`, no ChatQueue/chat-page/
  use-chat-controller components, no `ListPendingChatTasksForSession`/
  `DirectChatSendResult` server plumbing, no `channel_ingested` column.
  Porting = rewriting a 1000+ line subsystem. The only extractable subset
  (task-status-pill `deferred`/`retrying`) is dead code in the fork today
  (`GetPendingChatTask` excludes `deferred`; no fork path produces it).
