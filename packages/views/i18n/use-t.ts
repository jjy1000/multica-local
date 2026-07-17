// Side-effect import: pulls the i18next module augmentation into the
// compilation graph. Without this, apps that consume @multica/views won't
// see the resources types or the selector-API enablement, and their
// typecheck would reject `t($ => $.foo.bar)` calls inside views.
import "./resources-types";

// Project alias for react-i18next's useTranslation hook.
// Use the selector form when calling: t($ => $.signin.title)
//
// IMPORTANT: the selector MUST be an arrow expression whose value is the
// proxy path itself (e.g. `($) => $.sidebar.foo` or
// `($) => $.sidebar[dynamicKey]`). i18next's `keysFromSelector` does
// `const { [PATH_KEY]: path } = selector(createProxy())` — it reads
// `PATH_KEY` off whatever the selector returns. A block-body selector
// like `($) => { const v = $.sidebar.foo; return v; }` returns a plain
// string instead of the proxy, `path` ends up `undefined`, and the very
// next line `if (path.length > 1 && nsSeparator)` throws
// `TypeError: Cannot read properties of undefined (reading 'length')`.
// The throw escapes React's render pass and unmounts the surrounding
// tree, which in our case blanked the entire desktop window (see
// .omc/release-notes-0.3.21-patch.1.md for the 2026-07-14 incident).
// If you need to compute the key conditionally, build the key outside
// `t(...)` and pass a plain expression form: `($) => $.sidebar[labelKey]`.
export { useTranslation as useT } from "react-i18next";
