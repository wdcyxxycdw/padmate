// The development preview of the controller layouts (npm run preview:layouts):
// a page that shows every placement of the Joy-Cons at once, in a normal
// browser, without the daemon. It is a Vite project of its own, apart from the
// app's electron-vite build (electron.vite.config.ts), so nothing here is in
// out/ or in the installer. Port 5199, on the loopback address only.
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

const here = dirname(fileURLToPath(import.meta.url));
const app = resolve(here, "../..");

export default defineConfig({
  root: here,
  plugins: [react()],
  esbuild: { jsx: "automatic" },
  server: {
    host: "127.0.0.1",
    port: 5199,
    strictPort: true,
    // The page imports the renderer's sources from app/src.
    fs: { allow: [app] },
  },
  build: {
    outDir: resolve(here, "dist"),
    emptyOutDir: true,
    rollupOptions: { input: resolve(here, "controller-layouts.html") },
  },
});
