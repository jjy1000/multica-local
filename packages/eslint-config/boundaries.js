/**
 * Per-package boundary rules enforcing the hard boundaries documented in
 * packages/CLAUDE.md. Each consuming package spreads its own set into its
 * eslint.config.mjs so the documented boundary maps 1:1 to a lint rule.
 */

// core/: no localStorage (use StorageAdapter), no process.env.
/** @type {import("eslint").Linter.Config[]} */
export const coreBoundaries = [
  {
    files: ["**/*.{ts,tsx}"],
    ignores: [
      // platform/storage.ts IS the StorageAdapter implementation — the one
      // sanctioned localStorage access point everything else funnels through.
      "**/platform/storage.ts",
      // Tests may shim/inspect localStorage to exercise persisted stores.
      "**/*.test.{ts,tsx}",
      "**/*.spec.{ts,tsx}",
      "**/vitest.config.*",
      "**/eslint.config.*",
    ],
    rules: {
      "no-restricted-globals": [
        "error",
        {
          name: "localStorage",
          message:
            "core must not access localStorage directly — inject a StorageAdapter instead.",
        },
      ],
      "no-restricted-properties": [
        "error",
        {
          object: "process",
          property: "env",
          message:
            "core must not read process.env — pass configuration in via adapters or options.",
        },
      ],
    },
  },
];

// ui/: no @multica/core imports, no business logic.
/** @type {import("eslint").Linter.Config[]} */
export const uiBoundaries = [
  {
    files: ["**/*.{ts,tsx}"],
    rules: {
      "no-restricted-imports": [
        "error",
        {
          patterns: [
            {
              group: ["@multica/core", "@multica/core/*"],
              message:
                "ui must stay independent of @multica/core — keep business logic out of atomic components.",
            },
          ],
        },
      ],
    },
  },
];
