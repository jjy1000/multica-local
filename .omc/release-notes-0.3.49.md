---
name: 0.3.49 release notes
created: 2026-07-20T00:00:00Z
updated: 2026-07-20T00:00:00Z
status: shipped
---

# 0.3.49 — LOW leftover cleanup (self-opt cadence + claude-lab doc + VIEW_LAB_SOURCES anchor)

## Why this ship

The 0.3.48 audit surfaced 5 LOW items that did not block ship but
left small drifts in the lab-class query cadence contract and the
renderer-side catalog anchors. 0.3.48 closed #4 (`api.getIssue`
parseWithFallback) and the audit of 0.3.49 found that #5 (the
`WHERE archived_at IS NULL` test-fixture race) was a **misdiagnosis**
— no such race exists in the codebase. #2 (self-opt staleTime/idle
asymmetry) was the only behavioural fix on the LOW list. #1
(`VIEW_LAB_SOURCES` vs `HideFromIssueLabPicker` overlap) hides a
5-file cross-package refactor that belongs in a dedicated PR, not a
LOW ship; the 0.3.49 docblock change locks the semantic gap so
future audits do not misdiagnose again.

## What changed

### LOW — `self-opt` staleTime/idle asymmetry fixed

`apps/desktop/src/renderer/src/pages/self-opt-history-view.tsx`

`staleTime` was `30_000` while `refetchInterval`'s idle branch was
`60_000`. With `staleTime < idle`, TanStack Query marked the cache
stale on every focus and triggered a redundant refetch on top of the
idle poll — focus-driven churn the idle beat was supposed to
suppress. Mode C contract (no WS + very low frequency) from the
0.3.45.7 → 0.3.45.9 lineage requires both numbers to agree on the
same baseline.

Change: `staleTime: 30_000` → `60_000`. Now matches the
`active ? 5_000 : 60_000` idle cadence exactly.

### LOW — `claude-lab` session-tab `idle: 15_000` documented as Mode B variant

`apps/desktop/src/renderer/src/pages/claude-lab-view.tsx:1319` and `:1554`

Both `claude-lab-runtime-sessions` (Artifact tab) and
`claude-lab-code-sessions` (Code tab) use a `15_000` idle cadence
instead of the canonical Mode B `30_000`. The `0.3.45.8 ship log` called
this deliberate (per-issue scoped tabs where the user is actively
viewing the surface). 0.3.49 expands the inline comments to make the
rationale explicit (per-issue viewing assumption + what to flip if the
assumption changes) and mirrors the same comment in
`CLAUDE.md` Labs Platform §5s polling fallback so future audits do
not flag it as a drift again.

Behaviour unchanged: still `15_000` idle. Documentation-only fix.

### LOW — `VIEW_LAB_SOURCES` semantics vs `HideFromIssueLabPicker` locked

`packages/views/issues/components/issue-labs-section.tsx:47-70`

The 7-element hardcoded `VIEW_LAB_SOURCES` Set is the renderer-side
anchor for "does this lab own a workbench view + hide agent
deliverable comments in the plain timeline?". It happens to share 6
keys with `HideFromIssueLabPicker`, but the two answer different
questions:

- `VIEW_LAB_SOURCES` answers "does this lab ship a workspace-scoped
  view?" (used by `issue-detail.tsx` to hide the agent deliverable
  thread).
- `HideFromIssueLabPicker` answers "should this lab be hidden from
  the per-issue LabPicker popover?" (used by `lab-picker.tsx`).

A future audit could misdiagnose the overlap again and propose to
"switch consumers to read the server-driven truth" — which would
silently flip `hideLabAgentComments` OFF for 5 of 7 labs. The
0.3.49 docblock makes the split explicit. **Behaviour unchanged.**

The full server-driven migration (new catalog field
`HidesDeliverableInIssueTimeline bool`, plumbed through
`experimental_flags.go` + `core/types/experimental.ts` + the two
consumer sites) is **deferred to 0.3.49.1** because it crosses
5 files spanning server → core → views.

### CLOSED without fix — test fixture `WHERE archived_at IS NULL` race

A targeted grep across `server/internal/handler/`,
`server/internal/service/`, and `server/cmd/server/` `*_test.go`
files found exactly one `archived_at IS NULL` reference:

- `cmd/server/rerun_session_test.go:25-29` — SELECT-only fixture
  picker; the matching cleanup at `:43-46` is already id-scoped
  (`DELETE FROM agent_task_queue WHERE issue_id = $1`).

All other `archived_at` test usage is in id-scoped UPDATEs
(`issue_lab_dispatch_test.go:47`, `runtime_archive_squad_cleanup_test.go:66/99`,
`squad_briefing_test.go:240`) or workspace+LIKE-prefixed DELETEs
(`runtime_visibility_test.go:146`, `agent_thinking_test.go:27/214/314`,
etc.) — neither pattern exhibits the SELECT-then-DELETE race the
LOW item described. **No race exists.** Item closed.

A speculative follow-up (per-test random suffix on LIKE prefixes so
parallel `go test -race -count=1` runs cannot collide) is filed but
not actioned unless a real flake is observed.

## What did NOT change

- **No schema migration** — pure renderer + docs cleanup.
- **No IPC change** — channels unchanged.
- **No Go change** — server never had a staleTime concept; only
  client-side TanStack Query state.
- **No new flag** — same 9 lab flags shipped since 0.3.48.

## Verification

```
cd packages/core && pnpm vitest run                       # 63 files / 645 tests pass in 2.26 s
cd packages/views && pnpm vitest run issues/             # 25 files / 242 tests pass in 9.27 s
tsc --noEmit -p tsconfig.json   (core)                   # 0 errors
tsc --noEmit -p tsconfig.json   (views)                  # 0 errors
tsc --noEmit -p tsconfig.node.json --composite false     (desktop node)  # 0 errors
```

The desktop renderer-side `tsc --noEmit -p tsconfig.web.json`
still emits two pre-existing TS6307 errors (daemon hooks shared/,
pageview-tracker) — unchanged from 0.3.47/0.3.48. Not in scope.

## Diff size

| File | Change |
|------|--------|
| `apps/desktop/src/renderer/src/pages/self-opt-history-view.tsx` | +7 / -1 |
| `apps/desktop/src/renderer/src/pages/claude-lab-view.tsx` | +17 / -0 |
| `packages/views/issues/components/issue-labs-section.tsx` | +18 / -0 |
| `CLAUDE.md` | +2 / -0 |
| `apps/desktop/package.json` | version bump |

Net: **+45 / -1** across 5 files. Single atomic commit
`fix(labs): 0.3.49 — LOW leftover cleanup`.

## 0.3.49.1 candidate (deferred)

- **`VIEW_LAB_SOURCES` → catalog field migration** — add
  `Flag.HidesDeliverableInIssueTimeline bool` (JSON
  `hides_deliverable_in_issue_timeline`), seed `true` for the
  current 7 flags, switch the two consumers. ~5 files, ~+40/-20.
