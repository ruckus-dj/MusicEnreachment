import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { documentPresentation } from "./src/ui/styles/documentPresentation.ts";
export default {
  plugins: [documentPresentation, react(), tailwindcss()],
  build: { outDir: "../../build/design-prototype", emptyOutDir: true },
};
