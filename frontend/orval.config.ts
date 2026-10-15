import { defineConfig } from "orval";

export default defineConfig({
  api: {
    input: { target: "./openapi.json" },
    output: {
      client: "react-query",
      mode: "split",
      baseUrl: "/api",
      target: "./src/api/generated/client.ts",
    },
  },
});
