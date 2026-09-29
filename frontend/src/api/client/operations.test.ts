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
import { server } from "../../test/server";
import type { OperationResponse } from "../generated/client.schemas";
import { subscribeToOperation } from "./operations";

const operationId = "76092c63-0e0f-4dc1-af39-674dc1ce037c";
const snapshot: OperationResponse = {
  id: operationId,
  kind: "install",
  state: "running",
  stage: "download",
  bytes_completed: 12,
  bytes_total: 100,
  created_at: "2026-09-28T00:00:00Z",
  updated_at: "2026-09-28T00:00:01Z",
};

// Only the browser event source is controlled; snapshots use the real Orval
// client and HTTP interception. Each test has Vitest's bounded async timeout.
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

beforeEach(() => {
  TestEventSource.instances = [];
  vi.stubGlobal("EventSource", TestEventSource);
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

function watch() {
  const onSnapshot = vi.fn<(value: OperationResponse) => void>();
  const onError = vi.fn<(error: Error) => void>();
  const close = subscribeToOperation(operationId, onSnapshot, onError);
  onTestFinished(close);
  const source = TestEventSource.instances[0];
  if (!source) throw new Error("EventSource was not created");
  return { source, onSnapshot, onError, close };
}

function snapshotAfter(watcher: ReturnType<typeof watch>, event: Event) {
  const received = new Promise<OperationResponse>((resolve) => {
    watcher.onSnapshot.mockImplementationOnce(resolve);
  });
  watcher.source.dispatchEvent(event);
  return received;
}

describe("operation SSE snapshots", () => {
  it("reads REST on initial open, reconnect, and named wake-ups", async () => {
    let current = snapshot;
    const requests: Request[] = [];
    server.use(
      http.get(`/api/operations/${operationId}`, ({ request }) => {
        requests.push(request);
        return HttpResponse.json(current);
      }),
    );
    const watcher = watch();

    expect(watcher.source.url).toBe(`/api/operations/${operationId}/events`);
    expect(await snapshotAfter(watcher, new Event("open"))).toEqual(snapshot);
    watcher.source.dispatchEvent(new Event("error"));
    current = { ...snapshot, bytes_completed: 70 };
    expect(await snapshotAfter(watcher, new Event("open"))).toEqual(current);
    current = { ...snapshot, state: "succeeded", bytes_completed: 100 };
    expect(
      await snapshotAfter(
        watcher,
        new MessageEvent("operation-changed", { data: '{"state":"failed"}' }),
      ),
    ).toEqual(current);

    expect(TestEventSource.instances).toHaveLength(1);
    expect(watcher.source.close).not.toHaveBeenCalled();
    expect(watcher.onError).toHaveBeenCalledTimes(1);
    expect(requests.map((request) => request.cache)).toEqual([
      "no-store",
      "no-store",
      "no-store",
    ]);
  });

  it.each([
    "http",
    "network",
  ])("reports %s snapshot failure and recovers on reconnect", async (failure) => {
    server.use(
      http.get(`/api/operations/${operationId}`, () =>
        failure === "http"
          ? HttpResponse.json({ status: 503 }, { status: 503 })
          : HttpResponse.error(),
      ),
    );
    const watcher = watch();
    const failed = new Promise<Error>((resolve) => {
      watcher.onError.mockImplementationOnce(resolve);
    });

    watcher.source.dispatchEvent(new Event("open"));
    expect(await failed).toBeInstanceOf(Error);
    expect(watcher.onSnapshot).not.toHaveBeenCalled();
    watcher.source.dispatchEvent(new Event("error"));
    server.use(
      http.get(`/api/operations/${operationId}`, () =>
        HttpResponse.json(snapshot),
      ),
    );

    expect(await snapshotAfter(watcher, new Event("open"))).toEqual(snapshot);
  });

  it("aborts an outdated snapshot when a new wake-up arrives", async () => {
    let release = () => {};
    let count = 0;
    const started = new Promise<Request>((resolve) => {
      server.use(
        http.get(`/api/operations/${operationId}`, ({ request }) => {
          count += 1;
          if (count > 1)
            return HttpResponse.json({ ...snapshot, bytes_completed: 90 });
          return new Promise<Response>((respond) => {
            release = () => respond(HttpResponse.json(snapshot));
            resolve(request);
          });
        }),
      );
    });
    onTestFinished(() => release());
    const watcher = watch();
    watcher.source.dispatchEvent(new Event("open"));
    const oldRequest = await started;

    const latest = await snapshotAfter(watcher, new Event("operation-changed"));

    expect(oldRequest.signal.aborted).toBe(true);
    expect(latest.bytes_completed).toBe(90);
    expect(watcher.onSnapshot).toHaveBeenCalledTimes(1);
    expect(watcher.onError).not.toHaveBeenCalled();
  });

  it("closes the stream, aborts pending REST, and removes event listeners", async () => {
    let release = () => {};
    const started = new Promise<Request>((resolve) => {
      server.use(
        http.get(
          `/api/operations/${operationId}`,
          ({ request }) =>
            new Promise<Response>((respond) => {
              release = () => respond(HttpResponse.json(snapshot));
              resolve(request);
            }),
        ),
      );
    });
    onTestFinished(() => release());
    const watcher = watch();
    watcher.source.dispatchEvent(new Event("open"));
    const request = await started;
    const aborted = new Promise<void>((resolve) => {
      request.signal.addEventListener("abort", () => resolve(), { once: true });
    });

    watcher.close();
    await aborted;
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    watcher.source.dispatchEvent(new Event("open"));
    watcher.source.dispatchEvent(new Event("operation-changed"));
    watcher.source.dispatchEvent(new Event("error"));

    expect(fetchSpy).not.toHaveBeenCalled();
    expect(watcher.source.close).toHaveBeenCalled();
    expect(watcher.onSnapshot).not.toHaveBeenCalled();
    expect(watcher.onError).not.toHaveBeenCalled();
  });
});
