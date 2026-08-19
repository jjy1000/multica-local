# Client Log Audit — 2026-08-19

**Scope**: Packaged desktop (Multica 0.5.36 @ /Applications/Multica.app) log system completeness + stability.
**Date**: 2026-08-19 (audit ~04:26 UTC, fixes ~06:06 UTC)
**Branch**: epic/0.5.13-integration
**Status**: 4 fixes landed, 2 port SKIPs documented, 1 agent-fail revert documented

---

## Audit findings (9 issues)

| # | Issue | Severity | Resolution |
|---|---|---|---|
| 1 | stale 818KB server.log in daemon profile (`desktop-localhost-8090/`) | P1 data trap | **DELETED** (leftover from pre-0.3.33 manual spawn, wrong profile dir) |
| 2 | daemon.log truncated at 23:23 with no .1 backup | P1 mystery | **ROOT-CAUSED**: one-time manual `mv` at 0.5.13 ship (8月6日 23:23). NOT a code bug. No cron, no O_TRUNC anywhere |
| 3 | renderer console → no disk capture (dev-only listener) | P2 gap | **FIXED** `ef4484fa5`: new `renderer-log.ts` → `~/.multica/profiles/<active>/renderer.log` |
| 4 | macOS unified log 0 entries | P2 gap | LEAVE (upstream SAME-BUG; both rely on file logs) |
| 5 | db pool pressure WRN threshold too eager (265/13d) | P3 noise | LEAVE (fork `dbstats.go` is **byte-identical** to upstream — verified via diff) |
| 6 | `/api/invitations` 404 ×166/day from client v0.5.36 | P3 real bug | **FIXED** `e5e0c8b40`: removed 4 dead client methods + 6 cascade callers (sidebar/queries/realtime/App/overlay/navigation/pageview) |
| 7 | daemon double-spawn race (10 EADDRINUSE/13h) | P3 race | **FIXED** `ef4484fa5`: `startInFlight` single-flight wrapper around `startDaemon` |
| 8 | server.log 60MB unrotated 13d | P3 growth | **FIXED** `ef5a7f615`: 50MB `rotateLogIfNeeded` (POSIX-atomic rename to .1) for server.log + daemon.log |
| 9 | server.log no stderr split | P4 nice-to-have | LEAVE (fork-specific; slog already carries `component=`; UI value low) |

## Upstream comparison (multica-ai/multica)

- `dbstats.go` byte-identical → any local change = divergence (LEAVE)
- Renderer console + macOS log: SAME-BUG in both (dev-only capture)
- Daemon spawn race: SAME-BUG in both (no pidfile anywhere) → fork-local fix landed
- server-manager.ts does NOT exist upstream (upstream never bundled Go server) → issues 1/8/9 have no upstream reference

## Commits (4 atomic)

```
572eeaba5  feat(issues): scroll-to-comment (MUL-6140) — 5 files +125/-14, ratios ≤1.6x
ef5a7f615  fix(desktop): 50MB log rotation — 3 files +47/-2
ef4484fa5  fix(desktop): renderer console sink + daemon spawn mutex — 3 files +80/-16
e5e0c8b40  fix(invitations): dead user-scoped code removal — 10 files +46/-179
```

All green: pnpm typecheck 6/6, vitest (app-sidebar 4/4, daemon-manager 8/8).

## Port-attempt SKIPs (2026-08-19 UI integration pass)

- **MUL-6327 mcode provider logo** (`b55322d938f2`) — SKIP-DEAD-CASE: fork has no `mcode` runtime type (13-case ProviderLogo switch, no mcode). Logo would be unreachable.
- **MUL-6335 skills bulk update** (`2014ae3613bb`) — SKIP-NO-ENDPOINT: fork lacks `POST /api/skills/{id}/refresh` (prerequisite `147cd8d84` never ported). UI would 404 on every refresh.
- **MUL-6323 worktree gate** (`5f4ba41d1`) — **AGENT-FAIL (429) + REVERT**: agent died mid-port leaving 23 files +1819 LOC, 6 typecheck errors, 2 wholesale-adoption files (auth-initializer 11.6x, config_test 5.5x), 4 new files, UU unmerged state. Main thread reverted all to HEAD. Port prerequisite: fresh session + small-chunk pattern (Go batch → TS batch → main-thread commit per 0.5.33 lesson). Deferred.

## Deferred (tracked in CLAUDE.md header)

- MUL-6286 actor properties, MUL-5651 batch primitives, MUL-6321 OpenClaw slow hosts, MUL-6063 delegated task failures, MUL-5991 jcode, 0c69f1f95 Hermes resume-auth, MUL-6350 plugin rebuild 1-4, MUL-6327 (SKIP-DEAD-CASE), MUL-6335 (SKIP-NO-ENDPOINT), MUL-6323 (agent-fail, re-defer)
- Upstream scan: 9d6c0c81e..HEAD = 46 commits (17 UI-touched, 5 tiers categorized)

## Ship decision

**NOT shipped** — 4 commits stay on `epic/0.5.13-integration`. Current /Applications/Multica.app = 0.5.36 (stable, good). Ship 0.5.37 when the batch accumulates ≥5 commits or a user-visible bug needs it.

## Process lessons (this session)

1. Explore agents can miss `packages/core/**` when grep-ing renderer surfaces — Issue 6's "fork doesn't call /api/invitations" verdict was wrong; the methods were in `client.ts:1750-1764`. Always grep `packages/` + `apps/` together.
2. 429 quota kills agents mid-port — main thread reverts, never salvages wholesale-adoption files.
3. "目前版本不错" is the highest-priority signal for a localized stable fork — defer parity-chasing work when the running version is stable.
