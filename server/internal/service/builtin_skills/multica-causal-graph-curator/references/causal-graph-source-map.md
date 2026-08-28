# multica-causal-graph-curator — Source Map

Every contract the SKILL.md teaches, traced to the enforcing code.
Fork paths are repository-relative; trust symbol names over line
numbers.

## Server

| Contract | Source |
| --- | --- |
| Suggestion write path (Tier D): `POST /api/causal-graph/suggestions` — rationale required, confidence halved, lands `status='suggested'` + `proposed_by='curator'` | `server/internal/handler/causal_graph.go` (`createCausalSuggestion`) |
| Curation gate: confirm (suggested→active) / reject (suggested→deleted); decided edges 409; unique-active-triple → 409 | `server/internal/handler/causal_graph.go` (`confirmCausalEdge`, `rejectCausalEdge`) + `uq_causal_edge_active_from_to_type` (migration 278) |
| Trust ceiling: suggested confidence ≤ 0.5, human-confirm ladder | roadmap §3.0 Tier D; mirrors `agent_self_optimization::edits` suggested-gate |
| Node read path the curator links against | `GET /api/causal-graph/nodes` (same handler) |
| Node types (`decision/action/outcome/assumption/evidence/constraint`) and edge types (`causes/supports/contradicts/depends_on/enables/blocks`) | migrations 277/278 CHECKs — verbatim CONTRACT |
| Tier A native nodes the curator links between | `server/internal/service/causal_graph/recorder.go` (provenance dedup prefixes `task_action:`, `task_outcome:`, `issue_root:`) |
| Tier B decision nodes | `server/internal/handler/decision_sync.go` (`mirrorDecisionToCausalGraph`, dedup `decision:<id>`) |
| Flag gate: uniform 404 when `causal_graph` is off | `RequireExperimentalFlag("causal_graph")` in `server/cmd/server/router.go` |

## Client

| Contract | Source |
| --- | --- |
| Issue-header icon + popup preview (ICP-5 passive) | `packages/views/issues/components/issue-causal-icon.tsx` |
| Workspace graph view + curation queue (confirm/reject UI) | `apps/desktop/src/renderer/src/pages/causal-graph-view.tsx` |
| Read hooks (subgraph poll, flag-off degradation) | `packages/core/experimental/causal-graph-queries.ts` |

## Trust ladder rationale

Tier D output is LLM-generated and therefore the weakest source in the
graph. The suggestion endpoint is the ONLY write surface the curator
skill teaches; active-edge writes stay human-only (manual POST in the
desktop) or system-native (Tier A recorder, Tier B mirror). If this
skill ever appears to teach an active-edge write path, that is a bug —
stop and re-read `createCausalSuggestion`.
