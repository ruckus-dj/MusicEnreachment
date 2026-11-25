import {
  act,
  cleanup,
  fireEvent,
  screen,
  waitFor,
} from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, expect, it, vi } from "vitest";
import { createMemoryStorage } from "../../test/memoryStorage";
import { server } from "../../test/server";
import {
  changed,
  nextResponse,
  openDetail,
  operationError,
  operationPath,
  ScanEventSource,
  scanFixture,
  startScan,
} from "./sourceScanRefreshTestSupport";

afterEach(() => {
  cleanup();
  window.location.hash = "";
  ScanEventSource.onCreated = undefined;
  vi.unstubAllGlobals();
});

it("refreshes inventory once per terminal scan, including a same-ID retry", async () => {
  const storage = createMemoryStorage();
  storage.setItem("melotrove-language", "ru");
  vi.stubGlobal("localStorage", storage);
  vi.stubGlobal("EventSource", ScanEventSource);
  const fixture = scanFixture();
  await openDetail();
  const start = await screen.findByRole("button", {
    name: "Сканировать каталог",
  });
  await waitFor(() => expect(start).toBeEnabled());
  const firstStream = await startScan();
  expect(await screen.findByText("Читаем каталог…")).toBeVisible();
  const rootReads = fixture.rootReads;
  const locationReads = fixture.locationReads;

  // Delivery drops the active root selector, including on failure.
  const failed = {
    ...fixture.operation,
    target_source_root_id: undefined,
    state: "failed",
    stage: "failed",
    updated_at: "2026-10-01T00:00:02Z",
    safe_error: operationError,
  };
  fixture.operation = failed;
  await changed(firstStream);
  expect(await screen.findByText(operationError)).toBeVisible();
  expect(fixture.rootReads).toBe(rootReads + 1);
  expect(fixture.locationReads).toBe(locationReads + 1);

  // A stale preceding attempt can still be returned by the first read after
  // the retry POST. It must not show failure again or refresh the inventory.
  let latest = false;
  server.use(
    http.get(operationPath, () =>
      HttpResponse.json(latest ? fixture.operation : failed),
    ),
  );
  const connection = new Promise<ScanEventSource>((resolve) => {
    ScanEventSource.onCreated = resolve;
  });
  const staleRead = nextResponse(operationPath);
  fireEvent.click(
    screen.getByRole("button", { name: "Повторить сканирование" }),
  );
  expect(
    await screen.findByText("Сканирование поставлено в очередь."),
  ).toBeVisible();
  const retryStream = await connection;
  await act(async () => staleRead);
  expect(screen.queryByText(operationError)).not.toBeInTheDocument();
  expect(fixture.retryRequests).toBe(1);
  expect(fixture.rootReads).toBe(rootReads + 1);
  expect(fixture.locationReads).toBe(locationReads + 1);

  latest = true;
  fixture.operation = {
    ...fixture.operation,
    target_source_root_id: undefined,
    state: "succeeded",
    stage: "succeeded",
    updated_at: "2026-10-01T00:00:03Z",
  };
  fixture.locations = [
    { ...fixture.locations[0], relative_path: "New/02.flac" },
  ];
  await changed(retryStream);
  expect(await screen.findByText("Сканирование завершено.")).toBeVisible();
  expect(await screen.findByText("New/02.flac")).toBeVisible();
  expect(fixture.rootReads).toBe(rootReads + 2);
  expect(fixture.locationReads).toBe(locationReads + 2);
});
