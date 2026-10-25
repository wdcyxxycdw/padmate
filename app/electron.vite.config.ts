import { resolve } from "node:path";
import react from "@vitejs/plugin-react";
import { defineConfig, externalizeDepsPlugin } from "electron-vite";

export default defineConfig(({ command }) => ({
  main: {
    // electron-updater (PKG-10) is bundled: the installed application has no
    // node_modules to find it in (electron-builder.yml packs none).
    plugins: [externalizeDepsPlugin({ exclude: ["electron-updater"] })],
    build: { rollupOptions: { input: resolve("src/main/index.ts") } },
  },
  preload: {
    build: {
      rollupOptions: {
        // The overlay's preload (HUD-04, ADR-0063) is a second, separate
        // entry: a sandboxed preload cannot require a shared chunk.
        input: {
          index: resolve("src/preload/index.ts"),
          overlay: resolve("src/preload/overlay.ts"),
        },
        output: { format: "cjs" },
      },
    },
  },
  renderer: {
    // React refresh's inline preamble is incompatible with script-src 'self'.
    plugins: command === "build" ? [react()] : [],
    server: { host: "127.0.0.1" },
    esbuild: { jsx: "automatic" },
    // The overlay (HUD-01, ADR-0063) is a second page of the same renderer
    // root, served the same way (padmate://app/overlay.html) with the same CSP.
    build: {
      rollupOptions: {
        input: {
          index: resolve("src/renderer/index.html"),
          overlay: resolve("src/renderer/overlay.html"),
        },
      },
    },
  },
}));
