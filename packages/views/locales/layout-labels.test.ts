// @vitest-environment node
import { describe, expect, it } from "vitest";

import enLayout from "./en/layout.json";
import zhHansLayout from "./zh-Hans/layout.json";
import jaLayout from "./ja/layout.json";
import koLayout from "./ko/layout.json";

// 0.5.74 PR 7: regression pin — mythos_swarm's user-visible sidebar label
// was renamed to a "v1" suffix in all 4 locales to visually distinguish
// it from the newer swarm_topology lab. swarm_topology itself must NOT
// carry the "v1" suffix (it's the v2, not the v1).
//
// Both keys live in the `layout.sidebar` namespace (sidebar tooltip labels);
// the catalog.go Title string ("Mythos Swarm Topology") is unchanged
// per this PR's scope — it's the Labs tab card title, separately
// maintained.

type LayoutFile = { sidebar: Record<string, unknown> };

describe("layout labels — mythos_swarm v1 suffix (0.5.74 PR 7)", () => {
  const locales = [
    { name: "en", layout: enLayout as unknown as LayoutFile },
    { name: "zh-Hans", layout: zhHansLayout as unknown as LayoutFile },
    { name: "ja", layout: jaLayout as unknown as LayoutFile },
    { name: "ko", layout: koLayout as unknown as LayoutFile },
  ] as const;

  for (const { name, layout } of locales) {
    it(`${name}: experimental_mythos ends with "v1"`, () => {
      const label = layout.sidebar.experimental_mythos;
      expect(label, `missing sidebar.experimental_mythos in ${name}`).toBeDefined();
      expect(typeof label, `${name}: experimental_mythos must be a string`).toBe("string");
      expect(label, `${name}: expected v1 suffix on mythos_swarm label`).toMatch(/v1$/);
    });

    it(`${name}: experimental_swarm_topology does NOT contain "v1"`, () => {
      const label = layout.sidebar.experimental_swarm_topology;
      expect(label, `missing sidebar.experimental_swarm_topology in ${name}`).toBeDefined();
      expect(typeof label, `${name}: experimental_swarm_topology must be a string`).toBe("string");
      expect(label, `${name}: swarm_topology must NOT carry v1 suffix (it's the v2)`).not.toContain("v1");
    });
  }
});
