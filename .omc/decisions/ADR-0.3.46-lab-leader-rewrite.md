---
name: ADR-0.3.46-lab-leader-rewrite
created: 2026-08-12T13:07:00Z
updated: 2026-08-12T13:07:00Z
type: decision
status: accepted
date: 2026-07-19
ship: 0.3.46
fixed-batch-parity: 2026-07-28
---

# ADR: Lab leader rewrite on `lab_source` flip (4-case contract)

## Status

Accepted — shipped 0.3.46 (`P0#4` of the labs audit). Batch parity
fixed in 2026-07-28 labs audit (`.omc/release-notes-0.3.64.md`).

## Context

When a user picks an agent (e.g. `pythia_runtime`) and later flips
the issue to a leader lab (e.g. `pythia_oracle`), what should happen
to the assignee? The pre-0.3.46 gate `!issue.AssigneeType.Valid`
only fired on unassigned issues and silently broke the common
"user picked an agent, then later flipped to a lab" path — the
assignee stayed at the manually-picked agent, blocking the lab from
auto-dispatching.

## Decision

**A 4-case decision table governs `lab_source` flip behavior on
both CreateIssue and UpdateIssue paths:**

| Caller intent | Existing assignee | Result |
|---|---|---|
| `lab_source` untouched | (any) | noop |
| `lab_source` → no-leader lab (`mythos_swarm`) | (any) | noop |
| `lab_source` → leader lab | already the leader | noop |
| `lab_source` → leader lab | missing / non-agent / different agent | **rewrite to leader** |

Leader resolution goes through `resolveLabLeader`:

1. Built-in table first (`defaultLabLeaderForKey`)
2. Falls through to `experimental.UserPluginLeader(manifestJSON)` for
   `user_<slug>` keys (0.3.63), so user-plugin labs auto-dispatch like
   built-ins

## Enforcement helpers

- `server/internal/handler/issue.go::shouldRewriteAssigneeForLabLeader`
- `server/internal/handler/issue.go::assignDefaultLabAgentOnUpdate`

Both CreateIssue / UpdateIssue paths consult these helpers BEFORE
the lab↔assignee mutex gate (ADR `0.3.31-mutex-narrowing`).

## Batch parity (2026-07-28 audit fix)

`BatchUpdateIssues` now honours the same contract:

- When a batch update flips `lab_source` to a leader lab AND the
  request body does NOT explicitly touch `assignee_type` /
  `assignee_id`, the assignee is rewritten to the leader.
- An explicit `assignee_*` in the same batch body wins (no rewrite).
- Pre-audit the batch path skipped the rewrite entirely, leaving lab
  issues with a stale manual assignee.

## Future paths that touch `lab_source`

CreateIssue / workflow-script paths that mutate `issue.lab_source` MUST
go through this helper rather than re-derive the gate. Tests:
`TestUpdateIssueLabSource*` in `server/internal/handler/issue_lab_dispatch_test.go`.

## Alternatives considered

- **A) Always rewrite regardless of explicit assignee**. Rejected: users
  who explicitly picked an agent would lose that choice every time they
  flipped the lab.
- **B) Never rewrite — require explicit `assignee_*` when flipping
  lab**. Rejected: most lab flips happen without an assignee pick (user
  just enables the lab), and forcing an extra step is a UX trap.
- **C) Rewrite + re-validate through the mutex gate**. Rejected: the
  mutex gate (ADR `0.3.31-mutex-narrowing`) is a separate concern;
  re-validating here creates a circular dependency.

## Consequences

- **Positive**: Lab dispatch never silently blocked by stale manual
  assignee. User can still explicitly pick a non-leader agent and have
  it survive a lab flip (e.g. enhancer mode target).
- **Negative**: 3 paths must stay in sync (CreateIssue / UpdateIssue /
  BatchUpdateIssues). Each has a pinned test.

## References

- Ship log: `.omc/0.3.46-ship-2026-07-19.md`
- Batch parity fix: `.omc/release-notes-0.3.64.md` §2
- CLAUDE.md "Active Contracts #2" section
- Related ADR: `0.3.31-mutex-narrowing` (mutex gate sits before this rewrite)