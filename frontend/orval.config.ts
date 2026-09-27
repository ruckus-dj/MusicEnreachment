import { defineConfig } from "orval";

export default defineConfig({
  api: {
    input: { target: "./openapi.json" },
    output: {
      client: "react-query",
      mode: "split",
      mock: true,
      baseUrl: "/api",
      target: "./src/api/generated/client.ts",
    },
  },
});
