import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";
import { documentPresentation } from "./src/ui/styles/documentPresentation";

export default defineConfig({
  plugins: [documentPresentation, react(), tailwindcss()],
  server: {
    proxy: {
      "/api": "http://127.0.0.1:8080",
      "/health": "http://127.0.0.1:8080",
    },
  },
  build: { outDir: "../build/frontend", emptyOutDir: true },
});
