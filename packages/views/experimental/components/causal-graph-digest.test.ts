import { describe, expect, it } from "vitest";
import type { CausalEdge, CausalNode } from "@multica/core/types/api";
import { buildGraphDigest } from "./causal-graph-digest";

function node(id: string, type: string, agent?: string): CausalNode {
  return {
    id,
    workspace_id: "ws",
    issue_id: "issue-1",
    type,
    label: `${type}-${id}`,
    description: null,
    metadata: agent ? { agent } : {},
    provenance: {},
    created_at: "2026-09-24T00:00:00Z",
    created_by: null,
    lab_source: null,
    lab_run_id: null,
    status: "active",
    last_observed_at: "2026-09-24T00:00:00Z",
  };
}

function edge(id: string, from: string, to: string, type = "causes", status = "active"): CausalEdge {
  return {
    id,
    workspace_id: "ws",
    from_node_id: from,
    to_node_id: to,
    type,
    weight: null,
    confidence: null,
    metadata: {},
    provenance: {},
    created_at: "2026-09-24T00:00:00Z",
    created_by: null,
    proposed_by: null,
    status,
  };
}

describe("buildGraphDigest — 0.5.121 compression layer", () => {
  it("summarizes counts by node/edge type, agent attribution, and hubs", () => {
    const nodes = [
      node("n1", "constraint"),
      node("n2", "action", "research"),
      node("n3", "action", "research"),
      node("n4", "outcome", "writer"),
      node("n5", "decision"),
    ];
    const edges = [
      edge("e1", "n1", "n2", "enables"),
      edge("e2", "n2", "n4", "causes"),
      edge("e3", "n3", "n4", "causes"),
      edge("e4", "n2", "n5", "supports"),
      edge("e5", "n4", "n5", "supports", "suggested"),
    ];
    const digest = buildGraphDigest(nodes, edges);

    expect(digest).toContain("## Causal Graph Digest");
    expect(digest).toContain("- nodes: 5");
    expect(digest).toContain("constraint 1");
    expect(digest).toContain("- edges: 5");
    expect(digest).toContain("causes 2");
    expect(digest).toContain("1 suggested (human-gated)");
    // agents sorted by run count desc
    expect(digest).toContain("research (2), writer (1)");
    // hub ranking by degree
    expect(digest).toContain("[action] action-n2 (deg 3)");
  });

  it("omits agent/hub lines on an empty or unattributed graph", () => {
    expect(buildGraphDigest([], [])).not.toContain("agents on record");
    expect(buildGraphDigest([], [])).not.toContain("key hubs");
    expect(buildGraphDigest([], [])).toContain("- nodes: 0");

    const noAgent = buildGraphDigest([node("n1", "outcome")], []);
    expect(noAgent).not.toContain("agents on record");
    expect(noAgent).not.toContain("key hubs");
  });
});
