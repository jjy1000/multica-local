# WL3 Server Wire Contract — client-half handoff (0.5.83)

Verified against the shipped code on `epic/0.5.83-causal-graph`
(commits 623b9b85e → 1a27fa4f1). Field names are VERBATIM from the
handler structs — pin them in zod schemas exactly.

## Flag & gating

- Flag key: **`causal_graph`** (verbatim law: catalog.go, router.go,
  lock.go SourceCausalGraph, mig 279 CHECK, manifest, desktop
  manager-factory descriptor `kind: "none"`).
- All routes sit behind `RequireExperimentalFlag("causal_graph")` →
  **uniform 404 when off**. Client 404 = flag-off (degrade to empty /
  hide the icon, ICP-5) — indistinguishable from a missing route.
- Server-native lab: NO loopback proxy, NO `/experimental/causal-graph`
  REST proxy prefix. The VIEW route `/experimental/causal-graph` is the
  only consumer of that path (routes.tsx) — no route-split needed
  (unlike timesfm/semantica).
- Sidebar: manifest `entry_points.sidebar` →
  `/experimental/causal-graph` (label key `experimental_causal_graph` —
  i18n ×4 needed for that key in the desktop sidebar surface).

## Timestamps: RFC3339 UTC strings. Numeric weight/confidence: number | null.

## Endpoints (all JSON; issue params accept identifier-or-UUID)

### Issue dependencies (revived, mig 276)

- `GET /api/issues/{issueID}/dependencies?workspace_id=` →
  `{dependencies: DependencyJSON[], dependents: DependencyJSON[]}`

  DependencyJSON: `{id, issue_id, depends_on_issue_id, type
  ("blocks"|"blocked_by"|"related"), evidence_comment_id: string|null,
  created_by: string|null, created_at, updated_at}`

- `POST /api/issues/{issueID}/dependencies?workspace_id=` body
  `{target_issue_id, type?, evidence_comment_id?}` → 201 DependencyJSON
  (type defaults "blocked_by"; self-dep 400; duplicate pair 409; bad
  type 400)
- `DELETE /api/issues/{issueID}/dependencies/{dependencyID}?workspace_id=`
  → `{status:"deleted"}`; 404 when missing/foreign

### Nodes

- `GET /api/causal-graph/nodes?workspace_id=&issue_id=&type=&limit=&offset=`
  → NodeJSON[] (limit default 50, clamp [1,100]; offset ≥ 0)
- `POST /api/causal-graph/nodes?workspace_id=` (workspace_id optional
  when `issue_id` present — the loader scopes it) body
  `{label, type, issue_id?, description?, metadata?, lab_source?,
  lab_run_id?}` → 201 NodeJSON

  NodeJSON: `{id, workspace_id, issue_id: string|null, type
  ("decision"|"action"|"outcome"|"assumption"|"evidence"|"constraint"),
  label, description: string|null, metadata: object, provenance: object,
  created_at, created_by: string|null, lab_source: string|null,
  lab_run_id: string|null, status: string, last_observed_at}`

### Edges

- `GET /api/causal-graph/edges?workspace_id=&issue_id=&type=&status=&min_confidence=&limit=&offset=`
  → EdgeJSON[] (min_confidence number [0,1])
- `POST /api/causal-graph/edges?workspace_id=` body
  `{from_node_id, to_node_id, type, confidence?, weight?, metadata?}`
  → 201 EdgeJSON (manual edges land `status:"active"`,
  `created_by:"user"`, provenance `{"source":"manual"}`; endpoints must
  share the workspace; duplicate active triple → 409)
- `DELETE /api/causal-graph/edges/{edgeID}?workspace_id=` →
  `{status:"deleted"}`
- `POST /api/causal-graph/edges/{edgeID}/confirm?workspace_id=` → 200
  EdgeJSON (suggested → active; 409 when not suggested or when the
  active-triple unique index collides)
- `POST /api/causal-graph/edges/{edgeID}/reject?workspace_id=` → 200
  EdgeJSON (the deleted row, status still "suggested" in the body)

  EdgeJSON: `{id, workspace_id, from_node_id, to_node_id, type
  ("causes"|"supports"|"contradicts"|"depends_on"|"enables"|"blocks"),
  weight: number|null, confidence: number|null, metadata: object,
  provenance: object, created_at, created_by: string|null,
  proposed_by: ("curator"|"evolver")|null, status
  ("active"|"suggested")}`

### Subgraph (powers BOTH the issue icon popup and the full view seed)

- `GET /api/causal-graph/subgraph?issue_id=&depth=` →
  `{issue_id, depth, nodes: NodeJSON[], edges: EdgeJSON[]}`
- depth default 2, clamped [1,4]; traversal is UNDIRECTED and active
  edges only; empty issue → empty arrays (never null)

### Path

- `GET /api/causal-graph/path?from=&to=` → `{nodes, edges}`
  (ordered source→target); 404 "no causal path" when unreachable;
  400 when endpoints span workspaces

## Tier A/B behavior the UI can rely on

- Recorder writes happen behind the flag (one EXISTS check) — flag-off
  = zero nodes ever appear; flag-on = action nodes appear as soon as a
  task is enqueued (issue assign/@mention/squad-leader), outcome nodes
  on completion; repeats are deduped by provenance dedup_key.
- Decision nodes appear when a semantica-bound issue's decision
  export succeeds (Tier B mirror).
- Suggested edges never appear in subgraph/path (active-only reads);
  they surface only via `GET /edges?status=suggested` for the
  curation queue.

## Client work remaining (C1)

core zod schemas + hooks (`useCausalSubgraph` 5s-poll fallback,
`useCausalGraphPath`), issue-header icon + radix Popover minimap,
`/experimental/causal-graph` view + routes.tsx entry + web stub, i18n
×4 (arrow-selector syntax), tests.
