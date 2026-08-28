"use client";

// CausalMinimap (0.5.83 WL3) — read-only force-free graph renderer for
// the issue causal graph. Pure <svg>, no chart/force dependency: node
// positions come from a deterministic BFS ring layout computed from
// the seed set (issue-bound nodes at the centre, neighbours on outer
// rings), so the same (sub)graph always renders identically — good
// enough for a glanceable minimap and honest about not being a force
// simulation.
//
// Shared by IssueCausalGraphPopup (issue header) and the
// /experimental/causal-graph workspace view. Node/edge colours key off
// the verbatim type CHECK sets (migrations 277/278).

import { useMemo } from "react";
import type { CausalEdge, CausalNode } from "@multica/core/types/api";

export const CAUSAL_NODE_TYPE_COLORS: Record<string, string> = {
  decision: "#2563eb", // blue
  action: "#059669", // emerald
  outcome: "#7c3aed", // violet
  assumption: "#d97706", // amber
  evidence: "#0891b2", // cyan
  constraint: "#64748b", // slate
};

// EDGE_TONES (0.5.84 P0 #5 fix — split by status). The pre-fix shape
// keyed by edge type only, so an active edge and a rejected tombstone
// rendered identically; rejected tombstones (mig 280) are the audit
// trail AND a dedup anchor, and the minimap must visibly distinguish
// them so the user can see what is confirmed vs awaiting a decision vs
// rejected forever. Suggested edges stay muted (they have not been
// accepted yet); rejected edges go ghost-opacity and drop the dash
// pattern so they read as "background annotation" rather than live.
interface EdgeTone {
  stroke: string;
  dashed: boolean;
  opacity: number;
}
type EdgeToneByStatus = Record<"active" | "suggested" | "rejected", EdgeTone>;
type EdgeToneByType = Record<string, EdgeToneByStatus>;
const FALLBACK_TONES: EdgeToneByStatus = {
  active: { stroke: "#94a3b8", dashed: false, opacity: 0.65 },
  suggested: { stroke: "#94a3b8", dashed: true, opacity: 0.4 },
  rejected: { stroke: "#94a3b8", dashed: false, opacity: 0.3 },
};
const EDGE_TONES: EdgeToneByType = {
  causes: {
    active: { stroke: "#334155", dashed: false, opacity: 0.7 },
    suggested: { stroke: "#64748b", dashed: true, opacity: 0.5 },
    rejected: { stroke: "#94a3b8", dashed: false, opacity: 0.3 },
  },
  supports: {
    active: { stroke: "#059669", dashed: true, opacity: 0.7 },
    suggested: { stroke: "#10b981", dashed: true, opacity: 0.5 },
    rejected: { stroke: "#94a3b8", dashed: true, opacity: 0.3 },
  },
  contradicts: {
    active: { stroke: "#dc2626", dashed: true, opacity: 0.7 },
    suggested: { stroke: "#f87171", dashed: true, opacity: 0.5 },
    rejected: { stroke: "#94a3b8", dashed: true, opacity: 0.3 },
  },
  depends_on: {
    active: { stroke: "#64748b", dashed: false, opacity: 0.7 },
    suggested: { stroke: "#94a3b8", dashed: false, opacity: 0.5 },
    rejected: { stroke: "#cbd5e1", dashed: false, opacity: 0.3 },
  },
  enables: {
    active: { stroke: "#2563eb", dashed: false, opacity: 0.7 },
    suggested: { stroke: "#60a5fa", dashed: false, opacity: 0.5 },
    rejected: { stroke: "#94a3b8", dashed: false, opacity: 0.3 },
  },
  blocks: {
    active: { stroke: "#dc2626", dashed: false, opacity: 0.7 },
    suggested: { stroke: "#f87171", dashed: false, opacity: 0.5 },
    rejected: { stroke: "#94a3b8", dashed: false, opacity: 0.3 },
  },
};

// resolveEdgeTone picks the tone for an edge by (type, status). Falls
// back to a neutral grey so unknown statuses never collapse the
// rendering — the audit flagged that an unknown status silently
// producing the same tone as 'active' was the original regression
// surface; the explicit fallback makes "I do not know this status"
// visible.
export function resolveEdgeTone(type: string, status: string | undefined | null): EdgeTone {
  const safeStatus: keyof EdgeToneByStatus =
    status === "suggested" || status === "rejected" || status === "active"
      ? status
      : "active";
  const byStatus = EDGE_TONES[type];
  if (byStatus) return byStatus[safeStatus];
  return FALLBACK_TONES[safeStatus];
}

interface LaidOutNode extends CausalNode {
  x: number;
  y: number;
  ring: number;
}

// Deterministic BFS ring layout: seeds (or the highest-degree nodes
// when every node is a seed candidate) sit at the centre; each BFS
// layer lands on the next ring, evenly spaced.
// Exported for unit tests (causal-minimap.test.tsx) so the ring-spread
// regression can be pinned without rendering the SVG.
export function layout(nodes: CausalNode[], edges: CausalEdge[], width: number, height: number): LaidOutNode[] {
  if (nodes.length === 0) return [];
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const adjacency = new Map<string, string[]>();
  for (const e of edges) {
    if (!byId.has(e.from_node_id) || !byId.has(e.to_node_id)) continue;
    adjacency.set(e.from_node_id, [...(adjacency.get(e.from_node_id) ?? []), e.to_node_id]);
    adjacency.set(e.to_node_id, [...(adjacency.get(e.to_node_id) ?? []), e.from_node_id]);
  }
  // Seeds: nodes with an issue binding (issue-scoped slice) or, in a
  // workspace-wide graph, the highest-degree nodes.
  let seeds = nodes.filter((n) => n.issue_id);
  if (seeds.length === 0) {
    const degree = (id: string) => (adjacency.get(id)?.length ?? 0);
    const sorted = [...nodes].sort((a, b) => degree(b.id) - degree(a.id));
    seeds = sorted.slice(0, Math.max(1, Math.ceil(nodes.length / 8)));
  }
  const ringOf = new Map<string, number>();
  const queue: string[] = [];
  for (const s of seeds) {
    ringOf.set(s.id, 0);
    queue.push(s.id);
  }
  for (let i = 0; i < queue.length; i++) {
    const cur = queue[i];
    if (!cur) continue;
    for (const next of adjacency.get(cur) ?? []) {
      if (ringOf.has(next)) continue;
      ringOf.set(next, (ringOf.get(cur) ?? 0) + 1);
      queue.push(next);
    }
  }
  // Disconnected nodes land on the outermost ring.
  const maxRing = Math.max(0, ...[...ringOf.values()]);
  for (const n of nodes) {
    if (!ringOf.has(n.id)) ringOf.set(n.id, maxRing + 1);
  }

  // Pre-compute ring lengths BEFORE any insertion so the angular
  // divisor is stable. The previous formula read byRing.get(ring).length
  // mid-loop, so the divisor grew as each node was inserted and the
  // spread inverted (N=3 gave angles 0, π, 4π/3 instead of 0, 2π/3, 4π/3).
  // 0.5.84 P0 #6 fix — even-spread regression pin.
  const ringLengths = new Map<number, number>();
  for (const n of nodes) {
    const ring = ringOf.get(n.id) ?? 0;
    ringLengths.set(ring, (ringLengths.get(ring) ?? 0) + 1);
  }

  const byRing = new Map<number, LaidOutNode[]>();
  for (const n of nodes) {
    const ring = ringOf.get(n.id) ?? 0;
    const cx = width / 2;
    const cy = height / 2;
    const maxR = Math.min(width, height) / 2 - 24;
    const indexWithinRing = byRing.get(ring)?.length ?? 0;
    const ringLength = ringLengths.get(ring) ?? 1;
    const radius = ring === 0 ? (seeds.length > 1 ? maxR * 0.28 : 0) : (maxR * ring) / (maxRing + 1);
    // Index within ring divided by the pre-computed ring length for
    // even angular spread. Pre-insertion count is intentional — the
    // divisor must NOT grow as nodes are added.
    const angle = ring === 0 && seeds.length === 1
      ? 0
      : (indexWithinRing * 2 * Math.PI) / Math.max(ringLength, 1);
    const laid: LaidOutNode = {
      ...n,
      x: cx + radius * Math.cos(angle),
      y: cy + radius * Math.sin(angle),
      ring,
    };
    byRing.set(ring, [...(byRing.get(ring) ?? []), laid]);
  }
  // Flatten preserving per-ring order (the angle formula needs the
  // pre-insertion count, which the loop above already applied).
  return nodes
    .map((n) => {
      for (const laid of byRing.get(ringOf.get(n.id) ?? 0) ?? []) {
        if (laid.id === n.id) return laid;
      }
      return undefined;
    })
    .filter((n): n is LaidOutNode => n !== undefined);
}

export function CausalMinimap({
  nodes,
  edges,
  width = 420,
  height = 300,
  selectedNodeId,
  onSelectNode,
}: {
  nodes: CausalNode[];
  edges: CausalEdge[];
  width?: number;
  height?: number;
  selectedNodeId?: string | null;
  onSelectNode?: (node: CausalNode) => void;
}) {
  const laid = useMemo(() => layout(nodes, edges, width, height), [nodes, edges, width, height]);
  const posById = useMemo(() => new Map(laid.map((n) => [n.id, n])), [laid]);

  if (laid.length === 0) return null;

  return (
    <svg
      viewBox={`0 0 ${width} ${height}`}
      role="img"
      aria-label="causal graph minimap"
      className="w-full rounded-md border border-border/60 bg-background"
    >
      {edges.map((e) => {
        const from = posById.get(e.from_node_id);
        const to = posById.get(e.to_node_id);
        if (!from || !to) return null;
        // P0 #5 fix: resolve tone by (type, status) so rejected
        // tombstones and pending suggestions render distinctly from
        // active edges — pre-fix this keyed by edge type only and
        // every status rendered identically.
        const tone = resolveEdgeTone(e.type, e.status);
        const mx = (from.x + to.x) / 2;
        const my = (from.y + to.y) / 2;
        const bend = Math.hypot(to.x - from.x, to.y - from.y) * 0.12;
        return (
          <path
            key={e.id}
            d={`M ${from.x} ${from.y} Q ${mx + bend} ${my - bend} ${to.x} ${to.y}`}
            fill="none"
            stroke={tone.stroke}
            strokeWidth={1.5}
            strokeDasharray={tone.dashed ? "4 3" : undefined}
            opacity={tone.opacity}
          />
        );
      })}
      {laid.map((n) => {
        const color = CAUSAL_NODE_TYPE_COLORS[n.type] ?? "#94a3b8";
        const selected = selectedNodeId === n.id;
        return (
          <g
            key={n.id}
            transform={`translate(${n.x}, ${n.y})`}
            className={onSelectNode ? "cursor-pointer" : undefined}
            onClick={onSelectNode ? () => onSelectNode(n) : undefined}
          >
            <circle
              r={selected ? 13 : 10}
              fill={color}
              opacity={0.9}
              stroke={selected ? "#0f172a" : "#ffffff"}
              strokeWidth={selected ? 2.5 : 1.5}
            />
            <title>{`${n.type}: ${n.label}`}</title>
            <text
              y={24}
              textAnchor="middle"
              fontSize={9}
              fill="currentColor"
              className="fill-muted-foreground"
            >
              {n.label.length > 18 ? `${n.label.slice(0, 17)}…` : n.label}
            </text>
          </g>
        );
      })}
    </svg>
  );
}
