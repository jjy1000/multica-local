import { useCallback, useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { AlertTriangle, Loader2, RefreshCw } from "lucide-react";
import {
  useExperimentalFlag,
  useCausalSubgraph,
  useCausalWorkspaceGraph,
  causalConfirmEdge,
  causalRejectEdge,
} from "@multica/core/experimental";
import type { CausalNode } from "@multica/core/types/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { useT } from "@multica/views/i18n";
import { IssueBreadcrumb } from "@multica/views/experimental/components";
import {
  CausalGraphCanvas,
  CAUSAL_NODE_TYPE_COLORS,
} from "@multica/views/experimental/components";
import type { CausalPositionOverride } from "@multica/views/experimental/components";

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
        <FocusedGraph issueId={issueId} />
      ) : (
        <WorkspaceGraph wsId={wsId} />
      )}
    </div>
  );
}

function FocusedGraph({ issueId }: { issueId: string }) {
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

  if (subgraph.isPending) {
    return (
      <div className="flex items-center gap-2 px-6 py-4 text-sm text-muted-foreground">
        <Loader2 className="size-3.5 animate-spin" aria-hidden />
        {t(($) => $.loading)}
      </div>
    );
  }
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
        <CausalGraphCanvas
          nodes={nodes}
          edges={edges}
          width={760}
          height={480}
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
        <NodeDetail node={selected} />
      </div>
      <Legend />
      <SuggestedQueue wsId={nodes[0]?.workspace_id ?? ""} />
    </div>
  );
}

function WorkspaceGraph({ wsId }: { wsId: string | null | undefined }) {
  const { t } = useT("causal-graph");
  const [selected, setSelected] = useState<CausalNode | null>(null);
  const [dragging, setDragging] = useState(false);
  const [positionOverrides, setPositionOverrides] = useState<Map<string, CausalPositionOverride>>(
    () => new Map(),
  );
  const graph = useCausalWorkspaceGraph(wsId, { pollPaused: dragging });

  const nodes = useMemo(() => graph.data?.nodes ?? [], [graph.data]);
  const edges = useMemo(() => graph.data?.edges ?? [], [graph.data]);

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

  if (graph.isPending) {
    return (
      <div className="flex items-center gap-2 px-6 py-4 text-sm text-muted-foreground">
        <Loader2 className="size-3.5 animate-spin" aria-hidden />
        {t(($) => $.loading)}
      </div>
    );
  }
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
              nodes: String(nodes.length),
              edges: String(edges.length),
            })}
          </span>
        </div>
      </div>
      {nodes.length === 0 ? (
        <div className="rounded-md border border-dashed border-border/60 px-3 py-3 text-xs text-muted-foreground">
          {t(($) => $.suggested_empty)}
        </div>
      ) : (
        <div className="grid grid-cols-[1fr_260px] gap-3">
          <CausalGraphCanvas
            nodes={nodes}
            edges={edges}
            width={760}
            height={480}
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
          <NodeDetail node={selected} />
        </div>
      )}
      <Legend />
    </div>
  );
}

function NodeDetail({ node }: { node: CausalNode | null }) {
  const { t } = useT("causal-graph");
  return (
    <div className="space-y-1.5 rounded-md border border-border/60 bg-card px-3 py-2.5">
      <p className="text-xs font-medium text-foreground">{t(($) => $.node_detail_title)}</p>
      {node ? (
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
          </p>
          {node.description ? <p>{node.description}</p> : null}
          {node.issue_id ? (
            <p className="font-mono text-[10px] opacity-80">{node.issue_id}</p>
          ) : null}
        </div>
      ) : (
        <p className="text-[11px] text-muted-foreground">{t(($) => $.no_selection)}</p>
      )}
    </div>
  );
}

function Legend() {
  const { t } = useT("causal-graph");
  const entries = useMemo(() => Object.entries(CAUSAL_NODE_TYPE_COLORS), []);
  return (
    <div className="flex flex-wrap items-center gap-2 text-[10px] text-muted-foreground">
      <span className="font-medium">{t(($) => $.legend_title)}:</span>
      {entries.map(([type, color]) => (
        <span key={type} className="inline-flex items-center gap-1">
          <span className="inline-block size-2 rounded-full" style={{ backgroundColor: color }} />
          {type}
        </span>
      ))}
    </div>
  );
}

// Tier D curation queue: suggested edges awaiting the human-confirm
// gate. Reads the suggested-only list via the workspace graph hook's
// cache family (a separate direct fetch keeps this self-contained).
function SuggestedQueue({ wsId }: { wsId: string }) {
  const { t } = useT("causal-graph");
  const qc = useQueryClient();
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
    onSuccess: () => qc.invalidateQueries({ queryKey: ["causal-graph"] }),
  });
  const reject = useMutation({
    mutationFn: (id: string) => causalRejectEdge(id, wsId),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["causal-graph"] }),
  });

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
          {suggested.map((edge) => (
            <div
              key={edge.id}
              className="flex items-center justify-between gap-2 rounded border border-border/60 px-2 py-1.5 text-[11px]"
            >
              <span className="font-mono text-muted-foreground">
                {edge.type}
                {edge.confidence != null ? ` · ${edge.confidence.toFixed(2)}` : ""}
              </span>
              <span className="flex shrink-0 items-center gap-1">
                <button
                  type="button"
                  disabled={busy}
                  onClick={() => confirm.mutate(edge.id)}
                  className="rounded border border-emerald-500/40 bg-emerald-500/5 px-2 py-0.5 font-medium text-emerald-700 hover:bg-emerald-500/10 disabled:opacity-50 dark:text-emerald-300"
                >
                  {t(($) => $.confirm)}
                </button>
                <button
                  type="button"
                  disabled={busy}
                  onClick={() => reject.mutate(edge.id)}
                  className="rounded border border-red-500/40 bg-red-500/5 px-2 py-0.5 font-medium text-red-700 hover:bg-red-500/10 disabled:opacity-50 dark:text-red-300"
                >
                  {t(($) => $.reject)}
                </button>
              </span>
            </div>
          ))}
        </div>
      )}
      <p className="flex items-center gap-1 text-[10px] text-muted-foreground">
        <AlertTriangle className="size-3" aria-hidden />
        proposed_by: curator | evolver → human-confirm gate
      </p>
    </div>
  );
}
