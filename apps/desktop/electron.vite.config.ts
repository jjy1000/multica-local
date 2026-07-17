import { resolve } from "path";
import { defineConfig, externalizeDepsPlugin } from "electron-vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  main: {
    plugins: [externalizeDepsPlugin()],
    // Path D fix (memory 0.2.89.5.1): disable electron-vite's automatic
    // modulepreload injection in the main bundle. With ~70 dynamic chunks the
    // Chromium single-thread parser serially fetched and parsed each preload
    // chunk before window-all-closed could fire, blowing past the 1.5s
    // BrowserHungDetector threshold and triggering a C++ NSAlert. Dynamic
    // imports still work (the renderer's `import('./Foo')` becomes a runtime
    // fetch), but the synchronous preload chain is gone — the renderer asks
    // for what it needs, when it needs it. Keep this true until Electron 38
    // is adopted upstream; re-evaluate then.
    build: {
      modulePreload: false,
    },
  },
  preload: {
    plugins: [externalizeDepsPlugin()],
  },
  renderer: {
    server: {
      // Allow parallel worktrees to run `pnpm dev:desktop` side-by-side
      // (e.g. Multica Canary alongside a primary checkout) by overriding
      // the renderer port via env. Falls back to 5173 for the common case.
      port: Number(process.env.DESKTOP_RENDERER_PORT) || 5173,
      strictPort: true,
    },
    plugins: [react(), tailwindcss()],
    resolve: {
      alias: {
        "@": resolve("src/renderer/src"),
      },
      dedupe: ["react", "react-dom", "@tanstack/react-query"],
    },
  },
});
