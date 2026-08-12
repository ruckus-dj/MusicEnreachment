import { defineConfig } from "@playwright/test";
import { resolve } from "node:path";

export function requireE2eBaseUrl(): string {
  const url = process.env.MUSIC_INGEST_E2E_BASE_URL;
  if (!url) throw new Error("MUSIC_INGEST_E2E_BASE_URL environment variable is required");
  return url;
}

const baseURL = requireE2eBaseUrl();
export const e2eSeedPath = resolve(import.meta.dirname, "test-results/e2e-seed.json");

export default defineConfig({
  testDir: "./e2e",
  outputDir: "./test-results",
  reporter: [["list"], ["json", { outputFile: "./test-results/results.json" }]],
  use: {
    baseURL,
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
    video: "retain-on-failure",
  },
  fullyParallel: false,
  workers: 1,
  globalSetup: "./e2e/global-setup.ts",
});
