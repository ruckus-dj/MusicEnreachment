import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

const referenceRoot = fileURLToPath(new URL("../app-design/", import.meta.url));
const referenceTypes = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css" };
const referencePlugin = {
  name: "unchanged-prototype-reference",
  configureServer(server) {
    server.middlewares.use(async (request, response, next) => {
      const url = new URL(request.url ?? "/", "http://localhost");
      if (!url.pathname.startsWith("/reference/")) return next();
      const name = url.pathname.slice("/reference/".length) || "index.html";
      if (name !== path.basename(name) || !Object.hasOwn(referenceTypes, path.extname(name))) {
        response.writeHead(404).end();
        return;
      }
      try {
        const content = await readFile(path.join(referenceRoot, name));
        response.setHeader("Content-Type", `${referenceTypes[path.extname(name)]}; charset=utf-8`);
        response.setHeader("Cache-Control", "no-store");
        response.end(content);
      } catch { response.writeHead(404).end(); }
    });
  },
};

export default {
  plugins: [referencePlugin, react(), tailwindcss()],
  build: { outDir: "../../build/design-prototype", emptyOutDir: true },
};
