"use client";

// IssueCausalGraphIcon (0.5.83 WL3, roadmap §3.4 item 16) — the causal
// awareness affordance on the issue header: a small icon in the top
// right actions cluster that opens a read-only subgraph preview popup
// (radix Popover) with a depth toggle and a jump into the full
// /experimental/causal-graph workspace view.
//
// 0.5.86 declutter: the popup defaults to depth 1, and selecting 深度 2
// renders the depth-1 map plus a "+N 节点 · M 边 在深度 2" summary row
// instead of drawing the dense second ring (the 440px surface cannot
// fit it legibly). The full detail stays one click away in the full
// graph view.
//
// ICP-5 (passive, flag-gated): the icon is HIDDEN entirely when the
// causal_graph flag is off (useExperimentalFlag) or when the issue has
// no causal nodes yet — it never nags, never blocks, and never asks
// for attention. A uniform-404 flag-off answer from the subgraph hook
// surfaces as an error with retries disabled, which lands here as the
// same "render nothing" path.

import { useState } from "react";
import { Loader2, ScanSearch, Users, Waypoints } from "lucide-react";
import { useExperimentalFlag, useCausalSubgraph, useCausalReads } from "@multica/core/experimental";
import type { CausalNode } from "@multica/core/types/api";
import { Popover, PopoverTrigger, PopoverContent } from "@multica/ui/components/ui/popover";
import { Button } from "@multica/ui/components/ui/button";
import { AppLink } from "../../navigation";
import { CausalMinimap } from "../../experimental/components/causal-minimap";
import { useT } from "../../i18n";

export function IssueCausalGraphIcon({ issueId }: { issueId: string }) {
  const { t } = useT("causal-graph");
  const enabled = useExperimentalFlag("causal_graph", false);
  const [open, setOpen] = useState(false);
  const [depth, setDepth] = useState(1);
  const [selected, setSelected] = useState<CausalNode | null>(null);
  // 0.5.121: second surface mode — 读取 (reads) shows which agents have
  // traced this issue's causal analysis (claim-time read receipts).
  const [mode, setMode] = useState<"graph" | "reads">("graph");

  // The depth-1 slice is always the drawn surface; the depth-2 fetch
  // only runs while 深度 2 is selected, and only to count the extras.
  const base = useCausalSubgraph(enabled ? issueId : null, 1);
  const extended = useCausalSubgraph(enabled && depth === 2 ? issueId : null, 2);
  const reads = useCausalReads(enabled ? issueId : null, { enabled: mode === "reads" });

  // ICP-5: hidden entirely when flag off or nothing to show yet.
  if (!enabled) return null;
  if (base.isError) return null;
  if (!base.data && !base.isPending) return null;
  const nodes = base.data?.nodes ?? [];
  if (!base.isPending && nodes.length === 0) return null;

  const edges = base.data?.edges ?? [];
  // "+N 节点 · M 边 在深度 2": the extras the depth-2 hop would add,
  // counted by id so already-visible nodes/edges are not double-counted.
  const nodeIds = new Set(nodes.map((n) => n.id));
  const edgeIds = new Set(edges.map((e) => e.id));
  const extendedNodes = extended.data?.nodes ?? [];
  const extendedEdges = extended.data?.edges ?? [];
  const extraNodes = extendedNodes.filter((n) => !nodeIds.has(n.id)).length;
  const extraEdges = extendedEdges.filter((e) => !edgeIds.has(e.id)).length;

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        render={(props) => (
          <Button
            {...props}
            variant="ghost"
            size="icon-sm"
            className="text-muted-foreground"
            aria-label={t(($) => $.icon_tooltip)}
          >
            {base.isPending ? (
              <Loader2 className="size-4 animate-spin" aria-hidden />
            ) : (
              <Waypoints className="size-4" aria-hidden />
            )}
          </Button>
        )}
      />
      <PopoverContent align="end" className="w-[440px] space-y-2">
        <div className="flex items-center justify-between gap-2">
          <p className="text-xs font-medium text-foreground">
            {t(($) => $.popup_title)}
          </p>
          <div className="flex shrink-0 items-center gap-2">
            {mode === "graph" ? (
              <div className="flex items-center gap-1 text-[10px] text-muted-foreground">
                <span>{t(($) => $.depth_label)}</span>
                {[1, 2].map((d) => (
                  <button
                    key={d}
                    type="button"
                    onClick={() => setDepth(d)}
                    className={
                      "rounded px-1.5 py-0.5 font-mono transition-colors " +
                      (depth === d
                        ? "bg-primary/10 text-primary"
                        : "hover:bg-accent")
                    }
                  >
                    {d}
                  </button>
                ))}
              </div>
            ) : null}
            {/* 0.5.121 mode switch: 图谱 (trace) vs 读取 (who read it). */}
            <div className="flex items-center rounded-md border border-border/60 p-0.5 text-[10px]">
              <button
                type="button"
                aria-label={t(($) => $.tab_graph)}
                title={t(($) => $.tab_graph)}
                onClick={() => setMode("graph")}
                className={
                  "flex items-center gap-1 rounded px-1.5 py-0.5 transition-colors " +
                  (mode === "graph"
                    ? "bg-primary/10 text-primary"
                    : "text-muted-foreground hover:bg-accent")
                }
              >
                <Waypoints className="size-3" aria-hidden />
                {t(($) => $.tab_graph)}
              </button>
              <button
                type="button"
                aria-label={t(($) => $.tab_reads)}
                title={t(($) => $.tab_reads)}
                onClick={() => setMode("reads")}
                className={
                  "flex items-center gap-1 rounded px-1.5 py-0.5 transition-colors " +
                  (mode === "reads"
                    ? "bg-primary/10 text-primary"
                    : "text-muted-foreground hover:bg-accent")
                }
              >
                <Users className="size-3" aria-hidden />
                {t(($) => $.tab_reads)}
              </button>
            </div>
          </div>
        </div>
        {mode === "graph" ? (
        <>
        <CausalMinimap
          nodes={nodes}
          edges={edges}
          width={440}
          height={280}
          selectedNodeId={selected?.id ?? null}
          onSelectNode={setSelected}
        />
        </>) : (
        <CausalReadsList reads={reads} />
        )}
        {mode === "graph" && depth === 2 ? (
          <p className="rounded-md border border-dashed border-border/60 px-2 py-1 text-[10px] text-muted-foreground">
            {t(($) => $.depth2_summary, {
              nodes: String(extraNodes),
              edges: String(extraEdges),
            })}
          </p>
        ) : null}
        {mode === "graph" && selected ? (
          <div className="space-y-0.5 rounded-md border border-border/60 bg-muted/30 px-2 py-1.5">
            <p className="text-[11px] font-medium text-foreground">
              <span
                className="mr-1.5 inline-block size-2 rounded-full align-middle"
                style={{ backgroundColor: typeColor(selected.type) }}
              />
              {selected.label}
            </p>
            <p className="text-[10px] leading-snug text-muted-foreground">
              {selected.type}
              {typeof selected.metadata?.agent === "string" && selected.metadata.agent
                ? ` · by ${selected.metadata.agent}`
                : ""}
              {selected.description ? ` · ${selected.description}` : ""}
            </p>
          </div>
        ) : null}
        <div className="flex items-center justify-between gap-2">
          <span className="text-[10px] text-muted-foreground">
            {mode === "reads"
              ? t(($) => $.reads_hint, {
                  n: String(reads.data?.length ?? 0),
                })
              : t(($) => $.counts_label, {
                  nodes: String(nodes.length),
                  edges: String(edges.length),
                })}
          </span>
          <AppLink
            href={`/experimental/causal-graph?issue=${encodeURIComponent(issueId)}`}
            className="text-[11px] text-purple-600 hover:text-purple-700 dark:text-purple-400 dark:hover:text-purple-300"
          >
            {t(($) => $.open_full_graph)} →
          </AppLink>
        </div>
      </PopoverContent>
    </Popover>
  );
}

// CausalReadsList (0.5.121) — the 读取 surface: which agents have read
// this issue's causal trace (claim-time read receipts), newest first.
// The flag-off guard degrades to the honest empty state (ICP-5).
function CausalReadsList({
  reads,
}: {
  reads: ReturnType<typeof useCausalReads>;
}) {
  const { t } = useT("causal-graph");
  if (reads.isPending) {
    return (
      <div className="flex h-[120px] items-center justify-center rounded-md border border-border/60">
        <Loader2 className="size-4 animate-spin text-muted-foreground" aria-hidden />
      </div>
    );
  }
  const rows = reads.data ?? [];
  if (rows.length === 0) {
    return (
      <div className="flex h-[120px] flex-col items-center justify-center gap-1.5 rounded-md border border-dashed border-border/60 px-3 text-center">
        <ScanSearch className="size-4 text-muted-foreground" aria-hidden />
        <p className="text-[11px] leading-snug text-muted-foreground">
          {t(($) => $.reads_empty)}
        </p>
      </div>
    );
  }
  return (
    <ul className="max-h-[264px] space-y-1 overflow-y-auto rounded-md border border-border/60 bg-card p-1.5">
      {rows.map((r, i) => (
        <li
          key={`${r.task_id}-${i}`}
          className="flex items-center justify-between gap-2 rounded px-1.5 py-1 text-[11px] hover:bg-accent/60"
        >
          <span className="flex min-w-0 items-center gap-1.5">
            <span className="inline-block size-1.5 shrink-0 rounded-full bg-emerald-500" />
            <span className="truncate font-medium text-foreground">
              {r.agent_name || t(($) => $.reads_unknown_agent)}
            </span>
            <span className="shrink-0 rounded bg-muted px-1 py-px font-mono text-[9px] text-muted-foreground">
              {r.source}
            </span>
          </span>
          <span className="shrink-0 text-[10px] text-muted-foreground">
            {formatReadTime(r.created_at)}
          </span>
        </li>
      ))}
    </ul>
  );
}

// Time display for read receipts: absolute short form — a receipt list
// is an audit surface, and relative "3 minutes ago" gets stale under the
// popover's 30s cache. Locale-aware via the runtime (Intl).
function formatReadTime(iso: string): string {
  const ts = Date.parse(iso);
  if (Number.isNaN(ts)) return "";
  const d = new Date(ts);
  const now = new Date();
  const sameDay = d.toDateString() === now.toDateString();
  const hm = `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
  return sameDay
    ? hm
    : `${d.getMonth() + 1}/${d.getDate()} ${hm}`;
}

// Local mirror of the minimap's colour table — the popup needs it for
// the selected-node chip before the minimap module is loaded in the
// consumer bundle.
function typeColor(type: string): string {
  const table: Record<string, string> = {
    decision: "#2563eb",
    action: "#059669",
    outcome: "#7c3aed",
    assumption: "#d97706",
    evidence: "#0891b2",
    constraint: "#64748b",
  };
  return table[type] ?? "#94a3b8";
}
