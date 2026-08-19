# Release Notes — 0.5.37

**Shipped 2026-08-19** (branch `epic/0.5.13-integration`, commits `e5e0c8b40`…`55114e921`)

## Summary

Client-log audit close-out (9 issues from the packaged-client audit) + one upstream UI port. 5 commits: 4 fixes + 1 audit doc. Zero migrations, zero Go source changes, zero schema drift.

## Changes

### 1. `fix(invitations)` `e5e0c8b40` — remove dead user-scoped invitation code path

The server-side `/api/invitations` routes were removed when invitations were retired, but 4 client wrappers remained — producing **166 GET /api/invitations 404 WRN per day** from the desktop client. Cascade cleanup across 10 files (+46/-179):

- `packages/core/api/client.ts` — dropped `listMyInvitations` / `getInvitation` / `acceptInvitation` / `declineInvitation`
- `packages/core/workspace/queries.ts` — dropped `myInvitationListOptions` + `workspaceKeys.myInvitations`
- `packages/core/realtime/use-realtime-sync.ts` — dropped `invitation:created` + `invitation:revoked` WS handlers
- `packages/core/types/events.ts` — dropped `InvitationCreatedPayload` + event variant
- `packages/views/layout/app-sidebar.tsx` — dropped pending-invitations dropdown + badge + mutations
- `apps/desktop/src/renderer/src/{App.tsx, navigation.tsx, window-overlay-store.ts, pageview-tracker.tsx}` — dropped invitations overlay/route

**User-visible effect**: none (invitations were already gone); log noise -166 WRN/day.

### 2. `fix(desktop)` `ef4484fa5` — renderer console sink + daemon spawn mutex

**B — renderer console → disk**: production renderer errors used to disappear silently (no DevTools in packaged builds). New `apps/desktop/src/main/renderer-log.ts` writes console.* messages to `~/.multica/profiles/<active>/renderer.log` once the profile resolves.

**C — daemon spawn single-flight**: `bootstrapCli` + `tryAutoStartFromMain` + IPC + `auto-start` could each spawn their own CLI supervisor, racing for port 19545 — 10 `bind: address already in use` events per 13h. Wrapped `startDaemon` in a `startInFlight` single-flight guard.

**User-visible effect**: silent renderer crashes now leave a breadcrumb; daemon double-spawn race eliminated.

### 3. `feat(issues)` `572eeaba5` — scroll to newly-posted comment (MUL-6140)

Upstream port. After posting a comment or reply on an issue, the timeline auto-scrolls to the new entry (both Virtuoso-virtualized and flat rAF-stabilized modes). 5 files +125/-14, per-file diff ratios ≤1.6x vs upstream (no wholesale adoption).

**User-visible effect**: UX improvement — no manual scroll after posting.

### 4. `fix(desktop)` `ef5a7f615` — 50MB log rotation

`server.log` was 60MB unrotated for 13 days; `daemon.log` had no rotation either. New `rotateLogIfNeeded()` (POSIX-atomic `rename` to `.1`, single-rotation retention) fires at spawn time for both logs. Also resolved the "23:23 daemon.log truncation" mystery: it was a one-time manual `mv` at the 0.5.13 ship, not a code bug.

### 5. `docs(audit)` `55114e921` — client log audit report

Full write-up at `.omc/audit/2026-08-19-client-log-audit.md`: 9 issues, upstream comparison (dbstats.go byte-identical → leave threshold alone), 3 port SKIPs documented (mcode logo SKIP-DEAD-CASE, skills bulk SKIP-NO-ENDPOINT, MUL-6323 agent-fail-429 + revert), process lessons.

## Verification

- `pnpm typecheck` 6/6 PASS
- `go build ./...` clean + `go test ./internal/... ./pkg/agent/...` passes except the documented pre-existing `TestTickSupervision_CompletionByFinalIssueStatus` panic (byte-identical at 0.5.34 baseline)
- vitest: app-sidebar 4/4, daemon-manager 8/8
- Ship chain 7/7: snapshot → migrate (no new) → bundle-cli → electron-vite → electron-builder `--dir` (no deadlock) → install + re-sign (multica exit 0) → cold start ~4s, server 0.5.37 PID 98708
- asar renderer integrity: rawRequest 155

## Deferred (unchanged from 0.5.36 header)

MUL-6286 / MUL-5651 / MUL-6321 / MUL-6063 / MUL-5991 / 0c69f1f95 / MUL-6350 plugin 1-4 / MUL-6327 (SKIP-DEAD-CASE) / MUL-6335 (SKIP-NO-ENDPOINT) / MUL-6323 (agent-fail, re-defer — retry with fresh quota + small-chunk pattern)
