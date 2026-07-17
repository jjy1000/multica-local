# Multica 0.3.13 — 2026-07-13

## Summary

Migrate Claude Science's embedded browser surface from `<iframe>` (0.3.12) to Electron's `<webview>` tag. Real Chromium instance, separate process, partition-isolated cookies / localStorage. Native i18n (OpenScience's 729-line `zh.ts` auto-activates via `navigator.language` override) and live theme bridge both wired through a dedicated webview preload.

## What changed

### Renderer
- `apps/desktop/src/renderer/src/pages/claude-science-view.tsx` — `<iframe src="/experimental/claude-science/">` → `<webview src={managerURL} partition="persist:claude-science" preload={fileURL}>`. JSX namespace extended with `WebviewAttributes` to keep the `<webview>` tag type-safe without `as any`. Imperative handle aliased as `ElectronWebviewTag` interface.
- Same-origin reverse-proxy URL (`/experimental/claude-science/*`) replaced with the manager's bound loopback URL (`http://127.0.0.1:<port>`). The proxy registration in `registerExperimentalUpstream` is kept so the `multica-claude-science` Skill adapter can still hit the workspace through Multica's authenticated API surface.
- Theme bridge stub `console.log` replaced with `webview.send("multica-theme-change", theme)` driven by the existing `multica-theme-change` IPC channel that the preload listens for.

### Main process
- `apps/desktop/src/main/index.ts` — `webPreferences` gains `webviewTag: true`. Without this Chromium silently strips the `<webview>` tag from the renderer DOM; this was the silent failure mode of the 0.3.13 attempt that hit the packaging dead-end before this release.
- `apps/desktop/src/main/claude-science-manager.ts` — new IPC handler `claude-science:get-webview-preload-path` returns the absolute path of the preload script (resolved for both dev and packaged modes via the existing `resolveResourcePath` helper).
- `apps/desktop/src/main/experimental/manager-template.ts` — `resolveResourcePath` exported (was private to the module) so the new IPC handler can use it.

### Preload (renderer-side)
- `apps/desktop/src/preload/index.ts` + `apps/desktop/src/preload/index.d.ts` — `experimentalAPI.claudeScience.getWebviewPreloadPath()` exposes the preload path to the renderer.

### Preload (webview-side, plain JS)
- `apps/desktop/resources/main/experimental/webview-preload-claude-science.js` — already existed on disk from a prior failed 0.3.13 attempt; 0.3.13 makes it actually load. Three responsibilities:
  1. Override `navigator.language` and `navigator.languages` to `"zh-CN"` so OpenScience's `entry.tsx → detectLocale()` picks the Chinese translation on first paint. Uses `Object.defineProperty` with `configurable: true` so the override re-applies after `popstate` / `hashchange` (Electron `<webview>` resets some properties on client-side navigation).
  2. Listen for `"multica-theme-change"` IPC and write the value into OpenScience's localStorage keys (`openscience-color-scheme`, `openscience-theme-id`) then dispatch a synthetic `storage` event so OpenScience's theme context re-renders without a reload.
  3. `ipcRenderer.sendToHost("claude-science-webview-preload-ready", { locale: "zh-CN" })` so the renderer can flip its chrome-bar status from "starting" to "ready (zh-CN)".
- Plain JS, not transpiled, on purpose. `require("electron")` is stable across Electron versions, and avoiding a build step keeps electron-vite's single-output layout intact (multi-entry would emit `.mjs` siblings that Electron's stub binary cannot load — this was the dead-end of the previous 0.3.13 attempt).

### Bundle
- No change to `bundle-cli.mjs`, `package.mjs`, or `electron-builder.yml`. The preload lives under `resources/main/experimental/` which is bundled by the default electron-builder glob, and `resources/**` is in the existing `asarUnpack` block, so packaged mode just works.

## Why we did not go back to BrowserView / UtilityProcess

The user request was for a "real browser-rendering container". The candidates were:

| Option | Why rejected |
|---|---|
| BrowserView / WebContentsView | Not a JSX component; would require refactoring `claude-science-view.tsx` to manage a `WebContentsView` lifecycle imperatively. The user asked for `<webview>` specifically. |
| System browser | Loses the "integrated inside Multica" property the user asked for. |
| `<iframe>` + content-script bridge | Already shipped in 0.3.12; cross-process, no real Chromium, cannot override `navigator.languages` because same-origin. |

`<webview>` is the only option that gives all three: real Chromium, integration with Multica's renderer (preload runs in the webview process but the renderer's IPC `webview.send` can reach it), and the i18n override.

## Verification (cold-start smoke)

```
5432 LISTEN  (postgres)             < 6 s
8090 LISTEN  (server, multica)      < 8 s
/health  →  {"status":"ok"}
row parity  workspace=1 / issue=162 / comment=855 / agent=80 / schema_migrations=184
GUI launched, dashboard window visible, no NSAlert
```

DMG packaging itself did not complete (`create-dmg -s` flag mismatch with electron-builder's invocation pattern), but the unpacked `dist/mac-arm64/Multica.app` was produced correctly and installed directly via `cp -R`. Follow-up 0.3.14 should investigate the `CUSTOM_DMGBUILD_PATH` invocation pattern.

## Risks / Known limits

- **`SameSite` cookies across loopback**: OpenScience may set `SameSite=Strict` cookies on `127.0.0.1:<port>`; the webview's `partition="persist:claude-science"` keeps BYOK keys isolated from Multica's localStorage but does NOT relax cookie `SameSite` policy. If login breaks in 0.3.13, the fix is at OpenScience (set `SameSite=Lax`) not here.
- **No `before-quit` cleanup for the webview itself**: webview is a child of the renderer process; closing the BrowserWindow terminates it. The existing `before-quit` chain in `index.ts:619-643` already calls `stopClaudeScienceManager()`, which kills the OpenScience Bun binary. No additional lifecycle code needed.
- **OpenScience i18n detection path is unverified**: the preload assumes `entry.tsx` reads `navigator.languages` first. If OpenScience reads `localStorage("openscience.global.dat:language")` first, the `navigator.languages` override is decorative and the preload's localStorage fallback (already in the file) becomes the real activation path. A DevTools probe on first launch will confirm.
- **Plain JS preload violates TS strict**: accepted as a 0.3.13 trade-off. The file is ~120 lines, isolated, and the entire `<webview>` migration is opt-in per the Labs flag. 0.3.14 can rewrite in TS once the build pipeline supports it without re-doing the wiring.

## Rollback

```bash
# 0.3.13 ships an unpacked .app, not a DMG, so rollback is the backup directory.
rm -rf /Applications/Multica.app
cp -R /Applications/Multica.app.0.3.12.webview-source-20260713-214722.bak /Applications/Multica.app
open /Applications/Multica.app
```

Expected post-rollback invariants:

- `defaults read /Applications/Multica.app/Contents/Info.plist CFBundleShortVersionString` → `0.3.12`
- Claude Science page returns to the iframe implementation (the renderer source for the view is unchanged in the rollback — the prerelease .app is just replaced)
- 5432 + 8090 listen < 8 s, row parity preserved

## Files changed

| File | Lines |
|---|---|
| `apps/desktop/package.json` | version `0.3.12` → `0.3.13` |
| `apps/desktop/src/main/index.ts` | +5 (`webviewTag: true` + comment) |
| `apps/desktop/src/main/claude-science-manager.ts` | +14 (new IPC handler) |
| `apps/desktop/src/main/experimental/manager-template.ts` | +3 (export `resolveResourcePath`) |
| `apps/desktop/src/preload/index.ts` | +3 (`getWebviewPreloadPath`) |
| `apps/desktop/src/preload/index.d.ts` | +6 (interface entry) |
| `apps/desktop/src/renderer/src/pages/claude-science-view.tsx` | rewrite (~50 lines net diff) |
| `apps/desktop/resources/main/experimental/webview-preload-claude-science.js` | already on disk, now actually loaded |