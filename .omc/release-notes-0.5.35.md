---
name: release-notes-0.5.35
created: 2026-08-18T11:50:00Z
updated: 2026-08-18T11:50:00Z
---

# 0.5.35 Release Notes — Cherry-Pick Batch (2026-08-18)

Surgical upstream cherry-pick batch — 4 atomic commits landed, 6 deferred due to structural divergence. MUL-6243 UI close-out deferred to 0.5.36.

## Highlights

### MUL-6303 — `9b9b588cf` fix(desktop): keep workspace singleton across same-workspace tab swap
- `apps/desktop/src/renderer/src/components/workspace-route-layout.tsx` + test
- Ownership-checked `useEffect` cleanup — workspace singleton now survives sibling tab swaps referencing the same slug
- Eliminates unnecessary WS reconnects + dashboard flicker
- **Fork deviation**: dropped upstream's `isWorkspaceDeletePending` guard (fork has no `@multica/core/workspace/pending-delete` module); equivalent deletion guard lives in `useDeleteWorkspace` + tab-store. If a delete-pending race surfaces, add a fixup commit wiring the fork-local equivalent.

### Update notification changelog link — `e2b8a6852` fix(desktop)
- "Later" button on update prompt now opens the changelog URL via `window.desktopAPI.openExternal` instead of silently dismissing
- Reuses fork's existing `changelogUrl(state.version)` helper

### OpenClaw JSON error handling — `0ef9b9381` fix(execenv)
- `server/internal/daemon/execenv/openclaw_config.go` — `execOpenclawCLI` now returns the raw stdout on error and annotates the failure
- New helpers: `annotateOpenclawJSONError`, `openclawJSONErrorMessage`, `isOpenclawKeyMissingResult`, `isOpenclawKeyMissingMessage`
- **Fork gap**: upstream's `openclawShimDiagnostic` fallback (added in MUL-6321) not wired — MUL-6321 skipped (29 files, structural divergence). Close the gap when MUL-6321 lands later.

### NUL byte sanitization — `781cea00c` fix(agent tasks)
- `server/internal/util/text.go` — new `SanitizeTextForPostgres` + `SanitizeJSONForPostgres` (depth-bounded at 32, all keys sanitized)
- `handler.sanitizeNullBytes` now delegates to the util helper (single source of truth)
- `service.task.FailTask` sanitizes `errMsg` on entry so failure diagnostics are stored cleanly
- **Fork deviation**: `CancelTaskWithResult` opt-fields sanitization (upstream N/A) — fork signature is `(ctx, taskID)` with no opts. The `/cancel-ack` handler in `daemon.go` will need separate wiring if a MUL-6321/MUL-6063 follow-up lands.

## Skipped (deferred to 0.5.36+)

| Upstream | Reason |
|---|---|
| MUL-6140 (`8d9766d4b`) | Depends on MUL-5181 (14-file unified draft lifecycle refactor) — not in fork |
| MUL-6323 (`5f4ba41d1`) | 28 files TS+Go mixed; `local-directory-mode-dialog.{tsx,test.tsx}` deleted in fork |
| MUL-6333 (`f320af4a5`) | Test file `agent-transcript-dialog.test.tsx` deleted in fork 0.3.33 inventory |
| MUL-6321 (`4d5a679b9`) | OpenClaw slow hosts — 1610 LOC / 29 files / 3 files deleted in fork (above thrash threshold) |
| MUL-6063 (`afd05ca22`) | Terminal delegated task failures — 2616 LOC / 18 files (above thrash threshold) |
| MUL-6233 RELAND (`1b6d42864`) | **Audit verdict: RELAND-UNSAFE**. Reland is byte-identical to original; only "safe" because upstream shipped a separate MUL-6293 tab-host fix that's NOT in the fork. Skip until upstream lands a reland with actual safety guards (`isComposing` + `PRIMARY_RESERVED_KEYS` audit). |

## Verification
- `pnpm typecheck --force` 6/6 (51.9s)
- `go build ./...` exit 0
- Pre-existing `TestTickSupervision_CompletionByFinalIssueStatus` panic verified byte-identical on 0.5.34 baseline (`f1f5143ff`) — NOT caused by this port. Documented in CLAUDE.md Known Stability Surfaces (root cause: `db.New(nil)` returning non-nil struct with nil inner DBTX; pre-0.3.62 pattern).
- Ship chain: snapshot → migrate (no new) → bundle-cli → electron-vite → electron-builder `--dir` → install + re-sign → cold-start 6s, Server PID 64572, Info.plist 0.5.35

## Deferred to 0.5.36 (MUL-6243 UI close-out, scope risk)

- `e3a40b0b8` `feat(MUL-6243): custom issue status UI — picker, board, filters, settings` (96 files / +5186/-310 = 5476 LOC)
- `14c2e4e83` `MUL-6243 feat(issue-status): board fetches by category` (52 files / +1402/-249 = 1651 LOC)

Combined: **148 files / +6588/-559 = 7147 LOC** — 2x larger than the 0.5.33 backend port that thrashed 4 times. Requires a dedicated session with the main-thread commit pattern. Note: a substantial portion of `packages/core/*` and `server/internal/issuestatus/*` work is already in the fork from 0.5.33-34, so the actual fork diff will be smaller than upstream stats suggest, but still too large for one-session delivery.

## Process notes

- Small-scope cherry-pick batch (~370 LOC total) completed cleanly via parallel agents + main-thread commit step (per 0.5.33 lesson). No thrash.
- `git cherry-pick` of feature-branch atomic commits to main worked cleanly because all branches shared the same parent (`f1f5143ff`).
- The `git stash` dance for the 0.5.34 baseline verification left two testdata fixtures modified (absolute symlinks). Restoration: `git checkout HEAD -- <path>`. No real diff.

Related memory: `0.5.35-mul6303-and-cherry-pick-batch-2026-08-18.md` (forthcoming).