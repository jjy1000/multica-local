# Release Notes — 0.5.12 (2026-08-06)

## Summary

Batch C of the upstream 0.4.0+ cherry-picks: a fork-bug fix that has been
silently miscounting the Usage leaderboard, three upstream safety/perf
fixes the fork needed, one upstream feature drop, and one revert surfaced
by audit. Zero migrations.

| Commit | Severity | What it fixes |
|---|---|---|
| `58ba27e8a` fix(usage): #6194 | fork bug | per-agent rollup window was N+1 days, workspace KPI was N — single-agent row could read higher than workspace total at 1D. |
| `a31845b8f` fix(realtime): #5406 | safety | scope authorizer swallowed non-`ErrNoRows` DB errors, masking DB outages as wave-of-403s. |
| `c08e5ff4d` fix(daemon): #5355 | safety | daemon pinned agent CLI path; Homebrew/nvm in-place upgrades broke every task launch until daemon restart. |
| `9e8e60ba0` fix(cli): #5674 | revert | referenced `selfexec.Resolve` from a package the fork does not have. Reverted in `fdd0ff98b`. |
| `2d4ad52a7` fix(ui): #6095 | a11y | WCAG AA explanatory comment for the `0.505` muted-foreground lightness (fork already had the value from a prior ship). |
| `a385a8c36` feat(issues): #6124 | feature | "Open in new tab" item in the issue actions menu. |
| `dbde086e3` fix(0.5.12) | audit follow-up | bind the lost `path` variable, add `parseExactSinceParamInTZ`, drop `TestDashboardFailures*` tests that called handler methods the fork does not ship. |
| `a1097cdac` chore(release) | — | 0.5.11 → 0.5.12 + CLAUDE.md header. |

## Changes (upstream cherry-picks, semantically adapted)

### fix(usage): #6194 — close per-agent rollup windows (MUL-5551)

The Usage page's per-agent rollups (leaderboard + Run time / Tasks KPI
tiles) read from the N+1-day cutoff, but the workspace-side date-bucketed
series (chart + Cost / Tokens KPI tiles) read from the exactly-N-day
cutoff. The two halves disagreed by one calendar day; at 1D that let a
single agent's two-day total read higher than the workspace's one-day
total — a clear bug. Switched the two per-agent endpoints
(`GetDashboardUsageByAgent`, `GetDashboardAgentRunTime`) from
`parseSinceParamInTZ` to `parseExactSinceParamInTZ`. The fork did not
yet have the latter helper — it was first introduced upstream in #5991
(usage error/failure charts), which the fork skipped — so the helper
was added in the same ship. **Dropped the `TestDashboardFailures*` tests
that came along for the ride**: they call
`GetDashboardFailuresDaily` / `GetDashboardFailuresByAgent` handler
methods the fork does not ship (those ship with #5991, not #6194).
`TestDashboardPerAgentRollupsUseExactWindow`, which exercises the actual
fix, is preserved.

### fix(realtime): #5406 — propagate non-ErrNoRows scope DB errors (MUL-5406)

The scope authorizer was returning `ErrNoRows` → 403 on missing rows
and **silently swallowing every other DB error** also as 403. A real DB
outage was indistinguishable from "this row is forbidden". Replaced the
swallow with `scopeLookupErr(err)` that uses `errors.Is(err,
pgx.ErrNoRows)` to distinguish a legitimate "not found → forbidden"
from a transient DB failure that must propagate. All three lookup
sites (`GetAgentTask`, `GetIssue`, `GetChatSession` — both branches of
the issue/chat switch) now call `scopeLookupErr`. Drop-in.

### fix(daemon): #5355 — self-heal pinned agent executable path (MUL-4486)

The daemon pins each agent CLI's symlink-resolved absolute path at
startup to block PATH-redirect of a task launch. A version manager
(Homebrew Cask, nvm/fnm) upgrading in place deletes the pinned versioned
directory and repoints the stable name — leaving the daemon on a path
that no longer exists. Every codex task, model list, and version
detection hard-fails with "executable not found" until the daemon
restarts. `resolveAgentExecutablePath` was extended so the daemon
self-heals a vanished pin by re-resolving the recorded command once,
version-detecting and min-version-gating the candidate before adopting
it, and publishing `{path, command}` atomically. Coalesced with
singleflight so a live heal wins over a reappearing stale path. Custom
runtimes and custom-only hosts are untouched.

Conflict-resolution fix needed: the cherry-pick lost the `path` variable
in the `probe()` closure and the qoder probe (lines 227/306 of
`server/internal/daemon/config.go`); restored by binding the
`resolveAgentExecutablePath` return value before the struct literal.

### fix(ui): #6095 — annotate WCAG AA muted-foreground lightness (MUL-5447)

Lightness is pinned by WCAG AA, not by taste: muted text sits on every
light surface we have, and the darkest of them (`--sidebar-accent`, the
nav hover state) is the binding constraint. At the old `0.552` that
pair was 3.98:1; `0.505` clears 4.5:1 on all of app-shell / page-canvas /
sidebar / sidebar-accent / muted / surface-selected / surface, with the
worst case at 4.88:1. The value was already correct in the fork (from a
prior ship); this cherry-pick only adds the explanatory comment and a
new contrast test (`apps/web/app/muted-foreground-contrast.test.ts`)
that asserts the ratios.

### feat(issues): #6124 — Open in new tab in the issue actions menu (MUL-5455)

Per-issue "Open in new tab" menu item using the existing
`navigation.openInNewTab` adapter. Web fallback to
`window.open(..., "_blank", "noopener,noreferrer")`. Locale parity
(en/ja/ko/zh-Hans/issues.json).

### fix(cli): #5674 — REVERTED

The "fail fast with actionable daemon startup errors" commit was
cherry-picked cleanly but `var daemonExecutable = selfexec.Resolve`
references a `selfexec` package the fork does not have. The audit pass
flagged this as a BLOCKER (`go build` failure) before ship. The
cherry-pick has been reverted in `fdd0ff98b`. The actionable-error
behaviour (early `requireDaemonAuth` + 64 KiB log-tail report) is high
quality and can be re-attempted in a follow-up Batch D that introduces
the missing `selfexec` package as a fork-only addition.

## Migration & schema

**None.** Zero new migrations, zero `ALTER TABLE`, zero `CREATE INDEX`.
`migrate up` reports all migrations already applied (fork max 237).

## Packaging impact

- `apps/desktop/package.json` version: **0.5.11 → 0.5.12** (patch bump).
- Everything else (electron-builder config, asar layout, signing) unchanged.

## Verification

- `go build ./...` — OK.
- `go vet ./internal/handler/ ./internal/daemon/ ./cmd/multica/ ./cmd/server/` — OK.
- Full Go test suite (40 packages) — all pass:
  `cmd/{backfill_codex_usage_cache,migrate,multica,server}`,
  `internal/{agenttmpl,analytics,auth,cli,daemon,daemon/execenv,daemon/repocache,daemonws,events,experimental,experimental/helpers,featureflagdispatch,handler,integrations/channel,integrations/channel/engine,integrations/lark,integrations/slack,llmwiki,metrics,middleware,realtime,scheduler,service,service/agent_self_optimization,service/agent_trust,service/mythos,skill,storage,taskusagebackfill,util,util/secretbox}`,
  `pkg/{agent,featureflag,redact,skillbundle,taskfailure}`.
- pnpm typecheck / vitest — pending node_modules (worktree install in progress at ship time).

## Commits (since 0.5.11)

```
a1097cdac chore(release): bump 0.5.11 → 0.5.12 — Batch C upstream safety/UI cherry-picks
dbde086e3 fix(0.5.12): cherry-pick post-audit follow-ups
fdd0ff98b Revert "fix(cli): #5674 — fail fast with actionable daemon startup errors"
a385a8c36 feat(issues): #6124 — Open in new tab in the issue actions menu (MUL-5455)
2d4ad52a7 fix(ui): #6095 — annotate WCAG AA muted-foreground lightness pin
c08e5ff4d fix(daemon): #5355 — self-heal pinned agent executable path after in-place upgrade (MUL-4486)
9e8e60ba0 fix(cli): #5674 — fail fast with actionable daemon startup errors
a31845b8f fix(realtime): #5406 — propagate non-ErrNoRows scope DB errors
58ba27e8a fix(usage): #6194 — close per-agent rollup windows so leaderboard cannot exceed totals
```

## Not ported (deliberately)

- **Runtime Unbind (MUL-6220)** — P0 data-safety fix but 79-file
  cherry-pick blast radius; deferred to Batch C+ alongside the missing
  `selfexec` package introduction. Without it, deleting a runtime
  archives its agents, and the confirmation dialog's "archive" wording
  still misleads (the agents are recoverable in practice today only
  because the cherry-pick was reverted before ship — see
  `ArchiveAgentsAndDeleteRuntime` in `runtime_cascade_test.go`).
- **CodeX rollout gate (MUL-5305)** — 14 conflicts, involves chat retry
  / deferred tests fork doesn't ship.
- **Inbox arrow keys (MUL-5622)** — fork's inbox architecture uses
  `inbox-display.ts` + `IssueDetail` directly, not the `InboxList`
  component the upstream commit introduces.
- **409/coalesced on duplicate-key (MUL-5285)** — upstream PR #5958
  bundles Composio MCP + originator-chain code alongside the actual
  fix; cherry-picking would drag cloud-only surface into the fork.
- **Archived inbox sub-view (MUL-3736)** — backend (queries + handlers
  + events + the `archived` filter on `ListInbox`) is already shipped
  in the fork; only the UI sub-view (`inbox-context-menu.tsx` +
  sub-route) is missing, and 15 cherry-pick conflicts indicate the
  upstream inbox-page restructure does not fit the fork's inbox
  architecture.
- **i18n "issue" → local word for "task" (MUL-5703)** — the fork
  already uses "任务" / "task" terminology in zh-Hans via its own
  conventions.mdx (see `apps/docs/content/docs/developers/conventions.zh.mdx`);
  the upstream PR adds the opposite-direction translation, so the
  conflict is structural.
- **Daem fail-fast actionable errors (MUL-5674)** — referenced above;
  deferred pending `selfexec` package introduction.
- All Composio / Slack / GitHub / VCS / Webhook / Chat V2 / Attribution /
  Coalesced reply / Channel / Pinned agent / Custom Properties / Quick
  Actions / Issue Dependencies / RichContent / Telemetry / Diagnostic
  / Type-scale migration candidates — same exclusions as
  `.omc/upstream-0.4.0-main-port-candidates-2026-08-06.md`.
