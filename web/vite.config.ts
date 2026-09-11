import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      "/api": { target: "http://127.0.0.1:8080", ws: true },
      "/health": "http://127.0.0.1:8080",
      "/oauth/schwab": "http://127.0.0.1:8080",
      "/investment/terminal": "http://127.0.0.1:8080",
      "/charting_library": "http://127.0.0.1:8080"
    }
  },
  build: {
    outDir: "../internal/webui/dist",
    emptyOutDir: true
  }
});

