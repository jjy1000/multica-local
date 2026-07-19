---
name: 0.3.45.8 release notes
created: 2026-07-19T19:20:00Z
updated: 2026-07-19T19:20:00Z
status: shipped
---

# 0.3.45.8 — Claude Lab UX cleanup + by-issue route + lab-picker gating

## Why this ship

Three UX regressions in the Claude Lab + Labs surface surfaced
during the JYF-212 muscle-explosive-power experiment (2026-07-19
18:06). The lab's research agent ran end-to-end and produced a
2030-word report, but the user's view of the workbench had three
broken surfaces:

1. The "产物" tab's "最近实验 session" call returned 400 and
   rendered "加载 session 失败: by-issue 400".
2. The issue-detail LabPicker offered LLM Wiki bridge and
   智能体自优化循环 as per-issue "实验插件" choices even though
   those flags are infrastructure / self-driven (their effect is
   global, not per-issue).
3. The Claude Lab panel had a separate "实验室智能体" picker that
   let the user bind a lab agent independently of the issue's
   normal assignee, leaking the lock across surfaces.

Plus a fourth, smaller regression: the issue-detail right-rail
"执行日志" panel hid the freshly-completed run's transcript behind
a hover-only transcript button, so the user couldn't see the
"Excellent. Let me search for more sources..." reasoning chain
inline on the issue.

This ship closes all four with first-party logic (no shims), each
behind a unit test, and verified end-to-end on the local PG.

## What changed

### 1. GET /api/experimental/claude-science-runtime/sessions/by-issue

The route was never registered. chi fell through to
`/sessions/{sessionID}` with sessionID = literal "by-issue",
`util.ParseUUID` failed, the handler returned 400 "sessionID is not
a UUID", and the 产物 tab rendered
"加载 session 失败: by-issue 400".

Fix is additive:

- `pkg/db/queries/experimental_claude_runtime.sql` —
  `ListExperimentalClaudeRuntimeSessionsByIssue` (workspace_id +
  issue_id + LIMIT, mirrors the existing by-workspace query).
- `pkg/db/generated/experimental_claude_runtime.sql.go` —
  regenerated via `sqlc generate`.
- `internal/handler/claude_science_runtime.go` —
  `ListClaudeScienceRuntimeSessionsByIssue` handler with
  workspaceMember gate, issue_id + limit validation. Limit clamped
  to [1, 100], default 20.
- Route registered **BEFORE** `/sessions/{sessionID}` on the chi
  router so `{sessionID}` does not capture the literal "by-issue".
  Documented in the route block.

Verified end-to-end:
```
$ curl -s -H "Authorization: Bearer $tok" \
  ".../sessions/by-issue?workspace_id=$WS&issue_id=$ISSUE&limit=5"
{"sessions":[],"total":0}   # 200 OK
```

### 2. LabPicker — hide infrastructure / self-driven flags

Add `catalog.Flag.HideFromIssueLabPicker bool` (default false).
Set true on `llm_wiki_bridge` (MCP-served at runtime, picking it
per-issue has no meaning) and `agent_self_optimization` (4-day
self-driven scheduler, not issue-bound).

Surface via `ExperimentalFlagResponse.hide_from_issue_lab_picker`;
the LabPicker filters entries by this field. The Labs settings
tab still shows every flag — only the per-issue picker omits
them.

- `server/internal/experimental/catalog.go` — new field on `Flag`,
  set on 2 catalog entries, full first-party doc comment.
- `server/internal/handler/experimental_flags.go` — response
  carries the flag.
- `packages/core/types/experimental.ts` — TS type field with
  doc comment.
- `packages/views/issues/components/pickers/lab-picker.tsx` —
  filter entries; first-party comment block.
- `packages/views/issues/components/pickers/lab-picker.test.tsx` —
  new test "hides flags whose hide_from_issue_lab_picker is true".
  7/7 pass.

### 3. Claude Lab panel — drop lab-agent picker

The 0.3.29 `LabAgentLockBar` let the user pick a workspace-wide
agent and bind that lock to the lab, separately from the issue's
normal assignee picker. Replace with `LabAgentFromIssue`: the
running agent is now derived from the bound issue's
`assignee_type='agent'` + `assignee_id` (set when the user picked
the lab in IssueDetail's PropRow). The strip is read-only — no
select, no agent list, no clear button. The Code tab pulls
`agent_id` from the same `api.getIssue(id)` query.

- `apps/desktop/src/renderer/src/pages/claude-lab-view.tsx` —
  drop `LabAgentLockBar`, add `LabAgentFromIssue`, drop
  `lockedAgentId` state, remove the explicit-clear useEffect.
  `ActiveTab` and `CodeTab` props lose `lockedAgentId`. Comment
  block explains the new contract.
- `packages/views/locales/{en,zh-Hans,ja,ko}/claude-lab.json` —
  add `agent_lock_no_assignee_hint` (issue-panel-first hint when
  the bound issue has no agent assignee yet).

### 4. ExecutionLogSection — auto-open latest past-run transcript

The right-rail "执行日志" panel hid the freshly-completed run's
transcript behind a hover-only transcript button. Add
`TranscriptButton.autoOpen` prop; `ExecutionLogSection` pins the
newest past run (idx 0, sorted newest-first) with
`autoOpen=true`. The dialog fetches on mount via the existing
`api.listTaskMessages` and stays open until dismissed.

- `packages/views/common/task-transcript/transcript-button.tsx` —
  new `autoOpen` prop, mount-fetch effect, latch.
- `packages/views/common/task-transcript/transcript-button.test.tsx` —
  new test "autoOpen: terminal task fetches on mount and renders
  without a click". 6/6 pass.
- `packages/views/issues/components/execution-log-section.tsx` —
  pass `autoOpen={idx === 0}` on first past task.

## Verification (end-to-end on local DB + cold-start)

After shipping 0.3.45.8 to `/Applications/Multica.app`:

| Check | Result |
|---|---|
| `go vet ./...` | clean |
| `go build ./cmd/server/... ./cmd/multica/...` | clean |
| `go test -count=1 -timeout 120s ./internal/handler/...` | 1.081s pass |
| `tsc --noEmit -p packages/views/...` | clean |
| `tsc --noEmit -p apps/desktop/tsconfig.web.json` | clean |
| `pnpm vitest run` (packages/views) | 141 files / 1280 tests pass |
| `sqlc generate` | ListExperimentalClaudeRuntimeSessionsByIssue emitted |
| `bundle-cli` | 3 Go binaries built, `0.3.45.8` embedded |
| `electron-vite build` | renderer + main 0 errors |
| `electron-builder --mac --dir` | `dist/mac-arm64/Multica.app` (signed ad-hoc) |
| `cp -R /Applications/Multica.app` | replaced 0.3.45.7 |
| `defaults read Multica.app/Contents/Info CFBundleShortVersionString` | `0.3.4-5.8` |
| Cold-start 3-check (5432 / 8090 / `/health`) | all green |
| Row parity | workspace=1 / issue=205 / comment=1145 / agent=90 |
| `GET /api/experimental/claude-science-runtime/sessions/by-issue?...` | **200 OK** (was 400) |
| `GET /api/experimental/claude-science-runtime/sessions?workspace_id=...` | 200 OK (regression check) |
| `GET /api/experimental-flags` | llm_wiki_bridge / agent_self_optimization now carry `hide_from_issue_lab_picker: true` |

## NOT packaged in this commit

- 4 files modified by other programming tools that the user
  flagged but I did not author (experimental-artifact-view.tsx,
  self-opt-history-view.tsx, autopilots/queries.ts,
  workspace/queries.ts) — left for the next batch ship after
  the user verifies them.

## 0.3.45.9+ deferred

- DefaultLabLeaderForKey already wires `assignee_type=agent,
  assignee_id=research` when a Claude Lab issue is created. Now
  that the lab panel reads agent from issue, the assignee
  mutation must keep that invariant — currently it can be
  overwritten by the issue-detail AssigneePicker. The
  `assignDefaultLabAgentOnUpdate` path needs to merge into the
  issue-update mutation so a user who changes the assignee mid-run
  gets the same agent back. Tracked separately.
- OpenScience binary still not vendored (claude_science flag
  shows "service not bundled"). Drop binary at
  `apps/desktop/vendor/openscience-bin/openscience` before next
  bundle-cli to close.
- Pythia `oracle.py` per-issue forecast endpoint + 10-round SSE
  deliberation loop is still synthetic; swap to real model call
  pending.