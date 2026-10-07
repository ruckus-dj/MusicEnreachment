import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, beforeEach, vi } from "vitest";
import type {
  OperationResponse,
  SourceLocationDetailResponse,
} from "../../api/generated/client.schemas";
import { server } from "../../test/server";
import { SourceInspectorScreen } from "./SourceInspectorScreen";

export const detailPath = "/api/sources/root-1/locations/file-1";
export const stamp = "2026-10-01T10:20:00.123456Z";
export function detail(
  overrides: Partial<SourceLocationDetailResponse> = {},
): SourceLocationDetailResponse {
  return {
    root_id: "root-1",
    location_id: "file-1",
    relative_path: "album/01.flac",
    size_bytes: 1048576,
    mtime: stamp,
    analysis_state: "not_analyzed",
    probe_status: "audio",
    matching_eligible: false,
    steps: [],
    root: {
      enabled: true,
      stale: false,
      status: "available",
      inventory_path: "/srv/inbox",
    },
    ...overrides,
  };
}
export const result: NonNullable<SourceLocationDetailResponse["result"]> = {
  inspected_at: stamp,
  ffprobe_version: "ffprobe 8.0",
  analysis_policy_version: 1,
  applied_operation_id: "previous-operation",
  container: { name: "matroska", duration_ms: 10830000 },
  streams: [
    {
      index: 0,
      codec_name: "flac",
      sample_rate_hz: 96000,
      bits_per_sample: 24,
      channels: 2,
    },
    { index: 3, codec_name: "aac", profile: "LC", channels: 6 },
  ],
  tags: { ARTIST: ["First / artist; preserved", "Second"], TITLE: ["Title"] },
  raw_json: {
    format: { tags: { ARTIST: "First / artist; preserved" } },
    streams: [{ index: 4, codec_type: "video" }],
  },
};
export function operation(
  overrides: Partial<OperationResponse> = {},
): OperationResponse {
  return {
    id: "analysis-1",
    kind: "analyze_source",
    state: "queued",
    stage: "queued",
    bytes_completed: 0,
    created_at: stamp,
    updated_at: stamp,
    target_source_root_id: "root-1",
    target_source_location_id: "file-1",
    ...overrides,
  };
}

export class InspectorStream extends EventTarget {
  static instances: InspectorStream[] = [];
  readonly url: string;
  close = vi.fn();
  constructor(url: string) {
    super();
    this.url = url;
    InspectorStream.instances.push(this);
  }
}

// Observe the precise DOM state, not a polling interval or a fixed delay.
export async function observe(check: () => boolean) {
  await new Promise<void>((resolve, reject) => {
    const observer = new MutationObserver(finish);
    const timeout = setTimeout(() => {
      observer.disconnect();
      reject(new Error("Expected inspector DOM state was not reached"));
    }, 4000);
    function finish() {
      if (!check()) return;
      observer.disconnect();
      clearTimeout(timeout);
      resolve();
    }
    observer.observe(document.body, {
      childList: true,
      subtree: true,
      attributes: true,
    });
    finish();
  });
  await act(async () => {});
}

// Arm the exact HTTP response before the action that causes it.
export function responseFor(path: string, method = "GET", count = 1) {
  return new Promise<void>((resolve, reject) => {
    let received = 0;
    const timeout = setTimeout(() => {
      server.events.removeListener("response:mocked", listener);
      reject(new Error(`Missing ${method} ${path}`));
    }, 4000);
    function listener({ request }: { request: Request }) {
      if (new URL(request.url).pathname !== path || request.method !== method)
        return;
      received += 1;
      if (received < count) return;
      clearTimeout(timeout);
      server.events.removeListener("response:mocked", listener);
      resolve();
    }
    server.events.on("response:mocked", listener);
  });
}
export async function openInspector() {
  const loaded = responseFor(detailPath);
  render(<SourceInspectorScreen sourceId="root-1" locationId="file-1" />);
  await act(async () => {
    await loaded;
  });
  await observe(() => !screen.queryByText(/Загрузка файла/));
}
export function stream() {
  const value = InspectorStream.instances.at(-1);
  if (!value) throw new Error("Inspector subscription was not created");
  return value;
}
beforeEach(() => {
  InspectorStream.instances = [];
  vi.stubGlobal("EventSource", InspectorStream);
  server.use(http.get(detailPath, () => HttpResponse.json(detail())));
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
