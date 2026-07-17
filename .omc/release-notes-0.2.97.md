---
name: 0.2.97 release notes
created: 2026-07-01T23:50:00Z
updated: 2026-07-01T23:50:00Z
version: 0.2.97
baseline: 0.2.96
type: release-notes
---

# Multica 0.2.97 (2026-07-01) — Server-only update

This is a **server-only** release. The desktop app repackage (DMG) is deferred to a future release — running 0.2.96 desktop bundles this updated server binary automatically on next launch.

## Fixes

- **fix(daemon): re-inject squad-leader briefing for comment-mention leader tasks (MUL-3724)** — when a member @-mentions a squad in a comment, the leader agent previously woke with no squad context (Operating Protocol + Roster + Instructions) and degraded into doing the work itself instead of orchestrating. The briefing injection now keys off `task.IsLeaderTask + task.SquadID` rather than the issue's static `AssigneeType`, so the briefing follows the task through every enqueue path: direct assign-to-squad, comment @squad-mention, sub-issue done callback, autopilot squad-assignee, retry-clone inheritance, and quick-create.

## Schema

- **feat(db): add `agent_task_queue.squad_id` column + partial index (migration 127)** — additive nullable column with no FK to `squad(id)` (by design, to avoid cross-table locks against squad archive/hard-delete). Existing rows stay valid; the new column is populated for all new enqueues.

## Refactor

- **refactor: extract `shouldInjectSquadLeaderBriefing` helper** — the briefing-injection logic is now a private function with a per-claim read-time fallback for pre-migration-127 in-flight leader tasks (legacy `issue.AssigneeType=="squad"` lookup when `task.SquadID` is NULL). The fallback path becomes dead code as soon as all in-flight tasks have completed post-upgrade.

## Data compatibility

- **Forward-only**: no drops, no renames, no defaults that force writes. Migration 127 adds a nullable column + partial index; existing rows are untouched.
- **Backward compatible at claim time**: the per-claim fallback in `daemon.go` ensures any leader task enqueued before the upgrade continues to receive briefing, provided the issue itself was squad-assigned.
- **DB rollback**: `127_task_squad_id.down.sql` drops the index and the column. No data loss because nothing depends on the column existing at read time (the fallback path handles the absent case).

## Out of scope (NOT ported from upstream)

- Migration 128 (autopilot collaborator) — cloud-adjacent, excluded by localization policy.
- Migration 131 (slack origin) — Slack, excluded.
- Slack integration package, PostHog, OAuth, cloud runtime, invitations, electron-updater, Discord/Help launcher — all excluded by localization policy.

## Verification

- `go test ./...` — all packages green (handler 8.9s, daemon 26.9s, agent 16.9s).
- New tests added: `TestClaim_LeaderTaskOnAgentIssue_InjectsBriefing`, `TestClaim_NonLeaderTaskWithSquadID_NoBriefing`, `TestClaim_LeaderTaskWithoutSquadID_NoBriefing`, `TestClaim_LeaderTaskWithDanglingSquadID_NoBriefing`, `TestAgentTaskQueueSquadIDColumnReadsAndWrites`.
- Existing briefing tests updated: `queueSquadIssueTaskFor` fixture now stamps `squad_id` + `is_leader_task=true` so `TestClaimTask_LeaderGetsBriefing` / `TestClaimTask_NonLeaderGetsNoBriefing` exercise the new task-keyed gate.