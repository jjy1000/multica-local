# Release Notes 0.5.72 — 2026-08-26

Three follow-up ships from the 0.5.71 triage's deferred-items list
(`.omc/release-notes-0.5.71.md` §Follow-ups). All three are forks of
upstream work or fork-local fixes that the 0.5.71 audit flagged.

## Shipped

### 1. Renderer process sandbox port (was: 0.5.71 SKIP `8f48c380`)

Manually ported the upstream renderer-sandbox extraction. The
cherry-pick attempt failed because upstream commit `8f48c380` also
pulled in a new feature (`createIssueWindow` + `AuthSessionCoordinator` +
`NotificationGate` + `MainRendererMessageQueue` + `dev-log`) that the
fork never adopted — those modules don't exist in the fork, so the
cherry-pick produced uncountable TypeScript errors. Resolution: take
just the sandbox change, port it by hand.

| File | Change |
|---|---|
| `apps/desktop/src/main/renderer-web-preferences.ts` (NEW) | extracted `createRendererWebPreferences(preloadPath, systemLocale, additionalArguments?, overrides?)` factory; `sandbox: true`, `webSecurity: false`, `plugins: true` (PDFium), `--multica-locale` arg |
| `apps/desktop/src/main/renderer-web-preferences.test.ts` (NEW) | vitest pins the security-relevant defaults |
| `apps/desktop/src/main/index.ts` | `createWindow` webPreferences block → `createRendererWebPreferences(...)` call, preserving `webviewTag: true` via the new `overrides` parameter |
| `apps/desktop/electron.vite.config.ts` | preload now bundles `@electron-toolkit/preload` (sandboxed preload `require` can only load `electron` + node builtins) |

**Fork deviations from upstream:**

- No `createIssueWindow` / `IssueWindowContext` / `AuthSessionCoordinator`
  / `NotificationGate` / `MainRendererMessageQueue` / `dev-log` — fork
  doesn't have the upstream IPC architecture those depend on.
- No `installNavigationGuard` — `navigation-guard.ts` was already removed
  by an earlier fork commit; renderer navigation is bounded by the
  webRequest Origin-strip and `setWindowOpenHandler` denial.
- PDF plugin comment rewritten to remove the "signed CloudFront URLs"
  reference (fork has no CloudFront; download URLs are minted by the
  local backend on demand via `GET /api/attachments/{id}/download`).

### 2. `Service.queries` interface seam — re-enable 2 SKIP'd mythos subtests

The 0.3.64 supervise completion test had two SKIP'd subtests
(`final_issue_closed_flips_to_done_and_snaps` +
`final_issue_empty_status_stays_supervising`) because
`issuestatus.Effective` walks the catalog via `s.queries` for custom
statuses, and the test fixture leaves `s.queries` nil.

| File | Change |
|---|---|
| `server/internal/service/mythos/runner.go` | added `effectiveQ issuestatus.Querier` field to `Service` struct (nil-fallback to `*db.Queries` in production) |
| `server/internal/service/mythos/supervise.go` | `tickSupervision` final-issue branch routes through `effectiveQ` seam |
| `server/internal/service/mythos/supervise_completion_test.go` | added `fakeEffectiveQuerier` (returns Category="done" for "closed", `pgx.ErrNoRows` for empty); re-enabled both SKIP'd subtests; all 6 subtests now pass |

### 3. Restore fork-specific daemon-polling description (was: 0.5.71 regression)

The 0.5.71 JWT commit's 4 env-vars mdx conflicts took upstream's
generic copy for the database-pool table, overwriting the fork's more
accurate "守护进程高频轮询（每 3 秒）" description.

| File | Change |
|---|---|
| `apps/docs/content/docs/environment-variables.mdx` | restored `DATABASE_MAX_CONNS` description to fork-specific wording |
| `apps/docs/content/docs/environment-variables.ja.mdx` | same |
| `apps/docs/content/docs/environment-variables.ko.mdx` | same |
| `apps/docs/content/docs/environment-variables.zh.mdx` | same |

## STOP flags honoured

- No `git rebase upstream/main` — would re-introduce the 0.5.36
  wholesale-adoption trap.
- No cherry-picks of the upstream `createIssueWindow` / IPC-architecture
  features — fork never had them, the architecture mismatch is the
  reason the original cherry-pick was abandoned.
- Cherry-pick completeness verified at `file:line` for fork-applicability
  before commit.

## Numbers

- **Files touched**: 9 (3 ship steps × 2-3 files each + 1 version bump
  + 2 release docs)
- **Commits**: 4 (3 ship steps + 1 version bump)
- **LOC delta**: +200 / -89
- **Tests**: typecheck 6/6 packages green; go test all packages green
  (including 2 newly re-enabled mythos subtests); 357 desktop unit tests pass

## Follow-ups (for 0.5.73)

1. Cherry-pick batch 2 per `.omc/upstream-integration-triage-2026-08-26.md`
   §P1 (agent runtime, issues, chat, autopilot, inbox, daemon, skill
   bundle, source-context).
2. Audit the `webviewTag: true` exception — is the Claude Science view
   still the only consumer? If upstream moved to iframe-only, the
   override can be dropped and `createRendererWebPreferences` can lose
   the `overrides` parameter.