/**
 * @vitest-environment jsdom
 */
// causal-minimap path overlay tests (0.5.131) — pin the semantica-
// ported trace rendering: the overlay draws the ordered chain, non-
// path nodes/edges recede (focus-dim keyed on the path's node set),
// and the endpoint rings render. The minimap's other behaviour is
// pinned by the pure-function suite in causal-minimap.test.tsx.

import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";
import type { CausalEdge, CausalNode } from "@multica/core/types/api";
import { CausalMinimap, type CausalPathHighlight } from "./causal-minimap";

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

function edge(id: string, from: string, to: string): CausalEdge {
  return {
    id,
    workspace_id: "ws-1",
    from_node_id: from,
    to_node_id: to,
    type: "causes",
    weight: null,
    confidence: null,
    metadata: {},
    provenance: {},
    proposed_by: null,
    status: "active",
    created_at: "",
    created_by: null,
  };
}

// a → b → c chain plus an unrelated d — e island.
const nodes = [node("a"), node("b"), node("c"), node("d"), node("e")];
const edges = [
  edge("e1", "a", "b"),
  edge("e2", "b", "c"),
  edge("e3", "d", "e"),
];

const highlight: CausalPathHighlight = {
  key: "a->c",
  orderedEdgeIds: ["e1", "e2"],
  nodeIds: new Set(["a", "b", "c"]),
  fromNodeId: "a",
  toNodeId: "c",
};

describe("CausalMinimap path overlay", () => {
  it("renders the ordered chain as an overlay group", () => {
    const { container } = render(
      <CausalMinimap nodes={nodes} edges={edges} width={420} height={300} pathHighlight={highlight} />,
    );
    const overlay = container.querySelector('[data-testid="causal-path-overlay"]');
    expect(overlay).not.toBeNull();
    // one overlay path per ordered path edge (e1, e2) — the island
    // edge e3 never joins the chain.
    expect(overlay!.querySelectorAll("path")).toHaveLength(2);
  });

  it("dims nodes off the path and keeps the chain at full opacity", () => {
    const { container } = render(
      <CausalMinimap nodes={nodes} edges={edges} width={420} height={300} pathHighlight={highlight} />,
    );
    const opacityById = new Map<string, string | null>();
    for (const g of container.querySelectorAll("[data-causal-node-id]")) {
      opacityById.set(g.getAttribute("data-causal-node-id")!, g.getAttribute("opacity"));
    }
    expect(opacityById.get("a")).toBe("1");
    expect(opacityById.get("b")).toBe("1");
    expect(opacityById.get("c")).toBe("1");
    // The off-path island recedes to the focus-dim floor (0.15).
    expect(opacityById.get("d")).toBe("0.15");
    expect(opacityById.get("e")).toBe("0.15");
  });

  it("renders no overlay without a path", () => {
    const { container } = render(
      <CausalMinimap nodes={nodes} edges={edges} width={420} height={300} />,
    );
    expect(container.querySelector('[data-testid="causal-path-overlay"]')).toBeNull();
    // No path → no dimming either.
    const d = container.querySelector('[data-causal-node-id="d"]');
    expect(d?.getAttribute("opacity") ?? "1").toBe("1");
  });
});
