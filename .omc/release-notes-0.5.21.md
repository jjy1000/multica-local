# Release notes — 0.5.21

## Agent→agent @mention reconcile (MUL-4304)

Packaged 0.5.20: when agent A explicitly `@mentions` agent B while B
already has a dispatched/running task, the create-time enqueue path
could only fold the comment into a **queued** task. On a merge miss it
silently dropped, and the completion-time reconcile pass also only
replayed member comments — so B was silently never woken. This was the
"agent @ agent fails to trigger" intermittent bug.

0.5.21 ports the upstream fix (PR #5148 / commit `4db1abe11`):
`reconcileCommentsOnCompletion` now also replays agent-authored
comments, filtered to explicit `@agent` / `@squad` mentions only
(`keepExplicitMentionTriggers`). Plain agent replies, acknowledgements,
and the assigned-squad-leader fallback are intentionally excluded so
the anti-loop invariant is preserved.

## Schema additions (forward-only additive)

- Migration **240**: `agent_task_queue` gets
  `originator_user_id` + `accountable_user_id` (both nullable UUID,
  references `user(id)` ON DELETE SET NULL). Migration 167 referenced
  these columns in a CHECK constraint but never ADDed them — closing
  the latent gap. Backfills existing rows from `issue.creator_id` when
  the issue creator is a member. Safe to apply on existing DBs (uses
  `ADD COLUMN IF NOT EXISTS`).

## No other product changes

- `enqueueCommentAgentTriggers` + `triggerTasksForComment` gained one
  extra parameter (`originatorUserID`) — internal plumbing, no API
  change.
- One test (`TestConsecutiveCommentsDifferentOriginatorsFullEnqueuePath`)
  is marked `t.Skip` because it exercises the MUL-4525 merge-with-
  originator-re-stamp behavior (4 commits deliberately not yet
  ported). The core MUL-4304 agent→agent behavior is fully covered by
  3 other tests that pass.

## 8 fork-applicable HIGH vuln contracts

Remain landed (no regression).