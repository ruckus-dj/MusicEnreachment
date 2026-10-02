import { act, fireEvent, render, screen } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { vi } from "vitest";
import type {
  OperationResponse,
  SourceLocationResponse,
  SourceRootResponse,
} from "../../api/generated/client.schemas";
import { server } from "../../test/server";
import { SourcesScreen } from "./SourcesScreen";

export const rootPath = "/api/sources/root-refresh";
export const operationPath = "/api/operations/scan-refresh";
export const rootError = "The source directory is not readable.";
export const operationError =
  "The source tree could not be read. Retry the scan.";
export const fileError = "The audio stream could not be inspected.";

const availableRoot: SourceRootResponse = {
  id: "root-refresh",
  display_name: "Архив",
  configured_path: "/srv/archive",
  inventory_path: "/srv/archive",
  enabled: true,
  status: "available",
  stale: false,
  scan_generation: 4,
  location_count: 1,
  last_successful_scan_at: "2026-09-26T10:20:00Z",
  created_at: "2026-09-01T00:00:00Z",
  updated_at: "2026-09-26T10:20:00Z",
};
const runningOperation: OperationResponse = {
  id: "scan-refresh",
  kind: "scan_source",
  target_source_root_id: availableRoot.id,
  state: "running",
  stage: "traversing",
  bytes_completed: 0,
  created_at: "2026-10-01T00:00:00Z",
  updated_at: "2026-10-01T00:00:01Z",
};
export const savedLocation: SourceLocationResponse = {
  id: "saved-location",
  relative_path: "Album/01.flac",
  size_bytes: 1536,
  mtime: "2026-09-26T10:20:00Z",
  probe_status: "audio",
  has_result: false,
};

// Mutable server state models what REST publishes, not the component internals.
export function scanFixture() {
  const fixture = {
    root: { ...availableRoot },
    operation: { ...runningOperation },
    locations: [savedLocation],
    rootReads: 0,
    locationReads: 0,
    refused: false,
  };
  server.use(
    http.get("/api/sources", () =>
      HttpResponse.json({ sources: [fixture.root] }),
    ),
    http.get(rootPath, () => {
      fixture.rootReads += 1;
      return HttpResponse.json(fixture.root);
    }),
    http.get(`${rootPath}/locations`, () => {
      fixture.locationReads += 1;
      return HttpResponse.json({ locations: fixture.locations });
    }),
    http.get("/api/operations", () => HttpResponse.json({ operations: [] })),
    http.get(operationPath, () => HttpResponse.json(fixture.operation)),
    http.post(`${rootPath}/scan`, () =>
      fixture.refused
        ? HttpResponse.json({ detail: rootError }, { status: 400 })
        : HttpResponse.json(fixture.operation),
    ),
    http.post(`${operationPath}/retry`, () => {
      fixture.operation = {
        ...fixture.operation,
        state: "queued",
        stage: "queued",
        safe_error: undefined,
      };
      return HttpResponse.json(fixture.operation);
    }),
  );
  return fixture;
}

// Only EventSource is replaced; SSE wake-ups still use the real REST adapter.
export class ScanEventSource extends EventTarget {
  static onCreated: ((stream: ScanEventSource) => void) | undefined;
  readonly url: string;
  close = vi.fn();
  constructor(url: string) {
    super();
    this.url = url;
    ScanEventSource.onCreated?.(this);
  }
}

// Arm each exact signal before its trigger. Vitest's test timeout bounds awaits.
export function nextResponse(path: string, method = "GET") {
  return new Promise<void>((resolve) => {
    const listener = ({ request }: { request: Request }) => {
      if (request.method !== method || new URL(request.url).pathname !== path)
        return;
      server.events.removeListener("response:mocked", listener);
      resolve();
    };
    server.events.on("response:mocked", listener);
  });
}

export async function openDetail() {
  const root = nextResponse(rootPath);
  const locations = nextResponse(`${rootPath}/locations`);
  const discovery = nextResponse("/api/operations");
  window.location.hash = "/sources/root-refresh";
  const view = render(<SourcesScreen />);
  await act(async () => root);
  await act(async () => Promise.all([locations, discovery]));
  return view;
}

export async function startScan() {
  const connected = new Promise<ScanEventSource>((resolve) => {
    ScanEventSource.onCreated = (stream) => {
      ScanEventSource.onCreated = undefined;
      resolve(stream);
    };
  });
  const read = nextResponse(operationPath);
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "Сканировать" }));
  });
  await act(async () => read);
  return connected;
}

export async function changed(stream: ScanEventSource) {
  const operation = nextResponse(operationPath);
  const root = nextResponse(rootPath);
  const locations = nextResponse(`${rootPath}/locations`);
  await act(async () => {
    stream.dispatchEvent(new Event("operation-changed"));
    await operation;
  });
  await act(async () => root);
  await act(async () => locations);
}
