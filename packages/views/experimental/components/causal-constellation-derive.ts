// causal-constellation-derive — pure derivation for the causal
// constellation canvas (0.5.132, ported from
// .omc/prototypes/causal-constellation-visual.html after approval).
// Projects the REAL workspace causal graph (nodes + edges from
// useCausalWorkspaceGraph) into a focus-centric star map: upstream
// causes on the left arc, downstream effects on the right, active edges
// carrying confidence-paced flow, and a BFS "impact cone" layered by
// hops over active edges only.
//
// Trust-ladder counts derive from the wire edge's status + provenance
// ({source: manual|curator|…}) + proposed_by (LLM proposals): Tier D is
// exactly the suggested set (server law: D lands suggested ≤0.5);
// A/B/C provenance granularity is grouped into "confirmed" unless the
// provenance names a source.

import type { CausalEdge, CausalNode } from "@multica/core/types/api";

export type ConstellationSide = "up" | "down";

export interface ConstellationStar {
  node: CausalNode;
  side: ConstellationSide;
  /** In/out degree within the full edge list (sizing + bottleneck). */
  degree: number;
  /** Mean confidence over this node's active edges (0 when none). */
  confidence: number;
}

export interface ConstellationEdge {
  edge: CausalEdge;
  fromId: string;
  toId: string;
}

export interface ImpactLayer {
  /** Node ids reached at this hop (1 = direct neighbours of focus). */
  hop: number;
  nodeIds: string[];
}

export interface TrustTierCount {
  tier: "confirmed" | "suggested" | "rejected";
  count: number;
}

export interface ConstellationProjection {
  focus: CausalNode | null;
  stars: ConstellationStar[];
  edges: ConstellationEdge[];
  /** BFS layers over ACTIVE edges from the focus (impact cone). */
  layers: ImpactLayer[];
  tiers: TrustTierCount[];
  impactedCount: number;
}

export function constellationNodeDegree(nodes: CausalNode[], edges: CausalEdge[]): Map<string, number> {
  const deg = new Map<string, number>(nodes.map((n) => [n.id, 0]));
  for (const e of edges) {
    deg.set(e.from_node_id, (deg.get(e.from_node_id) ?? 0) + 1);
    deg.set(e.to_node_id, (deg.get(e.to_node_id) ?? 0) + 1);
  }
  return deg;
}

function nodeConfidence(id: string, edges: CausalEdge[]): number {
  const confs = edges
    .filter((e) => e.status === "active" && (e.from_node_id === id || e.to_node_id === id))
    .map((e) => e.confidence)
    .filter((c): c is number => c != null);
  if (confs.length === 0) return 0;
  return confs.reduce((a, b) => a + b, 0) / confs.length;
}

/** Default focus when nothing is selected: highest-degree node. */
export function defaultFocusId(nodes: CausalNode[], edges: CausalEdge[]): string | null {
  const deg = constellationNodeDegree(nodes, edges);
  let best: string | null = null;
  let bestDeg = -1;
  for (const n of nodes) {
    const d = deg.get(n.id) ?? 0;
    if (d > bestDeg) {
      best = n.id;
      bestDeg = d;
    }
  }
  return best;
}

export function projectConstellation(
  nodes: CausalNode[],
  edges: CausalEdge[],
  focusId: string | null,
  maxPerSide = 4,
): ConstellationProjection {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const focus = focusId ? (byId.get(focusId) ?? null) : null;
  if (!focus) {
    return { focus: null, stars: [], edges: [], layers: [], tiers: countTiers(edges), impactedCount: 0 };
  }

  // 1-hop neighbourhood split by direction, capped per side by degree
  const upIds = new Set<string>();
  const downIds = new Set<string>();
  for (const e of edges) {
    if (e.status === "rejected" || e.status === "superseded") continue;
    if (e.to_node_id === focus.id && byId.has(e.from_node_id)) upIds.add(e.from_node_id);
    if (e.from_node_id === focus.id && byId.has(e.to_node_id)) downIds.add(e.to_node_id);
  }
  const deg = constellationNodeDegree(nodes, edges);
  const cap = (ids: Set<string>, side: ConstellationSide): ConstellationStar[] =>
    [...ids]
      .map((id) => ({
        node: byId.get(id)!,
        side,
        degree: deg.get(id) ?? 0,
        confidence: nodeConfidence(id, edges),
      }))
      .sort((a, b) => b.degree - a.degree || a.node.label.localeCompare(b.node.label))
      .slice(0, maxPerSide);
  const stars = [...cap(upIds, "up"), ...cap(downIds, "down")];

  // edges among focus + kept stars (constellation view is a neighbourhood)
  const kept = new Set<string>([focus.id, ...stars.map((s) => s.node.id)]);
  const cEdges: ConstellationEdge[] = edges
    .filter((e) => kept.has(e.from_node_id) && kept.has(e.to_node_id))
    .map((e) => ({ edge: e, fromId: e.from_node_id, toId: e.to_node_id }));

  return {
    focus,
    stars,
    edges: cEdges,
    layers: deriveImpactLayers(edges, focus.id),
    tiers: countTiers(edges),
    impactedCount: 1 + deriveImpactLayers(edges, focus.id).reduce((n, l) => n + l.nodeIds.length, 0),
  };
}

/** BFS over ACTIVE edges from the focus, capped at 2 hops. */
export function deriveImpactLayers(edges: CausalEdge[], focusId: string, maxHops = 2): ImpactLayer[] {
  const adj = new Map<string, string[]>();
  for (const e of edges) {
    if (e.status !== "active") continue;
    // direction-agnostic propagation for the cone visual
    if (!adj.has(e.from_node_id)) adj.set(e.from_node_id, []);
    if (!adj.has(e.to_node_id)) adj.set(e.to_node_id, []);
    adj.get(e.from_node_id)!.push(e.to_node_id);
    adj.get(e.to_node_id)!.push(e.from_node_id);
  }
  const seen = new Set([focusId]);
  const layers: ImpactLayer[] = [];
  let frontier = [focusId];
  for (let hop = 1; hop <= maxHops; hop++) {
    const next: string[] = [];
    for (const id of frontier) {
      for (const nb of adj.get(id) ?? []) {
        if (!seen.has(nb)) {
          seen.add(nb);
          next.push(nb);
        }
      }
    }
    if (next.length === 0) break;
    layers.push({ hop, nodeIds: next });
    frontier = next;
  }
  return layers;
}

/**
 * Trust ladder counts. Tier D ≡ suggested (server law: LLM proposals
 * land suggested at confidence ≤ 0.5 and stay invisible to paths until
 * a human confirms — reject is a tombstone, never a delete).
 */
export function countTiers(edges: CausalEdge[]): TrustTierCount[] {
  let confirmed = 0;
  let suggested = 0;
  let rejected = 0;
  for (const e of edges) {
    if (e.status === "suggested") suggested++;
    else if (e.status === "rejected") rejected++;
    else if (e.status === "active" || e.status === "superseded") confirmed++;
  }
  return [
    { tier: "confirmed", count: confirmed },
    { tier: "suggested", count: suggested },
    { tier: "rejected", count: rejected },
  ];
}
