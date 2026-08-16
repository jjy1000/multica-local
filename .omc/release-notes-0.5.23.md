# Release Notes — 0.5.23

**Ship date:** 2026-08-16
**Branch:** `epic/0.5.13-integration`
**Baseline:** 0.5.22 (commit `c7ed060cc`, packaged 2026-08-15)

---

## Headline: MUL-3963 + MUL-4525 full port — comment trigger outcomes + invocation-permission model

The flagship of this ship is the **upstream MUL-3963 + MUL-4525 port**: the canonical-agent invocation-permission model (`canInvokeAgent`) on the backend, the AccessPicker + permission_mode wiring on the frontend, and the comment-trigger outcomes fan-out that surfaces blocked @mentions to the user honestly. The fork previously had `t.Skip` on `TestConsecutiveCommentsDifferentOriginatorsFullEnqueuePath` — that test now **passes** (commit `d8d73a9f5` + `c72a7453a`).

11 atomic commits on top of 0.5.22 baseline.

---

## What's new

### Backend (Go) — MUL-3963 + MUL-4525 server side

- **Permission model** — `agent.permission_mode` (`private`|`public_to`) + `agent_invocation_target` table (migration 130-fork). `canInvokeAgent(actorType, actorID, originator, agent)` is the 4-case decision table mirroring upstream PR #4844. Private agents deny-by-default; public_to evaluates an allow-list (member/workspace/team).
- **Invocation tracking** — `replaceInvocationTargetsWithQueries` runs inside the same TX as agent create/update; `parsePermissionInput` validates the wire shape.
- **canInvokeAgent** lives in `server/internal/handler/agent_permission.go` (NEW, 228 lines). `memberActorUserID(req)` + `invokeOriginatorFromRequest(req)` are the request-side helpers.
- **Fork-local decisions preserved** — keeps `DispatchReasonCode` types in `handler/` (no `internal/dispatch/` package per CLAUDE.md §Known Stability Surfaces admission.go doc-comment).
- **`service.ErrAttributionFailClosed`** sentinel introduced on `service/issue.go` — fails closed when attribution is ambiguous, never silently coalesces.
- **MUL-4305** — `OriginatorForIssueTask` exported helper on `service/task.go`; migration 246 extends `issue.origin` CHECK (`autopilot`, `quick_create`, `lark_chat`, `agent_create` — fork-specific drop of `slack_chat`).
- **MUL-4063** — `triggerChildDoneSquad` no longer drops `actorType`/`actorID` params; private-leader squad parent leader wakes on child-done.
- **MUL-4010** — `permission_mode` + `invocation_targets` threaded through `agent_template.go` template create path.

### MUL-4525 Comment Trigger Outcomes (§2 — 5 commits)

- `51325eeab` — **surface blocked @mention trigger_outcomes** — the user now sees WHICH @mentions were blocked and why.
- `7b03baa37` — **one outcome per explicit mention** — round-2 fan-out refinement (no double-counting).
- `507cc23c4` — **honest role + status in trigger_outcomes** — round-3; the rendered outcome matches the actual dispatch role.
- `9838751c9` — **fail-closed active-task check** — `hasActiveTaskForIssueAndAgent` returns `(bool, error)` instead of fail-open `bool`; `decideSuppressedLeaderOutcome` added.
- `300a4c629` — **honest merge outcome** — refused merge is blocked, not fake coalesced.

### Frontend (TypeScript / React) — MUL-3963 PR #4844 mirror

- **NEW `packages/views/agents/components/inspector/access-picker.tsx`** (449 lines) — owner-only-editable Dialog-mode picker. 3 radios (`permission_mode`) + workspace toggle + member multi-select + toast handling. Mount-only init pattern matches the inspector's `DescriptionEditor`.
- **`packages/core/permissions/rules.ts`** (+102 lines) — client-side `canInvokeAgent(agent, ctx, invocation)` mirroring Go's wire shape. 7 new vitest cases pin the decision tree (logged-out actors, owner bypass, admin bypass, member allow-list match, workspace-broad, agent-actor denied from member-only, empty allow-list lockdown, missing mode defaults to private).
- **`packages/core/types/agent.ts`** (+78 lines) — `PermissionMode` / `InvocationTargetType` / `InvocationTarget` types + `CreateAgentRequest` / `CreateAgentFromTemplateRequest` / `UpdateAgentRequest` extended.
- **`packages/core/api/schemas.ts`** (+108 lines) — `PermissionModeSchema` + `InvocationTargetSchema` + `InvocationTargetListSchema` + `AgentResponseSchema` (permissive `.loose()`) + `EMPTY_AGENT_RESPONSE` fallback.
- **`packages/views/agents/components/agent-detail-inspector.tsx`** (+20 lines) — `workspaceId` prop + `AccessPicker` integration with `PropRow` + `onUpdate({permission_mode, invocation_targets})`.

### i18n (4 locales)

- `packages/views/locales/{en,ja,ko,zh-Hans}/agents.json` (+30 each) — `access_picker` section with `prop_label`, mode labels, allowlist editor strings, members section, search placeholder, toast messages.
- `packages/views/i18n/resources-types.ts` (+2) — type additions for the new locale keys.
- `packages/views/locales/index.ts` (+8) — register namespace for all 4 locales + `swarm.json` import for the swarm workbench.

### Tests

- `TestConsecutiveCommentsDifferentOriginatorsFullEnqueuePath` — **un-skipped + passing** (was `t.Skip` since 0.5.21).
- `TestOriginatorForIssueTask_MatchesResolverForAgentCreate` — pins the symmetry contract between `service.task.OriginatorForIssueTask` and the `issue_resolve_originator` resolver.
- `TestChildDone_SquadPrivateLeader_AgentActorWakesLeader` — new case pinning the MUL-4063 fix.
- `TestCreateComment_WorkerAgentCommentWakesSquadLeader_MUL4015` — pins the MUL-4015 `source_task_id` thread (already shipped pre-session).
- 7 new vitest cases in `packages/core/permissions/rules.test.ts` (115 tests total in the permissions module).

---

## Verification

```
pnpm typecheck                                              6/6 ok
cd server && go test -race -count=1 -timeout 600s \
  ./internal/handler/ ./internal/experimental/ \
  ./internal/service/ ./cmd/multica/ ./pkg/agent/           all green
go build ./...                                              exit 0
go test -run TestConsecutiveCommentsDifferentOriginatorsFullEnqueuePath \
  ./internal/handler/                                        PASS
```

Ship log at `.omc/0.5.23-ship-2026-08-16.md`.

---

## Migration contract

- **0 migrations** added in this ship. MUL-3963's `permission_mode` column + `agent_invocation_target` table were already ported to the fork's migration 130-fork in pre-session work (0.5.21 cherry-pick batch).
- **Backward compat**: pre-existing agent rows have `permission_mode ?? "private"` and `invocation_targets ?? []` defaults so the AccessPicker renders correctly on first open until the owner edits.

---

## Risks and follow-ups

1. **PermissionMode values are `private` / `public_to`**, not the `public` / `private` / `restricted` set the upstream task description suggested. Trusted the already-ported Go backend (`server/internal/handler/agent_access.go` + the schema in `agent_invocation_target`) which uses these literal values. If upstream is later rebased to use `public`/`private`/`restricted`, the frontend mirrors will need a follow-up commit.
2. **`replaceInvocationTargetsWithQueries` runs inside the same TX as agent create/update** — design choice by upstream; large `invocation_targets` arrays (50+ members) lock the agent row for the duration. Acceptable for the single-user fork scope.
3. **`Dialog` not `Popover`** for the access-picker — 3 radios + workspace toggle + member list + save toast need more real estate than a popover can give. The mount-only init pattern matches the inspector's `DescriptionEditor`.
4. **Pre-existing `issue-detail.test.tsx` failures** (2 svg-icon related) verified unrelated by `git stash` + re-run. Out of scope for 0.5.23.
5. **Packaging**: `apps/desktop/package.json` bumped to `0.5.23`; full ship chain (`make ship-mac`) planned separately.

---

## Next session entry

- **0.5.23 packaging** (optional per "先无需打包").
- **orchestrator-side surfacing** of MUL-4525 §2 outcomes into the swarm_run state machine (separate upstream PR series, not ported).
- **MUL-4857 / MUL-5548** (other MUL-3963 follow-ons, not ported yet).
- **0.5.23 i18n coverage audit** — translate the new `access_picker.*` keys into the 4 locales by native speaker (current copy was machine-generated).
