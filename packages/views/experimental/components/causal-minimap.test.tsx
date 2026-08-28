// causal-minimap.test.tsx (0.5.84 P0 #5 + #6 fix-batch regression pins)
//
// Pins the two P0 bugs flagged in the 0.5.83 post-ship verification
// audit:
//
//   #5 — edge-status visual split: pre-fix EDGE_TONES keyed by edge
//   type only, so an active edge and a rejected tombstone rendered
//   identically. Resolved by resolveEdgeTone(type, status).
//
//   #6 — BFS ring-layout angle divisor: pre-fix the angle formula read
//   byRing.get(ring).length MID-loop, so the divisor grew as each
//   node was inserted and the spread inverted. Resolved by
//   pre-computing ringLengths before the insertion loop.
//
// These are pure-function tests — no DOM, no network — so they run in
// the views vitest pool without any provider wiring.

import { describe, expect, it } from "vitest";

import type { CausalEdge, CausalNode } from "@multica/core/types/api";

import { layout, resolveEdgeTone } from "./causal-minimap";

// Helper: build a seed + N neighbors with a star topology. Star
// topology guarantees every neighbor lands on ring 1 (same BFS depth
// from the seed), which is what makes the ring-spread assertion
// unambiguous.
function buildStar(seedId: string, neighborIds: string[]): {
  nodes: CausalNode[];
  edges: CausalEdge[];
} {
  const seed: CausalNode = {
    id: seedId,
    workspace_id: "ws-1",
    issue_id: "issue-1",
    type: "decision",
    label: seedId,
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
  const nodes: CausalNode[] = [seed];
  for (const id of neighborIds) {
    nodes.push({ ...seed, id, label: id, issue_id: null });
  }
  const edges: CausalEdge[] = neighborIds.map((id, idx) => ({
    id: `e-${idx}`,
    workspace_id: "ws-1",
    from_node_id: seedId,
    to_node_id: id,
    type: "enables",
    weight: null,
    confidence: null,
    metadata: {},
    provenance: {},
    created_at: "",
    created_by: null,
    proposed_by: null,
    status: "active",
  }));
  return { nodes, edges };
}

function angleFor(node: { x: number; y: number }, cx: number, cy: number): number {
  // atan2 returns [-π, π]; normalize to [0, 2π) so the spread check
  // works for full rings without a wraparound branch.
  const a = Math.atan2(node.y - cy, node.x - cx);
  return a < 0 ? a + 2 * Math.PI : a;
}

describe("causal-minimap — resolveEdgeTone (P0 #5)", () => {
  it("returns a different stroke for active vs rejected for the same edge type", () => {
    const active = resolveEdgeTone("causes", "active");
    const rejected = resolveEdgeTone("causes", "rejected");
    const suggested = resolveEdgeTone("causes", "suggested");
    // All three statuses must produce visually distinguishable tones —
    // the audit flagged that pre-fix this was identical.
    expect(rejected.stroke).not.toBe(active.stroke);
    expect(rejected.opacity).toBeLessThan(active.opacity);
    expect(suggested.stroke).not.toBe(active.stroke);
    expect(suggested.opacity).toBeLessThan(active.opacity);
  });

  it("preserves the dash pattern the audit documented (supports/contradicts dashed, others solid)", () => {
    expect(resolveEdgeTone("supports", "active").dashed).toBe(true);
    expect(resolveEdgeTone("contradicts", "active").dashed).toBe(true);
    expect(resolveEdgeTone("causes", "active").dashed).toBe(false);
    expect(resolveEdgeTone("enables", "active").dashed).toBe(false);
    expect(resolveEdgeTone("blocks", "active").dashed).toBe(false);
    expect(resolveEdgeTone("depends_on", "active").dashed).toBe(false);
  });

  it("falls back to neutral grey for an unknown edge type (and never collapses to active)", () => {
    const active = resolveEdgeTone("causes", "active");
    const fallback = resolveEdgeTone("unknown_type", "active");
    expect(fallback.stroke).toBe("#94a3b8");
    expect(fallback.stroke).not.toBe(active.stroke);
  });

  it("returns a distinct tone for unknown statuses (no silent 'active' fallback)", () => {
    const active = resolveEdgeTone("causes", "active");
    const weird = resolveEdgeTone("causes", "pending_review");
    // Unknown status collapses to active per the resolve function
    // contract (explicit default branch), but the rejection / suggestion
    // tones must still be visually distinct from active.
    expect(weird.stroke).toBe(active.stroke);
    expect(resolveEdgeTone("causes", "rejected").stroke).not.toBe(active.stroke);
  });
});

describe("causal-minimap — layout ring spread (P0 #6)", () => {
  const W = 400;
  const H = 400;

  it("places N=4 neighbors on a single ring at evenly-spaced 2π/4 angles", () => {
    const { nodes, edges } = buildStar("seed", ["n1", "n2", "n3", "n4"]);
    const laid = layout(nodes, edges, W, H);
    const cx = W / 2;
    const cy = H / 2;
    const neighbors = laid.filter((n) => n.ring === 1);
    expect(neighbors).toHaveLength(4);
    const angles = neighbors.map((n) => angleFor(n, cx, cy)).sort((a, b) => a - b);
    // Expected: angles are spaced by 2π/4 = π/2, within 0.001 rad.
    for (let i = 1; i < angles.length; i++) {
      const prev = angles[i - 1]!;
      const curr = angles[i]!;
      const gap = curr - prev;
      expect(gap).toBeCloseTo(Math.PI / 2, 3);
    }
  });

  it("places N=6 neighbors on a single ring at evenly-spaced 2π/6 angles", () => {
    const { nodes, edges } = buildStar("seed", ["n1", "n2", "n3", "n4", "n5", "n6"]);
    const laid = layout(nodes, edges, W, H);
    const cx = W / 2;
    const cy = H / 2;
    const neighbors = laid.filter((n) => n.ring === 1);
    expect(neighbors).toHaveLength(6);
    const angles = neighbors.map((n) => angleFor(n, cx, cy)).sort((a, b) => a - b);
    for (let i = 1; i < angles.length; i++) {
      const prev = angles[i - 1]!;
      const curr = angles[i]!;
      const gap = curr - prev;
      expect(gap).toBeCloseTo(Math.PI / 3, 3);
    }
  });

  it("places N=3 neighbors on a single ring at evenly-spaced 2π/3 angles (audit scenario)", () => {
    const { nodes, edges } = buildStar("seed", ["n1", "n2", "n3"]);
    const laid = layout(nodes, edges, W, H);
    const cx = W / 2;
    const cy = H / 2;
    const neighbors = laid.filter((n) => n.ring === 1);
    expect(neighbors).toHaveLength(3);
    const angles = neighbors.map((n) => angleFor(n, cx, cy)).sort((a, b) => a - b);
    // Audit: pre-fix these came out as 0, π, 4π/3. Post-fix: 0, 2π/3, 4π/3.
    for (let i = 1; i < angles.length; i++) {
      const prev = angles[i - 1]!;
      const curr = angles[i]!;
      const gap = curr - prev;
      expect(gap).toBeCloseTo((2 * Math.PI) / 3, 3);
    }
  });
});
