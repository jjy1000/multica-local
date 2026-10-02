import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import { UI_EASE_OUT, UI_MOTION_DURATION } from "@multica/ui/lib/motion";
import { AlertTriangle, ClipboardCopy, ExternalLink, Loader2, RefreshCw, Search } from "lucide-react";
import {
  useExperimentalFlag,
  useCausalSubgraph,
  useCausalWorkspaceGraph,
  useCausalGraphPath,
  causalConfirmEdge,
  causalRejectEdge,
} from "@multica/core/experimental";
import type { CausalEdge, CausalNode, CausalPath } from "@multica/core/types/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { useT } from "@multica/views/i18n";
import { IssueBreadcrumb } from "@multica/views/experimental/components";
import {
  CausalGraphCanvas,
  CAUSAL_NODE_TYPE_COLORS,
} from "@multica/views/experimental/components";
import { buildGraphDigest, summarizeCausalPath } from "@multica/views/experimental/components";
import type { CausalPathHighlight } from "@multica/views/experimental/components";
import { AppLink } from "@multica/views/navigation";
import type { CausalPositionOverride } from "@multica/views/experimental/components";
import { ArrowUpDown, Route, X } from "lucide-react";

// CausalGraphView (0.5.83 WL3) — the workspace-wide causal graph
// surface at /experimental/causal-graph (manifest sidebar entry point).
//
// Two read modes:
//   - ?issue=<id> bound (ICP-2/ICP-3): the issue's N-hop subgraph with
//     a depth toggle, plus the shared IssueBreadcrumb back-link.
//   - unbound: the workspace-wide first-page graph (nodes + edges
//     lists, limit 100 — the S1 ceiling).
//
// The Tier D curation queue (suggested edges, human-confirm gate)
// renders under the graph; confirm/reject call the gated REST surface
// through the shared core functions.

export function CausalGraphView() {
  const { t } = useT("causal-graph");
  const enabled = useExperimentalFlag("causal_graph", false);
  const [searchParams] = useSearchParams();
  const issueId = searchParams.get("issue");
  const wsId = useWorkspaceId();

  if (!enabled) {
    return (
      <div className="flex h-full w-full items-center justify-center px-6 text-sm text-muted-foreground">
        <span className="max-w-md text-center">{t(($) => $.flag_off)}</span>
      </div>
    );
  }

  return (
    <div className="flex h-full w-full flex-col overflow-y-auto">
      {!issueId && (
        <div className="border-b border-border bg-muted/40 px-6 py-2 text-xs text-muted-foreground">
          <span className="font-medium text-foreground/90">
            {t(($) => $.unbound_title)} ·{" "}
          </span>
          {t(($) => $.unbound_hint)}
        </div>
      )}
      <div className="px-6 pt-3">
        <IssueBreadcrumb />
      </div>
      {issueId ? (
        <FocusedGraph issueId={issueId} wsId={wsId} />
      ) : (
        <WorkspaceGraph wsId={wsId} />
      )}
    </div>
  );
}

// Loading overlay (0.5.86 polish): instead of REPLACING the whole surface
// with a spinner row (which remounted the graph on every load and popped it
// in), the canvas stays mounted underneath and this dimmed overlay sits on
// top until the first fetch resolves. Depth switches re-enter pending state
// without unmounting the graph.
function GraphLoadingOverlay({ label }: { label: string }) {
  return (
    <div
      data-testid="causal-graph-loading"
      className="absolute inset-0 z-10 flex items-center justify-center rounded-md bg-background/60 backdrop-blur-[1px]"
    >
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <Loader2 className="size-3.5 animate-spin" aria-hidden />
        {label}
      </div>
    </div>
  );
}

function FocusedGraph({ issueId, wsId }: { issueId: string; wsId?: string }) {
  const { t } = useT("causal-graph");
  const [depth, setDepth] = useState(2);
  const [selected, setSelected] = useState<CausalNode | null>(null);
  // 0.5.86: session-only node-drag overrides. The page owns the Map so
  // stale ids can be pruned when the depth toggle shrinks the graph;
  // they never enter the react-query cache. While a drag is live the
  // 5s poll pauses (pollPaused) so a refetch cannot yank positions.
  const [dragging, setDragging] = useState(false);
  const [positionOverrides, setPositionOverrides] = useState<Map<string, CausalPositionOverride>>(
    () => new Map(),
  );
  const subgraph = useCausalSubgraph(issueId, depth, { pollPaused: dragging });

  const nodes = useMemo(() => subgraph.data?.nodes ?? [], [subgraph.data]);
  const edges = useMemo(() => subgraph.data?.edges ?? [], [subgraph.data]);

  // Drop overrides whose node vanished (depth change / refetch shrink).
  const nodeIds = useMemo(() => new Set(nodes.map((n) => n.id)), [nodes]);
  useEffect(() => {
    setPositionOverrides((prev) => {
      let changed = false;
      const next = new Map<string, CausalPositionOverride>();
      for (const [id, pos] of prev) {
        if (nodeIds.has(id)) next.set(id, pos);
        else changed = true;
      }
      return changed ? next : prev;
    });
  }, [nodeIds]);

  const handleOverride = useCallback((id: string, pos: CausalPositionOverride | null) => {
    setPositionOverrides((prev) => {
      const next = new Map(prev);
      if (pos) next.set(id, pos);
      else next.delete(id);
      return next;
    });
  }, []);

  if (subgraph.isError) {
    return (
      <div className="mx-6 my-3 flex items-center justify-between gap-2 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2">
        <p className="text-xs text-destructive">{t(($) => $.load_failed)}</p>
        <button
          type="button"
          onClick={() => subgraph.refetch()}
          className="shrink-0 rounded-md border border-input bg-background px-2 py-0.5 text-xs font-medium text-foreground hover:bg-muted"
        >
          {t(($) => $.retry)}
        </button>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-3 px-6 py-4">
      <div className="flex items-baseline justify-between gap-3">
        <h2 className="text-sm font-semibold text-foreground">{t(($) => $.title)}</h2>
        <div className="flex items-center gap-1 text-[11px] text-muted-foreground">
          <DigestButton nodes={nodes} edges={edges} />
          <span>{t(($) => $.depth_label)}</span>
          {[1, 2, 3, 4].map((d) => (
            <button
              key={d}
              type="button"
              onClick={() => setDepth(d)}
              className={
                "rounded px-1.5 py-0.5 font-mono transition-colors " +
                (depth === d ? "bg-primary/10 text-primary" : "hover:bg-accent")
              }
            >
              {d}
            </button>
          ))}
          <button
            type="button"
            onClick={() => setPositionOverrides(new Map())}
            disabled={positionOverrides.size === 0}
            className={
              "rounded px-1.5 py-0.5 transition-colors " +
              (positionOverrides.size === 0
                ? "opacity-50"
                : "text-muted-foreground hover:bg-accent hover:text-foreground")
            }
          >
            {t(($) => $.reset_layout)}
          </button>
          <span className="ml-2">
            {t(($) => $.counts_label, {
              nodes: String(nodes.length),
              edges: String(edges.length),
            })}
          </span>
        </div>
      </div>
      <div className="grid grid-cols-[1fr_260px] gap-3">
        <div className="relative">
          <CausalGraphCanvas
            nodes={nodes}
            edges={edges}
            width={760}
            height={560}
            selectedNodeId={selected?.id ?? null}
            onSelectNode={setSelected}
            positionOverrides={positionOverrides}
            onPositionOverride={handleOverride}
            onDragStateChange={setDragging}
            labels={{
              zoomIn: t(($) => $.zoom_in),
              zoomOut: t(($) => $.zoom_out),
              resetView: t(($) => $.reset_view),
            }}
          />
          <GraphSearch nodes={nodes} onSelect={setSelected} />
          <Legend />
          {subgraph.isPending ? <GraphLoadingOverlay label={t(($) => $.loading)} /> : null}
        </div>
        <NodeDetail node={selected} />
      </div>
      {/* 0.5.131: wsId comes from the route context, NOT
          nodes[0].workspace_id — a node-less issue (sub-issues not yet
          materialized) used to derive "" here and the queue went
          silently inert. */}
      <SuggestedQueue wsId={wsId ?? ""} />
    </div>
  );
}

// Edge filter vocabulary (mirrors the causal_edge type CHECK + status
// CHECK; the server accepts these verbatim on GET /api/causal-graph/edges).
const CAUSAL_EDGE_TYPES = [
  "causes",
  "supports",
  "contradicts",
  "depends_on",
  "enables",
  "blocks",
] as const;
const CAUSAL_EDGE_STATUSES = ["active", "suggested", "rejected"] as const;
const CAUSAL_MIN_CONFIDENCE_STEPS = [0.3, 0.6, 0.9] as const;

function WorkspaceGraph({ wsId }: { wsId: string | null | undefined }) {
  const { t } = useT("causal-graph");
  const [selected, setSelected] = useState<CausalNode | null>(null);
  const [dragging, setDragging] = useState(false);
  const [positionOverrides, setPositionOverrides] = useState<Map<string, CausalPositionOverride>>(
    () => new Map(),
  );
  // 0.5.131 edge filter bar — the server has accepted type/status/
  // min_confidence since 0.5.83, but no client ever sent them (the
  // locale keys existed unused since then too).
  const [filterType, setFilterType] = useState("");
  const [filterStatus, setFilterStatus] = useState("");
  const [filterMinConf, setFilterMinConf] = useState("0");
  const filterActive = filterType !== "" || filterStatus !== "" || filterMinConf !== "0";
  // 0.5.131 path trace (semantica path-intelligence port): toggle on,
  // click a start node, click an end node — the server's directed BFS
  // path renders as a flowing chain with a summary card.
  const [pathMode, setPathMode] = useState(false);
  const [pathFrom, setPathFrom] = useState<string | null>(null);
  const [pathTo, setPathTo] = useState<string | null>(null);
  const graph = useCausalWorkspaceGraph(wsId, {
    pollPaused: dragging,
    filters: {
      type: filterType || undefined,
      status: filterStatus || undefined,
      minConfidence: filterMinConf !== "0" ? Number(filterMinConf) : undefined,
    },
  });

  const nodes = useMemo(() => graph.data?.nodes ?? [], [graph.data]);
  const edges = useMemo(() => graph.data?.edges ?? [], [graph.data]);
  // Nodes are not filterable server-side — prune to the ones the
  // filtered edge set still touches so the canvas has no floaters.
  const visibleNodes = useMemo(() => {
    if (!filterActive) return nodes;
    const ids = new Set<string>();
    for (const e of edges) {
      ids.add(e.from_node_id);
      ids.add(e.to_node_id);
    }
    return nodes.filter((n) => ids.has(n.id));
  }, [filterActive, nodes, edges]);

  const pathQuery = useCausalGraphPath(pathMode ? pathFrom : null, pathMode ? pathTo : null);
  const path = pathQuery.data ?? null;
  const nodesById = useMemo(() => new Map(nodes.map((n) => [n.id, n])), [nodes]);
  const pathSummary = useMemo(() => (path ? summarizeCausalPath(path, edges) : null), [path, edges]);
  const pathHighlight = useMemo<CausalPathHighlight | null>(() => {
    if (!pathMode || !path || path.edges.length === 0) return null;
    return {
      key: `${pathFrom ?? ""}->${pathTo ?? ""}`,
      orderedEdgeIds: path.edges.map((e) => e.id),
      nodeIds: new Set(path.nodes.map((n) => n.id)),
      fromNodeId: path.nodes[0]?.id ?? "",
      toNodeId: path.nodes[path.nodes.length - 1]?.id ?? "",
    };
  }, [pathMode, path, pathFrom, pathTo]);

  const handleSelectNode = (node: CausalNode) => {
    setSelected(node);
    if (!pathMode) return;
    // Two-click picking; a click after a completed pair restarts.
    if (!pathFrom || pathTo) {
      setPathFrom(node.id);
      setPathTo(null);
      return;
    }
    if (node.id === pathFrom) return;
    setPathTo(node.id);
  };

  const nodeIds = useMemo(() => new Set(visibleNodes.map((n) => n.id)), [visibleNodes]);
  useEffect(() => {
    setPositionOverrides((prev) => {
      let changed = false;
      const next = new Map<string, CausalPositionOverride>();
      for (const [id, pos] of prev) {
        if (nodeIds.has(id)) next.set(id, pos);
        else changed = true;
      }
      return changed ? next : prev;
    });
  }, [nodeIds]);

  const handleOverride = useCallback((id: string, pos: CausalPositionOverride | null) => {
    setPositionOverrides((prev) => {
      const next = new Map(prev);
      if (pos) next.set(id, pos);
      else next.delete(id);
      return next;
    });
  }, []);

  if (graph.isError) {
    return (
      <div className="mx-6 my-3 flex items-center justify-between gap-2 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2">
        <p className="text-xs text-destructive">{t(($) => $.load_failed)}</p>
        <button
          type="button"
          onClick={() => graph.refetch()}
          className="shrink-0 rounded-md border border-input bg-background px-2 py-0.5 text-xs font-medium text-foreground hover:bg-muted"
        >
          {t(($) => $.retry)}
        </button>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-3 px-6 py-4">
      <div className="flex items-baseline justify-between gap-3">
        <h2 className="text-sm font-semibold text-foreground">{t(($) => $.title)}</h2>
        <div className="flex items-center gap-2 text-[11px] text-muted-foreground">
          <DigestButton nodes={visibleNodes} edges={edges} />
          <button
            type="button"
            onClick={() => setPositionOverrides(new Map())}
            disabled={positionOverrides.size === 0}
            className={
              "rounded px-1.5 py-0.5 transition-colors " +
              (positionOverrides.size === 0
                ? "opacity-50"
                : "text-muted-foreground hover:bg-accent hover:text-foreground")
            }
          >
            {t(($) => $.reset_layout)}
          </button>
          <span>
            {t(($) => $.counts_label, {
              nodes: String(visibleNodes.length),
              edges: String(edges.length),
            })}
          </span>
        </div>
      </div>
      {/* Edge filter bar (0.5.131). Selects stay native + compact —
          they sit beside the digest/reset row, not in the canvas HUD. */}
      <div className="flex flex-wrap items-center gap-2 text-[11px]">
        <label className="flex items-center gap-1 text-muted-foreground">
          {t(($) => $.filter_type)}
          <select
            value={filterType}
            onChange={(e) => setFilterType(e.target.value)}
            aria-label={t(($) => $.filter_type)}
            className="h-7 rounded border border-border bg-background px-1.5 text-[11px] text-foreground"
            data-testid="causal-filter-type"
          >
            <option value="">{t(($) => $.filter_all_types)}</option>
            {CAUSAL_EDGE_TYPES.map((ty) => (
              <option key={ty} value={ty}>
                {ty}
              </option>
            ))}
          </select>
        </label>
        <label className="flex items-center gap-1 text-muted-foreground">
          {t(($) => $.filter_status)}
          <select
            value={filterStatus}
            onChange={(e) => setFilterStatus(e.target.value)}
            aria-label={t(($) => $.filter_status)}
            className="h-7 rounded border border-border bg-background px-1.5 text-[11px] text-foreground"
            data-testid="causal-filter-status"
          >
            <option value="">{t(($) => $.filter_all_statuses)}</option>
            {CAUSAL_EDGE_STATUSES.map((st) => (
              <option key={st} value={st}>
                {st}
              </option>
            ))}
          </select>
        </label>
        <label className="flex items-center gap-1 text-muted-foreground">
          {t(($) => $.min_confidence_label)}
          <select
            value={filterMinConf}
            onChange={(e) => setFilterMinConf(e.target.value)}
            aria-label={t(($) => $.min_confidence_label)}
            className="h-7 rounded border border-border bg-background px-1.5 text-[11px] text-foreground"
            data-testid="causal-filter-confidence"
          >
            <option value="0">{t(($) => $.filter_any)}</option>
            {CAUSAL_MIN_CONFIDENCE_STEPS.map((c) => (
              <option key={c} value={String(c)}>
                ≥ {c.toFixed(1)}
              </option>
            ))}
          </select>
        </label>
        <button
          type="button"
          onClick={() => {
            setPathMode((v) => !v);
            setPathFrom(null);
            setPathTo(null);
          }}
          aria-pressed={pathMode}
          data-testid="causal-path-toggle"
          className={
            "inline-flex h-7 items-center gap-1 rounded border px-1.5 transition-colors " +
            (pathMode
              ? "border-purple-500/50 bg-purple-500/10 text-purple-700 dark:text-purple-300"
              : "border-border bg-background text-muted-foreground hover:bg-accent hover:text-foreground")
          }
        >
          <Route className="size-3" aria-hidden />
          {t(($) => $.path_toggle)}
        </button>
        {pathMode && (!pathFrom || !pathTo) ? (
          <span
            className="animate-pulse text-[10px] font-medium text-purple-700 dark:text-purple-300"
            data-testid="causal-path-pick-hint"
          >
            {pathFrom ? t(($) => $.path_pick_to) : t(($) => $.path_pick_from)}
          </span>
        ) : null}
      </div>
      {/* Pending keeps the canvas mounted under the overlay so the first
          load doesn't pop the graph in; the "no nodes" empty state only
          applies AFTER a successful fetch. A filter matching nothing is
          a different message than a workspace with no graph at all. */}
      {!graph.isPending && visibleNodes.length === 0 ? (
        <div className="rounded-md border border-dashed border-border/60 px-3 py-3 text-xs text-muted-foreground">
          {filterActive ? t(($) => $.search_empty) : t(($) => $.graph_empty)}
        </div>
      ) : (
        <div className="grid grid-cols-[1fr_260px] gap-3">
          <div className="relative">
            <CausalGraphCanvas
              nodes={visibleNodes}
              edges={edges}
              width={760}
              height={560}
              selectedNodeId={selected?.id ?? null}
              onSelectNode={handleSelectNode}
              positionOverrides={positionOverrides}
              onPositionOverride={handleOverride}
              onDragStateChange={setDragging}
              pathHighlight={pathHighlight}
              labels={{
                zoomIn: t(($) => $.zoom_in),
                zoomOut: t(($) => $.zoom_out),
                resetView: t(($) => $.reset_view),
              }}
            />
            <GraphSearch nodes={visibleNodes} onSelect={setSelected} />
            <Legend />
            {graph.isPending ? <GraphLoadingOverlay label={t(($) => $.loading)} /> : null}
          </div>
          <div className="space-y-3">
            {pathMode && pathFrom && pathTo ? (
              <PathTraceCard
                summary={pathSummary}
                path={path}
                nodesById={nodesById}
                isLoading={pathQuery.isPending}
                onSwap={() => {
                  setPathFrom(pathTo);
                  setPathTo(pathFrom);
                }}
                onClear={() => {
                  setPathFrom(null);
                  setPathTo(null);
                }}
              />
            ) : null}
            <NodeDetail node={selected} />
          </div>
        </div>
      )}
    </div>
  );
}

function NodeDetail({ node }: { node: CausalNode | null }) {
  const { t } = useT("causal-graph");
  const reduceMotion = useReducedMotion() ?? false;

  const agentName =
    typeof node?.metadata?.agent === "string" && node.metadata.agent ? node.metadata.agent : null;
  const source =
    typeof node?.provenance?.source === "string" && node.provenance.source ? node.provenance.source : null;
  const body = node ? (
    <div className="space-y-1 text-[11px] leading-snug text-muted-foreground">
      <p className="flex items-center gap-1.5 font-medium text-foreground">
        <span
          className="inline-block size-2 rounded-full"
          style={{ backgroundColor: CAUSAL_NODE_TYPE_COLORS[node.type] ?? "#94a3b8" }}
        />
        {node.label}
      </p>
      <p>
        {t(($) => $.node_type_label)}: {node.type}
        {agentName ? (
          <span className="ml-1 rounded bg-muted px-1 py-px text-[9px]">
            {t(($) => $.attributed_by, { agent: agentName })}
          </span>
        ) : null}
      </p>
      {source ? (
        <p className="font-mono text-[10px] opacity-80">source: {source}</p>
      ) : null}
      {node.description ? <p>{node.description}</p> : null}
      {node.issue_id ? <IssueRefChip issueId={node.issue_id} /> : null}
    </div>
  ) : (
    <p className="text-[11px] text-muted-foreground">{t(($) => $.no_selection)}</p>
  );

  return (
    <div className="space-y-1.5 rounded-md border border-border/60 bg-card px-3 py-2.5">
      <p className="text-xs font-medium text-foreground">{t(($) => $.node_detail_title)}</p>
      {/* Subtle fade-through on selection change (0.5.86 polish), keyed on
          the node id; reduced motion swaps instantly. */}
      {reduceMotion ? (
        <div key={node?.id ?? "none"}>{body}</div>
      ) : (
        <AnimatePresence initial={false} mode="wait">
          <motion.div
            key={node?.id ?? "none"}
            initial={{ opacity: 0 }}
            animate={{
              opacity: 1,
              transition: { duration: UI_MOTION_DURATION.fast, ease: UI_EASE_OUT },
            }}
            exit={{
              opacity: 0,
              transition: { duration: UI_MOTION_DURATION.micro, ease: UI_EASE_OUT },
            }}
          >
            {body}
          </motion.div>
        </AnimatePresence>
      )}
    </div>
  );
}

// GraphSearch (0.5.121) — canvas HUD top-left. Filters nodes by label /
// type / agent / issue id; picking a result selects the node (the
// focus-dim + detail pane follow from selection). Mirrors the legend's
// glassy HUD style; the input itself stays interactive while the shell
// lets canvas gestures pass around it.
function GraphSearch({
  nodes,
  onSelect,
}: {
  nodes: CausalNode[];
  onSelect: (node: CausalNode) => void;
}) {
  const { t } = useT("causal-graph");
  const [query, setQuery] = useState("");
  const results = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return [];
    return nodes
      .filter(
        (n) =>
          n.label.toLowerCase().includes(q) ||
          n.type.toLowerCase().includes(q) ||
          (typeof n.metadata?.agent === "string" && n.metadata.agent.toLowerCase().includes(q)) ||
          n.issue_id?.toLowerCase().includes(q),
      )
      .slice(0, 8);
  }, [query, nodes]);

  return (
    <div className="absolute left-2 top-2 z-[5] w-56">
      <div className="flex items-center gap-1.5 rounded-lg border border-border/50 bg-background/80 px-2 py-1 shadow-sm backdrop-blur">
        <Search className="size-3 shrink-0 text-muted-foreground" aria-hidden />
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t(($) => $.search_placeholder)}
          className="w-full bg-transparent text-[11px] text-foreground outline-none placeholder:text-muted-foreground"
        />
      </div>
      {results.length > 0 ? (
        <ul className="mt-1 max-h-56 overflow-y-auto rounded-lg border border-border/50 bg-popover/95 p-1 shadow-md backdrop-blur">
          {results.map((n) => (
            <li key={n.id}>
              <button
                type="button"
                onClick={() => {
                  onSelect(n);
                  setQuery("");
                }}
                className="flex w-full items-center gap-1.5 rounded px-1.5 py-1 text-left text-[11px] hover:bg-accent"
              >
                <span
                  className="inline-block size-1.5 shrink-0 rounded-full"
                  style={{ backgroundColor: CAUSAL_NODE_TYPE_COLORS[n.type] ?? "#94a3b8" }}
                />
                <span className="truncate text-foreground">{n.label}</span>
                {typeof n.metadata?.agent === "string" && n.metadata.agent ? (
                  <span className="ml-auto shrink-0 text-[9px] text-muted-foreground">
                    {t(($) => $.attributed_by, { agent: n.metadata.agent })}
                  </span>
                ) : null}
              </button>
            </li>
          ))}
        </ul>
      ) : query.trim() ? (
        <p className="mt-1 rounded-lg border border-border/50 bg-background/80 px-2 py-1 text-[10px] text-muted-foreground backdrop-blur">
          {t(($) => $.search_empty)}
        </p>
      ) : null}
    </div>
  );
}

// IssueRefChip (0.5.121) — closes the node → task loop: a graph node
// points at the concrete 任务问题 (issue) it was recorded on. Resolves
// the display key/title via the typed getIssue (parseWithFallback
// protects against backend drift); the link itself works even before
// the fetch lands.
function IssueRefChip({ issueId }: { issueId: string }) {
  const { t } = useT("causal-graph");
  const ref = useQuery({
    queryKey: ["causal-node-issue-ref", issueId],
    queryFn: () => api.getIssue(issueId),
    staleTime: 60_000,
    retry: false,
  });
  const identifier = ref.data?.identifier;
  const title = ref.data?.title;
  return (
    <AppLink
      href={`/issues/${encodeURIComponent(issueId)}`}
      className="mt-1 inline-flex max-w-full items-center gap-1 text-[11px] font-medium text-primary hover:underline"
    >
      <ExternalLink className="size-3 shrink-0" aria-hidden />
      <span className="truncate">
        {identifier ? (title ? `${identifier} · ${title}` : identifier) : t(($) => $.open_issue)}
      </span>
    </AppLink>
  );
}

function DigestButton({ nodes, edges }: { nodes: CausalNode[]; edges: CausalEdge[] }) {
  const { t } = useT("causal-graph");
  const disabled = nodes.length === 0;
  return (
    <button
      type="button"
      disabled={disabled}
      title={t(($) => $.digest_copy)}
      aria-label={t(($) => $.digest_copy)}
      onClick={() => {
        navigator.clipboard
          .writeText(buildGraphDigest(nodes, edges))
          .then(() => toast.success(t(($) => $.digest_copied)))
          .catch(() => toast.error(t(($) => $.digest_copy_failed)));
      }}
      className={
        "flex items-center gap-1 rounded px-1.5 py-0.5 transition-colors " +
        (disabled
          ? "opacity-50"
          : "text-muted-foreground hover:bg-accent hover:text-foreground")
      }
    >
      <ClipboardCopy className="size-3" aria-hidden />
      {t(($) => $.digest_copy)}
    </button>
  );
}

function Legend() {
  const { t } = useT("causal-graph");
  const entries = useMemo(() => Object.entries(CAUSAL_NODE_TYPE_COLORS), []);
  // 0.5.120: the legend is a canvas HUD — pinned to the viewport corner,
  // glassy, and click-transparent so it never blocks pan/drag gestures.
  return (
    <div
      className="pointer-events-none absolute bottom-2 left-2 z-[5] flex max-w-[85%] flex-wrap items-center gap-x-2.5 gap-y-1 rounded-lg border border-border/50 bg-background/75 px-2.5 py-1.5 text-[10px] text-muted-foreground shadow-sm backdrop-blur"
    >
      <span className="font-medium">{t(($) => $.legend_title)}</span>
      {entries.map(([type, color]) => (
        <span key={type} className="inline-flex items-center gap-1">
          <span
            className="inline-block size-2 rounded-full"
            style={{ backgroundColor: color, boxShadow: `0 0 4px ${color}` }}
          />
          {type}
        </span>
      ))}
    </div>
  );
}

// PathTraceCard (0.5.131, semantica GraphInspectorPanel port) — the
// path-intelligence companion to the canvas trace: hop count, distance
// band, weakest-link confidence with traffic-light fill, bottleneck
// hub, and endpoint labels. Purely presentational; every number comes
// from summarizeCausalPath (unit-pinned).
function PathTraceCard({
  summary,
  path,
  nodesById,
  isLoading,
  onSwap,
  onClear,
}: {
  summary: ReturnType<typeof summarizeCausalPath> | null;
  path: CausalPath | null;
  nodesById: Map<string, CausalNode>;
  isLoading: boolean;
  onSwap: () => void;
  onClear: () => void;
}) {
  const { t } = useT("causal-graph");
  if (isLoading) {
    return (
      <div
        className="space-y-1.5 rounded-md border border-purple-500/30 bg-purple-500/5 px-3 py-2.5"
        data-testid="causal-path-card-loading"
      >
        <p className="text-[11px] text-muted-foreground">{t(($) => $.loading)}</p>
      </div>
    );
  }
  if (!path) {
    // The hook resolves an unreachable pair to null — an answer, not an
    // error (no directed chain between the two nodes).
    return (
      <div
        className="space-y-1.5 rounded-md border border-dashed border-border/70 px-3 py-2.5"
        data-testid="causal-path-card-unreachable"
      >
        <p className="text-[11px] text-muted-foreground">{t(($) => $.path_unreachable)}</p>
        <button
          type="button"
          onClick={onClear}
          className="text-[10px] text-muted-foreground underline hover:text-foreground"
        >
          {t(($) => $.path_clear)}
        </button>
      </div>
    );
  }
  if (!summary) return null;

  const fromNode = nodesById.get(path.nodes[0]?.id ?? "");
  const toNode = nodesById.get(path.nodes[path.nodes.length - 1]?.id ?? "");
  const bottleneck = summary.bottleneckNodeId ? nodesById.get(summary.bottleneckNodeId) : null;
  const bandLabel =
    summary.band === "direct"
      ? t(($) => $.path_band_direct)
      : summary.band === "near"
        ? t(($) => $.path_band_near)
        : summary.band === "mid"
          ? t(($) => $.path_band_mid)
          : t(($) => $.path_band_distant);
  const verdictLabel =
    summary.verdict == null
      ? null
      : summary.verdict === "strong"
        ? t(($) => $.path_verdict_strong)
        : summary.verdict === "ok"
          ? t(($) => $.path_verdict_ok)
          : t(($) => $.path_verdict_weak);
  // Traffic-light fill keyed on the verdict thresholds (>0.6 / >0.3).
  const confFill =
    summary.verdict === "strong"
      ? "bg-emerald-500"
      : summary.verdict === "ok"
        ? "bg-amber-500"
        : "bg-red-500";
  const confPct = summary.minConfidence != null ? Math.round(summary.minConfidence * 100) : null;

  return (
    <div
      className="space-y-2 rounded-md border border-purple-500/30 bg-purple-500/5 px-3 py-2.5"
      data-testid="causal-path-card"
    >
      <div className="flex items-center justify-between gap-2">
        <p className="flex items-center gap-1 text-xs font-medium text-foreground">
          <Route className="size-3 text-purple-500" aria-hidden />
          {t(($) => $.path_title)}
        </p>
        <span className="flex shrink-0 items-center gap-1">
          <button
            type="button"
            onClick={onSwap}
            aria-label={t(($) => $.path_swap)}
            className="rounded p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          >
            <ArrowUpDown className="size-3" aria-hidden />
          </button>
          <button
            type="button"
            onClick={onClear}
            aria-label={t(($) => $.path_clear)}
            className="rounded p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          >
            <X className="size-3" aria-hidden />
          </button>
        </span>
      </div>
      <p className="truncate text-[11px] text-muted-foreground">
        {fromNode?.label ?? "—"} → {toNode?.label ?? "—"}
      </p>
      <div className="flex flex-wrap gap-1 text-[10px]">
        <span className="rounded bg-muted px-1.5 py-0.5 font-medium text-foreground/85">
          {t(($) => $.path_hops, { n: String(summary.hops) })}
        </span>
        <span className="rounded bg-muted px-1.5 py-0.5 text-muted-foreground">{bandLabel}</span>
        {verdictLabel ? (
          <span
            className={
              "rounded px-1.5 py-0.5 font-medium " +
              (summary.verdict === "strong"
                ? "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
                : summary.verdict === "ok"
                  ? "bg-amber-500/10 text-amber-700 dark:text-amber-300"
                  : "bg-red-500/10 text-red-700 dark:text-red-300")
            }
          >
            {verdictLabel}
          </span>
        ) : null}
      </div>
      {confPct != null && (
        <div className="space-y-0.5">
          <div className="flex items-baseline justify-between text-[10px] text-muted-foreground">
            <span>{t(($) => $.path_confidence_label)}</span>
            <span className="font-mono">{confPct}%</span>
          </div>
          <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
            <div
              className={`h-full rounded-full ${confFill} transition-[width] duration-500`}
              style={{ width: `${confPct}%` }}
              data-testid="causal-path-confidence"
            />
          </div>
        </div>
      )}
      {bottleneck ? (
        <p className="text-[10px] leading-snug text-muted-foreground">
          {t(($) => $.path_bottleneck_label)}:{" "}
          <span className="font-medium text-foreground/85">{bottleneck.label}</span>
          <span className="ml-1 font-mono opacity-70">deg {summary.bottleneckDegree}</span>
        </p>
      ) : null}
    </div>
  );
}

// Tier D curation queue: suggested edges awaiting the human-confirm
// gate. Reads the suggested-only list via the workspace graph hook's
// cache family (a separate direct fetch keeps this self-contained).
//
// 0.5.86 polish: confirm/reject are OPTIMISTIC — the row leaves the local
// list immediately and the mutation reconciles on settle (success →
// sonner toast + graph invalidation + queue resync; error → row restored
// in place + error toast). Rows animate in/out, reduced-motion aware.
function SuggestedQueue({ wsId }: { wsId: string }) {
  const { t } = useT("causal-graph");
  const qc = useQueryClient();
  const reduceMotion = useReducedMotion() ?? false;
  const [suggested, setSuggested] = useState<
    Array<{ id: string; from_node_id: string; to_node_id: string; type: string; confidence: number | null }>
  >([]);

  const refresh = useMutation({
    mutationFn: async () => {
      // rawRequest per the experimental network-call law (never bare
      // fetch in experimental surfaces).
      const r = await api.rawRequest(
        `/api/causal-graph/edges?workspace_id=${encodeURIComponent(wsId)}&status=suggested`,
      );
      if (r.status === 404 || !r.ok) return [];
      const raw: unknown = await r.json();
      return raw as typeof suggested;
    },
    onSuccess: (rows) => setSuggested(rows),
  });

  const confirm = useMutation({
    mutationFn: (id: string) => causalConfirmEdge(id, wsId),
  });
  const reject = useMutation({
    mutationFn: (id: string) => causalRejectEdge(id, wsId),
  });

  // Optimistic removal bookkeeping: on error the removed row goes back
  // where it was; on success a resync reconciles the queue with the
  // server's view anyway.
  const removedRef = useRef<{
    edge: (typeof suggested)[number];
    index: number;
  } | null>(null);

  function restoreRemoved() {
    const removed = removedRef.current;
    removedRef.current = null;
    if (!removed) return;
    setSuggested((prev) => {
      if (prev.some((e) => e.id === removed.edge.id)) return prev;
      const next = [...prev];
      next.splice(Math.min(removed.index, next.length), 0, removed.edge);
      return next;
    });
  }

  function decide(
    edge: (typeof suggested)[number],
    kind: "confirm" | "reject",
  ) {
    removedRef.current = {
      edge,
      index: suggested.findIndex((e) => e.id === edge.id),
    };
    setSuggested((prev) => prev.filter((e) => e.id !== edge.id));
    const mutation = kind === "confirm" ? confirm : reject;
    mutation.mutate(edge.id, {
      onSuccess: () => {
        removedRef.current = null;
        toast.success(
          kind === "confirm"
            ? t(($) => $.suggested_confirmed)
            : t(($) => $.suggested_rejected),
        );
        qc.invalidateQueries({ queryKey: ["causal-graph"] });
        // Resync the queue with server truth (keeps manual-refresh
        // semantics; the graph invalidation above covers the canvas).
        void refresh.mutate();
      },
      onError: () => {
        restoreRemoved();
        toast.error(t(($) => $.suggested_action_failed));
      },
    });
  }

  // Load once per mount + after each decision (simple effect-driven
  // read; the confirm/reject invalidations refresh the graph queries).
  useEffect(() => {
    if (wsId) void refresh.mutate();
    // Refresh is stable for the lifetime of this queue component.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [wsId]);

  const busy = confirm.isPending || reject.isPending;

  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between gap-2">
        <p className="text-xs font-medium text-foreground">{t(($) => $.suggested_queue_title)}</p>
        <button
          type="button"
          onClick={() => refresh.mutate()}
          className="inline-flex shrink-0 items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground"
        >
          <RefreshCw className="size-3" aria-hidden />
          {t(($) => $.refresh)}
        </button>
      </div>
      {suggested.length === 0 ? (
        <p className="text-[11px] text-muted-foreground">{t(($) => $.suggested_empty)}</p>
      ) : (
        <div className="flex flex-col gap-1">
          <AnimatePresence initial={false}>
            {suggested.map((edge, index) => (
              <motion.div
                key={edge.id}
                layout={!reduceMotion}
                className="flex items-center justify-between gap-2 rounded border border-border/60 px-2 py-1.5 text-[11px]"
                initial={reduceMotion ? false : { opacity: 0, y: 4 }}
                animate={{ opacity: 1, y: 0 }}
                exit={
                  reduceMotion
                    ? { opacity: 1, transition: { duration: 0 } }
                    : { opacity: 0, transition: { duration: UI_MOTION_DURATION.fast, ease: UI_EASE_OUT } }
                }
                transition={{ duration: UI_MOTION_DURATION.fast, ease: UI_EASE_OUT, delay: reduceMotion ? 0 : Math.min(index * 0.02, 0.1) }}
              >
                <span className="font-mono text-muted-foreground">
                  {edge.type}
                  {edge.confidence != null ? ` · ${edge.confidence.toFixed(2)}` : ""}
                </span>
                <span className="flex shrink-0 items-center gap-1">
                  <button
                    type="button"
                    disabled={busy}
                    onClick={() => decide(edge, "confirm")}
                    className="rounded border border-emerald-500/40 bg-emerald-500/5 px-2 py-0.5 font-medium text-emerald-700 hover:bg-emerald-500/10 disabled:opacity-50 dark:text-emerald-300"
                  >
                    {t(($) => $.confirm)}
                  </button>
                  <button
                    type="button"
                    disabled={busy}
                    onClick={() => decide(edge, "reject")}
                    className="rounded border border-red-500/40 bg-red-500/5 px-2 py-0.5 font-medium text-red-700 hover:bg-red-500/10 disabled:opacity-50 dark:text-red-300"
                  >
                    {t(($) => $.reject)}
                  </button>
                </span>
              </motion.div>
            ))}
          </AnimatePresence>
        </div>
      )}
      <p className="flex items-center gap-1 text-[10px] text-muted-foreground">
        <AlertTriangle className="size-3" aria-hidden />
        {t(($) => $.suggested_queue_footer)}
      </p>
    </div>
  );
}
