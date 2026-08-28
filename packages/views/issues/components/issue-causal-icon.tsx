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
import { Loader2, Waypoints } from "lucide-react";
import { useExperimentalFlag, useCausalSubgraph } from "@multica/core/experimental";
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

  // The depth-1 slice is always the drawn surface; the depth-2 fetch
  // only runs while 深度 2 is selected, and only to count the extras.
  const base = useCausalSubgraph(enabled ? issueId : null, 1);
  const extended = useCausalSubgraph(enabled && depth === 2 ? issueId : null, 2);

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
          <div className="flex shrink-0 items-center gap-1 text-[10px] text-muted-foreground">
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
        </div>
        <CausalMinimap
          nodes={nodes}
          edges={edges}
          width={440}
          height={280}
          selectedNodeId={selected?.id ?? null}
          onSelectNode={setSelected}
        />
        {depth === 2 ? (
          <p className="rounded-md border border-dashed border-border/60 px-2 py-1 text-[10px] text-muted-foreground">
            {t(($) => $.depth2_summary, {
              nodes: String(extraNodes),
              edges: String(extraEdges),
            })}
          </p>
        ) : null}
        {selected ? (
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
              {selected.description ? ` · ${selected.description}` : ""}
            </p>
          </div>
        ) : null}
        <div className="flex items-center justify-between gap-2">
          <span className="text-[10px] text-muted-foreground">
            {t(($) => $.counts_label, {
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
