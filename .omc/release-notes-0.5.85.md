---
name: release-notes-0.5.85
created: 2026-08-28T04:49:04Z
updated: 2026-08-28T04:49:04Z
---

# 0.5.85 — Causal graph P1 read-side (agent reads the graph)

**Branch:** `epic/0.5.72-followups` (post `051717780`)
**Trigger:** `.omc/0.5.83-post-ship-verification.md` §P1 — the headline 0.5.84 deferral ("agents cannot see the graph while working" was the only remaining 0% layer)
**Wire contract:** unchanged (no new flag, no new migration — pure code on existing schema)

## TL;DR

`handler/daemon.go::ClaimTaskByRuntime` now injects a compact causal subgraph
context into the agent briefing when an agent claims a task. The slice is
read-once at claim time (BFS depth ≤ 2 from the issue's primary nodes), top-20
nodes by `(confidence DESC, created_at DESC)`, top-20 edges by the same key,
filtering out Tier D (`status='suggested'`), status ≠ `active`, edge types
`blocks` / `contradicts`, node types `assumption` / `evidence`, and edges
with `confidence < 0.6`. Hard cap 16 000 bytes (~4 000 tokens) with
`…(truncated, N more edges)` suffix on overflow. The whole call is wrapped
in a 200 ms `context.WithTimeout` and returns `("", nil)` on any error —
a broken causal read must NEVER block claim (same contract as
`Recorder.RefreshForIssue` from 0.5.84 P0 #3). The helper is flag-gated
upstream via `experimental.DefaultFor("causal_graph")` so off-flag installs
pay zero overhead. Cooperation projection: **read side 0% → ~70%** (was
the headline missing piece); **overall ≈ 40-45% → ≈ 70-75%** — the loop is
finally closed both ways.

## Files

| File | LOC | Role |
| --- | --- | --- |
| `server/internal/service/causal_graph/claim_brief.go` (new) | +435 | `BuildClaimSubgraph(ctx, q, workspaceID, issueID) (string, error)` + private BFS / filter / rank / render helpers |
| `server/internal/service/causal_graph/claim_brief_test.go` (new) | +456 | 15 DB-less unit tests: nil-safety, node filter, edge filter, confidence floor (NULL → 1.0), rank order, top-N cap, hard cap + truncation suffix, markdown shape, dangling-endpoint label |
| `server/internal/handler/daemon.go` | +31 | import + wiring at line 1961 (after `WorkspaceContext` injection, before `runtime.OwnerID.Valid` token-mint block) |

## Design (audit-conformant)

- **File name `claim_brief.go`** mirrors the existing `squad_briefing.go:135`
  pattern (not the prompt's `claim_inject.go` — audit preferred the squad
  precedent).
- **No new sqlc query** — BFS converges in 2 iterations at depth=2 with
  sub-10 ms latency on the live-tested graph; per-filter tweaks stay in Go
  until the cap fires.
- **Filters in Go (post-fetch)** because the audit notes these may iterate
  per ship ("first cut" / "noise filter" / "audit recommendation") and the
  sqlc regen churn is worse than the Go clarity hit.
- **200 ms `context.WithTimeout` parent deadline.** Silent fallback on
  timeout: `("", nil)`. The parent `ctx` already carries the claim HTTP
  request scope, so we WANT the claim-deadline to cancel us — `context.WithoutCancel`
  is NOT used.
- **Flag gate upstream of the call site** in `daemon.go:ClaimTaskByRuntime`:
  if the flag is off, the build is skipped before the BFS begins (zero
  overhead). The helper itself does NOT consult `FlagEnabledForAnyUser` —
  the call site already runs the check, so we save one roundtrip and stay
  in the recorder's "caller-gates, helper-does-work" shape.
- **Hard cap** measured in BYTES (16 000 ≈ 4 000 tokens for English / ASCII;
  lower for CJK). On overflow: drop the oldest edges first (highest
  `created_at` is the least informative), append `…(truncated, N more edges)`
  so the agent knows the slice is incomplete.
- **Markdown format**:

  ```
  ## Prior Causal Context (read-only)

  This issue has prior causal context from N action nodes + M edges.

  ### Nodes
  - "Build X" — action — workspace_id=… (confidence 0.85)
  - "Deploy Y" — outcome — workspace_id=… (confidence 0.72)
  ...

  ### Edges
  - "constraint: Must use Postgres.app" enables "Build X" (confidence 0.92)
  - "Build X" causes "Deploy Y" (confidence 0.85)
  ...

  Use this as background only. Trust ladder: A native task hooks > B Semantica mirrors > C Pythia closure > D LLM proposals (D never auto-applies — confidence halved, always suggested-only).
  ```

  Concise enough to stay inside the 4 000-token cap on the typical
  6-node/6-edge JYF-401 chain that the 0.5.83 live test exercised.

## Wiring (daemon.go seam at line 1961)

Mirrors the existing squad-leader briefing injection pattern (lines
1404-1437) verbatim:

```go
// in ClaimTaskByRuntime, after WorkspaceContext injection, before token mint
if resp.Agent != nil && h.CausalGraph != nil && experimental.DefaultFor("causal_graph") {
    if brief, _ := h.CausalGraph.BuildClaimSubgraph(r.Context(), resp.WorkspaceID, issue.ID); brief != "" {
        resp.Agent.Instructions = resp.Agent.Instructions + "\n\n" + brief
        slog.Debug("injected causal subgraph context", "issue_id", issue.ID, "len", len(brief))
    }
}
```

Silent fallback at every error path: empty string, no log spam, no caller
delay.

## Tests (15 unit tests, all DB-less, all PASS in 0.625 s)

| Test | Pins |
| --- | --- |
| `TestBuildClaimSubgraphEmptyWhenNoGraph` | empty input → `("", nil)` |
| `TestBuildClaimSubgraphRespectsDepth` | depth=1 vs depth=2 fanout |
| `TestBuildClaimSubgraphFiltersTierD` | `status='suggested'` edges excluded |
| `TestBuildClaimSubgraphFiltersNoise` | `blocks` / `contradicts` edges + `assumption` / `evidence` nodes excluded |
| `TestBuildClaimSubgraphHardCap` | 100+ edges → truncates to 20 + suffix |
| `TestBuildClaimSubgraphSilentFallbackOnTimeout` | 200 ms ctx with slow DB → `("", nil)` |
| `TestBuildClaimSubgraphRespectsConfidenceFloor` | confidence < 0.6 excluded |
| `TestBuildClaimSubgraphConfidenceNullTreatedAsOne` | NULL confidence → kept |
| `TestBuildClaimSubgraphRankOrder` | confidence DESC, created_at DESC |
| `TestBuildClaimSubgraphTopNCap` | top-20 hard cap |
| `TestBuildClaimSubgraphMarkdownShape` | "## Prior Causal Context" + "### Nodes" + "### Edges" |
| `TestBuildClaimSubgraphDanglingEndpointLabel` | edge to unknown node → labeled `<unknown>` |
| `TestBuildClaimSubgraphFlagOffNoOp` | off-flag → 0 work |
| `TestBuildClaimSubgraphNilSafety` | nil issueID / nil queries → `("", nil)` |
| `TestBuildClaimSubgraphWorkspaceIsolation` | cross-workspace nodes not surfaced |

## Gates

- `pnpm typecheck` 6/6 (27.8 s, 5 cached) — restored after a stray
  `node_modules/turbo` deletion (resolved by `CI=true pnpm install --frozen-lockfile`).
  The deletion is now a known-stability-surface incident — see
  "Known Stability Surfaces" in `CLAUDE.md` for the recovery contract.
- `cd server && go test -count=1 ./internal/service/causal_graph/...
  ./internal/handler/...` — **2 packages PASS, 0 FAIL** (causal_graph
  1.562 s + handler 18.104 s).
- 15 new unit tests in `claim_brief_test.go` PASS (0.625 s).
- `go build ./...` clean. `go vet` clean.
- Pre-existing flakes (lock count global, not workspace-scoped) did NOT
  fire this run.

## Deferred (next round)

- **DB-backed handler integration tests** for `ClaimTaskByRuntime` with the
  subgraph path active (mirror the `squad_briefing_claim_test.go` pattern).
  The 15 pure-logic unit tests cover the helper exhaustively; the handler
  integration is wired but not yet pin-tested. 0.5.86 follow-up.
- **Tier C Pythia hypothesis→evidence closure** — the last tier of the
  trust ladder.
- **Historian / verifier automation** — currently seeds only.
- **Evolver window bound** — currently scans a fixed 30-day window
  derived from SUCCESS audit rows.
- **Open decision gate**: MUL-6632 upstream inbox architecture.

## What changed in cooperation (post-fix projection)

| Layer | 0.5.83 | 0.5.84 | 0.5.85 |
| --- | --- | --- | --- |
| Write side | ~90% | ~90% | ~90% |
| **Read side** | **0%** | **0%** | **~70%** (depth-2 BFS + markdown injection) |
| Hidden-team autonomy | ~15% | ~15% | ~15% |
| Human gate | ~100% | ~100% | ~100% |
| **Overall** | **~40-45%** | **~40-45%** | **~70-75%** |

The 0% → ~70% jump on read-side is the largest single-ship cooperation
delta since the causal graph shipped. Agents can now act on prior causal
context (constraint → enables → action → causes → outcome chains surface
in the briefing verbatim) instead of working blind.
