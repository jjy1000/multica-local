# MUL-3963 + MUL-4525 Port Plan (2026-08-16)

This document captures the full upstream→fork port analysis for the
two feature blocks that gate the `TestConsecutiveCommentsDifferentOriginatorsFullEnqueuePath`
un-skip. Generated from a 11-commit Workflow analysis (2026-08-16) plus
direct upstream inspection.

## Hard block: MUL-3963 must be ported before MUL-4525

The fork has NEVER ported MUL-3963 (invocation-permission model). MUL-4525's
core mechanism — `canInvokeAgent` — lives in MUL-3963, not MUL-4525.
Without MUL-3963, every MUL-4525 commit that wires `canInvokeAgent` (ad15ee2bd,
1acc4516e, 51325eeab, etc.) has no substrate to wire to. The fork uses
the simpler `canAccessPrivateAgent` visibility-based gate; porting
MUL-4525 verbatim would require either porting MUL-3963 first or
adapting every `canInvokeAgent` call site to the fork's existing gate.

## MUL-3963 (PR #4844, core commit `9c876d7a0`)

40+ files, 2000+ lines. Cross-cuts server + packages/core + packages/views +
locales. Adds:

- `server/internal/handler/agent_permission.go` (228 lines, NEW) — core
  invocation-permission model: `canInvokeAgent(actorType, actorID, originator, agent)`,
  `memberActorUserID(req)`, `invokeOriginatorFromRequest(req)`.
- `server/internal/handler/agent_permission_test.go` (616 lines, NEW) — must-fix
  security tests.
- `server/internal/handler/agent.go` (+150/-44) — `permission_mode`
  column read + write + visibility gate.
- `server/internal/handler/agent_access.go` (+236 lines) — owner-only
  editable agent access picker; shifts from visibility-based gate
  to `permission_mode`-based gate.
- `packages/core/permissions/rules.ts` (+48 lines) — client-side
  permission decision for create-agent visibility.
- `packages/core/types/agent.ts` (+82 lines) — adds `permission_mode`
  + `invocation_targets[]` types.
- `packages/views/agents/components/inspector/access-picker.tsx` (279 lines,
  NEW) — owner-only-editable, read-only for others.
- `packages/core/api/schemas.ts` (+34 lines) — wire shape for
  permission_mode + invocation_targets.
- 4 × locales (en/ja/ko/zh-Hans) `agents.json` (+25 lines each) —
  `permission_mode.public_to.private` labels.

Plus a new SQL migration (upstream #130, not yet in fork): adds
`agent.permission_mode TEXT NOT NULL DEFAULT 'public'` +
`agent_invocation_target (agent_id, member_id, role)` table +
`ListAgentInvocationTargets(agentID)` sqlc query.

### Fork port-state for MUL-3963

| File | Fork state | Port work needed |
|---|---|---|
| `agent.permission_mode` column | **MISSING** | migration 130 |
| `agent_invocation_target` table | **MISSING** | migration 130 |
| `ListAgentInvocationTargets` query | **MISSING** | sqlc query |
| `canInvokeAgent` | **MISSING** | port from PR #4844 |
| `invokeOriginatorFromRequest` | **MISSING** | port from PR #4844 |
| `memberActorUserID` | **MISSING** | port from PR #4844 |
| `canCreatorInvokeAgent` (service) | **MISSING** | port from PR #4844 |
| `canMemberInvokeAgent` (service) | **MISSING** | port from PR #4844 |
| `agent_access.go` invocation gate | uses `canAccessPrivateAgent` | rewrite to `canInvokeAgent` |
| Frontend `access-picker.tsx` | **MISSING** | port from PR #4844 |
| `agent.ts` `permission_mode` type | **MISSING** | port from PR #4844 |

### MUL-3963 follow-on commits (MUL-4010/4015/4063/4305/4857/5548)

Subsequent MUL-3963 follow-ups also unported:
- MUL-4010 — `permission_mode/invocation_targets` thread through
  template create path + composio MCP apps flag
- MUL-4015 — `source_task_id` on HTTP-authored agent comments
- MUL-4063 — wake private-leader squad parent leader on child-done
- MUL-4305 — keep originator on agent-created issues
- MUL-4857 — restore autopilot @mention delegation authority
- MUL-5548 — stop blaming permission for unresolved mention target

These are bug-fixes layered on MUL-3963; port order matters — each
refines the prior. After porting PR #4844, port these in commit-order.

## MUL-4525 (11 commits) — full per-commit port instructions

### Dependency graph (linear, sequential)

```
ca7edadcf (root, NOT in HEAD, NOT needed — fork has no common history)
  → ad15ee2bd — feat(admission): unify dispatch outcome
    → f7b3a6aab — test: thread nil invoke gate
      → 1acc4516e — fix(admission): typed reason codes + run-now whitelist
        → 0b4fca4d3 — test: must-fix 3 acceptance tests
          → 51325eeab — feat(comments): surface blocked @mention outcomes (§2)
            → 61322506c — i18n(admission): clearer blocked-trigger copy
              → 7b03baa37 — fix(comments): one outcome per explicit mention + FE whitelist
                → 507cc23c4 — fix(comments): honest role + status in trigger_outcomes
                  → 9838751c9 — fix(comments): fail-closed active-task check
                    → 300a4c629 — fix(comments): honest merge outcome
                      → 9f437fade — fix(comments): name blocked @mentions in preview
```

### Per-commit port state

Each commit is annotated with port-state in the Workflow analysis
output (saved at `/Users/jiangjianyan/.claude/projects/-Users-jiangjianyan-jjy-multica-exploration-dev/40f8cc4a-ad56-4ebe-8d87-779ed5572e11/tasks/w020m1qrv.output`).
Summary:

- **ad15ee2bd**: HARD BLOCKER (needs MUL-3963) — 32 files / +840 LOC
- **f7b3a6aab**: depends on ad15ee2bd — 1 file / +5/-3 LOC
- **1acc4516e**: depends on MUL-3963 + dispatch-package decision
  (introduces `internal/dispatch/` pkg — conflicts with fork's deliberate
  "no separate package" decision in admission.go doc-comment)
- **0b4fca4d3**: depends on 1acc4516e — test refinement only
- **51325eeab**: comment outcomes — back-end depends on MUL-3963,
  FRONT-END is clean additive (no fork_conflict)
- **61322506c**: i18n copy — backend already ported; frontend keys
  don't exist yet (fold into 51325eeab port)
- **7b03baa37**: round-2 fan-out refinement — depends on 51325eeab
- **507cc23c4**: round-3 honest role/status — depends on 7b03baa37
- **9838751c9**: fail-closed — **partially ported**:
  - `ReasonAlreadyHandled → ReasonSelfTriggerSuppressed` rename ALREADY done
    (fork admission.go:72, no `ReasonAlreadyHandled` symbol remains)
  - `decidePostMergeMiss` ALREADY ported (fork admission.go:162, param
    named `hadActiveTask`, identical logic)
  - **Remaining work**: flip `hasActiveTaskForIssueAndAgent`
    (comment.go:1447) from `bool` fail-open (`return true` on error, line 1455)
    to `(bool, error)` fail-closed; add `decideSuppressedLeaderOutcome`
- **300a4c629**: depends on §2 + `service.ErrAttributionFailClosed`
  (fork doesn't have this sentinel — needs to be introduced on the
  TaskService attribution path)
- **9f437fade**: pure frontend UX refinement — cannot port standalone,
  depends on §2 frontend chain

## Port strategy (after MUL-3963 ships)

1. **First commit: MUL-3963 PR #4844** — port verbatim from `9c876d7a0`.
   Brings `canInvokeAgent`, `permission_mode` column, `agent_invocation_target`
   table, the new handler/service helpers, and the access-picker UI.
2. **Then: MUL-3963 follow-ons** — MUL-4010 → 4015 → 4063 → 4305 →
   4857 → 5548, in upstream commit order.
3. **Then: MUL-4525 in commit order** — ad15ee2bd → f7b3a6aab → 1acc4516e
   → 0b4fca4d3 → 51325eeab → 61322506c (fold into §2) → 7b03baa37 →
   507cc23c4 → 9838751c9 (partial) → 300a4c629 → 9f437fade.
4. **Then: remove `t.Skip` from
   `TestConsecutiveCommentsDifferentOriginatorsFullEnqueuePath`** and
   verify it passes against the new merge-with-originator-re-stamp +
   `commentMergeSucceeded` path.

## Cross-cutting decision needed (1acc4516e)

Upstream introduces `server/internal/dispatch/reason.go` (a leaf package)
and converts `handler.DispatchReasonCode` to a type alias. The fork
deliberately rejects this in `handler/admission.go`:
```
// Local adaptation: the upstream extract puts DispatchStatus /
// DispatchReasonCode behind a server/internal/dispatch/ package and aliases
// the ReasonCode via dispatch.ReasonCode. The local fork keeps the types in
// handler/ (no separate package) because (a) the existing call sites already
// live in handler/, and (b) introducing a new internal package is more
// invasive than warranted for the single-user / self-hosted scope.
```

**Decision required**: (a) accept the leaf `dispatch` package (reverses
the fork's documented decision; resolves import-cycle tension between
service/autopilot.go and handler/); (b) find a different leaf location
(e.g. internal/protocol or pkg/dispatch). This is an explicit design
choice, not a silent one.

## What's already shipped (2026-08-16 audit + fix batch)

| Dimension | Status |
|---|---|
| 13 P0 closed-loop breakages | ✅ all fixed |
| 12 P1 race/leak windows | ✅ all fixed |
| 15 P2 cleanup items | ✅ all fixed |
| migrations 243 + 244 (swarm pause + resource_type widening) | ✅ applied |
| 11 atomic fix commits | ✅ |
| `MUL-4525 port plan` (this document) | ✅ analysis complete |

## Next session entry point

Start by porting MUL-3963 PR #4844 (`9c876d7a0`):
1. Read `git show 9c876d7a0` — full diff
2. Port migration 130 (`permission_mode` column + `agent_invocation_target`
   table) — fork's `internal/handler/reserved_slugs.json` + sqlc regen
3. Port `server/internal/handler/agent_permission.go` + `agent_permission_test.go`
   verbatim
4. Adapt `server/internal/handler/agent.go` and `agent_access.go` to use
   the new gate (rewrite the visibility-based `canAccessPrivateAgent`
   callsites in autopilot.go, chat.go, comment.go, squad.go,
   issue_child_done.go, issue_trigger.go, runtime.go, runtime_profile.go,
   workspace_revoke.go)
5. Port frontend `access-picker.tsx` + `permissions/rules.ts` +
   `types/agent.ts` + `api/schemas.ts` + 4 locales

This is the blocker for the entire MUL-4525 series.