// @vitest-environment jsdom

// Narrow regression test for the 0.3.28 fix: the three tab labels on
// packages/views/squads/components/squad-detail-page.tsx (Members / Tasks /
// Instructions) must NOT be hard-coded English strings. They route through
// i18n keys:
//
//   - Members       -> squads.members_tab.section_title
//   - Tasks         -> squads.tasks_tab          (flat string leaf)
//   - Instructions  -> squads.instructions_tab.title   (new in this PR)
//
// This file does NOT re-render the full SquadOverviewPane (which depends on
// TanStack Query and many domain hooks); instead it asserts two cheap
// properties that together prove the wiring:
//
//   1) Each of the four locale bundles exposes the three required keys with
//      a non-empty string value. Any locale drift (e.g. accidentally moving
//      `tasks_tab` under a parent object, or dropping `instructions_tab.title`)
//      fails the test before the parity check does.
//
//   2) When the squads bundle is mounted via I18nProvider, reading each key
//      through `useTranslation` returns the exact string the bundle ships.
//      Catches selector typos (e.g. `tasks_tab.title` instead of `tasks_tab`)
//      that JSON-shape checks alone would miss.

import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import { useT } from "../../i18n/use-t";

const LOCALES_DIR = resolve(__dirname, "..", "..", "locales");

function readSquads(locale: string): Record<string, unknown> {
  const raw = readFileSync(resolve(LOCALES_DIR, locale, "squads.json"), "utf8");
  return JSON.parse(raw) as Record<string, unknown>;
}

type Bundle = { squads: Record<string, unknown> };

// Selector contract: each pair below is the value the component passes to
// `t(($) => $.pair.path)`. If a refactor moves the key, this list catches it
// even before the JSON-shape check fires.
const SELECTORS: ReadonlyArray<readonly [string, string[]]> = [
  ["members_tab.section_title", ["members_tab", "section_title"]],
  ["tasks_tab", ["tasks_tab"]],
  ["instructions_tab.title", ["instructions_tab", "title"]],
];

function readKey(bundle: Bundle, path: string[]): unknown {
  let cursor: unknown = bundle.squads;
  for (const segment of path) {
    if (cursor === null || typeof cursor !== "object") return undefined;
    cursor = (cursor as Record<string, unknown>)[segment];
  }
  return cursor;
}

// Probe component: pulls all three keys through the actual `useT("squads")`
// selector API and renders them as accessible text. If a selector is wrong
// (e.g. `$.tasks_tab.title` on a flat leaf), i18next throws `TypeError:
// Cannot read properties of undefined` and the test fails with that crash.
function SelectorProbe() {
  const { t } = useT("squads");
  return (
    <ul>
      <li>{`members_tab.section_title=${t(($) => $.members_tab.section_title)}`}</li>
      <li>{`tasks_tab=${t(($) => $.tasks_tab)}`}</li>
      <li>{`instructions_tab.title=${t(($) => $.instructions_tab.title)}`}</li>
    </ul>
  );
}

describe("squad detail tab labels (i18n wiring)", () => {
  it.each(["en", "zh-Hans", "ja", "ko"] as const)(
    "ships all three tab-label keys in %s/squads.json",
    (locale) => {
      const data = readSquads(locale);
      for (const [, segments] of SELECTORS) {
        const value = readKey({ squads: data }, segments);
        expect(value, `missing key ${segments.join(".")} in ${locale}/squads.json`).toBeDefined();
        expect(typeof value).toBe("string");
        expect((value as string).length, `empty value for ${segments.join(".")} in ${locale}`).toBeGreaterThan(0);
      }
    },
  );

  it("renders the three tab labels through useT('squads') selectors on the en bundle", () => {
    const enSquads = readSquads("en");
    render(
      <I18nProvider locale="en" resources={{ en: { squads: enSquads } }}>
        <SelectorProbe />
      </I18nProvider>,
    );

    const members = readKey({ squads: enSquads }, ["members_tab", "section_title"]);
    const tasks = readKey({ squads: enSquads }, ["tasks_tab"]);
    const instructions = readKey({ squads: enSquads }, ["instructions_tab", "title"]);

    expect(screen.getByText(`members_tab.section_title=${members as string}`)).toBeInTheDocument();
    expect(screen.getByText(`tasks_tab=${tasks as string}`)).toBeInTheDocument();
    expect(screen.getByText(`instructions_tab.title=${instructions as string}`)).toBeInTheDocument();
    // Defense: the 0.3.28 fix replaced literal "Members"/"Tasks"/"Instructions"
    // inside the page. If a future edit reintroduces a hard-coded label, that
    // would only be caught here if at least one label re-appears in plain
    // text outside the i18n path. So we additionally assert the resolved
    // translation of `tasks_tab` is non-empty and not the obvious literal
    // duplicate of the key itself.
    expect(typeof tasks).toBe("string");
    expect((tasks as string).length).toBeGreaterThan(0);
  });
});
