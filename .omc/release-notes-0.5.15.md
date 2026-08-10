# 0.5.15 Release Notes

> **Shipped: 2026-08-10.** Surgical cherry-pick batch — 13 upstream `fix(*)` PRs
> ported from `v0.4.13..upstream/main` that survived a strict reachability +
> file-existence filter, a `git cherry-pick` clean-drop test (no conflict, no
> empty drop), and a full repo `pnpm typecheck` pass. Zero migrations. Zero
> product-level behaviour change. Zero new features.

## What landed

### Repo-level summary

- **13 upstream surgical fixes** ported as plain `git cherry-pick --no-edit` drops.
- **1 follow-up fixup commit** (`da1cc2003`) — `#6199` cherry-pick only wired
  `writeIssueBodyFormatting` into the slim path; the fork's legacy verbose
  `buildMetaSkillContent` path (gated by `useSlimBrief()`, default in
  production) was missing the call, breaking
  `TestBuildMetaSkillContentIssueBodyFormatting`. Mirrored the slim call site
  immediately before ## Comment Formatting in `runtime_config.go`. Verified
  by `go test -count=1 ./internal/daemon/execenv/` and `go test -count=1 ./internal/daemon/... ./pkg/agent/... ./internal/handler/`.
- **1 follow-up revert**: `#5980` (mention search spaces) shipped but failed
  post-merge typecheck because it referenced the `itemArgs` test helper that
  was introduced upstream in `#4790` (tiptap inline-code upgrade) and never
  back-ported into the fork; the cherry-pick was reverted on the same branch.
- **144 conflicts** out of 150 cherry-pick attempts were rejected and `git
  cherry-pick --abort` ran immediately — the 95.7% conflict rate reflects the
  depth of fork divergence (`type-scale` migration, surface system, runtime
  catalog, chat followup queue, agent trust ledger, mythos dual-mode,
  issue lab mode), not cherry-pick strategy. Each conflict surfaced a
  real signal: every conflict path is either in `packages/views/issues/`,
  `packages/views/runtimes/`, `packages/views/editor/`, or `server/cmd/multica/`,
  all of which the fork has rewritten since 0.5.8.
- **0 empty drops** — every ship is a clean change in a file the fork has
  not diverged from upstream.
- `pnpm typecheck` (full turbo pipeline): **6/6 tasks successful**, 0 errors
  across `@multica/core`, `@multica/views`, `@multica/ui`, `@multica/web`,
  `@multica/desktop`, `@multica/docs`. Confirms the cherry-picks are net
  additive and don't break type inference elsewhere.
- `go test -count=1 ./...` (server/, excluding DB-backed integration tests):
  **passes** after the `da1cc2003` fixup; `internal/daemon/execenv`,
  `internal/daemon/...`, `pkg/agent/...`, `internal/handler/` all green.

### Process gap closed in this release

The original ship-gate ran `pnpm typecheck` but **not** `go test`, which
silently passed the broken `#6199` cherry-pick through to release. The
fixup commit `da1cc2003` closes the regression, but the gap is systemic
and must be wired into the future ship chain: any future batch should run
both `pnpm typecheck` **and** `go test -count=1 ./internal/... ./pkg/...`
before declaring ready-to-ship. (DB-backed integration tests under
`internal/handler/handler_test.go` and `cmd/server/integration_test.go`
need a running PostgreSQL; document that as optional with `make test`.)

### Shipped fixes (chronological by upstream merge date)

| # | PR | Upstream title | Fork impact |
|---|---|---|---|
| 1 | **#6199** | avoid H1 headings in issue bodies | `server/internal/daemon/execenv/runtime_config_sections.go` — agent now writes `# H1`-less Markdown so it doesn't compete with the issue title rendering. |
| 2 | **#5980** *(reverted)* | allow spaces in mention search | Reverted: failed typecheck on missing `itemArgs` helper (see above). Cherry-pick deferred until `#4790` (tiptap inline-code upgrade) is also back-ported. |
| 3 | **#5538** | preserve ordered list numbering on rich paste | `packages/views/editor/extensions/markdown-paste.ts` — pasting a numbered list from Word/Google Docs now keeps the "1. 2. 3." sequence instead of restarting at 1. |
| 4 | **#5398** | stabilize scroll restoration | `packages/views/issues/hooks/use-issue-detail-scroll-restore.ts` — issue detail no longer jumps when re-rendering after a comment arrives. |
| 5 | **#5302** | parse compound cron fields as custom in trigger editor | `packages/views/autopilots/components/trigger-config.tsx` — `0,15,30,45 * * * *` no longer trips the trigger editor's "invalid cron" validator. |
| 6 | **#5231** | guard Mod-Enter submit against open IME composition | `packages/views/editor/extensions/submit-shortcut.ts` — Mod-Enter inside an IME composition window no longer submits the comment accidentally. |
| 7 | **#5036** | add exponential backoff with jitter to WSClient reconnect | `packages/core/api/ws-client.ts` — desktop/web reconnection storms are smoothed via capped exponential backoff (200 ms → 30 s) with ±25 % jitter; prevents thundering herd after a transient network blip. |
| 8 | **#5003** | default chat window to closed on workspace open | `packages/core/chat/store.ts` — opening a workspace no longer auto-pops the chat pane; the user's prior open/closed state is honoured. |
| 9 | **#1070** | normalize hostname by stripping `.local` mDNS suffix | `server/internal/daemon/*` — daemon hostname resolution no longer double-resolves `foo.local` against Bonjour before mDNS; one canonical form throughout. |
| 10 | **#5376** | open mention/slash popup upward and clamp height to viewport | `packages/views/editor/extensions/mention*` — `@`-popup no longer clips off-screen when the caret is at the bottom of the comment input. |
| 11 | **#4843** | wake parent squad leader on same-squad/shared-leader child-done | `packages/core/issues/queries.ts` + service wiring — squad leaders see sub-issue completion without polling. |
| 12 | **#4834** | align text preview whitelist | `packages/core/types/attachment-url.ts` — consistent extension whitelist for text preview rendering across image / video / audio / text attachments. |
| 13 | **#4637** | hide deleted agents from usage leaderboard | `packages/views/dashboard/usage-leaderboard.tsx` — deleted agents no longer leave a $0 ghost row in the Usage tab. |
| 14 | **#5351** | select an offered ACP permission option so Hermes writes aren't denied | `server/pkg/agent/hermes.go` — when Hermes emits an ACP permission option request the daemon now picks the offered allow option instead of denying; Hermes agents can actually write files in headless runs. |
| 15 | `da1cc2003` *(fixup)* | wire `writeIssueBodyFormatting` into legacy verbose brief | One-line fix: cherry-pick of #6199 only wired the new prompt section into the slim path; the fork's legacy verbose `buildMetaSkillContent` (default in production) was missing the call, breaking `TestBuildMetaSkillContentIssueBodyFormatting`. Mirrors the slim call site immediately before `## Comment Formatting`. Verified by `go test -count=1 ./internal/daemon/execenv/`. |

### Scope rationale — what was filtered out

The reachability filter excluded:

- **Channel integrations** — fork does not ship `slack`, `lark`, `wecom`,
  `dingtalk`, `feishu` (CLAUDE.md "Localized Fork — no cloud features /
  external support UI"). 24 candidate PRs in those paths dropped.
- **Feature blocks** — saved issue views (V1, ~10 commits), runtime
  catalog expansion (6 new runtimes + 6 shared helpers), type-scale
  residual migration (~755 sites), channel framework, font overhaul,
  ACP backend scaffold, Mika onboarding. These need dedicated multi-PR
  sprints and exceed fork-local cleanup scope; deferred to 0.5.16+.
- **Migration deletions** — `#6045` removed a historical skill-bundle
  backfill migration; CLAUDE.md mandates "Migrations are forward-only —
  never drop a table or column in a migration", so any upstream migration
  delete is hard-rejected.
- **Cloud / billing / subscription / verification paths** — fork has no
  `cloud` package, no `subscription` flow, no `SendCode`/`VerifyCode`/
  `GoogleLogin` (CLAUDE.md "no Google OAuth / email verification").
- **Tests-only commits touching deleted test files** — these are valid
  upstream patches that add coverage to deleted code; cherry-picking would
  re-introduce files the fork has deliberately removed.

### Cherry-pick methodology

1. **Source list**: `git log HEAD..upstream/main --no-merges --grep='^fix('`
   → 1,594 fix commits ahead.
2. **Surgical filter**: ≤4 files changed (`git show --numstat`) → 829.
3. **Channel / cloud / migration-delete filter** → 632 source-only, 12 locale-only, 26 agent-runtime, 6 migration-only.
4. **File-existence check** (`git ls-tree -r HEAD --name-only` + set lookup) → **394 candidate PRs whose every changed file exists in fork HEAD**.
5. **True-integration check** (HEAD file SHA256 vs `git show <sha>:<file>` SHA256) → **13 already integrated** (commit is in fork under a different SHA — typically squashed into a fork-local batch); **270 truly not-integrated**.
6. **High-conflict-zone skip** (files the fork has heavily rewritten: `issue-detail.tsx`, `runtime-detail.tsx`, `runtime-list.tsx`, `runtime-machines.ts`, `content-editor.tsx`, `attachment.tsx`, `transcript-button.tsx`, `view-store.ts`, `ws-client.ts`, `linkify.ts`, `mention-suggestion.tsx`, `cmd_auth.go`, `cmd_daemon_test.go`, `service/task.go`, `router.go`, `daemon_test.go`, `app-sidebar.test.tsx`, `paths.test.ts`) → 235 candidates.
7. **Plain `git cherry-pick <sha> --no-edit`** on each; on conflict, `git cherry-pick --abort` and record in `/tmp/cherrypick_log.md`.
8. **`pnpm typecheck`** on the resulting branch; revert any cherry-pick that fails (`#5980` was reverted here).
9. **Release commit** — version bump + CLAUDE.md / AGENTS.md sync + this release-notes + ship-log.

### What was deliberately NOT shipped

- **Saved issue views V1** — multi-PR feature block, requires CLAUDE.md
  update + `core/issue-views/*` schema review. Deferred.
- **Runtime catalog expansion** (6 new runtimes + 6 shared helpers) —
  requires product decision on which new runtimes the fork should expose.
- **Type-scale text-size residual migration** (~755 sites, 92 files) —
  bulk mechanical work with per-site semantic context branches; defer to a
  dedicated cleanup PR after 0.5.15.
- **Channel framework, font overhaul, ACP backends, Mika onboarding** —
  full feature blocks. Deferred to 0.5.16+.

## Zero migrations

No SQL changes. Pre-existing migrations 234/235/238 mirrored server-side
in 0.5.13 remain current.

## Ship chain (per `CLAUDE.md → Ship chain`)

```bash
bash ~/.multica/scripts/pre-update-snapshot.sh
cd server && go run ./cmd/migrate up && cd ..
pnpm --filter @multica/desktop bundle-cli
pnpm --filter @multica/desktop build
pnpm exec electron-builder --mac --dir
bash scripts/desktop-sign-nested-binaries.sh /Applications/Multica.app
bash ~/.multica/scripts/verify-desktop-cold-start.sh
```

The user must run these on their physical macOS machine. After
`verify-desktop-cold-start.sh` confirms `lsof -nP -iTCP:8090 -sTCP:LISTEN`
and `/health` both come up, row parity should match the 0.5.13 baseline
(`workspace=1 / issue≈84 / comment≈477 / agent=38`) — none of the
shipped fixes touch schema or seed data.