import { describe, expect, it } from "vitest";
import type { ExperimentalFlag } from "@multica/core/types/experimental";
import { isAssigneeLabLocked } from "./assignee-lab-lock";

function flag(key: string, interactionModel?: "assignee" | "auxiliary"): ExperimentalFlag {
  return {
    key,
    enabled: true,
    default_enabled: false,
    title: { en: key, zh: key },
    description: { en: "", zh: "" },
    ...(interactionModel ? { interaction_model: interactionModel } : {}),
  };
}

const flags: ExperimentalFlag[] = [
  flag("pythia_oracle", "assignee"),
  flag("timesfm", "assignee"),
  flag("claude_science_lab", "assignee"),
  flag("mythos_swarm", "assignee"),
  flag("semantica", "assignee"),
  flag("swarm_topology", "assignee"),
  flag("causal_graph", "auxiliary"),
  flag("llm_wiki_bridge", "auxiliary"),
  flag("code_canvas"), // legacy, unclassified
];

describe("isAssigneeLabLocked (0.5.86 assignee-lock)", () => {
  it("locks assignee-model labs", () => {
    for (const key of [
      "pythia_oracle",
      "timesfm",
      "claude_science_lab",
      "mythos_swarm",
      "semantica",
      "swarm_topology",
    ]) {
      expect(isAssigneeLabLocked(flags, key), key).toBe(true);
    }
  });

  it("never locks auxiliary labs", () => {
    expect(isAssigneeLabLocked(flags, "causal_graph")).toBe(false);
    expect(isAssigneeLabLocked(flags, "llm_wiki_bridge")).toBe(false);
  });

  it("keeps legacy unclassified labs unlocked", () => {
    expect(isAssigneeLabLocked(flags, "code_canvas")).toBe(false);
  });

  it("enhancer mode unlocks even assignee-model labs", () => {
    expect(isAssigneeLabLocked(flags, "mythos_swarm", "enhancer")).toBe(false);
    expect(isAssigneeLabLocked(flags, "pythia_oracle", "enhancer")).toBe(false);
  });

  it("no lab / unknown flag → unlocked", () => {
    expect(isAssigneeLabLocked(flags, null)).toBe(false);
    expect(isAssigneeLabLocked(flags, undefined)).toBe(false);
    expect(isAssigneeLabLocked(flags, "user_not_a_flag")).toBe(false);
    expect(isAssigneeLabLocked(undefined, "pythia_oracle")).toBe(false);
  });

  it("legacy payload without interaction_model keeps the 0.3.33 mutex pair", () => {
    const legacyFlags = [flag("mythos_swarm"), flag("swarm_topology"), flag("pythia_oracle")];
    expect(isAssigneeLabLocked(legacyFlags, "mythos_swarm")).toBe(true);
    expect(isAssigneeLabLocked(legacyFlags, "swarm_topology")).toBe(true);
    // Pre-0.5.86 servers did not lock pythia — absence of the field
    // must NOT invent a lock for keys outside the legacy pair.
    expect(isAssigneeLabLocked(legacyFlags, "pythia_oracle")).toBe(false);
  });
});
