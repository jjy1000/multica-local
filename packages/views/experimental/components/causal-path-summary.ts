// causal-path-summary — pure analytics for the causal path trace
// (0.5.131, ported from semantica's GraphInspectorPanel path-
// intelligence card: distance bands + confidence decay + bottleneck).
//
// Pure on purpose: the view renders it, the tests pin the band edges
// and the weakest-link math. No transport, no i18n — the caller maps
// the returned enums onto locale keys.

import type { CausalEdge, CausalNode } from "@multica/core/types/api";

export type CausalPathBand = "direct" | "near" | "mid" | "distant";
export type CausalPathVerdict = "strong" | "ok" | "weak";

export interface CausalPathSummary {
  hops: number;
  band: CausalPathBand;
  /** Weakest link: the lowest non-null edge confidence on the chain. */
  minConfidence: number | null;
  avgConfidence: number | null;
  /** Highest-degree node ON the path (the connector everything routes
   *  through — where the chain is most fragile to re-arrangement). */
  bottleneckNodeId: string | null;
  bottleneckDegree: number;
  /** Verdict from the weakest link: >0.6 strong, >0.3 ok, else weak.
   *  null confidence (unmeasured edges) → null verdict. */
  verdict: CausalPathVerdict | null;
}

/** Distance bands, mirroring semantica's interpret_causal_distance. */
export function causalPathBand(hops: number): CausalPathBand {
  if (hops <= 1) return "direct";
  if (hops <= 3) return "near";
  if (hops <= 5) return "mid";
  return "distant";
}

export function summarizeCausalPath(
  path: { nodes: CausalNode[]; edges: CausalEdge[] },
  allEdges: CausalEdge[],
): CausalPathSummary {
  const hops = path.edges.length;
  const confidences = path.edges
    .map((e) => e.confidence)
    .filter((c): c is number => typeof c === "number" && Number.isFinite(c));
  const minConfidence = confidences.length > 0 ? Math.min(...confidences) : null;
  const avgConfidence =
    confidences.length > 0
      ? confidences.reduce((a, b) => a + b, 0) / confidences.length
      : null;

  // Bottleneck: degree measured against the WHOLE graph (a node can be
  // quiet on the path yet carry the workspace), tie-break by first on
  // path for determinism.
  const degree = new Map<string, number>();
  for (const e of allEdges) {
    degree.set(e.from_node_id, (degree.get(e.from_node_id) ?? 0) + 1);
    degree.set(e.to_node_id, (degree.get(e.to_node_id) ?? 0) + 1);
  }
  let bottleneckNodeId: string | null = null;
  let bottleneckDegree = 0;
  for (const n of path.nodes) {
    const d = degree.get(n.id) ?? 0;
    if (d > bottleneckDegree) {
      bottleneckDegree = d;
      bottleneckNodeId = n.id;
    }
  }

  const verdict: CausalPathVerdict | null =
    minConfidence == null ? null : minConfidence > 0.6 ? "strong" : minConfidence > 0.3 ? "ok" : "weak";

  return {
    hops,
    band: causalPathBand(hops),
    minConfidence,
    avgConfidence,
    bottleneckNodeId,
    bottleneckDegree,
    verdict,
  };
}
