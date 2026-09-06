# Release Notes — 0.5.98 (full code quality audit + fix batch)

**Date**: 2026-09-06
**Scope**: TS + Go + docs, no schema migrations, no new flags.
**Baseline**: 0.5.97 (HEAD 332a9d4a9)
**Audit source**: `.omc/audit/2026-09-06-full-code-quality-audit.md`

## TL;DR

Audit identified 88 findings (30 HIGH + 31 MED + 27 LOW) across the
full stack. **19 items closed in this release** (15 HIGH + 4 MED);
remaining work deferred to 0.5.99+.

**Net diff**: +493 / -2239 = **−1746 行** (delete-heavy).
**Gates**: typecheck ✅ / lint ✅ / test ✅ (1832 passed / 33
parked MUL-6632) / go test ✅ (12 packages PASS).

## Closed in this release

### Ship-blocking (3)

- **H1 TestInstallTimesfm regression** — pre-test DELETE on shared
  `lab_managed` flag lock row. Without cleanup, the test inherits a
  stale row from a prior failed run.
- **H2 panic_context.go LIFO defer bug** — `WithPanicFlagContext`
  cleared the panic flag slot on return via `defer`. The outer
  `recover()` sentinel in `cmd/server/main.go` runs AFTER
  package-level defer unwinds, so the slot was always empty when
  the sentinel tried to attribute the panic to its flag key.
  Removed the defer. Tests rewritten: `TestWithPanicFlagContextRetainsOnReturn`
  + `TestWithPanicFlagContextRetainsOnPanic`.
- **H3 CompleteTask/FailTask on dispatched silently 200** — both
  helpers hit the `ErrNoRows` branch from a `UPDATE … WHERE id=$1
  AND status IN (...)` and returned the existing row without
  distinguishing "already terminal" from "still pre-terminal".
  New helper `classifyFinalizeNoRows` returns the row on
  `completed`/`failed`/`cancelled` (no-op success) and an error
  on `dispatched`/`queued` (caller mis-ordered).
  Tests: `TestCompleteTaskPreTerminalReturnsError` +
  `TestFailTaskPreTerminalReturnsError`.

### Active Contract #9 (TouchCausalNode RefreshForIssue) — 4 paths

- **H4 BatchUpdateIssues** (`handler/issue.go`) — post-success hook
  now refreshes every affected issue.
- **H5 daemon UpdateIssueStatus** (`handler/daemon.go`, parent wake
  path) — now refreshes.
- **H6 advanceIssueToDone** (`handler/github.go`, PR-merged flip) —
  now refreshes.
- **H7 notifyParentOfChildDone** (`handler/issue_child_done.go`,
  parent after system comment) — now refreshes.

Failure mode before: issue/comment/task mutations updated the issue
row but the causal graph held stale node state until the next 24h
refresh tick — visible as "the graph forgot this issue exists".

### Security hardening

- **H8 writeError error-string leaks ×14** — `handler/handler.go`
  new `writeInternalError(action, err)` helper logs raw err via
  `slog.Warn` and writes generic body. Migrated sites: `issue.go`
  ×2, `swarm_run.go` ×12. Before fix: pgx/sqlc errors leaked SQL
  state, table/column names, prepared-statement hints.
- **H9 env-spread child subprocess** — `apps/desktop/src/main/util/spawn-env.ts`
  new `pickEnvForSpawn(extra)` allowlists `PATH/HOME/USER/TMPDIR/LANG/LC_ALL`
  and merges caller-supplied extras. Migrated 4 spawn sites in
  `server-manager.ts` (3) + `daemon-manager.ts` (1). Before fix:
  parent `process.env` (including `JWT_SECRET`, `DATABASE_URL`,
  AWS_*, `GH_TOKEN`, etc.) leaked into spawned claude/codex/qoder
  processes.
- **H10 mythos_supervise.go utilParseUUID shim** — shim removed;
  uses `util.ParseUUID` directly (request-context error wrapping
  was being silently dropped).
- **H11 experimental_mythos_run.go uuid.Parse on user input** —
  replaced with `parseUUIDOrBadRequest` (400 with sanitized body).
- **M11 runtime_update.go:240 error leak** — `slog.Warn` + generic
  500 body.

### Localized fork cleanup

- **H15 contact-sales** — page + client component + 5 i18n keys +
  landing-hero link + sitemap URL all removed.
- **H16 cloud-quickstart** — 4 root MDX + 1 nested + 4 meta.json
  nav entries removed.

### i18n + storage

- **H13 sessionStorageAdapter** — `packages/core/platform/storage.ts`
  new adapter wrapping `StorageAdapter` surface for
  `sessionStorage`. 4 of 7 sites migrated (issue-detail,
  user-plugins-section, mermaid-diagram, use-issue-actions).
  Remaining 3 deferred to 0.5.99 (lab-run-heuristics, create-issue,
  discord-card-dismissed — last has `typeof window` guard).
- **H14 runtimes.json plural drift** — 4 unused plural variants
  (`running_one`/`_other`, `queued_one`/`_other`) removed from all
  4 locales. No `t()` consumer referenced them; `runtime-detail.tsx`
  reads nested `detail.running_chip` keys instead.

### Docs

- **H17 CLAUDE.md slim** — 1185 → 411 lines (−65%). Release notes
  extracted to `.omc/release-notes-<ver>.md`, Labs Platform spec
  to `server/CLAUDE.md` + `.omc/labs-runtime-lifecycle-map.md`,
  security contracts to `.omc/audit/`.
- **H28 user_plugin.sql filter** — audit verified all 4 SELECT
  queries already carry `AND status != 'deleted'`. No-op.
- **M11 runtime_update.go** — see Security above.

## Deferred to 0.5.99 (Phase B)

| ID | Finding | Scope |
|---|---|---|
| H12 | 13+ `api.*` direct calls bypassing `useMutation`/`useQuery` | ~12 new mutation hooks + 13 file updates; 1-2 days |
| H19 | 10 web files import `next/navigation` directly | Migrate to `useNavigation()` adapter; 1 day |
| H18 | explicit-column pin script | `scripts/check-issue-column-sync.sh`; half day |
| M5–M33 | 25 MED/LOW items | Spread across 0.5.99–0.5.101 |

## Active Contracts (post-fix)

| # | Contract | Status |
|---|---|---|
| 1 | 5s polling fallback | ⚠️ PARTIAL — M23/M24 deferred (lab-output-panel + plugin-shell queryKey wsId) |
| 2 | Lab leader rewrite | ✅ |
| 3 | chi route order literal-first | ✅ |
| 4 | `lab_managed` DTO marker | ✅ |
| 5 | Swarm Topology | ✅ |
| 6 | Lab auto-dispatch opt-out | ✅ |
| 7 | Workflow file-overlap | ✅ |
| 8 | Causal-graph trust ladder | ✅ |
| 9 | TouchCausalNode + never-nag | ✅ **IMPROVED** — 4 sites fixed (H4-H7) |

## Security Surface (post-fix)

| Hardening | Status |
|---|---|
| F-002 BypassPermissions gate | ✅ |
| F-005 isBlockedEnvKey | ✅ (H9 was bypassing it via env spread — now fixed) |
| F-006 multipart sanitize | ✅ |
| F-008 plugin-skill ack | ✅ |
| F-013 seedPluginVisibility workspace-scope | ✅ |
| F-027 openExternal + isAllowedTargetApiUrl | ✅ |
| F-028 self-opt anchor validation | ✅ |
| writeError error-string leak (H8) | ✅ **IMPROVED** — 14 sites fixed |
| Backend UUID rules (H10, H11) | ✅ **IMPROVED** — 2 sites fixed |
| Child subprocess env isolation (H9) | ✅ **NEW** |

## Recommended next cycle (Phase B)

1. **H12** (~12 hooks + 13 file updates) — 1-2 days
2. **H19** (10-file migration) — 1 day
3. **H18** (static pin) — half day
4. Remaining MED/LOW (25 items) — schedule across 0.5.99–0.5.101

No additional full audit needed. Future audits only when surface changes.
