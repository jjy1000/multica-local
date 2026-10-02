/**
 * @vitest-environment node
 */
import { describe, expect, it } from "vitest";
import type { CausalEdge, CausalNode } from "@multica/core/types/api";
import {
  countTiers,
  defaultFocusId,
  deriveImpactLayers,
  projectConstellation,
} from "./causal-constellation-derive";

let seq = 0;
function node(id: string, label = id): CausalNode {
  return {
    id, workspace_id: "ws", issue_id: null, type: "decision", label,
    description: null, metadata: {}, provenance: {}, created_at: "",
    created_by: null, lab_source: null, lab_run_id: null, status: "active",
    last_observed_at: "",
  } as CausalNode;
}
function edge(from: string, to: string, over: Partial<CausalEdge> = {}): CausalEdge {
  return {
    id: `e${seq++}`, workspace_id: "ws", from_node_id: from, to_node_id: to,
    type: "causes", weight: null, confidence: 0.7, metadata: {}, provenance: {},
    created_at: "", created_by: null, proposed_by: null, status: "active",
    ...over,
  } as CausalEdge;
}

describe("projectConstellation — focus-centric projection", () => {
  const nodes = [
    node("focus", "焦点"),
    node("a"), node("b"), node("c"), node("d"), node("e"),
    node("far"), // connected to neither side of focus
  ];
  const edges = [
    edge("a", "focus"), edge("b", "focus"),
    edge("focus", "c"), edge("focus", "d"), edge("focus", "e"),
    edge("far", "a"),
  ];

  it("splits the 1-hop neighbourhood into upstream/downstream by direction", () => {
    const p = projectConstellation(nodes, edges, "focus", 2);
    expect(p.stars.filter((s) => s.side === "up").map((s) => s.node.id).sort()).toEqual(["a", "b"]);
    // c/d/e compete for 2 downstream slots — degree tie → label order
    const down = p.stars.filter((s) => s.side === "down").map((s) => s.node.id);
    expect(down).toHaveLength(2);
    // far is 2 hops away: not a star, but reached by the impact layers
    expect(p.stars.some((s) => s.node.id === "far")).toBe(false);
  });

  it("keeps edges among focus + kept stars; rejected stay in the edge list but ghosted", () => {
    const edges2 = [
      edge("a", "focus"),
      edge("focus", "c"),
      edge("a", "focus", { status: "rejected" }),
    ];
    const p = projectConstellation([node("focus"), node("a"), node("c")], edges2, "focus");
    // rejected edge still excluded from the up set
    expect(p.stars.filter((s) => s.side === "up").map((s) => s.node.id)).toEqual(["a"]);
    // all three edges live in the kept subgraph — the rejected one renders
    // as a ghost (status styling), it is never dropped from the map
    expect(p.edges.length).toBe(3);
    expect(p.edges.filter((x) => x.edge.status === "rejected")).toHaveLength(1);
  });

  it("null focus yields an empty projection with tier counts intact", () => {
    const p = projectConstellation(nodes, edges, null);
    expect(p.focus).toBeNull();
    expect(p.stars).toEqual([]);
    expect(p.tiers.find((x) => x.tier === "confirmed")?.count).toBe(edges.length);
  });
});

describe("deriveImpactLayers — BFS over ACTIVE edges only, ≤2 hops", () => {
  it("propagates direction-agnostically in hop layers and stops at 2", () => {
    const edges = [
      edge("f", "a"), edge("a", "b"), edge("b", "c"), // c is 3 hops — excluded
      edge("f", "z", { status: "suggested" }),          // not active — excluded
    ];
    const layers = deriveImpactLayers(edges, "f");
    expect(layers.map((l) => l.nodeIds)).toEqual([["a"], ["b"]]);
  });

  it("no active edges → no layers", () => {
    expect(deriveImpactLayers([edge("f", "a", { status: "rejected" })], "f")).toEqual([]);
  });
});

describe("countTiers + defaultFocusId", () => {
  it("buckets by status: suggested ≡ Tier D, reject is its own bucket", () => {
    const tiers = countTiers([
      edge("a", "b"),
      edge("a", "b", { status: "suggested" }),
      edge("a", "b", { status: "suggested" }),
      edge("a", "b", { status: "rejected" }),
      edge("a", "b", { status: "superseded" }),
    ]);
    expect(tiers).toEqual([
      { tier: "confirmed", count: 2 },
      { tier: "suggested", count: 2 },
      { tier: "rejected", count: 1 },
    ]);
  });

  it("default focus is the highest-degree node", () => {
    // hub carries degree 4, x/y tie at 2 — iteration-order-proof assertion
    const nodes = [node("x"), node("hub"), node("y"), node("z")];
    const edges = [edge("hub", "x"), edge("hub", "y"), edge("hub", "z"), edge("x", "y")];
    expect(defaultFocusId(nodes, edges)).toBe("hub");
    expect(defaultFocusId([], [])).toBeNull();
  });
});
