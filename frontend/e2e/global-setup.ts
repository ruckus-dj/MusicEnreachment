import { request, type FullConfig } from "@playwright/test";
import { mkdir, writeFile } from "node:fs/promises";
import { dirname } from "node:path";
import { e2eSeedPath, requireE2eBaseUrl } from "../playwright.config";

type SeedResponse = {
  readonly record_id: string;
  readonly source_ids: readonly string[];
  readonly recording_mbid: string;
  readonly correction_mbid: string;
};

function parseSeedResponse(payload: unknown): SeedResponse {
  const candidate = typeof payload === "object" && payload !== null ? payload : null;
  if (
    candidate === null ||
    typeof Reflect.get(candidate, "record_id") !== "string" ||
    !Array.isArray(Reflect.get(candidate, "source_ids")) ||
    Reflect.get(candidate, "source_ids").some((sourceId: unknown) => typeof sourceId !== "string") ||
    typeof Reflect.get(candidate, "recording_mbid") !== "string" ||
    typeof Reflect.get(candidate, "correction_mbid") !== "string"
  ) {
    throw new Error("E2E seed response did not match the required fixture DTO");
  }
  return {
    record_id: Reflect.get(candidate, "record_id"),
    source_ids: Reflect.get(candidate, "source_ids"),
    recording_mbid: Reflect.get(candidate, "recording_mbid"),
    correction_mbid: Reflect.get(candidate, "correction_mbid"),
  };
}

export default async function globalSetup(_config: FullConfig) {
  const baseURL = requireE2eBaseUrl();
  const api = await request.newContext({
    baseURL: String(baseURL),
  });
  try {
    const health = await api.get("/healthz");
    if (!health.ok()) throw new Error(`Test stand health check failed: ${health.status()}`);
    const seedResponse = parseSeedResponse(
      await (async () => {
        const response = await api.post("/api/e2e/seed");
        if (!response.ok()) throw new Error(`E2E seed failed: ${response.status()}`);
        return response.json();
      })(),
    );
    await mkdir(dirname(e2eSeedPath), { recursive: true });
    await writeFile(e2eSeedPath, `${JSON.stringify(seedResponse, null, 2)}\n`, "utf8");
    const fixturePath = process.env.MUSIC_INGEST_E2E_FIXTURE_PATH;
    if (fixturePath) {
      const seed = await api.post("/api/intake/notification", { data: { paths: [fixturePath] } });
      if (!seed.ok() && seed.status() !== 409) throw new Error(`E2E fixture intake failed: ${seed.status()}`);
      const scan = await api.post("/api/reconciliation/scan");
      if (!scan.ok()) throw new Error(`E2E fixture scan failed: ${scan.status()}`);
    }
    if (fixturePath) {
      const deadline = Date.now() + 30_000;
      let lastStatus = 0;
      let lastBody = "";
      let catalogReady = false;
      while (Date.now() < deadline) {
        const library = await api.get("/api/library/records");
        lastStatus = library.status();
        lastBody = await library.text();
        if (library.ok()) {
          const payload: unknown = JSON.parse(lastBody);
          if (
            typeof payload === "object" &&
            payload !== null &&
            "items" in payload &&
            Array.isArray(payload.items) &&
            payload.items.length > 0
          ) {
            catalogReady = true;
            break;
          }
        }
        await new Promise((resolve) => setTimeout(resolve, 500));
      }
      if (catalogReady) {
        const detailDeadline = Date.now() + 30_000;
        let detailStatus = 0;
        let detailBody = "";
        while (Date.now() < detailDeadline) {
          const detail = await api.get(`/api/library/records/${encodeURIComponent(seedResponse.record_id)}`);
          detailStatus = detail.status();
          detailBody = await detail.text();
          if (detail.ok()) {
            const payload: unknown = JSON.parse(detailBody);
            if (typeof payload === "object" && payload !== null && "sources" in payload) return;
          }
          await new Promise((resolve) => setTimeout(resolve, 500));
        }
        throw new Error(
          `E2E seeded record detail was not ready within 30s (record ${seedResponse.record_id}, status ${detailStatus}): ${detailBody}`,
        );
      }
      throw new Error(
        `E2E worker pipeline did not expose a library record within 30s (status ${lastStatus}): ${lastBody}`,
      );
    }
  } finally {
    await api.dispose();
  }
}
