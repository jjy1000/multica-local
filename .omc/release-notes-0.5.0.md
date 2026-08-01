---
name: release-notes-0.5.0-fork
created: 2026-07-31
updated: 2026-07-31
status: complete
---

# 0.5.0 fork — wave 1 schema-first upstream integration

A schema-first upstream-integration release on the
`epic/0.4.0-integration` branch. Ports 6 forward-only migrations
from `Downloads/multica-main 2` (a snapshot newer than the
2026-07-30 snapshot wave 2 was ported from) into the fork, plus
2 new query surfaces for client-usage / cost-authoritative, plus
the long-deferred sqlc regen for the 0.4.0 wave 2 schema.

## Schema (6 migrations, all IF NOT EXISTS guarded)

- `207_client_usage_daily` (M1) — 17-column table for per-`(user,
  client_type, install_id, UTC day)` heartbeat reporting
- `208_client_usage_daily_unique_index` → `209_..._primary_key` (M1)
  — composite unique `(user_id, client_type, install_id,
  activity_date)` promoted to PRIMARY KEY via USING INDEX
- `210_client_usage_daily_query_index` (M1) — `(activity_date,
  client_type, user_id)` for the dashboard query path
- `211_client_usage_daily_workspace_index` (M1) — partial index
  on `workspace_id` WHERE NOT NULL
- `213_task_usage_authoritative_cost` (M2) — 1 column on
  `task_usage` (`cost_usd_ticks` NULL) + 5 columns on
  `task_usage_hourly` (`cost_usd_ticks` NOT NULL DEFAULT 0,
  `uncosted_{input,output,cache_read,cache_write}_tokens` NULL)
  + `rollup_task_usage_hourly_window` SQL function replaced
  with the cost / uncosted split via `FILTER (WHERE
  cost_usd_ticks IS NULL)` on every aggregate

## Application layer (1 new handler, 0 UI changes)

- `server/internal/handler/client_usage.go` (220 lines) —
  `UpsertClientUsage` with body-size cap (16 KiB),
  regex-validated `client_version` and `provider_name`,
  workspace existence validation via `GetMemberByUserAndWorkspace`
- `server/internal/handler/testdata/...` — `client_usage_test.go`
  (91 lines)
- `server/cmd/server/router.go` — `POST /api/client-usage` route
  gated by `RequireHumanActor`
- **No client (web/desktop) currently calls this route** — the
  renderer caller is a follow-up PR
- **No `dashboard.go` wire-up** for M2 — the 330-line diff between
  fork and upstream includes a `foldRestrictedAgents` privacy
  fix that depends on `restrictedAgentIDs` query (a 0.5.0 wave 3+
  candidate). The query interface change is live; a follow-up
  PR can wire the dashboard handler without further schema work

## Query changes (4 edits, 0 new queries)

- `UpsertTaskUsage :exec` — adds `cost_usd_ticks` to INSERT
  VALUES and ON CONFLICT DO UPDATE SET. NULL via `sqlc.narg` so
  pre-cost code paths that omit it compile and pass NULL.
- `GetIssueUsageSummary :one` — adds 5 SUM(...) aggregate
  columns (total_cost_usd_ticks + 4 uncosted_*_tokens) to the
  row struct.
- `ListDashboardUsageDaily :many` — adds the same 5 columns,
  using `COALESCE(uncosted_*, <base>)` to degrade to today's
  behaviour for buckets that haven't been recomputed since the
  split existed.
- `ListDashboardUsageByAgent :many` — mirrors the daily
  version.

## sqlc regen (latent, picked up by M1's regen pass)

- `agent / autopilot / chat / chat_input_ownership / models /
  runtime / task_usage` .sql.go: reflect the wave 2 schema
  (218-227) columns that have been live in production since
  0.4.0 but were never re-generated. Closes a long-standing
  gap where the runtime fork was querying by manual column
  lists.

## Fork-first deviations from upstream

| Module | Deviation | Why |
|---|---|---|
| M1 | Dropped `queries.LockWorkspaceForChatSessionCreate` from UpsertClientUsage | Upstream uses it to serialize client-usage reports against chat-session delete/create on the same workspace; the fork has no chat-session delete race, so the advisory lock is unnecessary. Workspace existence is still validated by `GetMemberByUserAndWorkspace`. |
| M2 | Did not port `ListDashboardFailuresDaily` / `ListDashboardFailuresByAgent` queries | No handler caller in the fork; porting queries alone would be dead code. |
| M2 | Did not port the dashboard.go wire-up of the new cost / uncosted columns | The 330-line diff includes a `foldRestrictedAgents` privacy fix that depends on `restrictedAgentIDs` query (a 0.5.0 wave 3+ candidate). Deferring the wire-up keeps this wave purely additive in the production row format. |

## Version

`apps/desktop/package.json` 0.4.0 → 0.5.0 (canonical version source per root
`CLAUDE.md` "Version source" rule; root `package.json` is the workspace
manifest and is left at 0.2.0). `git describe` is tried first by
`bundle-cli.mjs` but this repo's tags are `pre-update-*` snapshot
markers, so the build falls back to the `package.json` value.

## Verification (this release's pre-flight)

- `pnpm install` 干净
- `pnpm typecheck` 6/6 PASS
- `pnpm test` 8/8 PASS (1282 + 321 desktop tests)
- `cd server && go build ./...` PASS
- `cd server && go test -count=1 -timeout 180s ./internal/...` PASS
  (24 packages, ~30 s)
- `cd server && go run ./cmd/migrate up` PASS (207-211 + 213 all
  `up`, 218-227 all `skip (already applied)`)
- Row parity: workspace=1 / issue=244 / comment=1510 / agent=96 /
  user_plugin=0 / client_usage_daily=0 / task_usage=1735 /
  task_usage_hourly=681 (all unchanged from 0.3.69 baseline)

## Out of scope (deferred to follow-up PRs)

- `dashboard.go` cost column wire-up (needs `foldRestrictedAgents` privacy fix)
- `ListDashboardFailuresDaily` / `ListDashboardFailuresByAgent` queries + `/api/dashboard/failures/*` routes
- `client_usage` renderer caller (web/desktop clients need to POST a heartbeat on activity)
- Wave 2 (212 `agent_service_tier`), Wave 3 (VCS), Wave 4 (channel_media), Wave 5 (chat_quick_actions), Wave 6+ (search trgm + chat pinned)
- 0.5.0 desktop ship (the 7-step chain is documented in `0.5.0-fork-ship-2026-07-31.md`; the user has not yet requested the desktop rebuild)
