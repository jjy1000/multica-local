---
name: ADR-0.3.31-mutex-narrowing
created: 2026-08-12T13:07:00Z
updated: 2026-08-12T13:07:00Z
type: decision
status: accepted
date: 2026-07-16
ship: 0.3.31
narrowed: 0.3.33
realigned: 2026-07-28
---

# ADR: Lab ↔ Assignee Mutex narrowed to `mythos_swarm` sole mode ONLY

## Status

Accepted (3 iterations): 0.3.31 (initial any-lab mutex) → 0.3.33
(narrowed to mythos sole) → 2026-07-28 audit (batch + LabPicker
realigned to the narrowed contract).

## Context

When a user assigns an issue to a lab-bound agent (e.g. picks Mythos
Swarm in `LabPicker`), the question arose: should the manual assignee
be cleared? The 0.3.31 initial contract said "any lab = clears
assignee" — the lab owns the issue end-to-end. This was wrong:

- `mythos_swarm` has 2 modes (sole + enhancer) — sole clears assignee,
  enhancer REQUIRES an assignee (the supervised target).
- Other labs (claude_science_lab / pythia_oracle / llm_wiki_bridge /
  code_canvas / user_* plugin) do not reserve the roster; the manual
  assignee is a legal combination.

## Decision

**The mutex applies to `mythos_swarm` ONLY.** Specifically:

| `lab_source` | `lab_mode` | Mutex |
|---|---|---|
| `mythos_swarm` | `'sole'` (or NULL) | **YES** — manual assignee rejected |
| `mythos_swarm` | `'enhancer'` | **REVERSED** — assignee REQUIRED (supervised target) |
| any other built-in lab | NULL | NO mutex — manual assignee legal |
| any `user_*` plugin | NULL | NO mutex — manual assignee legal |

When `lab_source` flips to a leader lab and the caller did not pick an
assignee, the server auto-rewrites the assignee to the lab's leader
(0.3.46, see ADR `0.3.46-lab-leader-rewrite`). An explicit assignee
always wins over the auto-rewrite.

## Three enforcement layers

All three layers carry the SAME narrowed gate (pre-audit they had
drifted — batch + LabPicker still enforced the old any-lab mutex):

1. **UI (`LabPicker`)** — fires `onClearAssignee` ONLY when picking
   `mythos_swarm` in sole mode (including switching the mode tab back
   to sole); every other lab keeps the current assignee and leaves
   `AssigneePicker` unlocked. `lockedReason` on `AssigneePicker` is set
   only for mythos sole.
2. **Server (CreateIssue + UpdateIssue)** — mythos sole + assignee →
   400 mutex error; mythos enhancer without assignee → 400 "requires an
   assignee". The gate sits BEFORE `validateAssigneePair` so a
   non-existent member/agent row never produces a misleading "does not
   refer to a member" error.
3. **Server (BatchUpdateIssues)** — same narrowed gate, but honouring
   the batch contract: violations `continue` (per-issue skip), never 400
   the whole batch. Batch cannot set `lab_mode`, so enhancer-ness is
   decided from the persisted `prevIssue.LabMode`; the post-state
   (lab/assignee) is computed by overlaying the batch fields on the
   previous row.

## Schema

- `issue.lab_source` nullable TEXT (migration 155)
- `issue.lab_mode` nullable TEXT CHECK `'sole'|'enhancer'` (migration 157)

Both NULL for non-lab issues. Any new lab with per-issue mode semantics
must extend the CHECK constraint in a migration and add the mode
handling to the lab's runner + frontend picker.

## Alternatives considered

- **A) Keep the any-lab mutex**. Rejected: most labs (claude_science,
  pythia, llm_wiki_bridge, code_canvas, user_* plugins) don't reserve
  the roster — clearing the user's chosen assignee was a UX trap.
- **B) Drop the mutex entirely**. Rejected: mythos 5-agent RDT runner
  owns the issue end-to-end in sole mode; manual assignee would
  double-dispatch.
- **C) Apply mutex to every lab that has a leader**. Rejected: the
  leader auto-rewrite (ADR `0.3.46-lab-leader-rewrite`) already handles
  the "user picked nothing" case without blocking an explicit user pick.

## Consequences

- **Positive**: Mythos sole owners issue end-to-end (no double dispatch).
  Other labs keep user assignee intent. Enhancer mode surfaces a
  coherent "user picks target → lab supervises" flow.
- **Negative**: 3 layers of enforcement must stay in sync. Tests pin the
  narrowed contract: `TestBatchUpdateIssuesRespectsLabMutex` in
  `server/internal/handler/issue_lab_source_test.go`.

## References

- Ship log: `.omc/0.3.31-ship-2026-07-16.md` (initial)
- Narrowing: `.omc/release-notes-0.3.33.md`
- Batch + LabPicker realignment: `.omc/release-notes-0.3.64.md`
  (2026-07-28 labs audit, 9 high-severity fixes)
- CLAUDE.md "Lab ↔ Assignee Mutex" section