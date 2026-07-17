import reactConfig from "@multica/eslint-config/react";
import i18next from "eslint-plugin-i18next";

// Global i18n protection. Every JSX text node in this package must pass
// through useT() — raw strings become a build error. Scope of
// `mode: "jsx-text-only"`: flags raw strings inside JSX children only;
// attribute values and plain TS literals are allowed through.

export default [
  ...reactConfig,
  {
    files: ["**/*.tsx"],
    ignores: ["**/*.test.tsx", "test/**"],
    plugins: { i18next },
    rules: {
      "i18next/no-literal-string": [
        "error",
        { mode: "jsx-text-only" },
      ],
    },
  },
  // Block-body i18next selector guard. i18next's `keysFromSelector` reads
  // `[PATH_KEY]` off whatever the selector returns; a block-body selector
  // like `t(($) => { return $.foo; })` returns a plain string instead of
  // the proxy, so `path` becomes undefined and the next line
  // `if (path.length > 1 && ...)` throws — escaping React's render pass
  // and unmounting the entire tree (2026-07-14 desktop blank-window
  // incident, see .omc/release-notes-0.3.21-patch.1.md). Force selectors
  // to be plain arrow expressions: `($) => $.foo` or
  // `($) => $.foo[dynamicKey]`. Anything wrapped in `{ ... }` is an error.
  {
    files: ["**/*.tsx", "**/*.ts"],
    ignores: ["**/*.test.tsx", "**/*.test.ts"],
    rules: {
      "no-restricted-syntax": [
        "error",
        {
          // Matches `t(...)` and `useT(...)` calls whose first argument is
          // an arrow function with a block body (i.e. `=> { ... }` rather
          // than a plain expression). The selector form is the documented
          // pattern; the block form silently crashes the renderer.
          selector:
            "CallExpression[callee.name=/^(t|useT)$/] > ArrowFunctionExpression[body.type='BlockStatement']",
          message:
            "useT()/t() selector must be an arrow expression form `($) => $.foo[bar]` — block-body selectors break i18next's keysFromSelector (PATH_KEY is read off the selector return value, which a block body makes a string). See packages/views/i18n/use-t.ts for the incident reference.",
        },
      ],
    },
  },
];
