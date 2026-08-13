import { describe, expect, it } from "vitest";
import { pluginInjectedSkillNames } from "./user-plugins-section";

// F-008: the global skill-injection ack gate keys off a plugin manifest's
// capabilities.skills. The helper must return the declared skill names only
// when the manifest actually carries them — a plugin with no skills block (or
// no manifest at all) is NOT a global-injection lab and needs no ack.
describe("pluginInjectedSkillNames — F-008 ack gate", () => {
  it("returns declared skills for a tool-lab manifest", () => {
    expect(
      pluginInjectedSkillNames({
        capabilities: { skills: ["kb-search", "kb-graph"], agents: [], leader: "" },
      }),
    ).toEqual(["kb-search", "kb-graph"]);
  });

  it("returns [] when the manifest declares no skills", () => {
    expect(
      pluginInjectedSkillNames({
        capabilities: { agents: ["my-agent"], leader: "my-agent" },
      }),
    ).toEqual([]);
    expect(pluginInjectedSkillNames({ runtime: { kind: "inline" } })).toEqual([]);
  });

  it("returns [] for undefined / non-object manifests", () => {
    expect(pluginInjectedSkillNames(undefined)).toEqual([]);
    expect(pluginInjectedSkillNames(null as unknown as Record<string, unknown>)).toEqual([]);
  });

  it("filters out non-string entries defensively", () => {
    expect(
      pluginInjectedSkillNames({
        capabilities: { skills: ["real", 42, "", null] },
      }),
    ).toEqual(["real"]);
  });
});
