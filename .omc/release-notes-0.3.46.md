---
name: 0.3.46 release notes
created: 2026-07-19T19:38:00Z
updated: 2026-07-19T19:38:00Z
status: shipped
---

# 0.3.46 — Lab leader rewrite on stale assignee (P0#4)

## Why this ship

0.3.34 added `assignDefaultLabAgentOnUpdate` so flipping
`lab_source` onto `claude_science_lab` / `constitution_agent` on an
unassigned issue auto-assigns the lab leader and dispatches a run.
The gate was `!issue.AssigneeType.Valid` — auto-assign only fired
when the issue had **no** assignee yet. The common path was missed:
the user creates an issue, picks any agent from the AssigneePicker,
then later flips `lab_source` onto the science lab. The lab runner
resolves its leader from `issue.assignee_id`, so the issue silently
ran on the wrong agent while the UI showed the lab badge.

This ship closes the gap with a dedicated `shouldRewriteAssigneeForLabLeader`
companion helper.

## What changed

`server/internal/handler/issue.go` — single-pass rewrite of the
`assignDefaultLabAgentOnUpdate` gate. New contract:

| Caller intent | Existing assignee | Result |
|---|---|---|
| lab_source untouched | (any) | noop (existing branch) |
| lab_source → no-leader lab (mythos_swarm) | (any) | noop (mythos owns roster) |
| lab_source → leader lab | already leader | noop (preserves deliberate AssigneePicker choice) |
| lab_source → leader lab | missing / non-agent / different agent | **rewrite to leader** |

The leader lookup is best-effort: if the leader agent row is missing
(install never ran), the helper conservatively skips to avoid
clobbering with "no leader installed" — `assignDefaultLabAgentOnUpdate`
still logs the install miss and proceeds for the unassigned branch.

The Mythos enhancer case is intentionally untouched — enhancer mode
expects the user-picked target assignee, and `defaultLabLeaderForKey`
returns `("", false)` for `mythos_swarm`, so the new branch is a
no-op for that lab.

## Verification (end-to-end on local DB + cold-start)

| Check | Result |
|---|---|
| `gofmt -w internal/handler/issue.go` | clean |
| `go vet ./...` | clean |
| `go build ./cmd/server/... ./cmd/multica/...` | clean |
| `go test -run "TestUpdateIssueLabSource" ./internal/handler/...` | 4/4 pass (1.272s) — incl. 2 new tests |
| `go test -count=1 -timeout 180s ./internal/handler/...` | 7.765s pass (no regression) |
| `bundle-cli` | 0.3.46 embedded |
| `electron-vite build` | renderer + main 0 errors |
| `electron-builder --mac --dir` | `dist/mac-arm64/Multica.app` (signed ad-hoc) |
| `cp -R /Applications/Multica.app` | replaced 0.3.45.9 |
| `defaults read Multica.app/Contents/Info CFBundleShortVersionString` | `0.3.46` |
| Cold-start 3-check (5432 / 8090 / `/health`) | all green |

## New tests

- `TestUpdateIssueLabSourceRewritesStaleAssignee`: pre-assigns a
  decoy agent, flips `lab_source` to `claude_science_lab`, asserts
  the assignee is rewritten to the `research` leader.
- `TestUpdateIssueLabSourceKeepsMatchingAssignee`: pre-assigns the
  leader, flips `lab_source`, asserts the assignee is preserved.
- Existing `TestUpdateIssueLabSourceDispatchesResearch` and
  `TestUpdateIssueLabSourceBacklogParks` still pass — the change
  is backward-compatible on the unassigned / backlog paths.

## 0.3.47+ deferred

- OpenScience binary still not vendored — claude_science flag
  shows "service not bundled" when enabled. Drop binary at
  `apps/desktop/vendor/openscience-bin/openscience` before next
  bundle-cli to close.
- Pythia `oracle.py` per-issue forecast endpoint + 10-round SSE
  deliberation loop is still synthetic; swap to real model call
  pending.
- self-opt LLM rationale + prompt_suggestion write-back to
  `agent.prompt` / `agent_prompt_history` still heuristic.
- Real KB integration for self-opt learning vault (replace
  `FileSystemKBWriter` with `llm_wiki_bridge` or a new IPC).