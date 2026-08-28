---
name: release-notes-0.5.83
created: 2026-08-28T09:40:00+08:00
updated: 2026-08-28T09:40:00+08:00
---

# 0.5.83 — Issue causal graph (WL3: decision tracking + hidden agent team)

**Branch:** `epic/0.5.83-causal-graph` → merged `epic/0.5.72-followups`
**Trigger:** labs evolution roadmap §3 (`.omc/plans/0.5.81-0.5.83-labs-evolution-roadmap.md`), user vision 「决策追踪因果图 + 隐藏 Multica 智能体团队」
**Wire contract:** `.omc/wl3-server-contract.md` (incl. S2 addendum)

## TL;DR

Every issue now grows a causal graph: native task hooks record runs and
outcomes (tier A), Semantica decision records mirror in (tier B), a
deterministic scanner + a nightly evolver + the hidden LLM curator
PROPOSE extra relations that stay `suggested` until a human confirms
(tier D, confidence ≤ 0.5). The issue page shows a causal icon beside
the header actions → popover minimap → deep link to the full
`/experimental/causal-graph` view. A hidden three-agent team (curator /
historian / verifier) is provisioned per workspace; nothing
auto-dispatches to them. Revives the dormant `issue_dependency` table
for first-class dependencies.

## Schema (migrations 276-280 — applied post-backup)

- **276** revive `issue_dependency`: `evidence_comment_id`,
  `created_by` ('user'|'agent'|'system'), timestamps. Forward-only
  additive on the dormant 001_init table.
- **277** `causal_node`: type CHECK (decision|action|outcome|
  assumption|evidence|constraint), NULL issue_id for abstract nodes,
  `provenance` JSONB is the dedup anchor.
- **278** `causal_edge`: type CHECK (causes|supports|contradicts|
  depends_on|enables|blocks), confidence NUMERIC(4,3), proposed_by
  (curator|evolver), status (active|suggested), partial UNIQUE
  (from,to,type) WHERE active.
- **279** indices + `uq_causal_node_provenance_dedup_key` + lock
  source CHECK widened with 'causal_graph'.
- **280** reject = TOMBSTONE (status 'rejected'), not delete — the
  audit trail is what keeps proposers silent (ICP-5 never-nag).

## Server

- Gated REST surface under `RequireExperimentalFlag("causal_graph")`:
  dependencies CRUD, nodes/edges CRUD, subgraph (undirected BFS,
  depth 1-4), path (directed BFS), confirm/reject, and the tier-D
  POST /suggestions (rationale required, confidence halved).
- Tier A recorder (`service/causal_graph/recorder.go`) hooked at the
  enqueue/complete seams — zero LLM, non-fatal, fail-closed flag
  check, dedup anchors `task_action:`/`task_outcome:`/`issue_root:`.
- Tier B mirror in `decision_sync.go` (`decision:<id>` nodes).
- **Hidden team** (`install_causal_graph.go`):
  `causal_graph_curator` (bound to the multica-causal-graph-curator
  builtin skill) / `causal_graph_historian` / `causal_graph_verifier`.
  NO dispatch leader (timesfm precedent); purge-before-seed
  visibility; all three Claim'd under the causal_graph source.
- **Nightly evolver** (scheduler job `causal_graph_evolver`, 24h
  catch-up-latest-only): deterministic comment-window scan
  ("blocked by / depends on / 依赖…" + `<PREFIX>-<N>`) + transitive
  A→B→C ⇒ A→C shortcuts at min(conf)×0.8 capped 0.5.
- **Maintenance ticker** (SemanticaGC mirror; Start takes NO ctx):
  stale-mark volatile nodes >30d unobserved (constraint/assumption
  exempt), GC unconfirmed suggestions >30d, tombstones kept forever.
- Bug fix found by the install test: `SourceCausalGraph` was missing
  from `experimental.AllSources` (Claim rejected every install);
  pinned by `TestAllSourcesContainsCausalGraph`.

## Client (desktop primary)

- `useCausalSubgraph` (5s poll, CausalFlagOffError disables retry AND
  poll), `useCausalGraphPath`, `useCausalWorkspaceGraph` + zod
  schemas (lenient-parse contract).
- `IssueCausalGraphIcon` in the issue header actions cluster — hidden
  when flag off / no nodes (ICP-5); popover minimap (deterministic
  BFS ring layout, curved SVG edges), depth 1/2 toggle, deep link.
- `/experimental/causal-graph` route: focused (?issue=) + workspace
  views, node detail panel, legend, suggested-queue with
  confirm/reject (api.rawRequest law held). Sidebar entry via catalog
  manifest. Web stub parity page.
- i18n ×4 (causal-graph + layout sidebar keys), arrow-selector
  syntax throughout.

## Trust ladder (the design law)

A (native hooks, machine-native) > B (Semantica mirrors) > C (Pythia
closure, 0.5.84) > D (LLM proposals — suggested-only, confidence
≤ 0.5, human-gated). Suggested edges NEVER appear in subgraph/path.

## Deviations & deferrals (documented in the wire contract addendum)

- Roadmap's "1 autopilot" row superseded by the evolver JobSpec.
- Tier C Pythia closure, daemon claim-response context injection
  (§3.5 item 22), mention-flow doc notes, historian/verifier
  automation → 0.5.84 (roadmap allows the split).

## Gates

turbo typecheck 6/6; desktop vitest 370/370; core 824/827 + views
1750/1792 (ledgered baseline only — inbox-page ×29 etc., unchanged);
go build + FULL `go test ./...` with DATABASE_URL: 47 packages, 0
FAIL (two load-flaked timing tests re-verified green serially);
lint: WL3-introduced errors 0 (desktop main-process debt is
byte-identical to shipped 0.5.82). Migrations applied after
`~/.multica/backups/db-pre-wl3-migs-20260827T150543Z.sql` (full) +
schema-migration snapshots + targeted causal_edge dump pre-280.
