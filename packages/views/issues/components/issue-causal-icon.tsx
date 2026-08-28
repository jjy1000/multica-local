"use client";

// IssueCausalGraphIcon (0.5.83 WL3, roadmap §3.4 item 16) — the causal
// awareness affordance on the issue header: a small icon in the top
// right actions cluster that opens a read-only subgraph preview popup
// (radix Popover) with a depth toggle and a jump into the full
// /experimental/causal-graph workspace view.
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
  const [depth, setDepth] = useState(2);
  const [selected, setSelected] = useState<CausalNode | null>(null);

  const subgraph = useCausalSubgraph(enabled ? issueId : null, depth);

  // ICP-5: hidden entirely when flag off or nothing to show yet.
  if (!enabled) return null;
  if (subgraph.isError) return null;
  if (!subgraph.data && !subgraph.isPending) return null;
  const nodes = subgraph.data?.nodes ?? [];
  if (!subgraph.isPending && nodes.length === 0) return null;

  const edges = subgraph.data?.edges ?? [];

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
            {subgraph.isPending ? (
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
          width={420}
          height={280}
          selectedNodeId={selected?.id ?? null}
          onSelectNode={setSelected}
        />
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
