/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { CausalEdge, CausalNode } from "@multica/core/types/api";
import enCausal from "../../locales/en/causal-graph.json";
import { CausalConstellationCanvas } from "./causal-constellation-canvas";

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (sel: (d: unknown) => unknown, opts?: Record<string, unknown>) => {
      const v = sel(enCausal);
      if (typeof v !== "string") return undefined;
      return opts
        ? v.replace(/\{\{(\w+)\}\}/g, (_, k) => String(opts[k] ?? `{{${k}}}`))
        : v;
    },
  }),
}));

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

const NODES = [node("focus", "可复现性"), node("up1", "数据未开源"), node("down1", "可信度受疑"), node("down2", "评审收紧")];
const EDGES = [
  edge("up1", "focus", { confidence: 0.82 }),
  edge("focus", "down1", { confidence: 0.78 }),
  edge("down1", "down2", { confidence: 0.58 }),
  edge("focus", "down2", { status: "suggested", confidence: 0.44, type: "enables" }),
];

describe("CausalConstellationCanvas — 0.5.132", () => {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
    vi.useFakeTimers();
  });
  afterEach(() => vi.useRealTimers());

  it("renders upstream/downstream stars and status-styled edges", () => {
    render(<CausalConstellationCanvas nodes={NODES} edges={EDGES} focusId="focus" />);
    const stars = screen.getAllByTestId("causal-constellation-star");
    expect(stars.filter((s) => s.getAttribute("data-side") === "up")).toHaveLength(1);
    expect(stars.filter((s) => s.getAttribute("data-side") === "down")).toHaveLength(2);
    const suggested = screen
      .getAllByTestId("causal-constellation-edge")
      .find((e) => e.getAttribute("data-status") === "suggested");
    expect(suggested).toBeTruthy();
    expect(suggested!.querySelector("text")!.textContent).toBe("?");
  });

  it("shows the pending count chip and trust-ladder chips from real statuses", () => {
    render(<CausalConstellationCanvas nodes={NODES} edges={EDGES} focusId="focus" />);
    expect(screen.getByTestId("causal-constellation-suggested").textContent).toContain("1");
    const tiers = screen.getByTestId("causal-constellation-tiers");
    expect(tiers.textContent).toContain("confirmed ×3");
    expect(tiers.textContent).toContain("suggested ×1");
  });

  it("impact cone lights layer stars hop by hop", async () => {
    vi.useRealTimers();
    render(<CausalConstellationCanvas nodes={NODES} edges={EDGES} focusId="focus" />);
    expect(document.querySelector(".const-wave")).toBeNull();
    fireEvent.click(screen.getByTestId("causal-constellation-wave-btn"));
    // hop 1 (up1+down1+down2 via focus edges... suggested excluded → up1,down1)
    await waitFor(() => {
      expect(document.querySelectorAll(".const-wave").length).toBeGreaterThan(0);
    }, { timeout: 2000 });
  });

  it("collapses via the header toggle", () => {
    render(<CausalConstellationCanvas nodes={NODES} edges={EDGES} focusId="focus" />);
    fireEvent.click(screen.getByTestId("causal-constellation-toggle"));
    expect(screen.queryByTestId("causal-constellation-stage")).toBeNull();
  });
});
