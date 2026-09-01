import globals from "globals";
import reactConfig from "@multica/eslint-config/react";

export default [
  ...reactConfig,
  // vendor/ holds source-of-truth mirrors (`pythia-src`, `semantica-src`) that
  // `bundle-cli.mjs` re-copies on every run, so lint findings there are neither
  // ours to fix nor stable across builds.
  { ignores: ["out/", "dist/", "vendor/"] },
  {
    files: ["scripts/**/*.{mjs,js}"],
    languageOptions: {
      globals: { ...globals.node },
    },
  },
  // Security: every renderer-controlled URL that reaches the OS shell or the
  // native download system must flow through the safe wrappers in
  // src/main/external-url.ts (scheme allowlist). Enforce it statically so
  // direct shell.openExternal / webContents.downloadURL calls cannot silently
  // regress the protection.
  {
    files: ["src/main/**/*.ts"],
    rules: {
      "no-restricted-syntax": [
        "error",
        {
          selector:
            "CallExpression[callee.object.name='shell'][callee.property.name='openExternal']",
          message:
            "Do not call shell.openExternal directly. Use openExternalSafely from './external-url' so the http/https allowlist stays enforced.",
        },
        {
          selector:
            "CallExpression[callee.object.property.name='webContents'][callee.property.name='downloadURL']",
          message:
            "Do not call webContents.downloadURL directly. Use downloadURLSafely from './external-url' so the http/https allowlist stays enforced.",
        },
      ],
    },
  },
  {
    files: ["src/main/external-url.ts"],
    rules: {
      "no-restricted-syntax": "off",
    },
  },
];
