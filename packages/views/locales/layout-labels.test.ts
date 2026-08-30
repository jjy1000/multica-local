// @vitest-environment node
import { describe, expect, it } from "vitest";

import enLayout from "./en/layout.json";
import zhHansLayout from "./zh-Hans/layout.json";
import jaLayout from "./ja/layout.json";
import koLayout from "./ko/layout.json";

// 0.5.90 OpenMythos rebrand regression pin. History:
//   - 0.5.74 PR 7: the sidebar label gained a "v1" suffix to visually
//     distinguish mythos_swarm from the newer swarm_topology lab.
//   - 0.5.86: swarm_topology FROZE (mythos_swarm became the single
//     蜂群 lab) — the v1/v2 pairing lost its meaning.
//   - 0.5.90: the lab is branded OpenMythos (per-user decision,
//     consistent with the upstream reference project name and the
//     0.3.22 "OpenMythos Boost Badge" lab-badge label). The v1 suffix
//     is retired in all 4 locales; swarm_topology keeps its plain
//     frozen label.
//
// Both keys live in the `layout.sidebar` namespace (sidebar tooltip
// labels); the catalog.go Title is rebranded in the same cycle — the
// Labs tab card title flows from the catalog, the sidebar label from
// these files. The flag KEY stays `mythos_swarm` (VERBATIM law).
//
// The per-locale expected labels differ only in the localized suffix;
// every one of them must START with the "OpenMythos" brand token.

type LayoutFile = { sidebar: Record<string, unknown> };

const EXPECTED: Record<string, string> = {
  en: "OpenMythos",
  "zh-Hans": "OpenMythos 外循环",
  ja: "OpenMythos 外部ループ",
  ko: "OpenMythos 외부 루프",
};

describe("layout labels — OpenMythos brand (0.5.90)", () => {
  const locales = [
    { name: "en", layout: enLayout as unknown as LayoutFile },
    { name: "zh-Hans", layout: zhHansLayout as unknown as LayoutFile },
    { name: "ja", layout: jaLayout as unknown as LayoutFile },
    { name: "ko", layout: koLayout as unknown as LayoutFile },
  ] as const;

  for (const { name, layout } of locales) {
    it(`${name}: experimental_mythos carries the OpenMythos brand`, () => {
      const label = layout.sidebar.experimental_mythos;
      expect(label, `missing sidebar.experimental_mythos in ${name}`).toBeDefined();
      expect(label, `${name}: unexpected mythos label`).toBe(EXPECTED[name]);
      expect(label, `${name}: v1 suffix must stay retired`).not.toMatch(/v1$/);
    });

    it(`${name}: experimental_swarm_topology does NOT carry the brand or "v1"`, () => {
      const label = layout.sidebar.experimental_swarm_topology;
      expect(label, `missing sidebar.experimental_swarm_topology in ${name}`).toBeDefined();
      expect(typeof label, `${name}: experimental_swarm_topology must be a string`).toBe("string");
      expect(label, `${name}: frozen lab must not borrow the OpenMythos brand`).not.toContain("OpenMythos");
      expect(label, `${name}: swarm_topology must NOT carry v1 suffix`).not.toContain("v1");
    });
  }
});
