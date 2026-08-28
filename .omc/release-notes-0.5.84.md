---
name: release-notes-0.5.84
created: 2026-08-28T03:44:03Z
updated: 2026-08-28T03:44:03Z
---

# 0.5.84 — Causal Graph P0 fix batch (post-ship audit closure)

**Branch:** `epic/0.5.72-followups` (post `be80fe200`)
**Trigger:** `.omc/0.5.83-post-ship-verification.md` — 6 confirmed bugs head the fix list, 2 promoted to standing laws
**Wire contract:** Active Contracts #9 (0.5.84)

## TL;DR

All six P0 bugs from the 0.5.83 post-ship audit are fixed. The FLAG_ROUTE_SUFFIX
dict now carries the `causal_graph` row (silent-link trap closed for the second
time, regression-pinned). `createCausalSuggestion` probes `FindCausalEdgeBetween`
with ANY status before INSERT (never-nag contract now enforced on the proposer
path, mirroring the manual path that 0.5.83 already had). `TouchCausalNode` now
has callers at every issue / comment / task hot path via the new bulk SQL helper
`RefreshCausalNodesForIssue` + the `Recorder.RefreshForIssue` wrapper, so the
30-day stale TTL no longer hides Tier A chains. The maintenance ticker is
DB-anchored via the new mig 281 `causal_graph_maintenance_state` singleton row
plus a boot-time sweep — daily desktop restarts no longer starve the sweep.
Edge rendering splits by `status` (active solid, suggested muted-dashed,
rejected ghost-opacity). Ring layout pre-computes `ringLengths` before the
insertion loop (N=3 nodes now spread 0, 2π/3, 4π/3 as designed).

Two new standing laws codified (Active Contracts #9): every new lab flag key
MUST also add a `FLAG_ROUTE_SUFFIX` row; every proposer endpoint needs the
server-side never-nag probe. Plus the TouchCausalNode callsite contract:
every code path that touches an issue/comment/task MUST refresh
`last_observed_at` via `Recorder.RefreshForIssue`.

Cooperation ≈ 40–45% unchanged — these fixes close the structural write-side
bugs but do NOT unlock the read side. Daemon claim-response subgraph injection
(`handler/daemon.go::ClaimTaskByRuntime`) is the headline 0.5.85 item.

## Fixes (6 atomic commits + 3 merge commits + 1 bump = 10 new commits)

| # | Hash | Bug | Files | LOC | What |
| --- | --- | --- | --- | --- | --- |
| 1 | `5b33c25bc` | #1 FLAG_ROUTE_SUFFIX row | `issue-labs-section.{tsx,test.tsx}` | +63 | `causal_graph: "causal-graph"` dict row + 3-case regression pin (full mapping + unknown + null/empty/whitespace) |
| 2 | `ebff9cfcb` | #2 FindCausalEdgeBetween probe (+ contract #8b) | `causal_graph.{go,_test.go}` | +133 | Mirror manual-path probe at line ~580 (any-status 409) + 4-case regression pin (unique / duplicate / reject-tombstone+re-suggest / different type) |
| 3 | `fb659ed5f` | #3 TouchCausalNode callers | 9 files | +442/-3 | `Recorder.RefreshForIssue` wrapper on Tier A Recorder + new bulk SQL `RefreshCausalNodesForIssue` (CTE-based 1-hop neighbour expansion, DISTINCT + self-loop excluded) + callsites at `handler/issue.go::UpdateIssue`, `handler/comment.go::CreateComment`, `service/task.go::{enqueueIssueTask, enqueueMentionTask, CompleteTask}` + 3 regression pins (stale-survive-refresh / neighbour-expansion / flag-off no-op) |
| 4 | `9e7d00967` | #4 maintenance ticker DB-anchored | `maintenance.{go,_test.go}` + mig 281 (up/down) | +184 | Singleton table + boot sweep + pure `shouldSweepOnBoot` helper (DB-free) + 2 regression pins (boot-sweep / nil-pool short-circuit) |
| 5 | `cc4c761ec` | #6 ring layout angle divisor | `causal-minimap.tsx` | +106 | Pre-compute `ringLengths` BEFORE insertion; `layout()` exported for test; N=3 audit scenario now angles 0/2π/3/4π/3 |
| 6 | `ebfa68f77` | #5 edge status visual split | `causal-minimap.{tsx,test.tsx}` (new) | +170 | `EDGE_TONES[type][status]` shape + `resolveEdgeTone(type, status)` helper (unknown status → active, unknown type → neutral grey); 7-case regression (4 status + 3 ring spread N=3/4/6) |

## Schema (migration 281 — applied post-backup)

- **281** `causal_graph_maintenance_state` (singleton): `id BOOLEAN PRIMARY KEY
  CHECK (id = TRUE)`, `last_sweep_at TIMESTAMPTZ NOT NULL DEFAULT NOW()`.
  Seeded `ON CONFLICT DO NOTHING`. Powers the boot-anchor pattern that mirrors
  SemanticaGC.

## New standing laws (Active Contracts #9)

- **FLAG_ROUTE_SUFFIX row is required for every new lab flag key.** The dict
  row wires `labSourceRouteSuffix()` to `routes.tsx` paths; missing row
  silently no-ops. Same trap semantica 0.5.81 + causal 0.5.83 both hit
  (twice now). Pinned by the full-mapping assertion in
  `issue-labs-section.test.tsx` (covers all 9 built-in flag keys).
- **Every proposer endpoint (REST + internal service) needs the server-side
  never-nag probe.** `FindCausalEdgeBetween` with ANY status before INSERT.
  0.5.83 had it on manual (`createCausalEdge:580-587`), missed on REST
  (`createCausalSuggestion:707-781`). Pinned by
  `TestCreateCausalSuggestionProbesBeforeInsert` (4 cases incl. tombstone
  law).

## TouchCausalNode callsite contract (Active Contracts #9)

Every code path that touches an issue/comment/task MUST refresh
`last_observed_at` on relevant causal nodes via
`Recorder.RefreshForIssue(ctx, issueID)`. Legitimate hook points:

- `handler/issue.go::UpdateIssue` — post-success, L3228
- `handler/comment.go::CreateComment` — post-success, L1351
- `service/task.go::enqueueIssueTask` — after RecordTaskAction
- `service/task.go::enqueueMentionTask` — after RecordTaskAction
- `service/task.go::CompleteTask` — after RecordTaskOutcome

The bulk SQL helper `RefreshCausalNodesForIssue` is the only legitimate
refresh path (CTE-based 1-hop neighbour expansion; DISTINCT + self-loop
excluded; flag-off no-op). Pinned by `TestStaleNodesSurviveAfterRefresh` +
`TestRefreshForIssueTouchesNeighborNodes` + `TestRefreshForIssueNoOpWhenFlagOff`.

## Gates

- `pnpm typecheck` 6/6 (37.9 s, 3 cached)
- `cd server && go test -count=1 -timeout 600s ./internal/handler/...
  ./internal/service/causal_graph/... ./internal/experimental/...
  ./pkg/agent/...`: **5 packages PASS, 0 FAIL** (handler 16.7 s + causal_graph
  2.1 s + experimental 2.7 s + experimental/helpers 2.7 s + pkg/agent 15.0 s)
- Pre-existing flakes `TestInstallCausalGraphSeedsHiddenTeam` +
  `TestInstallTimesfm_SeedsVisibilityAndIdempotent` (DB lock count global, not
  workspace-scoped) did NOT fire this run; previously noted as same flake
  class as CLAUDE.md "Known Stability Surfaces".

## Process notes

- Three parallel fix agents (`worktree-wf_b31a5dc5-058-{1,2,3}`), each on its
  own worktree + branch. No file-set overlap except
  `service/causal_graph/maintenance.go` (Batch B 13-line package doc update +
  Batch C 80-line boot-anchor code) — git auto-merged cleanly.
- All 3 agents honoured hard rules: stayed within assigned bug scope, no
  version bump, no `git push`, no amending previous commits, surgical
  style-matched fixes.

## Deferred to 0.5.85 (next round)

- **P1 read-side**: daemon claim-response subgraph injection
  (`handler/daemon.go::ClaimTaskByRuntime`). Design from 0.5.83 audit:
  read-once at claim time, depth ≤ 2, top-20 nodes/edges, skip Tier D +
  status ≠ active, hard cap 4000 tokens + 200 ms ctx deadline + silent
  fallback. The headline item — agents cannot see the graph while working.
- **P2**: Tier C Pythia hypothesis→evidence closure (0.5.84 deferral);
  historian / verifier automation; evolver window bound; mention-flow doc
  notes.
- **Open decision gate**: MUL-6632 upstream inbox architecture (adopt vs
  fork-local filtering). Until decided, inbox-family upstream commits stay
  SKIP-DIVERGENCE.

## What changed in cooperation (post-fix projection)

| Layer | 0.5.83 | 0.5.84 | Δ |
| --- | --- | --- | --- |
| Write side | ~90% | ~90% (no change) | — |
| **Read side** | **0%** | **0%** (P1 pending) | — |
| Hidden-team autonomy | ~15% | ~15% (P2 pending) | — |
| Human gate | ~100% | ~100% (now tombstone-enforced on REST too) | ↑ |
| **Lab-picker integration** | **silent no-op** | **correctly wired** | ✅ fixed |
| **Tier A chain freshness** | **30-day stale** | **refreshed on every hot path** | ✅ fixed |
| **Maintenance GC** | **restart-fragile** | **DB-anchored, survives restarts** | ✅ fixed |
| **Edge visual** | **all 3 statuses same color** | **split (solid / muted / ghost)** | ✅ fixed |
| **Ring layout** | **angle divisor bug** | **even angular spread** | ✅ fixed |
| **Overall** | **~40-45%** | **~40-45%** | (read-side still pending) |
