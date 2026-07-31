---
name: release-notes-0.4.0-fork
created: 2026-07-31
updated: 2026-07-31
status: complete
---

# 0.4.0 fork — upstream integration Phase 1 + 0.3.68/0.3.69 carryover

A schema-first upstream integration. The 0.4.0 fork release consolidates:

1. **Phase 1 (upstream 0.4.0 integration, schema-only)** — the 0.3.55 →
   0.4.0 fork jump lands migrations from the 07-30 upstream snapshot
   without touching application code.
2. **0.3.68/0.3.69 carryover** — two uncommitted release batches already
   shipped to `/Applications/Multica.app` via manual asar-repack fallback
   (2026-07-30) are now committed into git history. The fork git
   baseline (HEAD `991b71e`) is 0.3.69; this release re-bases that onto
   the 0.4.0 wave-2 migrations and bumps the desktop version to 0.4.0.

## Schema-only delta (Phase 1)

Fork numbers 218-227 (committed as `36c18b7`) — ported from the 2026-07-30
upstream snapshot (`240_agent_task_regenerate_quick_actions`) with
`IF NOT EXISTS` guards and verified on the production DB (2026-07-31):
all 6 new indexes are `indisvalid=t`; the 3 new columns
(`agent.disabled_runtime_skills`, `chat_session.project_id`,
`agent_task_queue.session_rollout_missing`) are present.

| Fork # | Upstream # | Type | Index / Column |
|---:|---:|---|---|
| 218 | upstream 196 | CREATE INDEX | `idx_issue_workspace_assignee` |
| 219 | upstream 197 | CREATE INDEX | `idx_issue_workspace_parent` |
| 220 | upstream 198 | CREATE INDEX | `idx_issue_workspace_position` |
| 221 | upstream 200 | CREATE INDEX | `idx_agent_task_queue_terminal_completed_at` |
| 222 | upstream 201 | CREATE INDEX | `idx_agent_task_queue_agent_terminal_latest` |
| 223 | upstream 202 | ADD COLUMN | `agent.disabled_runtime_skills` |
| 224 | (new) | ADD COLUMN | `chat_session.project_id` |
| 225 | (new) | CREATE INDEX | `idx_chat_session_project` |
| 226 | (new) | ADD COLUMN | `agent_task_queue.session_rollout_missing` |
| 227 | (new) | column refactor | `agent_task_queue.retired_session_id` |

The 6 upstream cloud-excluded files (Composio, Slack, GitHub multi-workspace)
are not ported. The fork's `experimental_resource_lock` CHECK and the
cloud-neutralization state (per
`.omc/audit/upstream-0.4.0-cloud-physical-deletion-deferred.md`) are
unchanged.

## Carryover (already shipped, now in git)

- **0.3.68** (commit `d7d99cf`, renderer-only): `issue-detail` full-width
  layout + 6 sub-domain `AGENTS.md` mirrors + `check-agents-docs-sync.mjs`
  + `boundaries.js`.
- **0.3.69** (commit `991b71e`, backend + frontend): real
  `runtime_gc.go` `tarGz` (replaces the 0.3.19 placeholder stub that
  silently destroyed archived session data) + `install_error` toast on
  the Labs tab + `installableSources` += `pythia_oracle` / `code_canvas`.

Both ship logs are at `.omc/0.3.{68,69}-ship-2026-07-30.md` and were
already verified at install time (row parity
`workspace=1 / issue=244 / comment=1510 / agent=96 / user_plugin=0`).

## Cloud boundary audit (no code change)

The Phase-0 cloud-neutralization state is **stable**: all 6 stub/noop
items from the original checklist remain in place, and the 8 "complete
residual" items are in `neutralized-as-deferred` state — no production
code path actually calls into cloud services.

Physical deletion of the 8 residuals (`core/billing`,
`core/runtimes/cloud-runtime.ts`, `views/billing`, contact-sales page +
form + i18n, `JoinCloudWaitlist` UI, `events_test.go` PostHog) is
**explicitly deferred** to 4 follow-up fork-hygiene PRs (PR-A through
PR-D), each sized independently. The deferred PR plan is at
`.omc/audit/upstream-0.4.0-cloud-physical-deletion-deferred.md`. See
that file for the full audit table, the 4-PR follow-up plan, and risk
assessment.

## Ship path

This release is **documentation-and-version-bump only** — the schema
migrations have already been applied to the production DB (verified
`2026-07-31`) and the `apps/desktop/package.json` version bump from
0.3.69 to 0.4.0 is the only state change. No Go binary rebuild, no
`.app` re-package, no DMG — those are tracked as separate 0.4.x
ship tasks if and when the user requests a 0.4.0 desktop release.

## Conventions check (per `apps/docs/.../conventions.mdx`)

- i18n: no new keys (no UI changes in this release).
- Schema: all 10 wave 2 migrations carry `IF NOT EXISTS` guards; idempotent
  on re-run; no destructive changes.
- Versioning: `apps/desktop/package.json` is the canonical version source
  (per root `CLAUDE.md` "Version source" rule). Bumped nowhere else.

## Out of scope (deferred)

- Phase 2 (medium-conflict features): chat surfacing, settings 11-tab,
  cron editor, agent creation studio + MCP, table view, global shortcuts.
  Per `.omc/plans/upstream-integration-0.4.0-plan.html` and the
  user-approved scope at proposal time, this is a separate multi-session
  effort.
- Phase 3 (large refactors): daemon execenv, daemon WS RPC + batch claim,
  webhook delivery queue, desktop multi-window. Explicitly out of scope
  for this fork cycle.
- Cloud physical-deletion PRs A/B/C/D (see audit doc).
