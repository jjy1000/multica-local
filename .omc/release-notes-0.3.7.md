# Multica 0.3.7 — 2026-07-13

## Summary

Cherry-picked upstream at-least-once comment delivery framework (MUL-4195 +
MUL-4304 + MUL-4015 + MUL-4417) to fix a long-standing bug where the parent
agent did not reliably learn that a delegated sub-agent had finished a task
when the user posted follow-up comments while the sub-agent was running. The
fix lands the three pieces needed for the loop to close:

1. **Enqueue-time coalescing (MUL-4195)**: when a member posts a follow-up
   comment while a `(issue, agent)` task is already queued, the new comment
   is folded into the existing task via `coalesced_comment_ids[]` instead of
   being silently dropped by the dedup guard. The merged run's
   `originator_user_id` and `trigger_summary` are re-stamped to the latest
   comment so the run answers the most recent deliberate instruction under
   that instruction's originator.
2. **Completion-time reconciliation (MUL-4195 + MUL-4304)**: on
   `CompleteTask`, every undelivered member comment that landed during the
   run is replayed through the normal enqueue path so the agent picks it up
   next. The new `delivered_comment_ids[]` receipt on `agent_task_queue`
   (backfilled in migration 147 for in-flight tasks) is the source of truth
   for "what did the daemon actually see"; everything else earns a bounded
   follow-up.
3. **Reply-thread authorisation + source-task stamping (MUL-4015 + MUL-4417)**:
   when an agent posts with `X-Task-ID`, `comment.source_task_id` is stamped
   so the leader→worker mention hop preserves the originating member across
   `resolveOriginatorFromTriggerComment`. The reply-parent check now uses
   `taskCoversReplyParent` (trigger OR any coalesced id) so resumed sessions
   can reply to a coalesced mid-run comment without a 409.

## Schema migrations

- `146_agent_task_queue_coalesced_comment_ids` — adds
  `agent_task_queue.coalesced_comment_ids UUID[] NOT NULL DEFAULT '{}'`.
- `147_agent_task_queue_delivered_comment_ids_backfill` — backfills
  `delivered_comment_ids = ARRAY[trigger_comment_id]` for in-flight
  dispatched/running/waiting_local_directory tasks so reconciliation has a
  truthful starting point.

Both forward-only. `comment.source_task_id` (migration 120) and
`agent_task_queue.delivered_comment_ids` (migration 141) were already
present in 0.3.6.

## Subset of upstream ported (intentional)

The full upstream MUL-4195 frame includes a `DaemonCapabilityCoalescedCommentsV1`
capability negotiation, a claim-time `delivered_comment_ids` write-back, and a
broader `reconcileCommentsOnCompletion` that replays agent-authored
`@mention` comments. The 0.3.7 port is the minimum needed to fix the
dominant failure mode ("user tells the sub-agent to do something while it is
running and the instruction is silently dropped"). Outstanding to land in
0.3.8:

- `keepExplicitMentionTriggers` (MUL-4304 agent-authored @mention
  reconciliation) — out of scope here, surface in 0.3.8.
- Capability negotiation + claim-time write-back — out of scope here.
- `CreateAgentBuilder` / `DeleteSystemAgentByID` queries and
  `runtime_connected_apps` plumbing — local fork has not migrated
  `agent.system_key` or `agent_task_queue.runtime_connected_apps`; the
  agent-builder subsystem is out of scope for this fork.

## Verification

- `cd server && go test -count=1 -timeout 240s ./...` — all packages PASS.
- `pnpm typecheck` — 0 errors.
- DMG cold-start three-check pass:
  - `lsof -nP -iTCP:5432 -sTCP:LISTEN` and `lsof -nP -iTCP:8090 -sTCP:LISTEN`
    both have a listener within 6 s.
  - `curl -s http://localhost:8090/health` returns `{"status":"ok"}`.
- Row parity vs 0.3.6 baseline (`workspace=1 / issue=156 / agent=80 / squad=11
  / comment=700`) — preserved.
- `schema_migrations` count grew by 2 (146 + 147) — forward-only confirmed.

## Known caveats

- 0.3.7 was packaged with the dmg-builder download hang bypass
  (memory 0.3.4-chat-mul4351-and-ui-2026-07-12.md), using `create-dmg` to
  hand-build the DMG from the `.app` directory that `electron-builder` had
  already produced. The hand-built DMG is a UDZO `Multica` volume containing
  the same `Multica.app` that the normal pipeline would ship.
- DMG is 228 MB (vs 240 MB for prior releases); the size drop is because
  electron-builder no longer runs the final UDZO compression pass.
- `pre-update-snapshot.sh` step 5/6 (git tag) logs a WARN because the
  working tree is not a git repo. The other 5 steps complete cleanly:
  `Multica.app.0.3.6.pre-update-20260713-104628.bak` + the per-table CSV
  backup under `~/.multica/backups/pre-update-20260713-104628/`.
