---
name: 0.3.47 release notes
created: 2026-07-19T14:16:24Z
updated: 2026-07-19T14:16:24Z
status: shipped
---

# 0.3.47 — Batch parity for P0#4 lab-leader rewrite + ctx hygiene

## Why this ship

A full audit of the 0.3.46 P0#4 lab-leader rewrite surfaced one
CRITICAL regression and two HIGH findings that blocked the contract
from being honoured end-to-end. `CLAUDE.md` (Active Contracts §2)
explicitly states:

> Future CreateIssue / BatchUpdateIssues / workflow-script paths
> that touch `lab_source` must go through this helper rather than
> re-derive the gate.

0.3.46 shipped UpdateIssue + CreateIssue coverage but missed
`BatchUpdateIssues`. A scripted batch PATCH like
`{"issue_ids":[…50…], "updates": {"lab_source": "claude_science_lab"}}`
against 50 unassigned issues persisted 50 lab-tagged issues with no
leader assignee — `WillEnqueueRun` never fired because
`assigneeChanged` stayed false, so the research leader never started
on any of them while the UI showed lab badges. This ship closes the
gap.

## What changed

### CRITICAL — `BatchUpdateIssues` lab-leader rewrite parity

`server/internal/handler/issue.go` — `BatchUpdateIssues` per-issue
loop now mirrors the UpdateIssue 4-case contract. After
`params.LabSource` is set:

1. Call `h.shouldRewriteAssigneeForLabLeader(r.Context(), &prevIssue, …)`.
2. On `true`, look up the leader (same `GetAgentByWorkspaceAndName`
   query), write `params.AssigneeType = "agent"` + `params.AssigneeID = leader.ID`.
3. Fold `labAutoRewrote = true` into the post-`UpdateIssue`
   `assigneeChanged` calc so the dispatch path arms.

The pre-existing mutex gate (`postLab && postAssignee` ⇒ skip)
already enforces that requests carrying both `lab_source` and
`assignee_*` are skipped per-issue, so this rewrite only fires on
batch PATCHes that carry `lab_source` alone — exactly the gap that
was unhandled.

New test: `TestBatchUpdateIssuesLabSourceAutoAssignsLeader` — flips
3 unassigned issues to `claude_science_lab` via batch, asserts all
3 carry `assignee_type=agent` + `assignee_id=research` AND each
enqueued a non-zero research-run count (dispatch path arms).

### HIGH — `shouldRewriteAssigneeForLabLeader` ctx hygiene

The helper used `context.Background()` for its leader lookup —
the **only** handler helper in `issue.go` to bypass `r.Context()`.
This silently dropped per-request cancellation, deadlines, and any
future tracing/logging propagated via context. Signature changed:

```go
func (h *Handler) shouldRewriteAssigneeForLabLeader(
    ctx context.Context, issue *db.Issue, labSource string) bool
```

Call site at `issue.go:2822` passes `r.Context()`. `assignDefaultLabAgentOnUpdate`
already took a `ctx` parameter and threaded it correctly; this
brings the companion helper in line.

### HIGH — Dead `mythos_swarm → mythos_prelude` map entry removed

`server/internal/service/issue.go` `defaultLeaderAgentForLab`
contained `"mythos_swarm": "mythos_prelude"`, but:

- `IssueService.Create:306` explicitly short-circuits
  `labSourceKey == "mythos_swarm"` before consulting the map, so
  the entry was unreachable code.
- The handler-side `defaultLabLeaderForKey` returns `("", false)`
  for `mythos_swarm` (the runner owns the roster; auto-assign
  intentionally suppressed).

The divergence was a foot-gun: any future PR removing the explicit
short-circuit would silently start dispatching `mythos_prelude`
and fighting the 5-agent RDT runner. Entry deleted. Handler
behaviour is the source of truth; service map mirrors it.

### MEDIUM — Coverage gaps closed

`server/internal/handler/issue_lab_dispatch_test.go` — three new
tests + one extended assertion:

- `TestUpdateIssueLabSourceUntouchedNoOp` (case A) — PATCH carrying
  only `title` leaves assignee + lab_source alone.
- `TestUpdateIssueLabSourceMythosSoleNoAutoAssign` (case B) —
  flipping to `mythos_swarm` on an unassigned issue must NOT
  auto-assign (runner owns roster).
- Extended `TestUpdateIssueLabSourceRewritesStaleAssignee` (case D)
  with `taskCountFor > 0` assertion — pins that the rewrite arms
  the dispatch path (matches `TestUpdateIssueLabSourceDispatchesResearch`
  for the no-assignee case).

The 0.3.46 4-case decision table is now fully pinned by tests on
the UpdateIssue path; the Batch path has one parity test (case D).
Cases A/B/C via batch are lower-risk because the per-issue skip-on-
failure contract already covers them, but can be added on demand.

### LOW — Documentation

- `assignDefaultLabAgentOnUpdate` doc-comment (issue.go:2930)
  updated — the trigger condition is now "the existing assignee
  does NOT already point at the leader", not "no assignee yet".

## Verification

```
cd server && go test -race -count=1 -timeout 180s ./...
```

All packages green. Relevant:
- `internal/handler` 11.3s — 4 lab-source tests + 1 extended
  + 1 new batch parity test all pass.
- `internal/service` 1.7s — no regression from dead-map removal.
- `internal/experimental` 2.3s — catalog unaffected.

## Migration / Rollback

None. No schema change. No IPC change. The P0#4 helper signature
adds a `ctx` parameter; no other caller exists in the codebase
(`grep` confirmed: only the UpdateIssue call site at issue.go:2822).

## Diff size

- `server/internal/handler/issue.go` — +47 lines (batch rewrite +
  ctx signature + doc comment), -1 line (removed `context.Background`).
- `server/internal/handler/issue_lab_dispatch_test.go` — +153 lines
  (3 new tests + 1 extended assertion).
- `server/internal/service/issue.go` — -1 line (dead map entry).

Net: +198 / -2 across 3 files. Single commit `fix(issue/lab): batch parity for P0#4`.