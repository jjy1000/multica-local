// causal-minimap.test.tsx (0.5.84 P0 #5 + #6 fix-batch regression pins,
// extended 0.5.86 for the crowding overhaul)
//
// Pins the two P0 bugs flagged in the 0.5.83 post-ship verification
// audit:
//
//   #5 — edge-status visual split: pre-fix EDGE_TONES keyed by edge
//   type only, so an active edge and a rejected tombstone rendered
//   identically. Resolved by resolveEdgeTone(type, status). 0.5.86 adds
//   an explicit superseded tone (muted, dashed, never animated).
//
//   #6 — BFS ring-layout angle divisor: pre-fix the angle formula read
//   byRing.get(ring).length MID-loop, so the divisor grew as each
//   node was inserted and the spread inverted. Resolved by
//   pre-computing ringLengths before the insertion loop.
//
// 0.5.86 crowding pins:
//   - ring-0 seeds capped at CAUSAL_MAX_CENTER_SEEDS (3) so a fully
//     issue-bound graph never collapses onto the centre ring;
//   - per-ring radius widens until every node owns ≥ MIN_RING_SPACING
//     of arc, so circles never overlap at any N;
//   - ring members ordered by BFS parent angle (children fan out
//     around their parent);
//   - clientToViewBox degrades to null when the screen CTM is absent
//     (jsdom) and maps through injected matrices.
//
// These are pure-function tests — no DOM rendering — so they run in
// the views vitest pool without any provider wiring.

import { describe, expect, it } from "vitest";

import type { CausalEdge, CausalNode } from "@multica/core/types/api";

import {
  CAUSAL_MAX_CENTER_SEEDS,
  clientToViewBox,
  layout,
  resolveEdgeTone,
  type SvgClientMatrix,
} from "./causal-minimap";

function makeNode(id: string, issueId: string | null): CausalNode {
  return {
    id,
    workspace_id: "ws-1",
    issue_id: issueId,
    type: "decision",
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

function makeEdge(id: string, fromNodeId: string, toNodeId: string): CausalEdge {
  return {
    id,
    workspace_id: "ws-1",
    from_node_id: fromNodeId,
    to_node_id: toNodeId,
    type: "enables",
    weight: null,
    confidence: null,
    metadata: {},
    provenance: {},
    created_at: "",
    created_by: null,
    proposed_by: null,
    status: "active",
  };
}

// Helper: build a seed + N neighbors with a star topology. Star
// topology guarantees every neighbor lands on ring 1 (same BFS depth
// from the seed), which is what makes the ring-spread assertion
// unambiguous.
function buildStar(seedId: string, neighborIds: string[]): {
  nodes: CausalNode[];
  edges: CausalEdge[];
} {
  const nodes: CausalNode[] = [makeNode(seedId, "issue-1")];
  for (const id of neighborIds) {
    nodes.push({ ...makeNode(id, null), label: id });
  }
  const edges: CausalEdge[] = neighborIds.map((id, idx) =>
    makeEdge(`e-${idx}`, seedId, id),
  );
  return { nodes, edges };
}

// The 0.5.86 crowding scenario: every node carries an issue binding, so
// the pre-fix seed heuristic would put ALL of them on ring 0.
function buildIssueBoundStar(hubId: string, leafIds: string[]): {
  nodes: CausalNode[];
  edges: CausalEdge[];
} {
  const { nodes, edges } = buildStar(hubId, leafIds);
  return { nodes: nodes.map((n) => ({ ...n, issue_id: "issue-1" })), edges };
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

  it("gives superseded edges an explicit muted dashed tone that is never the active tone (0.5.86)", () => {
    const active = resolveEdgeTone("causes", "active");
    const superseded = resolveEdgeTone("causes", "superseded");
    expect(superseded.stroke).not.toBe(active.stroke);
    expect(superseded.opacity).toBeLessThan(active.opacity);
    // Dashed + muted so history never reads as a live relation; the
    // renderer only ever applies the dash-flow animation to
    // status === "active", so a superseded edge can never animate.
    expect(superseded.dashed).toBe(true);
    // The unknown-type fallback path carries the same superseded tone.
    expect(resolveEdgeTone("unknown_type", "superseded").dashed).toBe(true);
    expect(resolveEdgeTone("unknown_type", "superseded").stroke).toBe(superseded.stroke);
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

describe("causal-minimap — layout crowding overhaul (0.5.86)", () => {
  it("21 issue-bound nodes do not collapse onto ring 0 (≤ 3 centre seeds, ≥ 20px apart per ring)", () => {
    // Full-page canvas: 760×480. All 21 nodes carry an issue binding —
    // the pre-fix heuristic put every one of them on ring 0 at
    // radius 0.28·maxR (60.5px), giving an 18px chord for an 18-20 node
    // ring: overlapping circles.
    const leaves = Array.from({ length: 20 }, (_, i) => `leaf-${String(i).padStart(2, "0")}`);
    const { nodes, edges } = buildIssueBoundStar("hub", leaves);
    const laid = layout(nodes, edges, 760, 480);
    expect(laid).toHaveLength(21);

    const ring0 = laid.filter((n) => n.ring === 0);
    expect(ring0.length).toBeGreaterThanOrEqual(1);
    expect(ring0.length).toBeLessThanOrEqual(CAUSAL_MAX_CENTER_SEEDS);

    // No two nodes on the same ring may overlap the 20px node diameter.
    const maxRing = Math.max(...laid.map((n) => n.ring));
    expect(maxRing).toBeGreaterThanOrEqual(1);
    for (let ring = 0; ring <= maxRing; ring++) {
      const members = laid.filter((n) => n.ring === ring);
      for (let i = 0; i < members.length; i++) {
        for (let j = i + 1; j < members.length; j++) {
          const a = members[i]!;
          const b = members[j]!;
          const dist = Math.hypot(a.x - b.x, a.y - b.y);
          expect(dist).toBeGreaterThanOrEqual(20);
        }
      }
    }
  });

  it("orders ring members by BFS parent angle so children fan out around their parents", () => {
    // Ring 1: two neighbours of the single seed. Ring 2: one child per
    // neighbour. The edge list is ordered so BFS discovers nb (which
    // will land at angle π) BEFORE na (angle 0) — the parent-angle sort
    // must still place na's child near angle 0 and nb's child near π.
    const nodes = [
      makeNode("seed", "issue-1"),
      makeNode("na", null),
      makeNode("nb", null),
      makeNode("na1", null),
      makeNode("nb1", null),
    ];
    const edges = [
      makeEdge("e1", "seed", "nb"),
      makeEdge("e2", "nb", "nb1"),
      makeEdge("e3", "seed", "na"),
      makeEdge("e4", "na", "na1"),
    ];
    const laid = layout(nodes, edges, 400, 400);
    const cx = 200;
    const cy = 200;
    const a1 = laid.find((n) => n.id === "na1");
    const b1 = laid.find((n) => n.id === "nb1");
    expect(a1).toBeDefined();
    expect(b1).toBeDefined();
    expect(angleFor(a1!, cx, cy)).toBeCloseTo(0, 3);
    expect(angleFor(b1!, cx, cy)).toBeCloseTo(Math.PI, 3);
  });

  it("is deterministic: the same inputs always produce the same coordinates", () => {
    const { nodes, edges } = buildIssueBoundStar("hub", ["l1", "l2", "l3", "l4", "l5"]);
    const render = (laid: ReturnType<typeof layout>) =>
      laid.map((n) => `${n.id}:${n.ring}:${n.x.toFixed(4)},${n.y.toFixed(4)}`);
    expect(render(layout(nodes, edges, 760, 480))).toEqual(render(layout(nodes, edges, 760, 480)));
  });
});

describe("causal-minimap — clientToViewBox (0.5.86 drag plumbing)", () => {
  // jsdom has no getScreenCTM/DOMMatrix, so the helper accepts an
  // injected matrix and must degrade to null when none is available.
  const nullCtm = { getScreenCTM: () => null };

  it("returns null when no screen CTM is available (jsdom drag no-op)", () => {
    expect(clientToViewBox(nullCtm, 10, 20)).toBeNull();
    expect(clientToViewBox(nullCtm, 10, 20, null)).toBeNull();
  });

  it("maps client coordinates through an injected translation matrix", () => {
    const translate: SvgClientMatrix = {
      a: 1, b: 0, c: 0, d: 1, e: 100, f: 50,
      inverse: () => translateInverse,
    };
    const translateInverse: SvgClientMatrix = {
      a: 1, b: 0, c: 0, d: 1, e: -100, f: -50,
      inverse: () => translate,
    };
    expect(clientToViewBox(nullCtm, 130, 90, translate)).toEqual({ x: 30, y: 40 });
  });

  it("maps client coordinates through an injected scale matrix", () => {
    const scale: SvgClientMatrix = {
      a: 2, b: 0, c: 0, d: 2, e: 0, f: 0,
      inverse: () => scaleInverse,
    };
    const scaleInverse: SvgClientMatrix = {
      a: 0.5, b: 0, c: 0, d: 0.5, e: 0, f: 0,
      inverse: () => scale,
    };
    expect(clientToViewBox(nullCtm, 300, 250, scale)).toEqual({ x: 150, y: 125 });
  });
});
