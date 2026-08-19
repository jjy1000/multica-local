# Release Notes — 0.5.38

**Shipped 2026-08-19** (branch `epic/0.5.13-integration`, commit `5a57a89a1`)

## Summary

Bug fix: agent-completed child issues now notify + wake their parent agent. Zero migrations, zero schema drift, 2 files changed (+111).

## The bug

When a main agent created sub-issues (children) and the sub-agent finished them, the parent agent was never told. `notifyParentOfChildDone` (child-done → parent system comment + parent-assignee wake-up) was only wired into the HTTP `UpdateIssue` / `BatchUpdateIssues` / GH-merge paths. The daemon `CompleteTask` path — the one used when an **agent** finishes a task — flipped `issue.status` to `done` via direct SQL (`UpdateIssueStatus`), bypassing the handler, so:

- no system comment appeared on the parent timeline
- the parent assignee got no wake-up task
- parent agents had to be polled manually ("必须人工对话拉取")

**Live evidence** (JYF-352 workflow, 2026-08-19): children JYF-353/354 (parent set, stage barrier closed, squad assignee) completed via the daemon path at 12:12/12:17 — JYF-352 received **zero** system comments and **zero** wake-up tasks between 12:01 and 12:20. The main agent only found out by polling inside its own run (12:21 "Both sub-issues are done… I need to read the actual revision reports") and the user had to ask manually (12:20 "看一下具体情况").

Same gap exists upstream (`notifyParentOfChildDone` only on `issue.go` + `github.go` there too) — fork-local fix.

## The fix

`server/internal/handler/daemon.go` `CompleteTask`: after the `in_review`/`todo` → `done` flip, re-read the issue and call `notifyParentOfChildDone` exactly like the `UpdateIssue` path. All existing guards apply unchanged (parent state, backlog park, stage barrier, idempotency dedup, anti-loop).

**Regression test** `TestCompleteTask_ChildDone_NotifiesParent` pins the full chain: child flips to `done` → parent receives a system comment announcing completion → parent assignee is enqueued with a mention task triggered by that comment.

## Known boundary (not fixed, documented)

`mythos` lab auto-completion (`service/mythos/{runner,supervise}.go` also flip to `done` via direct SQL) has the same gap. Service layer cannot call the handler method — a fix requires sinking the notification logic (larger refactor). Lab issues used as sub-issues are a rare combination; tracked as a future item.

## Verification

- `pnpm typecheck` (full turbo) — see ship log
- `go test -count=1 -timeout 600s ./internal/... ./pkg/agent/...` — pass except documented pre-existing `TestTickSupervision_CompletionByFinalIssueStatus` panic (byte-identical at 0.5.34 baseline)
- handler package full suite 16.1s green; gofmt/vet clean

## Deferred (unchanged from 0.5.37)

MUL-6286 / MUL-5651 / MUL-6321 / MUL-6063 / MUL-5991 / 0c69f1f95 / MUL-6350 plugin 1-4 / MUL-6327 (SKIP-DEAD-CASE) / MUL-6335 (SKIP-NO-ENDPOINT) / MUL-6323 (agent-fail, re-defer). New: mythos child-done notification (service-layer gap).
