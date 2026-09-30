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
import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  onTestFinished,
  vi,
} from "vitest";
import type {
  OperationResponse,
  SourceRootResponse,
} from "../../api/generated/client.schemas";
import { server } from "../../test/server";
import { SourceScanControl } from "./SourceScanControl";

const rootId = "root-1";
const queuedText = "Сканирование поставлено в очередь.";
const traversingText =
  "Обход каталога: чтение дерева и проверка аудиопотока файлов.";
const applyingText =
  "Применение результатов: подтверждённый инвентарь сохраняется.";
const doneText =
  "Сканирование завершено: инвентарь обновлён последним успешным обходом.";
const reconnectText = /потоку событий прерваны/;

// Only the browser event source is controlled; snapshots travel through the
// real Orval client and intercepted HTTP.
class TestEventSource extends EventTarget {
  static instances: TestEventSource[] = [];
  readonly url: string;
  close = vi.fn();

  constructor(url: string) {
    super();
    this.url = url;
    TestEventSource.instances.push(this);
  }
}

function root(overrides: Partial<SourceRootResponse> = {}): SourceRootResponse {
  return {
    id: rootId,
    display_name: "Входящие",
    configured_path: "/srv/inbox",
    enabled: true,
    status: "available",
    stale: false,
    scan_generation: 3,
    location_count: 5,
    inventory_path: "/srv/inbox",
    last_successful_scan_at: "2026-09-26T10:20:00Z",
    created_at: "2026-09-01T00:00:00Z",
    updated_at: "2026-09-26T10:20:00Z",
    ...overrides,
  };
}

function scan(overrides: Partial<OperationResponse> = {}): OperationResponse {
  return {
    id: "scan-op-1",
    kind: "scan_source",
    state: "queued",
    stage: "queued",
    target_source_root_id: rootId,
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

// The control lists operations on mount; discovery must settle before the Scan
// button becomes usable, so tests arm this before rendering.
function nextOperationLists(count = 1) {
  return new Promise<void>((resolve) => {
    let seen = 0;
    const listener = ({ request }: { request: Request }) => {
      const url = new URL(request.url);
      if (url.pathname !== "/api/operations" || request.method !== "GET")
        return;
      seen += 1;
      if (seen < count) return;
      server.events.removeListener("response:mocked", listener);
      resolve();
    };
    server.events.on("response:mocked", listener);
  });
}

beforeEach(() => {
  TestEventSource.instances = [];
  vi.stubGlobal("EventSource", TestEventSource);
  // No scan is active unless a test says otherwise.
  server.use(
    http.get("/api/operations", () => HttpResponse.json({ operations: [] })),
  );
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

async function renderControl(
  props: Partial<{
    root: SourceRootResponse;
    onScanCompleted: () => void | Promise<void>;
  }> = {},
) {
  const listed = nextOperationLists();
  const view = render(
    <SourceScanControl
      root={props.root ?? root()}
      onScanCompleted={props.onScanCompleted ?? vi.fn()}
    />,
  );
  await act(async () => {
    await listed;
  });
  return view;
}

function startButton() {
  return screen.getByRole("button", { name: "Сканировать" });
}

describe("source scan lifecycle", () => {
  it("starts a scan on demand and follows queued, traversing and applying", async () => {
    let current = scan();
    const scans = vi.fn();
    server.use(
      http.post(`/api/sources/${rootId}/scan`, () => {
        scans();
        return HttpResponse.json(current);
      }),
      http.get(`/api/operations/${current.id}`, () =>
        HttpResponse.json(current),
      ),
    );
    await renderControl();

    expect(TestEventSource.instances).toHaveLength(0);
    expect(scans).not.toHaveBeenCalled();

    fireEvent.click(startButton());

    expect(await screen.findByText(queuedText)).toBeVisible();
    expect(scans).toHaveBeenCalledTimes(1);
    expect(TestEventSource.instances).toHaveLength(1);
    const stream = TestEventSource.instances[0];
    expect(stream.url).toBe(`/api/operations/${current.id}/events`);

    current = scan({ state: "running", stage: "traversing" });
    await act(async () => {
      stream.dispatchEvent(new Event("operation-changed"));
    });
    expect(await screen.findByText(traversingText)).toBeVisible();

    current = scan({ state: "running", stage: "applying" });
    await act(async () => {
      stream.dispatchEvent(new Event("open"));
    });
    expect(await screen.findByText(applyingText)).toBeVisible();
  });

  it("refreshes the parent once per successful scan", async () => {
    const onScanCompleted = vi.fn();
    let current = scan({ state: "running", stage: "traversing" });
    server.use(
      http.post(`/api/sources/${rootId}/scan`, () =>
        HttpResponse.json(current),
      ),
      http.get(`/api/operations/${current.id}`, () =>
        HttpResponse.json(current),
      ),
    );
    await renderControl({ onScanCompleted });
    fireEvent.click(startButton());
    await screen.findByText(traversingText);
    const stream = TestEventSource.instances[0];

    current = scan({ state: "succeeded", stage: "succeeded" });
    const finished = nextOperationRead(current.id);
    await act(async () => {
      stream.dispatchEvent(new Event("operation-changed"));
      await finished;
    });
    expect(await screen.findByText(doneText)).toBeVisible();
    await waitFor(() => expect(onScanCompleted).toHaveBeenCalledTimes(1));

    const repeated = nextOperationRead(current.id);
    await act(async () => {
      stream.dispatchEvent(new Event("operation-changed"));
      await repeated;
    });
    expect(onScanCompleted).toHaveBeenCalledTimes(1);
  });

  it("never renders a byte count or a percentage of the scan", async () => {
    const current = scan({
      state: "running",
      stage: "traversing",
      bytes_completed: 4096,
      bytes_total: 100,
    });
    server.use(
      http.post(`/api/sources/${rootId}/scan`, () =>
        HttpResponse.json(current),
      ),
      http.get(`/api/operations/${current.id}`, () =>
        HttpResponse.json(current),
      ),
    );
    await renderControl();
    fireEvent.click(startButton());

    const status = await screen.findByText(traversingText);
    expect(status).toHaveTextContent(traversingText);
    expect(screen.queryByText(/%/)).toBeNull();
    expect(screen.queryByText(/4096/)).toBeNull();
  });

  it("does not start a scan for a disabled root", async () => {
    const scans = vi.fn();
    server.use(
      http.post(`/api/sources/${rootId}/scan`, () => {
        scans();
        return HttpResponse.json(scan());
      }),
    );
    await renderControl({ root: root({ enabled: false }) });

    expect(startButton()).toBeDisabled();
    fireEvent.click(startButton());

    expect(
      screen.getByText(
        "Каталог выключен: новые сканирования запрещены, пока каталог не включён.",
      ),
    ).toBeVisible();
    expect(scans).not.toHaveBeenCalled();
    expect(TestEventSource.instances).toHaveLength(0);
  });

  it("keeps a single scan request while the operation is pending", async () => {
    const scans = vi.fn();
    const current = scan({ state: "running", stage: "traversing" });
    server.use(
      http.post(`/api/sources/${rootId}/scan`, () => {
        scans();
        return HttpResponse.json(current);
      }),
      http.get(`/api/operations/${current.id}`, () =>
        HttpResponse.json(current),
      ),
    );
    await renderControl();
    fireEvent.click(startButton());
    await screen.findByText(traversingText);

    expect(startButton()).toBeDisabled();
    fireEvent.click(startButton());
    fireEvent.click(startButton());

    expect(scans).toHaveBeenCalledTimes(1);
  });

  it("reports an active-scan conflict safely", async () => {
    const scans = vi.fn();
    server.use(
      http.post(`/api/sources/${rootId}/scan`, () => {
        scans();
        return HttpResponse.json(
          { detail: "source root has an active scan" },
          { status: 409 },
        );
      }),
    );
    await renderControl();
    fireEvent.click(startButton());

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Сканирование уже выполняется");
    expect(alert).not.toHaveTextContent("active scan");
    expect(startButton()).not.toBeDisabled();
    expect(scans).toHaveBeenCalledTimes(1);
    expect(TestEventSource.instances).toHaveLength(0);
  });

  it("retries the same failed operation without starting a new scan", async () => {
    const scans = vi.fn();
    const retries = vi.fn();
    let current = scan({ state: "running", stage: "traversing" });
    server.use(
      http.post(`/api/sources/${rootId}/scan`, () => {
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
    await renderControl();
    fireEvent.click(startButton());
    await screen.findByText(traversingText);
    const stream = TestEventSource.instances[0];

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

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(
      "The source tree could not be read. Retry the scan.",
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Повторить сканирование" }),
    );

    await waitFor(() => expect(retries).toHaveBeenCalledTimes(1));
    expect(await screen.findByText(queuedText)).toBeVisible();
    expect(scans).toHaveBeenCalledTimes(1);
    expect(TestEventSource.instances).toHaveLength(1);
    expect(TestEventSource.instances[0]).toBe(stream);
  });

  it("re-subscribes for a new scan and closes the previous stream", async () => {
    let current = scan({
      id: "scan-op-1",
      state: "succeeded",
      stage: "succeeded",
    });
    server.use(
      http.post(`/api/sources/${rootId}/scan`, () =>
        HttpResponse.json(current),
      ),
      http.get("/api/operations/:operationId", () =>
        HttpResponse.json(current),
      ),
    );
    await renderControl();
    fireEvent.click(startButton());
    await screen.findByText(doneText);
    const first = TestEventSource.instances[0];

    current = scan({ id: "scan-op-2", state: "queued", stage: "queued" });
    fireEvent.click(startButton());

    expect(await screen.findByText(queuedText)).toBeVisible();
    await waitFor(() => expect(TestEventSource.instances).toHaveLength(2));
    expect(first.close).toHaveBeenCalled();
    expect(TestEventSource.instances[1].url).toBe(
      "/api/operations/scan-op-2/events",
    );
  });

  it("re-reads REST on a stream error and shows the notice from known state", async () => {
    let current = scan({ state: "running", stage: "traversing" });
    const reads = vi.fn();
    server.use(
      http.post(`/api/sources/${rootId}/scan`, () =>
        HttpResponse.json(current),
      ),
      http.get(`/api/operations/${current.id}`, () => {
        reads();
        return HttpResponse.json(current);
      }),
    );
    await renderControl();

    expect(TestEventSource.instances).toHaveLength(0);
    expect(screen.queryByText(reconnectText)).toBeNull();

    fireEvent.click(startButton());
    await screen.findByText(traversingText);
    const stream = TestEventSource.instances[0];
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
  });

  it("drops the previous scan when the root changes", async () => {
    const current = scan({ state: "running", stage: "traversing" });
    server.use(
      http.post(`/api/sources/${rootId}/scan`, () =>
        HttpResponse.json(current),
      ),
      http.get(`/api/operations/${current.id}`, () =>
        HttpResponse.json(current),
      ),
    );
    const view = await renderControl();
    fireEvent.click(startButton());
    await screen.findByText(traversingText);
    const first = TestEventSource.instances[0];

    const relisted = nextOperationLists();
    view.rerender(
      <SourceScanControl
        root={root({ id: "root-2" })}
        onScanCompleted={vi.fn()}
      />,
    );
    await act(async () => {
      await relisted;
    });

    await waitFor(() => expect(first.close).toHaveBeenCalled());
    expect(TestEventSource.instances).toHaveLength(1);
    expect(screen.queryByText(traversingText)).toBeNull();
    expect(startButton()).not.toBeDisabled();
  });

  it("closes the stream and aborts the pending read on unmount", async () => {
    const current = scan();
    let release = () => {};
    const started = new Promise<Request>((resolve) => {
      server.use(
        http.get(`/api/operations/${current.id}`, ({ request }) => {
          resolve(request);
          return new Promise<Response>((respond) => {
            release = () => respond(HttpResponse.json(current));
          });
        }),
      );
    });
    onTestFinished(() => release());
    server.use(
      http.post(`/api/sources/${rootId}/scan`, () =>
        HttpResponse.json(current),
      ),
    );

    const view = await renderControl();
    fireEvent.click(startButton());
    const request = await started;
    const stream = TestEventSource.instances[0];
    expect(stream.url).toBe(`/api/operations/${current.id}/events`);
    const aborted = new Promise<void>((resolve) => {
      request.signal.addEventListener("abort", () => resolve(), { once: true });
    });

    view.unmount();

    await aborted;
    expect(stream.close).toHaveBeenCalled();
    expect(request.signal.aborted).toBe(true);
  });

  it("adopts the scan already running for the root without starting another", async () => {
    const scans = vi.fn();
    const current = scan({ state: "running", stage: "traversing" });
    server.use(
      http.get("/api/operations", () =>
        HttpResponse.json({ operations: [current] }),
      ),
      http.get(`/api/operations/${current.id}`, () =>
        HttpResponse.json(current),
      ),
      http.post(`/api/sources/${rootId}/scan`, () => {
        scans();
        return HttpResponse.json(current);
      }),
    );
    await renderControl();

    expect(await screen.findByText(traversingText)).toBeVisible();
    expect(startButton()).toBeDisabled();
    expect(scans).not.toHaveBeenCalled();
    expect(TestEventSource.instances).toHaveLength(1);
    expect(TestEventSource.instances[0].url).toBe(
      `/api/operations/${current.id}/events`,
    );
  });

  it("ignores other roots, finished scans and non-scan operations", async () => {
    server.use(
      http.get("/api/operations", () =>
        HttpResponse.json({
          operations: [
            scan({ id: "other-root", target_source_root_id: "root-2" }),
            scan({ id: "old-failed", state: "failed", stage: "traversing" }),
            scan({ id: "old-done", state: "succeeded", stage: "succeeded" }),
            {
              id: "install-op",
              kind: "install",
              state: "running",
              stage: "download",
              target_installation_id: "installation-1",
              bytes_completed: 0,
              created_at: "2026-09-30T00:00:00Z",
              updated_at: "2026-09-30T00:00:01Z",
            },
            {
              id: "move-op",
              kind: "move_tools_root",
              state: "queued",
              stage: "queued",
              bytes_completed: 0,
              created_at: "2026-09-30T00:00:00Z",
              updated_at: "2026-09-30T00:00:01Z",
            },
          ],
        }),
      ),
    );
    await renderControl();

    expect(startButton()).not.toBeDisabled();
    expect(TestEventSource.instances).toHaveLength(0);
    expect(screen.queryByText(traversingText)).toBeNull();
  });

  it("adopts only the active scan that targets each rendered root", async () => {
    const first = scan({
      id: "scan-root-1",
      state: "running",
      stage: "traversing",
    });
    const second = scan({
      id: "scan-root-2",
      state: "running",
      stage: "applying",
      target_source_root_id: "root-2",
    });
    server.use(
      http.get("/api/operations", () =>
        HttpResponse.json({ operations: [first, second] }),
      ),
      http.get(`/api/operations/${first.id}`, () => HttpResponse.json(first)),
      http.get(`/api/operations/${second.id}`, () => HttpResponse.json(second)),
    );
    const listed = nextOperationLists(2);
    render(
      <>
        <SourceScanControl root={root()} onScanCompleted={vi.fn()} />
        <SourceScanControl
          root={root({ id: "root-2" })}
          onScanCompleted={vi.fn()}
        />
      </>,
    );
    await act(async () => {
      await listed;
    });

    expect(await screen.findByText(traversingText)).toBeVisible();
    expect(await screen.findByText(applyingText)).toBeVisible();
    expect(
      TestEventSource.instances.map((source) => source.url).sort(),
    ).toEqual([
      "/api/operations/scan-root-1/events",
      "/api/operations/scan-root-2/events",
    ]);
    for (const button of screen.getAllByRole("button", {
      name: "Сканировать",
    })) {
      expect(button).toBeDisabled();
    }
  });

  it("re-enables the controls once the adopted scan succeeds", async () => {
    const scans = vi.fn();
    const onScanCompleted = vi.fn();
    let current = scan({ state: "running", stage: "traversing" });
    server.use(
      http.get("/api/operations", () =>
        HttpResponse.json({ operations: [current] }),
      ),
      http.get(`/api/operations/${current.id}`, () =>
        HttpResponse.json(current),
      ),
      http.post(`/api/sources/${rootId}/scan`, () => {
        scans();
        return HttpResponse.json(current);
      }),
    );
    await renderControl({ onScanCompleted });
    await screen.findByText(traversingText);
    const stream = TestEventSource.instances[0];

    current = scan({ state: "succeeded", stage: "succeeded" });
    const finished = nextOperationRead(current.id);
    await act(async () => {
      stream.dispatchEvent(new Event("operation-changed"));
      await finished;
    });

    expect(await screen.findByText(doneText)).toBeVisible();
    await waitFor(() => expect(onScanCompleted).toHaveBeenCalledTimes(1));
    expect(startButton()).not.toBeDisabled();
    expect(scans).not.toHaveBeenCalled();
  });

  it("shows the adopted scan failure safely and offers a retry", async () => {
    const scans = vi.fn();
    let current = scan({ state: "running", stage: "traversing" });
    server.use(
      http.get("/api/operations", () =>
        HttpResponse.json({ operations: [current] }),
      ),
      http.get(`/api/operations/${current.id}`, () =>
        HttpResponse.json(current),
      ),
      http.post(`/api/sources/${rootId}/scan`, () => {
        scans();
        return HttpResponse.json(current);
      }),
    );
    await renderControl();
    await screen.findByText(traversingText);
    const stream = TestEventSource.instances[0];

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

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(
      "The source tree could not be read. Retry the scan.",
    );
    expect(
      screen.getByRole("button", { name: "Повторить сканирование" }),
    ).toBeVisible();
    expect(startButton()).not.toBeDisabled();
    expect(scans).not.toHaveBeenCalled();
  });

  it("shows a safe discovery failure and keeps the backend refusal", async () => {
    const scans = vi.fn();
    server.use(
      http.get("/api/operations", () =>
        HttpResponse.json({ detail: "internal trace secret" }, { status: 500 }),
      ),
      http.post(`/api/sources/${rootId}/scan`, () => {
        scans();
        return HttpResponse.json(
          { detail: "source root has an active scan" },
          { status: 409 },
        );
      }),
    );
    await renderControl();

    const notice = screen.getByText(
      /Не удалось проверить активное сканирование/,
    );
    expect(notice).toBeVisible();
    expect(notice).not.toHaveTextContent("internal trace secret");
    expect(startButton()).not.toBeDisabled();

    fireEvent.click(startButton());
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Сканирование уже выполняется");
    expect(alert).not.toHaveTextContent("active scan");
    expect(scans).toHaveBeenCalledTimes(1);
    expect(TestEventSource.instances).toHaveLength(0);
  });

  it("aborts the discovery request on unmount and on a root change", async () => {
    let resolveFirst = (_request: Request) => {};
    const firstRequest = new Promise<Request>((resolve) => {
      resolveFirst = resolve;
    });
    let resolveSecond = (_request: Request) => {};
    const secondRequest = new Promise<Request>((resolve) => {
      resolveSecond = resolve;
    });
    let seen = 0;
    server.use(
      http.get("/api/operations", ({ request }) => {
        seen += 1;
        if (seen === 1) resolveFirst(request);
        else resolveSecond(request);
        return new Promise<Response>(() => {});
      }),
    );

    const view = render(
      <SourceScanControl root={root()} onScanCompleted={vi.fn()} />,
    );
    const pendingFirst = await firstRequest;
    expect(startButton()).toBeDisabled();

    view.rerender(
      <SourceScanControl
        root={root({ id: "root-2" })}
        onScanCompleted={vi.fn()}
      />,
    );
    expect(pendingFirst.signal.aborted).toBe(true);

    const pendingSecond = await secondRequest;
    expect(startButton()).toBeDisabled();

    view.unmount();
    expect(pendingSecond.signal.aborted).toBe(true);
  });
});
