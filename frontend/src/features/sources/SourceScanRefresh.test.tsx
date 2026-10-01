import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  screen,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  changed,
  fileError,
  nextResponse,
  openDetail,
  operationError,
  operationPath,
  rootError,
  rootPath,
  ScanEventSource,
  savedLocation,
  scanFixture,
  startScan,
} from "./sourceScanRefreshTestSupport";

beforeEach(() => {
  ScanEventSource.onCreated = undefined;
  vi.stubGlobal("EventSource", ScanEventSource);
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  ScanEventSource.onCreated = undefined;
  window.location.hash = "";
});

function summary() {
  return screen.getByRole("region", { name: "Каталог" });
}

function expectAvailability(status: string) {
  expect(summary().querySelector(".sources-chip")).toHaveAttribute(
    "data-status",
    status,
  );
}

function expectSavedInventory() {
  expect(screen.getByRole("row", { name: /Album\/01.flac/ })).toBeVisible();
  expect(
    within(summary()).getByText(new Date(savedLocation.mtime).toLocaleString()),
  ).toBeVisible();
}

describe("source availability refresh", () => {
  it("refreshes the open detail when a scan fails without reloading the page", async () => {
    // Given: an open root with a running scan and published inventory.
    const fixture = scanFixture();
    await openDetail();
    const stream = await startScan();
    fixture.root = {
      ...fixture.root,
      status: "unavailable",
      safe_error: rootError,
    };
    fixture.operation = {
      ...fixture.operation,
      state: "failed",
      safe_error: operationError,
    };

    // When: a payload-free event wakes the real REST snapshot reader.
    await changed(stream);

    // Then: the root error and prior inventory appear without route reload.
    expectAvailability("unavailable");
    expect(within(summary()).getByText(rootError)).toBeVisible();
    expect(screen.getByRole("alert")).toHaveTextContent(operationError);
    expectSavedInventory();
    expect(fixture.rootReads).toBe(2);
    expect(fixture.locationReads).toBe(2);
  });

  it("loads the persisted unavailable root and inventory when detail is reopened", async () => {
    // Given: the server already persisted a failure and the detail was closed.
    const fixture = scanFixture();
    fixture.root = {
      ...fixture.root,
      status: "unavailable",
      safe_error: rootError,
    };
    const view = await openDetail();
    view.unmount();

    // When: the same detail address is opened again.
    await openDetail();

    // Then: REST, not historical operation data, provides the root state.
    expectAvailability("unavailable");
    expect(within(summary()).getByText(rootError)).toBeVisible();
    expectSavedInventory();
    expect(fixture.rootReads).toBe(2);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("clears root unavailability and publishes restored inventory after a successful retry", async () => {
    // Given: a failed operation, followed by a queued retry of the same ID.
    const fixture = scanFixture();
    await openDetail();
    const stream = await startScan();
    fixture.root = {
      ...fixture.root,
      status: "unavailable",
      safe_error: rootError,
    };
    fixture.operation = {
      ...fixture.operation,
      state: "failed",
      safe_error: operationError,
    };
    await changed(stream);
    const retried = nextResponse(`${operationPath}/retry`, "POST");
    await act(async () => {
      fireEvent.click(
        screen.getByRole("button", { name: "Повторить сканирование" }),
      );
      await retried;
    });
    fixture.root = {
      ...fixture.root,
      status: "available",
      safe_error: undefined,
      scan_generation: 5,
      location_count: 2,
      last_successful_scan_at: "2026-10-01T12:00:00Z",
    };
    fixture.locations = [
      savedLocation,
      {
        ...savedLocation,
        id: "restored-location",
        relative_path: "Album/02.flac",
      },
    ];
    fixture.operation = {
      ...fixture.operation,
      state: "succeeded",
      stage: "succeeded",
      safe_error: undefined,
    };

    // When: the retry succeeds and wakes the snapshot reader.
    await changed(stream);

    // Then: available metadata replaces the error and new inventory is shown.
    expectAvailability("available");
    expect(within(summary()).queryByText(rootError)).not.toBeInTheDocument();
    expect(screen.getByRole("row", { name: /Album\/01.flac/ })).toBeVisible();
    expect(screen.getByRole("row", { name: /Album\/02.flac/ })).toBeVisible();
    expect(fixture.rootReads).toBe(3);
    expect(fixture.locationReads).toBe(3);
  });

  it("refreshes an inaccessible root after a refused start without creating a stream", async () => {
    // Given: validation now refuses the path, without creating an operation.
    const fixture = scanFixture();
    await openDetail();
    fixture.refused = true;
    fixture.root = {
      ...fixture.root,
      status: "unavailable",
      safe_error: rootError,
    };
    const created = vi.fn();
    ScanEventSource.onCreated = created;
    const root = nextResponse(rootPath);
    const locations = nextResponse(`${rootPath}/locations`);

    // When: the user starts a scan.
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Сканировать" }));
      await root;
    });
    await act(async () => locations);

    // Then: the authoritative root refresh preserves inventory without SSE.
    expectAvailability("unavailable");
    expect(within(summary()).getByText(rootError)).toBeVisible();
    expectSavedInventory();
    expect(created).not.toHaveBeenCalled();
    expect(fixture.rootReads).toBe(2);
  });

  it("keeps file probe errors separate from root availability on refresh", async () => {
    // Given: a file has probe_error, but REST still reports an available root.
    const fixture = scanFixture();
    fixture.root = { ...fixture.root, safe_error: fileError };
    fixture.locations = [
      { ...savedLocation, probe_status: "probe_error", safe_error: fileError },
    ];
    await openDetail();
    const stream = await startScan();
    fixture.operation = {
      ...fixture.operation,
      state: "failed",
      safe_error: operationError,
    };

    // When: a failed operation causes the root to be read again.
    await changed(stream);

    // Then: no operation/file error is presented as root unavailability.
    expectAvailability("available");
    expect(within(summary()).queryByText(fileError)).not.toBeInTheDocument();
    const row = screen.getByRole("row", { name: /Album\/01.flac/ });
    expect(within(row).getByText(fileError)).toBeVisible();
    expect(row.querySelector(".sources-chip")).toHaveAttribute(
      "data-status",
      "probe_error",
    );
  });

  it("does not refresh repeatedly for duplicate failed snapshots", async () => {
    // Given: the first failed snapshot has already refreshed the root.
    const fixture = scanFixture();
    await openDetail();
    const stream = await startScan();
    fixture.operation = {
      ...fixture.operation,
      state: "failed",
      safe_error: operationError,
    };
    await changed(stream);
    const read = nextResponse(operationPath);

    // When: SSE requests the same failed REST snapshot again.
    await act(async () => {
      stream.dispatchEvent(new Event("operation-changed"));
      await read;
    });

    // Then: repeated delivery does not reload root or inventory again.
    expect(fixture.rootReads).toBe(2);
    expect(fixture.locationReads).toBe(2);
  });
});
