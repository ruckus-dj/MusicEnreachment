import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, expect, it, vi } from "vitest";
import type { SourceLocationDetailResponse } from "../../api/generated/client.schemas";
import { I18nProvider } from "../../app/i18n/host";
import { createMemoryStorage } from "../../test/memoryStorage";
import { server } from "../../test/server";
import { SourceInspectorScreen } from "./SourceInspectorScreen";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const fingerprintValue = "AQADt-test-compressed-fingerprint";
const fpcalcVersion = "1.6.1";
const activeFpcalcVersion = "1.6.2";

it.each([
  { state: "succeeded", saved: true, status: "State: Ready" },
  { state: "failed", saved: true, status: "State: Failed" },
  { state: "pending", saved: false, status: "State: Pending" },
] as const)("shows $state status without fingerprint details", async ({
  state,
  saved,
  status,
}) => {
  const storage = createMemoryStorage();
  storage.setItem("melotrove-language", "en");
  vi.stubGlobal("localStorage", storage);
  const detail: SourceLocationDetailResponse = {
    root_id: "root-1",
    location_id: "location-1",
    relative_path: "album/track.flac",
    size_bytes: 1200,
    mtime: "2026-11-18T10:00:00Z",
    probe_status: "audio",
    analysis_state: "analyzed",
    matching_eligible: saved,
    root: { status: "available", stale: false },
    staged_artifact: {
      state: "unknown",
      requested_steps: [],
      requested_steps_known: false,
    },
    active_fpcalc_version: activeFpcalcVersion,
    steps: [
      {
        name: "fingerprint",
        state,
        attempt: 1,
        fingerprint: saved
          ? {
              value: fingerprintValue,
              version: fpcalcVersion,
              version_banner: `fpcalc version ${fpcalcVersion}`,
              algorithm_id: 1,
              algorithm_namespace: "chromaprint",
              applied_operation_id: "operation-1",
              calculated_at: "2026-11-18T10:00:00Z",
              duration: 123,
              parser_contract_version: 1,
            }
          : undefined,
      },
    ],
  };
  server.use(
    http.get("/api/sources/root-1/locations/location-1", () =>
      HttpResponse.json(detail),
    ),
  );
  render(
    <I18nProvider>
      <SourceInspectorScreen sourceId="root-1" locationId="location-1" />
    </I18nProvider>,
  );

  const row = within(
    await screen.findByRole("listitem", { name: "Acoustic fingerprint" }),
  );
  expect(row.getByText(status)).toBeInTheDocument();
  expect(screen.queryByText(fingerprintValue)).not.toBeInTheDocument();
  expect(row.queryByText(/fpcalc|1\.6\./i)).not.toBeInTheDocument();
  if (state === "failed") {
    expect(
      row.getByText("A saved fingerprint is available."),
    ).toBeInTheDocument();
    expect(row.getByRole("button", { name: /Retry step/ })).toBeEnabled();
  } else if (state === "succeeded") {
    expect(
      row.getByRole("button", { name: "Recalculate fingerprint" }),
    ).toBeEnabled();
  }
});
