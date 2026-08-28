// issue-labs-section.test.tsx (0.5.84 P0 #1)
//
// Regression pin for the FLAG_ROUTE_SUFFIX map: every flag key with a
// dedicated /experimental/<suffix> view MUST appear here. The 0.5.81
// (semantica) and 0.5.83 (causal_graph) silent-link-death rounds both
// came from this dict silently missing a row — labSourceRouteSuffix()
// returned undefined and PropRow / create-issue redirect / IssueLabsSection
// trail links no-op'd without complaint. This test pins every flag key
// that ships a view today; a new lab without a row is caught here
// instead of in post-ship verification.

import { describe, it, expect } from "vitest";

import { labSourceRouteSuffix } from "./issue-labs-section";

// Expected suffix mapping mirrors routes.tsx (apps/desktop/.../routes.tsx):
//   claude_science_lab → claude-lab
//   pythia_oracle      → pythia
//   mythos_swarm       → mythos
//   llm_wiki_bridge    → llm-wiki
//   code_canvas        → code-canvas
//   swarm_topology     → swarm-topology
//   semantica          → semantica-explorer
//   timesfm            → timesfm-lab
//   causal_graph       → causal-graph  (0.5.83 — the regression pin)
describe("labSourceRouteSuffix — FLAG_ROUTE_SUFFIX rows", () => {
  it("resolves every built-in lab flag to its /experimental/<suffix> view", () => {
    const expected: Array<[string, string]> = [
      ["claude_science_lab", "claude-lab"],
      ["pythia_oracle", "pythia"],
      ["mythos_swarm", "mythos"],
      ["llm_wiki_bridge", "llm-wiki"],
      ["code_canvas", "code-canvas"],
      ["swarm_topology", "swarm-topology"],
      ["semantica", "semantica-explorer"],
      ["timesfm", "timesfm-lab"],
      ["causal_graph", "causal-graph"],
    ];
    for (const [flagKey, suffix] of expected) {
      expect(labSourceRouteSuffix(flagKey), flagKey).toBe(suffix);
    }
  });

  it("returns undefined for unknown / retired flag keys (silent no-op trap)", () => {
    // The audit's whole point: labSourceRouteSuffix('causal_graph') used
    // to return undefined, killing every external-link affordance.
    // Unknown keys SHOULD return undefined — this asserts the negative
    // case so the positive case above cannot drift to also-undefined.
    expect(labSourceRouteSuffix("not_a_real_flag")).toBeUndefined();
    expect(labSourceRouteSuffix("constitution_agent")).toBeUndefined(); // retired 0.3.57
    expect(labSourceRouteSuffix("chat_pin_ui")).toBeUndefined(); // removed 0.3.68
  });

  it("returns undefined for null / empty / whitespace inputs", () => {
    expect(labSourceRouteSuffix(null)).toBeUndefined();
    expect(labSourceRouteSuffix(undefined)).toBeUndefined();
    expect(labSourceRouteSuffix("")).toBeUndefined();
  });
});