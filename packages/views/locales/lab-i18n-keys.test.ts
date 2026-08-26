import { describe, expect, it } from "vitest";
import en from "./en/experimental.json";
import zhHans from "./zh-Hans/experimental.json";
import ja from "./ja/experimental.json";
import ko from "./ko/experimental.json";

// 0.5.74 PR 1 regression pin (Code-Reviewer audit finding P0 #1):
// sidebar shows raw i18n keys when the experimental sidebar label is missing.
// This test pins the four-flavor (en / zh-Hans / ja / ko) coverage for every
// sidebar.label_key referenced in apps/desktop/resources/experiments/*/manifest.json.
// The keys live under layout.json (sidebar.* namespace) for sidebar tooltip
// rendering; user_plugins.* lives under experimental.json (PR 5/8 source of truth).

const REQUIRED_SIDEBAR_KEYS = [
  "experimental_claude_science_lab",
  "experimental_pythia",
  "experimental_mythos",
  "experimental_swarm_topology",
  "experimental_llm_wiki_bridge",
  "experimental_code_canvas",
  "experimental_semantica",
] as const;

const REQUIRED_USER_PLUGIN_KEYS = [
  "trigger_mode.auto",
  "trigger_mode.issue_select",
  "runtime_kind.none",
  "runtime_kind.inline",
  "runtime_kind.subprocess",
  "status.active",
  "status.disabled",
  "status.deleted",
  "toast.delete_success",
  "toast.delete_failed",
  "toast.toggle_failed",
  // PR 9 closure: 2 leftover button states extracted from L283 ternary
  "delete_button",
  "delete_button_deleting",
] as const;

type ExperimentalMessages = Record<string, unknown>;

function dig(obj: unknown, path: string): unknown {
  return path.split(".").reduce<unknown>((acc, k) => {
    if (acc && typeof acc === "object" && k in (acc as Record<string, unknown>)) {
      return (acc as Record<string, unknown>)[k];
    }
    return undefined;
  }, obj);
}

function loadLayout(locale: string): ExperimentalMessages {
  // Dynamic require keeps the test compile-time agnostic of every locale file's
  // JSON shape; missing keys fail with a clear message rather than an
  // unresolved-import error.
  // eslint-disable-next-line @typescript-eslint/no-require-imports, @typescript-eslint/no-var-requires
  const raw = require(`./${locale}/layout.json`) as ExperimentalMessages;
  return (raw.sidebar as ExperimentalMessages) ?? {};
}

describe("lab-integration i18n keys (0.5.74)", () => {
  describe("sidebar experimental label_keys (layout.json)", () => {
    for (const key of REQUIRED_SIDEBAR_KEYS) {
      for (const [name, _] of [
        ["en", loadLayout("en")],
        ["zh-Hans", loadLayout("zh-Hans")],
        ["ja", loadLayout("ja")],
        ["ko", loadLayout("ko")],
      ] as const) {
        it(`${name}.${key} is non-empty`, () => {
          const value = dig(_ ?? {}, key);
          expect(typeof value).toBe("string");
          expect((value as string).length).toBeGreaterThan(0);
        });
      }
    }
  });

  describe("user_plugins labels (experimental.json)", () => {
    for (const key of REQUIRED_USER_PLUGIN_KEYS) {
      for (const [name, bundle] of [
        ["en", en as unknown as ExperimentalMessages],
        ["zh-Hans", zhHans as unknown as ExperimentalMessages],
        ["ja", ja as unknown as ExperimentalMessages],
        ["ko", ko as unknown as ExperimentalMessages],
      ] as const) {
        it(`${name}.user_plugins.${key} is non-empty`, () => {
          const value = dig(dig(bundle, "user_plugins") ?? {}, key);
          expect(typeof value).toBe("string");
          expect((value as string).length).toBeGreaterThan(0);
        });
      }
    }
  });
});
