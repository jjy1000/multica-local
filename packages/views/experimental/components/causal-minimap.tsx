"use client";

// CausalMinimap (0.5.83 WL3) — read-only force-free graph renderer for
// the issue causal graph. Pure <svg>, no chart/force dependency: node
// positions come from a deterministic BFS ring layout, so the same
// (sub)graph always renders identically — good enough for a glanceable
// minimap and honest about not being a force simulation.
//
// 0.5.86 layout contract (crowding overhaul — keep `layout` pure,
// deterministic, exported for the regression pins):
//   - Ring-0 seeds are CAPPED at CAUSAL_MAX_CENTER_SEEDS (3), chosen by
//     descending degree (id tie-break); every other former seed
//     re-enters the BFS as a normal ring-1 member, so a fully
//     issue-bound graph no longer collapses onto one tiny centre ring.
//   - Per-ring radius r_k = max(maxR·k/(maxRing+1), L_k·SPACING/2π)
//     guarantees ≥ CAUSAL_MIN_RING_SPACING of arc per node at any N —
//     circles can never overlap however dense the ring gets.
//   - Nodes within a ring are ordered by their BFS parent's angle
//     (ascending, id tie-break) to cut edge crossings.
//
// 0.5.86 interaction contract: nodes are draggable (pointer capture on
// the svg, 5px click-vs-drag threshold mirroring use-board-drag-pan.ts);
// positions merge into the layout AFTER useMemo via `positionOverrides`
// (parent-owned Map for the full page, internal state fallback for the
// popover) so edges re-follow automatically. `viewportTransform` +
// `onBackgroundPointerDown` let CausalGraphCanvas drive zoom/pan through
// an inner <g> without changing the svg's fixed viewBox or w-full CSS
// scaling — the popover stays a fixed-size surface. Animations (entry
// stagger, edge draw-in, selection pulse, active-edge dash-flow) are all
// gated behind useReducedMotion().
//
// Shared by IssueCausalGraphPopup (issue header) and the
// /experimental/causal-graph workspace view. Node/edge colours key off
// the verbatim type CHECK sets (migrations 277/278).

import { useCallback, useEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent } from "react";
import { motion, useReducedMotion } from "motion/react";
import type { CausalEdge, CausalNode } from "@multica/core/types/api";

export const CAUSAL_NODE_TYPE_COLORS: Record<string, string> = {
  decision: "#2563eb", // blue
  action: "#059669", // emerald
  outcome: "#7c3aed", // violet
  assumption: "#d97706", // amber
  evidence: "#0891b2", // cyan
  constraint: "#64748b", // slate
};

// 0.5.86 crowding constants. MIN_RING_SPACING is the arc length every
// node is guaranteed on its ring (20px node + 14px gap); MAX_CENTER_
// SEEDS caps the centre ring so a fully issue-bound graph spreads out
// instead of stacking every node at the same radius.
export const CAUSAL_MIN_RING_SPACING = 34;
export const CAUSAL_MAX_CENTER_SEEDS = 3;

// Click-vs-drag threshold (px) — mirrors use-board-drag-pan.ts so a node
// press and a board press feel identical up to the moment one is
// recognised as a drag.
const DRAG_ACTIVATION_DISTANCE = 5;

// EdgeTones (0.5.84 P0 #5 fix — split by status). The pre-fix shape
// keyed by edge type only, so an active edge and a rejected tombstone
// rendered identically; rejected tombstones (mig 280) are the audit
// trail AND a dedup anchor, and the minimap must visibly distinguish
// them so the user can see what is confirmed vs awaiting a decision vs
// rejected forever. Suggested edges stay muted (they have not been
// accepted yet); rejected edges go ghost-opacity and drop the dash
// pattern so they read as "background annotation" rather than live.
// 0.5.86: superseded edges get an explicit muted dashed tone — they are
// history and must never read as live; the dash-flow animation is only
// ever applied to status === "active".
interface EdgeTone {
  stroke: string;
  dashed: boolean;
  opacity: number;
}
type EdgeToneByStatus = Record<"active" | "suggested" | "rejected" | "superseded", EdgeTone>;
type EdgeToneByType = Record<string, EdgeToneByStatus>;
const SUPERSEDED_TONE: EdgeTone = { stroke: "#cbd5e1", dashed: true, opacity: 0.22 };
const FALLBACK_TONES: EdgeToneByStatus = {
  active: { stroke: "#94a3b8", dashed: false, opacity: 0.65 },
  suggested: { stroke: "#94a3b8", dashed: true, opacity: 0.4 },
  rejected: { stroke: "#94a3b8", dashed: false, opacity: 0.3 },
  superseded: SUPERSEDED_TONE,
};
const EDGE_TONES: EdgeToneByType = {
  causes: {
    active: { stroke: "#334155", dashed: false, opacity: 0.7 },
    suggested: { stroke: "#64748b", dashed: true, opacity: 0.5 },
    rejected: { stroke: "#94a3b8", dashed: false, opacity: 0.3 },
    superseded: SUPERSEDED_TONE,
  },
  supports: {
    active: { stroke: "#059669", dashed: true, opacity: 0.7 },
    suggested: { stroke: "#10b981", dashed: true, opacity: 0.5 },
    rejected: { stroke: "#94a3b8", dashed: true, opacity: 0.3 },
    superseded: SUPERSEDED_TONE,
  },
  contradicts: {
    active: { stroke: "#dc2626", dashed: true, opacity: 0.7 },
    suggested: { stroke: "#f87171", dashed: true, opacity: 0.5 },
    rejected: { stroke: "#94a3b8", dashed: true, opacity: 0.3 },
    superseded: SUPERSEDED_TONE,
  },
  depends_on: {
    active: { stroke: "#64748b", dashed: false, opacity: 0.7 },
    suggested: { stroke: "#94a3b8", dashed: false, opacity: 0.5 },
    rejected: { stroke: "#cbd5e1", dashed: false, opacity: 0.3 },
    superseded: SUPERSEDED_TONE,
  },
  enables: {
    active: { stroke: "#2563eb", dashed: false, opacity: 0.7 },
    suggested: { stroke: "#60a5fa", dashed: false, opacity: 0.5 },
    rejected: { stroke: "#94a3b8", dashed: false, opacity: 0.3 },
    superseded: SUPERSEDED_TONE,
  },
  blocks: {
    active: { stroke: "#dc2626", dashed: false, opacity: 0.7 },
    suggested: { stroke: "#f87171", dashed: false, opacity: 0.5 },
    rejected: { stroke: "#94a3b8", dashed: false, opacity: 0.3 },
    superseded: SUPERSEDED_TONE,
  },
};

// resolveEdgeTone picks the tone for an edge by (type, status). Falls
// back to a neutral grey so unknown statuses never collapse the
// rendering — the audit flagged that an unknown status silently
// producing the same tone as 'active' was the original regression
// surface; the explicit fallback makes "I do not know this status"
// visible. "superseded" has its own explicit tone (muted grey, dashed,
// never animated).
export function resolveEdgeTone(type: string, status: string | undefined | null): EdgeTone {
  const safeStatus: keyof EdgeToneByStatus =
    status === "suggested" || status === "rejected" || status === "active" || status === "superseded"
      ? status
      : "active";
  const byStatus = EDGE_TONES[type];
  if (byStatus) return byStatus[safeStatus];
  return FALLBACK_TONES[safeStatus];
}

export function markerIdFor(edgeType: string, stroke: string): string {
  return `causal-arrow-${`${edgeType}|${stroke}`.replace(/[^a-zA-Z0-9-]/g, "_")}`;
}

interface LaidOutNode extends CausalNode {
  x: number;
  y: number;
  ring: number;
}

// Minimal structural matrix (the DOMMatrix subset clientToViewBox
// needs) so tests can inject a plain object — getScreenCTM and DOMPoint
// are absent in jsdom.
export interface SvgClientMatrix {
  a: number;
  b: number;
  c: number;
  d: number;
  e: number;
  f: number;
  inverse(): SvgClientMatrix;
}

/**
 * Maps client (viewport) coordinates into the coordinate space of `el`
 * (the svg, or an inner transformed <g> when a viewport transform is
 * active — getScreenCTM of the group includes its own transform chain).
 * Pass `matrix` to inject the screen CTM explicitly: jsdom has no
 * getScreenCTM, so drag logic must degrade to a null return rather than
 * throw. Pure affine math on the matrix fields — no DOMPoint needed.
 */
export function clientToViewBox(
  el: { getScreenCTM(): SvgClientMatrix | null },
  clientX: number,
  clientY: number,
  matrix?: SvgClientMatrix | null,
): { x: number; y: number } | null {
  const m = matrix ?? el.getScreenCTM();
  if (!m || typeof m.inverse !== "function") return null;
  const inv = m.inverse();
  if (!inv) return null;
  return {
    x: inv.a * clientX + inv.c * clientY + inv.e,
    y: inv.b * clientX + inv.d * clientY + inv.f,
  };
}

// Deterministic BFS ring layout (see the header for the 0.5.86
// contract). Exported for unit tests (causal-minimap.test.tsx) so the
// ring-spread + crowding regressions can be pinned without rendering
// the SVG.
export function layout(nodes: CausalNode[], edges: CausalEdge[], width: number, height: number): LaidOutNode[] {
  if (nodes.length === 0) return [];
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const adjacency = new Map<string, string[]>();
  for (const e of edges) {
    if (!byId.has(e.from_node_id) || !byId.has(e.to_node_id)) continue;
    adjacency.set(e.from_node_id, [...(adjacency.get(e.from_node_id) ?? []), e.to_node_id]);
    adjacency.set(e.to_node_id, [...(adjacency.get(e.to_node_id) ?? []), e.from_node_id]);
  }
  const degree = (id: string) => (adjacency.get(id)?.length ?? 0);
  // Seeds: nodes with an issue binding (issue-scoped slice) or, in a
  // workspace-wide graph, the highest-degree nodes — then capped at
  // MAX_CENTER_SEEDS by descending degree. The cap overflow re-enters
  // the BFS as an ordinary node, landing on ring 1.
  let candidates = nodes.filter((n) => n.issue_id);
  if (candidates.length === 0) {
    candidates = [...nodes].sort((a, b) => degree(b.id) - degree(a.id) || a.id.localeCompare(b.id));
    candidates = candidates.slice(0, Math.max(1, Math.ceil(nodes.length / 8)));
  }
  const seeds = [...candidates]
    .sort((a, b) => degree(b.id) - degree(a.id) || a.id.localeCompare(b.id))
    .slice(0, CAUSAL_MAX_CENTER_SEEDS);

  const ringOf = new Map<string, number>();
  const parentOf = new Map<string, string>();
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
      parentOf.set(next, cur);
      queue.push(next);
    }
  }
  // Disconnected nodes land on the outermost ring.
  const maxRing = Math.max(0, ...[...ringOf.values()]);
  for (const n of nodes) {
    if (!ringOf.has(n.id)) ringOf.set(n.id, maxRing + 1);
  }

  // Pre-compute ring lengths BEFORE any insertion so the angular
  // divisor is stable (0.5.84 P0 #6 fix — even-spread regression pin).
  const ringLengths = new Map<number, number>();
  for (const n of nodes) {
    const ring = ringOf.get(n.id) ?? 0;
    ringLengths.set(ring, (ringLengths.get(ring) ?? 0) + 1);
  }

  const cx = width / 2;
  const cy = height / 2;
  const maxR = Math.min(width, height) / 2 - 24;
  // Per-ring radius: the even-spread base, widened until every node on
  // the ring owns ≥ MIN_RING_SPACING of arc. Ring 0 keeps a small
  // centre radius only when more than one seed sits at the centre.
  const radiusForRing = (ring: number): number => {
    const spacingRadius = ((ringLengths.get(ring) ?? 1) * CAUSAL_MIN_RING_SPACING) / (2 * Math.PI);
    if (ring === 0) return seeds.length > 1 ? spacingRadius : 0;
    return Math.max((maxR * ring) / (maxRing + 1), spacingRadius);
  };

  // Group ids per ring, then order each ring by BFS parent angle
  // ascending (id tie-break) so children fan out around their parent
  // instead of crossing the whole ring. Rings are processed in
  // ascending order because a parent's angle must be known before its
  // children are ordered.
  const byRing = new Map<number, string[]>();
  for (const n of nodes) {
    const ring = ringOf.get(n.id) ?? 0;
    byRing.set(ring, [...(byRing.get(ring) ?? []), n.id]);
  }
  const angleOfId = new Map<string, number>();
  const laidOut: LaidOutNode[] = [];
  const lastRing = Math.max(0, ...byRing.keys());
  for (let ring = 0; ring <= lastRing; ring++) {
    const members = byRing.get(ring) ?? [];
    const radius = radiusForRing(ring);
    const ringLength = Math.max(members.length, 1);
    const sorted = [...members].sort((a, b) => {
      const pa = angleOfId.get(parentOf.get(a) ?? "") ?? 0;
      const pb = angleOfId.get(parentOf.get(b) ?? "") ?? 0;
      if (pa !== pb) return pa - pb;
      return a.localeCompare(b);
    });
    sorted.forEach((id, indexWithinRing) => {
      // Pre-insertion ring length for even angular spread — the divisor
      // must NOT grow as nodes are added.
      const angle = ring === 0 && seeds.length === 1
        ? 0
        : (indexWithinRing * 2 * Math.PI) / ringLength;
      angleOfId.set(id, angle);
      const n = byId.get(id);
      if (!n) return;
      laidOut.push({
        ...n,
        x: cx + radius * Math.cos(angle),
        y: cy + radius * Math.sin(angle),
        ring,
      });
    });
  }
  return laidOut;
}

// ---------------------------------------------------------------------------
// 0.5.120 force layout — the ring layout above stays as the deterministic
// INITIAL positions and the >160-node performance fallback; this pass
// relaxes the graph into a force-directed arrangement (repulsion + edge
// springs + gentle centering) so structure reads from the geometry:
// hubs push apart, chains stretch, clusters settle.
//
// Determinism contract (same bar as layout()): pure arithmetic, fixed
// iteration count, no randomness — identical inputs always produce
// identical coordinates. The pin parameter freezes nodes the caller
// owns (drag overrides / previous positions across poll refetches) so
// the 5s poll never makes the graph drift under the cursor.
// ---------------------------------------------------------------------------

// Force-pass knobs. REST_LENGTH scales with the canvas so the popover
// (420px) and the full-page canvas (760px) both breathe.
const FORCE_ITERATIONS = 160;
const FORCE_REPULSION = 3200;
const FORCE_CENTERING = 0.012;
const FORCE_MAX_STEP = 14;
const FORCE_NODE_CAP = 160;
const FORCE_NODE_MARGIN = 20;

export function forceRestLength(width: number, height: number): number {
  return Math.min(110, Math.max(60, Math.min(width, height) * 0.22));
}

export function forceLayout(
  initial: LaidOutNode[],
  edges: CausalEdge[],
  width: number,
  height: number,
  pinned?: Map<string, CausalPositionOverride>,
): LaidOutNode[] {
  if (initial.length === 0) return initial;
  // Performance guard: O(n²) per iteration — past the cap the ring
  // layout (already overlap-free) renders as-is.
  if (initial.length > FORCE_NODE_CAP) return initial;

  const pos = initial.map((n) => {
    const pin = pinned?.get(n.id);
    return pin ? { ...n, x: pin.x, y: pin.y } : { ...n };
  });
  const indexOf = new Map(pos.map((n, i) => [n.id, i]));

  // Unique edge pairs (parallel edges of different types collapse to
  // one spring — two springs on the same pair double-count).
  const springs: Array<[number, number]> = [];
  const seenPair = new Set<string>();
  for (const e of edges) {
    const a = indexOf.get(e.from_node_id);
    const b = indexOf.get(e.to_node_id);
    if (a === undefined || b === undefined || a === b) continue;
    const key = a < b ? `${a}:${b}` : `${b}:${a}`;
    if (seenPair.has(key)) continue;
    seenPair.add(key);
    springs.push([a, b]);
  }

  const rest = forceRestLength(width, height);
  const cx = width / 2;
  const cy = height / 2;
  const vx = new Array<number>(pos.length).fill(0);
  const vy = new Array<number>(pos.length).fill(0);

  for (let iter = 0; iter < FORCE_ITERATIONS; iter++) {
    vx.fill(0);
    vy.fill(0);

    // Pairwise repulsion (Coulomb-ish, d² falloff with a near clamp so
    // overlapping nodes separate hard but never explode).
    for (let i = 0; i < pos.length; i++) {
      const a = pos[i]!;
      for (let j = i + 1; j < pos.length; j++) {
        const b = pos[j]!;
        let dx = a.x - b.x;
        let dy = a.y - b.y;
        let d2 = dx * dx + dy * dy;
        if (d2 < 1) {
          // Coincident nodes (same ring angle) get a deterministic
          // nudge direction so the repulsion can act at all.
          dx = 0.71;
          dy = 0.71;
          d2 = 1;
        }
        const d = Math.sqrt(d2);
        const f = FORCE_REPULSION / Math.max(d2, 64);
        const fx = (dx / d) * f;
        const fy = (dy / d) * f;
        vx[i]! += fx;
        vy[i]! += fy;
        vx[j]! -= fx;
        vy[j]! -= fy;
      }
    }

    // Edge springs toward the rest length.
    for (const [i, j] of springs) {
      const a = pos[i]!;
      const b = pos[j]!;
      const dx = b.x - a.x;
      const dy = b.y - a.y;
      const d = Math.max(Math.hypot(dx, dy), 1);
      const f = (d - rest) * 0.03;
      const fx = (dx / d) * f;
      const fy = (dy / d) * f;
      vx[i]! += fx;
      vy[i]! += fy;
      vx[j]! -= fx;
      vy[j]! -= fy;
    }

    // Gentle centering + clamped integration. Pinned nodes never move.
    for (let i = 0; i < pos.length; i++) {
      const n = pos[i]!;
      if (pinned?.has(n.id)) continue;
      vx[i]! += (cx - n.x) * FORCE_CENTERING;
      vy[i]! += (cy - n.y) * FORCE_CENTERING;
      let dx = vx[i]!;
      let dy = vy[i]!;
      const mag = Math.hypot(dx, dy);
      if (mag > FORCE_MAX_STEP) {
        dx = (dx / mag) * FORCE_MAX_STEP;
        dy = (dy / mag) * FORCE_MAX_STEP;
      }
      n.x += dx;
      n.y += dy;
    }
  }

  // Final clamp inside the viewBox with a node-radius margin.
  for (const n of pos) {
    if (pinned?.has(n.id)) continue;
    n.x = Math.min(Math.max(n.x, FORCE_NODE_MARGIN), width - FORCE_NODE_MARGIN);
    n.y = Math.min(Math.max(n.y, FORCE_NODE_MARGIN), height - FORCE_NODE_MARGIN);
  }
  return pos;
}

// Width-derived label budget: the full-page canvas affords longer
// labels than the 440px popover.
function maxLabelChars(width: number): number {
  return width >= 600 ? 12 : 8;
}

function truncateLabel(label: string, max: number): string {
  return label.length > max ? `${label.slice(0, max - 1)}…` : label;
}

export interface CausalViewportTransform {
  x: number;
  y: number;
  scale: number;
}

export type CausalPositionOverride = { x: number; y: number };

interface NodeDragState {
  node: CausalNode;
  pointerId: number;
  startClientX: number;
  startClientY: number;
  moved: boolean;
  raf: number | null;
  pending: CausalPositionOverride | null;
}

// Degree map for the radius encoding — hubs read bigger. Shared by the
// renderer; recomputed only when the edge list identity changes.
function degreeMap(edges: CausalEdge[]): Map<string, number> {
  const deg = new Map<string, number>();
  for (const e of edges) {
    deg.set(e.from_node_id, (deg.get(e.from_node_id) ?? 0) + 1);
    deg.set(e.to_node_id, (deg.get(e.to_node_id) ?? 0) + 1);
  }
  return deg;
}

// Radius encoding: degree 0 → 8px, +0.75 per degree, capped at 14px.
// Degree (not type) drives size so the graph's hubs are findable at a
// glance without reading labels.
export function nodeRadius(degree: number): number {
  return 8 + Math.min(degree, 8) * 0.75;
}

export function CausalMinimap({
  nodes,
  edges,
  width = 420,
  height = 300,
  selectedNodeId,
  onSelectNode,
  positionOverrides,
  onPositionOverride,
  onDragStateChange,
  viewportTransform,
  onBackgroundPointerDown,
}: {
  nodes: CausalNode[];
  edges: CausalEdge[];
  width?: number;
  height?: number;
  selectedNodeId?: string | null;
  onSelectNode?: (node: CausalNode) => void;
  /** Session-only drag overrides owned by the parent page (never cached). */
  positionOverrides?: Map<string, CausalPositionOverride>;
  /** Live drag callback; omit to let the minimap own the overrides internally. */
  onPositionOverride?: (nodeId: string, pos: CausalPositionOverride | null) => void;
  /** Drag-gesture notifier — the page pauses the 5s poll while true. */
  onDragStateChange?: (dragging: boolean) => void;
  /** Zoom/pan group transform — the full-page canvas owns it; the popover stays fixed-size. */
  viewportTransform?: CausalViewportTransform;
  /** Fired when a pointer press lands on the svg background (no node under it). */
  onBackgroundPointerDown?: (event: ReactPointerEvent<SVGSVGElement>) => void;
}) {
  const reducedMotion = useReducedMotion() ?? false;
  const svgRef = useRef<SVGSVGElement | null>(null);
  const contentRef = useRef<SVGGElement | null>(null);
  const dragRef = useRef<NodeDragState | null>(null);
  const suppressClickRef = useRef(false);
  const [hoveredId, setHoveredId] = useState<string | null>(null);
  const [internalOverrides, setInternalOverrides] = useState<Map<string, CausalPositionOverride>>(() => new Map());

  const overrides = positionOverrides ?? internalOverrides;

  const applyOverride = useCallback(
    (nodeId: string, pos: CausalPositionOverride | null) => {
      if (onPositionOverride) {
        onPositionOverride(nodeId, pos);
        return;
      }
      setInternalOverrides((prev) => {
        const next = new Map(prev);
        if (pos) next.set(nodeId, pos);
        else next.delete(nodeId);
        return next;
      });
    },
    [onPositionOverride],
  );

  const ringLaid = useMemo(() => layout(nodes, edges, width, height), [nodes, edges, width, height]);

  // 0.5.120: force-directed relaxation on top of the ring seed. The
  // signature cache is the poll-stability contract: the 5s workspace
  // poll hands us a fresh nodes array every tick, and re-relaxing on
  // every identity change would make the graph crawl under the cursor.
  // Only a change in the graph's MEMBERSHIP (node/edge id sets + canvas
  // size) retriggers the pass; when it does, the previous positions
  // pin as the starting point so the re-arrangement is a short local
  // settle, not a reshuffle.
  const graphSignature = useMemo(
    () =>
      `${width}x${height}|${ringLaid.map((n) => n.id).join(",")}|${edges.map((e) => e.id).join(",")}`,
    [ringLaid, edges, width, height],
  );
  const forceCacheRef = useRef<{ signature: string; positions: Map<string, CausalPositionOverride> } | null>(null);
  const laid = useMemo(() => {
    const cached = forceCacheRef.current;
    const positions = new Map<string, CausalPositionOverride>();
    if (cached && cached.signature === graphSignature) {
      // Membership unchanged — reuse the settled positions verbatim.
      for (const n of ringLaid) {
        const prev = cached.positions.get(n.id);
        positions.set(n.id, prev ?? { x: n.x, y: n.y });
      }
      return ringLaid.map((n) => {
        const p = positions.get(n.id);
        return p ? { ...n, x: p.x, y: p.y } : n;
      });
    }
    // Membership changed. Prior positions (for nodes that still exist)
    // pin as the settle start; brand-new nodes relax from their ring
    // seed. User drag overrides are applied later in posById and are
    // passed as pins too so neighbours arrange AROUND the dragged node.
    const startPins = new Map<string, CausalPositionOverride>();
    if (cached) {
      for (const n of ringLaid) {
        const prev = cached.positions.get(n.id);
        if (prev) startPins.set(n.id, prev);
      }
    }
    const relaxed = forceLayout(ringLaid, edges, width, height, startPins.size > 0 ? startPins : undefined);
    for (const n of relaxed) positions.set(n.id, { x: n.x, y: n.y });
    forceCacheRef.current = { signature: graphSignature, positions };
    return relaxed;
  }, [ringLaid, edges, width, height, graphSignature]);

  // Overrides merge AFTER the layout so edges re-follow automatically.
  const posById = useMemo(() => {
    const merged = new Map<string, LaidOutNode>();
    for (const n of laid) {
      const o = overrides.get(n.id);
      merged.set(n.id, o ? { ...n, x: o.x, y: o.y } : n);
    }
    return merged;
  }, [laid, overrides]);

  // Past 14 nodes, only the selected/hovered node and its direct
  // neighbours keep their labels (full names always live in <title>).
  const labelIds = useMemo(() => {
    if (laid.length <= 14) return null;
    const ids = new Set<string>();
    for (const anchor of [selectedNodeId, hoveredId]) {
      if (!anchor) continue;
      ids.add(anchor);
      for (const e of edges) {
        if (e.from_node_id === anchor) ids.add(e.to_node_id);
        if (e.to_node_id === anchor) ids.add(e.from_node_id);
      }
    }
    return ids;
  }, [laid.length, edges, selectedNodeId, hoveredId]);

  // 0.5.120 focus mode: hovering (or selecting) a node dims everything
  // not adjacent to it, so the anchor's immediate causal neighbourhood
  // pops out of even a dense workspace graph. No anchor → no dimming.
  // (Hooks live BEFORE the empty-graph early return — rules of hooks.)
  const focusAnchor = hoveredId ?? selectedNodeId ?? null;
  const focusIds = useMemo(() => {
    if (!focusAnchor) return null;
    const ids = new Set<string>([focusAnchor]);
    for (const e of edges) {
      if (e.from_node_id === focusAnchor) ids.add(e.to_node_id);
      if (e.to_node_id === focusAnchor) ids.add(e.from_node_id);
    }
    return ids;
  }, [focusAnchor, edges]);

  const degree = useMemo(() => degreeMap(edges), [edges]);

  const finishDrag = useCallback(
    (select: boolean) => {
      const drag = dragRef.current;
      if (!drag) return;
      if (drag.raf !== null) {
        cancelAnimationFrame(drag.raf);
        drag.raf = null;
      }
      // Flush the last buffered position synchronously so the final
      // frame of the drag never drops.
      if (drag.pending) applyOverride(drag.node.id, drag.pending);
      dragRef.current = null;
      if (drag.moved) {
        // The synthetic click after a drag must not re-trigger selection.
        suppressClickRef.current = true;
        window.setTimeout(() => {
          suppressClickRef.current = false;
        }, 0);
      } else if (select && onSelectNode) {
        onSelectNode(drag.node);
      }
      onDragStateChange?.(false);
    },
    [applyOverride, onSelectNode, onDragStateChange],
  );

  useEffect(() => {
    const handleBlur = () => {
      if (dragRef.current) finishDrag(false);
    };
    window.addEventListener("blur", handleBlur);
    return () => window.removeEventListener("blur", handleBlur);
  }, [finishDrag]);

  // Gesture-origin discrimination (use-board-drag-pan.ts pattern): a
  // press that starts on a node begins the node drag; anything else is
  // handed to the background-pan owner (CausalGraphCanvas).
  const handleSvgPointerDown = (event: ReactPointerEvent<SVGSVGElement>) => {
    const target = event.target as Element | null;
    const nodeG = target?.closest("[data-causal-node-id]");
    if (!nodeG) {
      onBackgroundPointerDown?.(event);
      return;
    }
    if (event.pointerType === "mouse" && event.button !== 0) return;
    const nodeId = nodeG.getAttribute("data-causal-node-id");
    const node = nodeId ? posById.get(nodeId) : undefined;
    if (!nodeId || !node) return;
    event.preventDefault();
    try {
      svgRef.current?.setPointerCapture(event.pointerId);
    } catch {
      // Capture unsupported (jsdom, synthetic events) — the drag still
      // works through bubbled pointer events while the button is held.
    }
    dragRef.current = {
      node,
      pointerId: event.pointerId,
      startClientX: event.clientX,
      startClientY: event.clientY,
      moved: false,
      raf: null,
      pending: null,
    };
  };

  const handleSvgPointerMove = (event: ReactPointerEvent<SVGSVGElement>) => {
    const drag = dragRef.current;
    if (!drag || event.pointerId !== drag.pointerId) return;
    // The primary button was released somewhere we didn't hear about.
    if (event.pointerType === "mouse" && (event.buttons & 1) === 0) {
      finishDrag(false);
      return;
    }
    if (!drag.moved) {
      const dist = Math.hypot(event.clientX - drag.startClientX, event.clientY - drag.startClientY);
      if (dist < DRAG_ACTIVATION_DISTANCE) return;
      drag.moved = true;
      onDragStateChange?.(true);
    }
    // Read the CONTENT group's screen CTM — it includes the zoom/pan
    // transform, so overrides stay in layout space at any zoom level.
    const el = contentRef.current ?? svgRef.current;
    const pos = el ? clientToViewBox(el, event.clientX, event.clientY) : null;
    if (!pos) return;
    drag.pending = {
      x: Math.min(Math.max(pos.x, 10), width - 10),
      y: Math.min(Math.max(pos.y, 10), height - 10),
    };
    // Coalesce state writes to one per animation frame.
    if (drag.raf === null) {
      if (typeof requestAnimationFrame === "function") {
        drag.raf = requestAnimationFrame(() => {
          const d = dragRef.current;
          if (!d) return;
          d.raf = null;
          if (d.pending) applyOverride(d.node.id, d.pending);
        });
      } else if (drag.pending) {
        applyOverride(drag.node.id, drag.pending);
      }
    }
  };

  const handleSvgPointerUp = (event: ReactPointerEvent<SVGSVGElement>) => {
    const drag = dragRef.current;
    if (!drag || event.pointerId !== drag.pointerId) return;
    try {
      svgRef.current?.releasePointerCapture(event.pointerId);
    } catch {
      /* already released */
    }
    // A press that stayed under the threshold selects the node.
    finishDrag(true);
  };

  // A cancelled gesture never selects.
  const handleSvgPointerCancel = () => {
    if (dragRef.current) finishDrag(false);
  };

  const handleSvgLostPointerCapture = () => {
    if (dragRef.current) finishDrag(false);
  };

  if (laid.length === 0) return null;

  const labelBudget = maxLabelChars(width);
  // Dash-flow is CSS-animated and only ever applied to status ===
  // "active" edges that carry a dash pattern (superseded/suggested/
  // rejected never animate). Precedent for an inline <style> tag in a
  // views component: priority-icon.tsx.
  const hasFlowEdges =
    !reducedMotion && edges.some((e) => e.status === "active" && resolveEdgeTone(e.type, e.status).dashed);

  return (
    <svg
      ref={svgRef}
      viewBox={`0 0 ${width} ${height}`}
      role="img"
      aria-label="causal graph minimap"
      className="w-full rounded-md border border-border/60 bg-background"
      onPointerDown={handleSvgPointerDown}
      onPointerMove={handleSvgPointerMove}
      onPointerUp={handleSvgPointerUp}
      onPointerCancel={handleSvgPointerCancel}
      onLostPointerCapture={handleSvgLostPointerCapture}
    >
      {hasFlowEdges ? (
        <style>{`@keyframes causal-edge-flow{to{stroke-dashoffset:-14}}.causal-edge-flow{animation:causal-edge-flow 0.9s linear infinite}`}</style>
      ) : null}
      <defs>
        {/* Subtle dot grid — gives the canvas a spatial substrate so
            pan/zoom reads as movement, not as shapes sliding on paper. */}
        <pattern id="causal-dot-grid" width="28" height="28" patternUnits="userSpaceOnUse">
          <circle cx="1.2" cy="1.2" r="1.2" className="fill-foreground" opacity={0.055} />
        </pattern>
        {/* Direction markers — causality without a visible direction is
            decoration. One marker per (type, status) tone actually in
            use; unknown types share the neutral fallback marker. */}
        {Array.from(
          edges.reduce((acc, e) => {
            const tone = resolveEdgeTone(e.type, e.status);
            const key = `${e.type}|${tone.stroke}`;
            if (!acc.has(key)) acc.set(key, tone.stroke);
            return acc;
          }, new Map<string, string>()),
        ).map(([key, stroke]) => {
          const markerId = markerIdFor(key.split("|")[0] ?? "", stroke);
          return (
            <marker
              key={markerId}
              id={markerId}
              viewBox="0 0 8 8"
              refX={7}
              refY={4}
              markerWidth={5.5}
              markerHeight={5.5}
              orient="auto-start-reverse"
            >
              <path d="M 0 1 L 7 4 L 0 7 z" fill={stroke} />
            </marker>
          );
        })}
      </defs>
      <g
        ref={contentRef}
        transform={
          viewportTransform
            ? `translate(${viewportTransform.x} ${viewportTransform.y}) scale(${viewportTransform.scale})`
            : undefined
        }
      >
        {/* Dot-grid substrate — lives INSIDE the transform group so it
            pans/zooms with the graph and the motion reads as space. */}
        <rect x={0} y={0} width={width} height={height} fill="url(#causal-dot-grid)" pointerEvents="none" />
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
          const d = `M ${from.x} ${from.y} Q ${mx + bend} ${my - bend} ${to.x} ${to.y}`;
          const flow = !reducedMotion && e.status === "active" && tone.dashed;
          // Focus dimming: when an anchor is active, only edges touching
          // it keep their authored opacity — the rest recede to 0.12×.
          const focused = !focusIds || (focusIds.has(e.from_node_id) && focusIds.has(e.to_node_id));
          const opacity = focused ? tone.opacity : tone.opacity * 0.12;
          const marker = focused ? markerIdFor(e.type, tone.stroke) : undefined;
          if (reducedMotion) {
            return (
              <path
                key={e.id}
                d={d}
                fill="none"
                stroke={tone.stroke}
                strokeWidth={1.5}
                strokeDasharray={tone.dashed ? "4 3" : undefined}
                markerEnd={marker ? `url(#${marker})` : undefined}
                opacity={opacity}
              />
            );
          }
          // Draw-in via pathLength only on mount (keys are stable
          // across poll refetches, so the animation never replays).
          // Dashed tones keep their authored dasharray — pathLength
          // owns stroke-dasharray while animating, so dashed edges
          // fade in instead.
          return (
            <motion.path
              key={e.id}
              d={d}
              fill="none"
              stroke={tone.stroke}
              strokeWidth={1.5}
              strokeDasharray={tone.dashed ? "4 3" : undefined}
              markerEnd={marker ? `url(#${marker})` : undefined}
              className={flow ? "causal-edge-flow" : undefined}
              initial={tone.dashed ? { opacity: 0 } : { opacity: 0, pathLength: 0 }}
              animate={tone.dashed ? { opacity } : { opacity, pathLength: 1 }}
              transition={{ duration: 0.4, ease: "easeOut" }}
            />
          );
        })}
        {laid.map((n) => {
          const color = CAUSAL_NODE_TYPE_COLORS[n.type] ?? "#94a3b8";
          const pos = posById.get(n.id) ?? n;
          const selected = selectedNodeId === n.id;
          const hovered = hoveredId === n.id;
          const labelsVisible = labelIds === null || labelIds.has(n.id);
          // Hub radius: degree drives size (see nodeRadius) so the
          // graph's connective tissue is findable without labels.
          const r = nodeRadius(degree.get(n.id) ?? 0);
          // Focus dimming mirrors the edges: non-neighbours recede.
          const focused = !focusIds || focusIds.has(n.id);
          const dim = focused ? 1 : 0.15;
          // Side-anchor ring labels so neighbouring rings stop stacking
          // text on text; centre (ring 0) nodes keep the below-node
          // middle label.
          const dx = pos.x - width / 2;
          const anchor =
            n.ring === 0 || Math.abs(dx) < 1 ? "middle" : dx > 0 ? "start" : "end";
          const entryDelay = Math.min(n.ring * 0.02, 0.3);
          return (
            <g
              key={n.id}
              transform={`translate(${pos.x}, ${pos.y})`}
              data-causal-node-id={n.id}
              className={onSelectNode ? "cursor-pointer" : undefined}
              opacity={dim}
              onMouseEnter={() => setHoveredId(n.id)}
              onMouseLeave={() => setHoveredId((cur) => (cur === n.id ? null : cur))}
            >
              <motion.g
                initial={reducedMotion ? false : { opacity: 0, scale: 0.5 }}
                animate={reducedMotion ? undefined : { opacity: 1, scale: 1 }}
                transition={{ delay: entryDelay, duration: 0.25, ease: "easeOut" }}
                style={{ transformOrigin: "center", transformBox: "fill-box" }}
              >
                {selected ? (
                  reducedMotion ? (
                    <circle r={r + 5} fill="none" stroke="#0f172a" strokeWidth={1.5} opacity={0.35} />
                  ) : (
                    <motion.circle
                      r={r + 5}
                      fill="none"
                      stroke="#0f172a"
                      strokeWidth={1.5}
                      initial={{ opacity: 0.5, scale: 1 }}
                      animate={{ opacity: [0.5, 0.05, 0.5], scale: [1, 1.25, 1] }}
                      transition={{ duration: 1.8, repeat: Infinity, ease: "easeInOut" }}
                      style={{ transformOrigin: "center", transformBox: "fill-box" }}
                    />
                  )
                ) : null}
                {/* Hover lift lives on a plain inner <g>: the outer g
                    owns the layout translate attribute, and a CSS
                    transform on this one cannot clobber it. */}
                <g
                  style={
                    reducedMotion
                      ? undefined
                      : {
                          transition: "transform 0.15s ease, filter 0.15s ease",
                          transform: hovered ? "translateY(-2px)" : "translateY(0)",
                          filter: hovered ? "drop-shadow(0 2px 2px rgba(0,0,0,0.35))" : undefined,
                        }
                  }
                >
                  {/* Type-coloured halo — the glow that makes the node
                      read as a light source rather than a dot. */}
                  <circle r={r + 4.5} fill={color} opacity={hovered || selected ? 0.28 : 0.14} />
                  <circle
                    r={selected ? r + 2 : r}
                    fill={color}
                    opacity={0.95}
                    stroke={selected ? "#0f172a" : "#ffffff"}
                    strokeWidth={selected ? 2 : 1.5}
                  />
                </g>
                <title>{`${n.type}: ${n.label}`}</title>
                {labelsVisible ? (
                  <text
                    x={anchor === "start" ? r + 3 : anchor === "end" ? -(r + 3) : 0}
                    y={anchor === "middle" ? r + 13 : 3}
                    textAnchor={anchor}
                    fontSize={9.5}
                    fontWeight={hovered || selected ? 500 : 400}
                    fill="currentColor"
                    className="fill-muted-foreground"
                    pointerEvents="none"
                  >
                    {truncateLabel(n.label, labelBudget)}
                  </text>
                ) : null}
              </motion.g>
            </g>
          );
        })}
      </g>
    </svg>
  );
}
