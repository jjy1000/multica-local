---
name: release-notes-0.5.0-wave1
created: 2026-07-31
updated: 2026-07-31
status: complete
---

# 0.5.0-wave1 — fork upstream integration wave 1

A schema-only upstream-integration wave that ports 6 forward-only
migrations into the fork on the `wave1-client-usage-2026-07-31` branch.
No application code uses the new columns yet; this release is a
database-schema + sqlc-regen baseline for the upcoming
`client_usage` renderer caller and the `task_usage` cost dashboard
wire-up (both follow-up PRs).

## What landed

- **`client_usage_daily` table** (5 migrations, 1 new handler, 1 new route)
  Records one row per `(user, client_type, install_id, UTC day)` for
  active/desktop usage reporting. 17 columns with three CHECK
  constraints enforcing the probe-result tri-state (absent / error /
  success-with-counts).
- **`task_usage` authoritative cost** (1 migration, 4 query edits)
  Adds `cost_usd_ticks` (provider-reported cost in 1e-10 USD) to
  `task_usage` and a 5-column cost / uncosted split to
  `task_usage_hourly`. Replaces the rollup function
  `rollup_task_usage_hourly_window` with one that computes the split
  via `FILTER (WHERE cost_usd_ticks IS NULL)` on every aggregate.
- **sqlc regen** for `agent / autopilot / chat / chat_input_ownership /
  models / runtime / task_usage` .sql.go (the wave 2 / 0.4.0 schema
  additions were never re-generated; wave 1 closes the gap as a
  side-effect of running sqlc end-to-end).
- **Latent cosmetic sqlc** that picked up `disabled_runtime_skills`,
  `project_id`, and other wave 2 columns that the runtime fork has
  been querying by manual column lists until now.

## Why this exists

`epic/upstream-0.4.0` and `epic/0.4.0-integration` brought the
2026-07-30 upstream snapshot's schema into the fork (wave 2 = 10
migrations, 218-227). The Downloads snapshot
(`/Users/jiangjianyan/Downloads/multica-main 2`) on the user's
machine is **newer** than the 07-30 snapshot — it includes wave 2's
content under their original upstream numbering (200-227), plus 13
genuinely new migrations (228-240: `channel_media_pending_object`,
`chat_quick_actions`, etc.) and the `client_usage_daily` + cost
authoritative work that became Module 1 / Module 2 of wave 1.

This is the schema-first slice of that delta — purely additive
migrations and SQL query edits, no application UI rewiring. The
upstream `dashboard.go` work that wires the new cost columns into
the dashboard response, the new `/api/dashboard/failures/*`
endpoints, the `foldRestrictedAgents` privacy fix, and the
`client_usage` renderer caller are all tracked as follow-up PRs
in `.omc/plans/upstream-integration-0.5.0-proposal-rev2.md`.

## Conventions check (per `apps/docs/.../conventions.mdx`)

- All migrations forward-only with `IF NOT EXISTS` guards; idempotent
  on re-run; no destructive changes.
- No new i18n keys.
- All cost-related query edits are NULL-tolerant: pre-existing rows
  and pre-existing buckets read as today's behaviour, with the
  new fields defaulting to `0` (cost) or `NULL` (uncosted).
- Version source unchanged: `apps/desktop/package.json` is the
  canonical version source per root `CLAUDE.md` "Version source"
  rule. The `0.4.0` value on the `wave1-client-usage-2026-07-31`
  worktree was inherited from `epic/0.4.0-integration` and is the
  integration baseline; bump nowhere else in this wave.

## Out of scope (deferred)

- `dashboard.go` cost column wire-up (needs `foldRestrictedAgents` privacy fix from wave 3+)
- `ListDashboardFailuresDaily` / `ListDashboardFailuresByAgent` (no handler caller in fork)
- `client_usage` renderer caller (web/desktop clients need to POST a heartbeat on activity)
- Wave 2 (212 `agent_service_tier`), Wave 3 (VCS), Wave 4 (channel_media), Wave 5 (chat_quick_actions), Wave 6+ (search trgm + chat pinned)
- 0.5.0 desktop ship (user did not request a desktop release)
