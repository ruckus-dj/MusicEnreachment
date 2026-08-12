import { expect, test } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { e2eSeedPath } from "../playwright.config";

type SeedResponse = {
  readonly record_id: string;
  readonly source_ids: readonly string[];
};

async function readSeedResponse(): Promise<SeedResponse> {
  const payload: unknown = JSON.parse(await readFile(e2eSeedPath, "utf8"));
  const candidate = typeof payload === "object" && payload !== null ? payload : null;
  if (
    candidate === null ||
    typeof Reflect.get(candidate, "record_id") !== "string" ||
    !Array.isArray(Reflect.get(candidate, "source_ids")) ||
    Reflect.get(candidate, "source_ids").some((sourceId: unknown) => typeof sourceId !== "string")
  ) {
    throw new Error("Persisted E2E seed response is invalid");
  }
  return { record_id: Reflect.get(candidate, "record_id"), source_ids: Reflect.get(candidate, "source_ids") };
}

test("operator can inspect source roots and output status", async ({ page }) => {
  await readSeedResponse();
  await page.goto("/");

  await page.getByTestId("nav-settings").click();
  await expect(page.getByTestId("source-root-list")).toBeVisible();

  await page.getByTestId("nav-library").click();
  const record = page.getByRole("button").filter({ hasText: "Fixture Artist" }).first();
  await expect(record).toContainText("Fixture Artist");
  await record.click();
  await expect(page.getByRole("button", { name: "Fixture Album" })).toBeVisible();
  await page.getByRole("button", { name: "Fixture Album" }).click();
  const track = page.getByRole("button").filter({ has: page.locator(".track-meta") }).first();
  await expect(track).toBeVisible();
  await track.click();
  await expect(page.getByTestId("output-status")).toBeVisible();
  await expect(page.getByTestId("output-path")).toBeVisible();
  await page.screenshot({ path: "test-results/operator-smoke.png", fullPage: true });
});

test("operator can select a source and correct its recording MBID", async ({ page }) => {
  const seed = await readSeedResponse();
  await page.goto(`/library/record/${seed.record_id}/source/${seed.source_ids[0]}`);
  const sourceId = seed.source_ids[1];
  if (sourceId === undefined) throw new Error("E2E seed response must contain two source IDs");
  await page.getByTestId("effective-source-choice").selectOption(sourceId);
  await page.getByText("Выбрать recording MBID вручную").click();
  await page.getByTestId("recording-mbid-input").fill("11111111-1111-4111-8111-111111111111");
  await page.getByTestId("recording-correction-submit").click();
  await expect(page.getByTestId("recording-review-status")).toBeVisible();
});
