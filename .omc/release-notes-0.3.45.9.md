---
name: 0.3.45.9 release notes
created: 2026-07-19T19:25:00Z
updated: 2026-07-19T19:25:00Z
status: shipped
---

# 0.3.45.9 — Polling fallback for 4 sibling query keys

## Why this ship

0.3.45.7 closed P0#3.7 (agentTaskSnapshotOptions 5s polling fallback)
for the workspace-wide agent task indicator. The audit found four
sibling query keys had the identical failure mode (WS push dropped,
fired before mount, or network blip), and the user landed the four
patch files via parallel tooling before this ship. This commit
captures those edits with the same first-party reasoning as 0.3.45.7
so the next reader can audit them in context.

## What changed

All four edits follow the same pattern: add `refetchInterval` (5s
while the rows can still change, 30s / 60s / false when terminal),
with first-party comment blocks documenting the WS gap.

| File | Key | Idle | Active | Reason |
|---|---|---|---|---|
| `packages/core/autopilots/queries.ts` | `autopilotRunsOptions` | false (WS + focus only) | 5s while any `status==='running'` | autopilot:run_start / autopilot:run_done drops |
| `packages/core/workspace/queries.ts` | `squadMemberStatusOptions` | 5s fixed | 5s fixed | squad-member pill presence signal matches daemon 5s poll |
| `apps/desktop/.../experimental-artifact-view.tsx` | `["claude-science-runtime", "sessions", wsId]` | 30s | 5s while any `queued`/`running` | no WS reaches this custom key |
| `apps/desktop/.../self-opt-history-view.tsx` | `["self-opt-runs", "list", wsId]` | 60s | 5s while any non-terminal | self-opt has no WS event at all |

Each file got a `LIVE_*_STATUSES` / `TERMINAL_*_STATUSES` constant
mirroring the StatusBadge branch set in the same component, so a
status flip in the backend lands in the renderer within 5s.

## Verification (end-to-end on local DB + cold-start)

After shipping 0.3.45.9 to `/Applications/Multica.app`:

| Check | Result |
|---|---|
| `tsc --noEmit -p packages/views/...` | clean |
| `tsc --noEmit -p packages/core/...` | clean |
| `tsc --noEmit -p apps/desktop/tsconfig.web.json` | clean |
| `pnpm vitest run` (packages/views) | 141 files / 1280 tests pass |
| `bundle-cli` | 0.3.45.9 embedded |
| `electron-vite build` | renderer + main 0 errors |
| `electron-builder --mac --dir` | `dist/mac-arm64/Multica.app` (signed ad-hoc) |
| `cp -R /Applications/Multica.app` | replaced 0.3.45.8 |
| `defaults read Multica.app/Contents/Info CFBundleShortVersionString` | `0.3.4-5.9` |
| Cold-start 3-check (5432 / 8090 / `/health`) | all green |

## Source provenance

These four edits were authored outside this conversation via parallel
programming tools before this ship; the commit message reflects
their intent with first-party reasoning, and the release notes
attribute the polling-fallback pattern to the 0.3.45.7 ship so
future readers can audit the lineage.

## 0.3.46+ deferred

- The 0.3.45.8 release notes' "OpenScience binary not vendored"
  carry-over: drop the binary at
  `apps/desktop/vendor/openscience-bin/openscience` before next
  bundle-cli to close.
- Pythia `oracle.py` per-issue forecast endpoint + 10-round SSE
  deliberation loop is still synthetic; swap to real model call
  pending.
- `assignDefaultLabAgentOnUpdate` should merge into the
  issue-update mutation so a user who changes the assignee mid-run
  keeps the lab's leader (currently can be overwritten by the
  AssigneePicker).