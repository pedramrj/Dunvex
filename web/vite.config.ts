import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// Built into web/dist and embedded in the Go binary. Relative base: the panel
// lives under a secret, configurable path.
export default defineConfig({
  base: "./",
  plugins: [react(), tailwindcss()],
  build: { outDir: "dist", emptyOutDir: true, assetsInlineLimit: 0, chunkSizeWarningLimit: 900 },
  server: {
    proxy: { "/panel/api": { target: "http://127.0.0.1:2096", changeOrigin: false } },
  },
});
