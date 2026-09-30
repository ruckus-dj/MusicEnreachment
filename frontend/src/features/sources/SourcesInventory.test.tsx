import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  OperationResponse,
  SourceLocationResponse,
  SourceRootResponse,
} from "../../api/generated/client.schemas";
import { server } from "../../test/server";
import { SourcesScreen } from "./SourcesScreen";

const rootId = "root-1";
const scanUrl = `/api/sources/${rootId}/scan`;
const queuedText = "Сканирование поставлено в очередь.";
const traversingText =
  "Обход каталога: чтение дерева и проверка аудиопотока файлов.";
const applyingText =
  "Применение результатов: подтверждённый инвентарь сохраняется.";
const doneText =
  "Сканирование завершено: инвентарь обновлён последним успешным обходом.";
const reconnectText = /потоку событий прерваны/;

// Only the browser event source is controlled; the operation snapshot travels
// through the real Orval client and intercepted HTTP, so a stream event only
// wakes the page into re-reading REST.
class TestEventSource extends EventTarget {
  static instances: TestEventSource[] = [];
  static onCreated: ((stream: TestEventSource) => void) | undefined;
  readonly url: string;
  close = vi.fn();

  constructor(url: string) {
    super();
    this.url = url;
    TestEventSource.instances.push(this);
    TestEventSource.onCreated?.(this);
  }
}

function nextEventSource(): Promise<TestEventSource> {
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => {
      TestEventSource.onCreated = undefined;
      reject(new Error("operation event source was not opened"));
    }, 5000);
    TestEventSource.onCreated = (stream) => {
      clearTimeout(timeout);
      TestEventSource.onCreated = undefined;
      resolve(stream);
    };
  });
}

function root(overrides: Partial<SourceRootResponse> = {}): SourceRootResponse {
  return {
    id: rootId,
    display_name: "Входящие",
    configured_path: "/srv/inbox",
    enabled: true,
    status: "available",
    stale: false,
    scan_generation: 4,
    location_count: 1,
    inventory_path: "/srv/inbox",
    last_successful_scan_at: "2026-09-26T10:20:00Z",
    created_at: "2026-09-01T00:00:00Z",
    updated_at: "2026-09-26T10:20:00Z",
    ...overrides,
  };
}

function location(
  overrides: Partial<SourceLocationResponse> = {},
): SourceLocationResponse {
  return {
    id: "loc-1",
    relative_path: "Альбом/01 Открытие.flac",
    size_bytes: 1536,
    mtime: "2026-09-26T10:20:00Z",
    probe_status: "audio",
    ...overrides,
  };
}

function scan(overrides: Partial<OperationResponse> = {}): OperationResponse {
  return {
    id: "scan-op-1",
    kind: "scan_source",
    state: "queued",
    stage: "queued",
    bytes_completed: 0,
    created_at: "2026-09-30T00:00:00Z",
    updated_at: "2026-09-30T00:00:01Z",
    ...overrides,
  };
}

// Arm before the trigger: the listener must exist before the request it awaits.
function nextOperationRead(operationId: string) {
  return new Promise<void>((resolve) => {
    const listener = ({ request }: { request: Request }) => {
      const url = new URL(request.url);
      if (
        url.pathname !== `/api/operations/${operationId}` ||
        request.method !== "GET"
      )
        return;
      server.events.removeListener("response:mocked", listener);
      resolve();
    };
    server.events.on("response:mocked", listener);
  });
}

function startButton() {
  return screen.getByRole("button", { name: "Сканировать" });
}

// The real detail route of the sources screen: one root at its own address.
async function openDetail(fixture: SourceRootResponse) {
  window.location.hash = `/sources/${fixture.id}`;
  render(<SourcesScreen />);
  await screen.findByRole("heading", {
    level: 1,
    name: fixture.display_name,
  });
  if (fixture.enabled) {
    await waitFor(() => expect(startButton()).toBeEnabled());
  }
}

beforeEach(() => {
  TestEventSource.instances = [];
  TestEventSource.onCreated = undefined;
  vi.stubGlobal("EventSource", TestEventSource);
  window.location.hash = "";
  server.use(
    http.get("/api/operations", () => HttpResponse.json({ operations: [] })),
  );
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  window.location.hash = "";
});

describe("source page scan lifecycle with the published inventory", () => {
  it("starts a scan from the root page and refreshes the root and its inventory on success", async () => {
    let generation = 4;
    let published: SourceLocationResponse[] = [location()];
    let current = scan({ state: "running", stage: "traversing" });
    const rootReads: string[] = [];
    const listCursors: (string | null)[] = [];
    const scans = vi.fn();
    server.use(
      http.get("/api/sources/:sourceId", ({ request }) => {
        rootReads.push(new URL(request.url).pathname);
        return HttpResponse.json(
          root({
            scan_generation: generation,
            location_count: published.length,
          }),
        );
      }),
      http.get("/api/sources/:sourceId/locations", ({ request }) => {
        listCursors.push(new URL(request.url).searchParams.get("cursor"));
        return HttpResponse.json({ locations: published });
      }),
      http.post(scanUrl, () => {
        scans();
        return HttpResponse.json(current);
      }),
      http.get(`/api/operations/${current.id}`, () =>
        HttpResponse.json(current),
      ),
    );
    await openDetail(root());

    expect(
      await screen.findByRole("row", { name: /01 Открытие/ }),
    ).toBeVisible();
    expect(rootReads).toHaveLength(1);
    expect(listCursors).toEqual([null]);

    const connected = nextEventSource();
    fireEvent.click(startButton());

    expect(await screen.findByText(traversingText)).toBeVisible();
    const stream = await connected;
    expect(TestEventSource.instances).toHaveLength(1);
    expect(screen.getByRole("row", { name: /01 Открытие/ })).toBeVisible();
    expect(listCursors).toEqual([null]);

    const pendingStart = startButton();
    expect(pendingStart).toBeDisabled();
    fireEvent.click(pendingStart);
    fireEvent.click(pendingStart);
    expect(scans).toHaveBeenCalledTimes(1);

    generation = 5;
    published = [
      location(),
      location({ id: "loc-2", relative_path: "Альбом/02 Новый.flac" }),
    ];
    current = scan({ state: "succeeded", stage: "succeeded" });
    const finished = nextOperationRead(current.id);
    await act(async () => {
      stream.dispatchEvent(new Event("operation-changed"));
      await finished;
    });

    expect(await screen.findByText(doneText)).toBeVisible();
    expect(await screen.findByRole("row", { name: /02 Новый/ })).toBeVisible();
    expect(screen.getByRole("row", { name: /01 Открытие/ })).toBeVisible();
    expect(screen.getByText("Показано 2 файла.")).toBeVisible();
    expect(rootReads).toHaveLength(2);
    expect(listCursors).toEqual([null, null]);
  });

  it("keeps the published inventory on a failed scan and retries the same operation", async () => {
    let current = scan({ state: "running", stage: "traversing" });
    let published: SourceLocationResponse[] = [location()];
    const rootReads: string[] = [];
    const scans = vi.fn();
    const retries = vi.fn();
    server.use(
      http.get("/api/sources/:sourceId", ({ request }) => {
        rootReads.push(new URL(request.url).pathname);
        return HttpResponse.json(root({ location_count: published.length }));
      }),
      http.get("/api/sources/:sourceId/locations", () =>
        HttpResponse.json({ locations: published }),
      ),
      http.post(scanUrl, () => {
        scans();
        return HttpResponse.json(current);
      }),
      http.get(`/api/operations/${current.id}`, () =>
        HttpResponse.json(current),
      ),
      http.post(`/api/operations/${current.id}/retry`, () => {
        retries();
        return HttpResponse.json({ ...current, state: "queued" });
      }),
    );
    await openDetail(root());
    await screen.findByRole("row", { name: /01 Открытие/ });
    const connected = nextEventSource();
    fireEvent.click(startButton());
    await screen.findByText(traversingText);
    const stream = await connected;

    current = scan({
      state: "failed",
      stage: "traversing",
      safe_error: "The source tree could not be read. Retry the scan.",
    });
    const failed = nextOperationRead(current.id);
    await act(async () => {
      stream.dispatchEvent(new Event("operation-changed"));
      await failed;
    });

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The source tree could not be read. Retry the scan.",
    );
    expect(screen.getByRole("row", { name: /01 Открытие/ })).toBeVisible();
    expect(rootReads).toHaveLength(1);

    fireEvent.click(
      screen.getByRole("button", { name: "Повторить сканирование" }),
    );

    await waitFor(() => expect(retries).toHaveBeenCalledTimes(1));
    expect(await screen.findByText(queuedText)).toBeVisible();
    expect(scans).toHaveBeenCalledTimes(1);
    expect(TestEventSource.instances).toHaveLength(1);

    current = scan({ state: "succeeded", stage: "succeeded" });
    published = [
      location(),
      location({ id: "loc-2", relative_path: "Альбом/02 Новый.flac" }),
    ];
    const succeeded = nextOperationRead(current.id);
    await act(async () => {
      stream.dispatchEvent(new Event("operation-changed"));
      await succeeded;
    });

    expect(await screen.findByText(doneText)).toBeVisible();
    expect(await screen.findByRole("row", { name: /02 Новый/ })).toBeVisible();
    expect(rootReads).toHaveLength(2);
  });

  it("resets the listing to its first page when a scan publishes a new generation", async () => {
    let generation = 4;
    let firstPage: SourceLocationResponse[] = [location()];
    let nextCursor: string | undefined = "page-2";
    let current = scan({ state: "running", stage: "traversing" });
    const cursors: (string | null)[] = [];
    server.use(
      http.get("/api/sources/:sourceId", () =>
        HttpResponse.json(root({ scan_generation: generation })),
      ),
      http.get("/api/sources/:sourceId/locations", ({ request }) => {
        const cursor = new URL(request.url).searchParams.get("cursor");
        cursors.push(cursor);
        if (cursor === null) {
          return HttpResponse.json({
            locations: firstPage,
            next_cursor: nextCursor,
          });
        }
        return HttpResponse.json({
          locations: [
            location({ id: "loc-2", relative_path: "Альбом/02 Ответ.flac" }),
          ],
        });
      }),
      http.post(scanUrl, () => HttpResponse.json(current)),
      http.get(`/api/operations/${current.id}`, () =>
        HttpResponse.json(current),
      ),
    );
    await openDetail(root());
    expect(
      await screen.findByRole("row", { name: /01 Открытие/ }),
    ).toBeVisible();

    fireEvent.click(screen.getByRole("button", { name: "Показать ещё" }));

    expect(await screen.findByRole("row", { name: /02 Ответ/ })).toBeVisible();
    expect(screen.getByText("Показано 2 файла.")).toBeVisible();
    expect(cursors).toEqual([null, "page-2"]);

    const connected = nextEventSource();
    fireEvent.click(startButton());
    await screen.findByText(traversingText);
    const stream = await connected;

    firstPage = [
      location({ id: "loc-9", relative_path: "Альбом/09 Новый.flac" }),
    ];
    nextCursor = undefined;
    generation = 5;
    current = scan({ state: "succeeded", stage: "succeeded" });
    const finished = nextOperationRead(current.id);
    await act(async () => {
      stream.dispatchEvent(new Event("operation-changed"));
      await finished;
    });

    expect(await screen.findByRole("row", { name: /09 Новый/ })).toBeVisible();
    expect(
      screen.queryByRole("row", { name: /01 Открытие/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("row", { name: /02 Ответ/ }),
    ).not.toBeInTheDocument();
    expect(screen.getByText("Показано 1 файл.")).toBeVisible();
    expect(cursors).toEqual([null, "page-2", null]);
  });

  it("re-reads the REST snapshot after a stream error and clears the notice on reconnect", async () => {
    let current = scan({ state: "running", stage: "traversing" });
    const reads = vi.fn();
    server.use(
      http.get("/api/sources/:sourceId", () => HttpResponse.json(root())),
      http.get("/api/sources/:sourceId/locations", () =>
        HttpResponse.json({ locations: [location()] }),
      ),
      http.post(scanUrl, () => HttpResponse.json(current)),
      http.get("/api/operations/:operationId", () => {
        reads();
        return HttpResponse.json(current);
      }),
    );
    await openDetail(root());
    await screen.findByRole("row", { name: /01 Открытие/ });
    const connected = nextEventSource();
    fireEvent.click(startButton());
    await screen.findByText(traversingText);
    const stream = await connected;
    expect(screen.queryByText(reconnectText)).toBeNull();
    const readsBeforeError = reads.mock.calls.length;

    current = scan({ state: "running", stage: "applying" });
    const reread = nextOperationRead(current.id);
    await act(async () => {
      stream.dispatchEvent(new Event("error"));
      await reread;
    });

    expect(reads.mock.calls.length).toBeGreaterThan(readsBeforeError);
    expect(await screen.findByText(applyingText)).toBeVisible();
    expect(screen.getByText(reconnectText)).toBeVisible();

    const recovered = nextOperationRead(current.id);
    await act(async () => {
      stream.dispatchEvent(new Event("open"));
      await recovered;
    });

    await waitFor(() => expect(screen.queryByText(reconnectText)).toBeNull());
    expect(screen.getByRole("row", { name: /01 Открытие/ })).toBeVisible();
    expect(screen.getByText("Показано 1 файл.")).toBeVisible();
  });

  it("refuses a redundant scan click on a disabled root yet still lists its inventory", async () => {
    const scans = vi.fn();
    server.use(
      http.get("/api/sources/:sourceId", () =>
        HttpResponse.json(root({ enabled: false })),
      ),
      http.get("/api/sources/:sourceId/locations", () =>
        HttpResponse.json({ locations: [location()] }),
      ),
      http.post(scanUrl, () => {
        scans();
        return HttpResponse.json(scan());
      }),
    );
    await openDetail(root({ enabled: false }));

    expect(
      await screen.findByRole("row", { name: /01 Открытие/ }),
    ).toBeVisible();
    expect(
      screen.getByText(
        "Каталог выключен: новые сканирования запрещены, пока каталог не включён.",
      ),
    ).toBeVisible();

    const start = startButton();
    expect(start).toBeDisabled();
    fireEvent.click(start);

    expect(scans).not.toHaveBeenCalled();
    expect(TestEventSource.instances).toHaveLength(0);
    expect(screen.getByRole("row", { name: /01 Открытие/ })).toBeVisible();
  });
});
