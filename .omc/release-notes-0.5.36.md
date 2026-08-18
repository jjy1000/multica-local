---
name: release-notes-0.5.36
created: 2026-08-18T21:50:00Z
updated: 2026-08-18T21:50:00Z
---

# 0.5.36 Release Notes — MUL-6243 UI Close-Out + Cherry-Pick Batch (2026-08-18)

**MUL-6243 is now fully closed out end-to-end**: backend (0.5.33) + TS/CLI (0.5.34) + board-by-category foundation (0.5.36 Task 6) + picker/board/filters/settings UI (0.5.36 Task 7). Custom per-workspace issue statuses are usable from every surface.

## What landed (7 atomic commits on `epic/0.5.13-integration`)

### Cherry-pick batch (from 0.5.35 deferred + new)

| Commit | Subject |
|---|---|
| `1d2ba1d3f` | MUL-6300 — daemon reply turns own the status arc for their own issue |
| `906b07c5d` | MUL-6334 — bill cache reads in daily and weekly cost charts |
| `7006b90ea` | gpt-5.6-sol pricing (fork-local adaptation for MUL-6334 tests) |
| `878ed6942` | MUL-6272 — command palette Pages group from nav registry |
| `5eb1f471c` | nav-registry self-referential type cycle fix |
| `16c8d6955` | MUL-6243 — board fetches by category; archive retires without migration |
| `7767b3f65` | MUL-6243 — custom issue status UI: picker, board, filters, settings |

### MUL-6243 UI close-out detail (`7767b3f65` + 2 surgical fixups)

**Main-thread surgical port** (agent died mid-flight on API 429 quota). 96-file upstream commit ported fork-true:

- **Dropped** (fork has no upstream `surface/` layer, table view, or quick-actions/properties): 15 DU files + surface test files + `issue_table_status_category_test.go` (references fork-missing `issueTablePageRequest`)
- **Reverted to fork versions**: issue-actions-menu-items, inbox-list-item, mention-suggestion, search-command (upstream refs fork-missing helpers)
- **Semantic type fixes**: `BOARD_STATUSES → IssueStatusCategory[]`, byStatus buckets keyed by category (`statusCategoryOfKey`), `issueBehavesAsAny` for done detection, `useStatusOptions` catalog-driven filter
- **New files ported**: `issue-statuses-tab.tsx` (678-line settings CRUD page), `color-picker.tsx` + `color-utils.ts` + `settings-layout.tsx`, `status-label.ts` + `status-options.ts`, `custom-status-chip.tsx`, `mutations.ts` (client), `color_picker` locale keys × 4 languages
- **client.ts**: merged duplicate 0.5.34 methods, dropped upstream properties/quick-actions methods (MUL-6286 not ported), added `reorderIssueStatuses`

**Settings page**: `SettingsPage` gained the `issue-statuses` tab (workspace group, CircleDot icon) — full catalog CRUD with drag reorder, category-pinned editor, archive-retires behavior.

## Verification

- `pnpm typecheck`: **0 errors** (full turbo)
- `go build ./...`: clean
- `go test -count=1 -timeout 600s ./internal/... ./pkg/agent/...`: all pass EXCEPT the documented pre-existing `TestTickSupervision_CompletionByFinalIssueStatus` panic (verified byte-identical at 0.5.34 baseline; Known Stability Surface)
- vitest: schemas.test 69/69, issues-page.test 10/10 (mock updated to `status_category` — pre-existing Task-6 breakage), issue-detail 35/37 (2 scroll-to-comment tests pre-existing failures at 16c8d6955, verified in isolated worktree)
- Ship chain: snapshot → migrate (no new) → bundle-cli → electron-vite → electron-builder `--dir` → install + re-sign → cold-start 4s, Server PID 37263, Info.plist 0.5.36

## Skipped this cycle (tracked)

- **MUL-6305** migration 341 fail-closed — gated on MUL-6286 actor properties port (fork never shipped migration 341)
- **MUL-5651** preserve tasks through network partitions — gated on MUL-4257/4302/4304/4351 batch primitives (fork-missing sqlc methods + AgentTaskQueue columns)
- MUL-6286 / MUL-5991 / 0c69f1f95 — user-deferred
- MUL-6350 plugin rebuild 1/4 — wait for series completion

## Process notes

1. **429 quota killed 2 agents mid-flight** (T4 MUL-6272 + T7 MUL-6243 UI). Main thread completed both: T4 needed a type-cycle fix (nav-registry), T7 needed a full surgical re-port. **Lesson: for large cherry-picks, the main thread must verify agent output file-by-file against upstream's per-file stat — agent "take theirs" resolutions can silently bring upstream's whole file (4x LOC inflation: 22767 vs upstream 5186).**
2. **Test files reference dropped modules** — always delete the test files that pin dropped code paths (surface tests, issue_table_status_category_test).
3. **`fetchFirstPages` status_category change broke issues-page.test** at Task 6 (never caught — Task 6 agent verified typecheck only, not tests). Mock updated.
4. **Pre-existing test failures must be bisect-verified**: the 2 scroll-to-comment failures were confirmed pre-existing via an isolated worktree at 16c8d6955 with symlinked node_modules (cheap baseline verification).
5. **App died mid-session** (PG + Multica not running) — ship's migrate step failed on connection refused. Recovery: `open /Applications/Multica.app` → app bootstraps PG → ship re-run. Lesson: verify PG/8090 liveness before `ship-mac`.

Related memory: `0.5.36-mul6243-ui-closeout-2026-08-18.md` (forthcoming).