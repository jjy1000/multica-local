// causal-path-summary tests (0.5.131) — pin the semantica-ported
// path-intelligence math: distance bands, weakest-link confidence,
// verdict thresholds, and the bottleneck (whole-graph degree) pick.

import { describe, expect, it } from "vitest";
import type { CausalEdge, CausalNode } from "@multica/core/types/api";
import { causalPathBand, summarizeCausalPath } from "./causal-path-summary";

function node(id: string): CausalNode {
  return {
    id,
    workspace_id: "ws-1",
    issue_id: null,
    type: "action",
    label: id,
    description: null,
    metadata: {},
    provenance: {},
    created_at: "",
    created_by: null,
    lab_source: null,
    lab_run_id: null,
    status: "active",
    last_observed_at: "",
  };
}

function edge(id: string, from: string, to: string, confidence: number | null): CausalEdge {
  return {
    id,
    workspace_id: "ws-1",
    from_node_id: from,
    to_node_id: to,
    type: "causes",
    weight: null,
    confidence,
    metadata: {},
    provenance: {},
    proposed_by: null,
    status: "active",
    created_at: "",
    created_by: null,
  };
}

describe("causalPathBand", () => {
  it("maps hop counts onto the four semantica distance bands", () => {
    expect(causalPathBand(0)).toBe("direct");
    expect(causalPathBand(1)).toBe("direct");
    expect(causalPathBand(2)).toBe("near");
    expect(causalPathBand(3)).toBe("near");
    expect(causalPathBand(4)).toBe("mid");
    expect(causalPathBand(5)).toBe("mid");
    expect(causalPathBand(6)).toBe("distant");
    expect(causalPathBand(12)).toBe("distant");
  });
});

describe("summarizeCausalPath", () => {
  it("takes the weakest link as the chain confidence and averages the rest", () => {
    const path = {
      nodes: [node("a"), node("b"), node("c")],
      edges: [edge("e1", "a", "b", 0.8), edge("e2", "b", "c", 0.35)],
    };
    const s = summarizeCausalPath(path, path.edges);
    expect(s.hops).toBe(2);
    expect(s.band).toBe("near");
    expect(s.minConfidence).toBeCloseTo(0.35);
    expect(s.avgConfidence).toBeCloseTo(0.575);
    // >0.3 but ≤0.6 → ok
    expect(s.verdict).toBe("ok");
  });

  it("verdict thresholds: >0.6 strong, >0.3 ok, else weak", () => {
    const mk = (c: number) => ({
      nodes: [node("a"), node("b")],
      edges: [edge("e1", "a", "b", c)],
    });
    expect(summarizeCausalPath(mk(0.61), mk(0.61).edges).verdict).toBe("strong");
    expect(summarizeCausalPath(mk(0.6), mk(0.6).edges).verdict).toBe("ok");
    expect(summarizeCausalPath(mk(0.31), mk(0.31).edges).verdict).toBe("ok");
    expect(summarizeCausalPath(mk(0.3), mk(0.3).edges).verdict).toBe("weak");
  });

  it("unmeasured edges (null confidence) yield a null verdict, not a fake zero", () => {
    const path = {
      nodes: [node("a"), node("b")],
      edges: [edge("e1", "a", "b", null)],
    };
    const s = summarizeCausalPath(path, path.edges);
    expect(s.minConfidence).toBeNull();
    expect(s.avgConfidence).toBeNull();
    expect(s.verdict).toBeNull();
  });

  it("picks the bottleneck by WHOLE-graph degree, not path-local degree", () => {
    // Node b is quiet on the path but carries the workspace: 3 extra
    // off-path edges → degree 4 must win over a's 2.
    const pathEdges = [edge("e1", "a", "b", null), edge("e2", "b", "c", null)];
    const allEdges = [
      ...pathEdges,
      edge("x1", "b", "d", null),
      edge("x2", "b", "e", null),
      edge("x3", "b", "f", null),
    ];
    const s = summarizeCausalPath({ nodes: [node("a"), node("b"), node("c")], edges: pathEdges }, allEdges);
    expect(s.bottleneckNodeId).toBe("b");
    expect(s.bottleneckDegree).toBe(5); // e1,e2 on-path + x1..x3
  });

  it("breaks degree ties by first-on-path for determinism", () => {
    const pathEdges = [edge("e1", "a", "b", null)];
    const s = summarizeCausalPath({ nodes: [node("a"), node("b")], edges: pathEdges }, pathEdges);
    // a and b both degree 1 → the first on-path node wins, stably.
    expect(s.bottleneckNodeId).toBe("a");
  });
});
