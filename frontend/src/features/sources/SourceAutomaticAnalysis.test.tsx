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
  SourceLocationDetailResponse,
  SourceLocationResponse,
  SourceRootResponse,
} from "../../api/generated/client.schemas";
import { server } from "../../test/server";
import { SourcesScreen } from "./SourcesScreen";
import { nextResponse } from "./sourceScanRefreshTestSupport";

const rootId = "root-1";
const locationId = "location-1";
const scanId = "scan-1";
const analysisId = "analysis-1";
const rootPath = `/api/sources/${rootId}`;
const scanPath = `${rootPath}/scan`;
const locationPath = `${rootPath}/locations/${locationId}`;
const stamp = "2026-10-01T10:20:00.123456Z";

class ControlledEventSource extends EventTarget {
  static instances: ControlledEventSource[] = [];
  readonly url: string;
  close = vi.fn();

  constructor(url: string) {
    super();
    this.url = url;
    ControlledEventSource.instances.push(this);
  }
}

function source(url: string) {
  const stream = ControlledEventSource.instances.find(
    (instance) => new URL(instance.url, window.location.href).pathname === url,
  );
  if (!stream) throw new Error(`No EventSource for ${url}`);
  return stream;
}

function root(generation: number, count: number): SourceRootResponse {
  return {
    id: rootId,
    display_name: "Входящие",
    configured_path: "/srv/inbox",
    processing_mode: "in_place",
    inventory_path: "/srv/inbox",
    enabled: true,
    status: "available",
    stale: false,
    scan_generation: generation,
    location_count: count,
    created_at: stamp,
    updated_at: stamp,
  };
}

const location: SourceLocationResponse = {
  id: locationId,
  relative_path: "Альбом/01 Открытие.flac",
  size_bytes: 1536,
  mtime: stamp,
  probe_status: "audio",
  has_result: false,
};

function operation(
  id: string,
  kind: OperationResponse["kind"],
  state: OperationResponse["state"],
  stage: string,
  overrides: Partial<OperationResponse> = {},
): OperationResponse {
  return {
    id,
    kind,
    state,
    stage,
    bytes_completed: 0,
    created_at: stamp,
    updated_at: stamp,
    target_source_root_id: rootId,
    ...(kind === "analyze_source"
      ? { target_source_location_id: locationId }
      : {}),
    ...overrides,
  };
}

function locationDetail(
  phase: "queued" | "running" | "succeeded",
): SourceLocationDetailResponse {
  const active = phase !== "succeeded";
  return {
    root_id: rootId,
    location_id: locationId,
    relative_path: location.relative_path,
    size_bytes: location.size_bytes,
    mtime: stamp,
    analysis_state: "analyzed",
    probe_status: "audio",
    matching_eligible: !active,
    active_fpcalc_version: "1.6.1",
    staged_artifact: {
      state: "unknown",
      requested_steps: [],
      requested_steps_known: false,
    },
    ...(active ? { active_analysis_operation_id: analysisId } : {}),
    result: {
      ffprobe_version: "ffprobe 8.0",
      analysis_policy_version: 1,
      applied_operation_id: scanId,
      inspected_at: stamp,
      container: { name: "flac" },
      streams: [{ index: 0, codec_name: "flac" }],
      tags: { TITLE: ["Automatic track"] },
      raw_json: { streams: [{ index: 0, codec_type: "audio" }] },
    },
    steps: [
      {
        name: "sha256",
        state: "succeeded",
        attempt: 1,
        sha256: { value: "automatic-digest" },
      },
      { name: "probe", state: "succeeded", attempt: 1 },
      {
        name: "fingerprint",
        state: phase,
        attempt: 1,
        ...(active
          ? {}
          : {
              fingerprint: {
                value: "automatic-fingerprint",
                version: "1.6.1",
                version_banner: "fpcalc version 1.6.1",
                algorithm_namespace: "chromaprint",
                algorithm_id: 1,
                duration: 120,
                calculated_at: stamp,
                applied_operation_id: analysisId,
                parser_contract_version: 1,
              },
            }),
      },
    ],
    root: {
      enabled: true,
      stale: false,
      status: "available",
      inventory_path: "/srv/inbox",
    },
  };
}

beforeEach(() => {
  ControlledEventSource.instances = [];
  vi.stubGlobal("EventSource", ControlledEventSource);
  window.location.hash = "";
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.location.hash = "";
});

describe("automatic source analysis after a successful scan", () => {
  it("queues and completes analysis for a newly inventoried location without a user analysis action", async () => {
    let generation = 0;
    let published: SourceLocationResponse[] = [];
    let scanSnapshot = operation(scanId, "scan_source", "queued", "queued");
    let analysisSnapshot = operation(
      analysisId,
      "analyze_source",
      "queued",
      "queued",
    );
    let analysisPhase: "queued" | "running" | "succeeded" = "queued";
    const scanRequests = vi.fn();
    const analysisStarts = vi.fn();
    const analysisDetailReads = vi.fn();

    server.use(
      http.get("/api/sources", () =>
        HttpResponse.json({ sources: [root(generation, published.length)] }),
      ),
      http.get(rootPath, () =>
        HttpResponse.json(root(generation, published.length)),
      ),
      http.get(`${rootPath}/locations`, () =>
        HttpResponse.json({ locations: published }),
      ),
      http.get("/api/operations", () => HttpResponse.json({ operations: [] })),
      http.post(scanPath, () => {
        scanRequests();
        return HttpResponse.json(scanSnapshot);
      }),
      http.get(`/api/operations/${scanId}`, () =>
        HttpResponse.json(scanSnapshot),
      ),
      http.get(`/api/operations/${analysisId}`, () =>
        HttpResponse.json(analysisSnapshot),
      ),
      http.get(locationPath, () => {
        analysisDetailReads();
        return HttpResponse.json(locationDetail(analysisPhase));
      }),
      http.post("*", () => {
        analysisStarts();
        return HttpResponse.json({}, { status: 500 });
      }),
    );

    // Open the real source detail through the SourcesScreen route.
    window.location.hash = `/sources/${rootId}`;
    render(<SourcesScreen />);
    await screen.findByRole("heading", { level: 1, name: "Входящие" });
    expect(screen.getByText("Не сканировался")).toBeVisible();

    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Сканировать" })).toBeEnabled(),
    );
    fireEvent.click(screen.getByRole("button", { name: "Сканировать" }));
    expect(
      await screen.findByText("Сканирование поставлено в очередь."),
    ).toBeVisible();
    await waitFor(() => expect(scanRequests).toHaveBeenCalledTimes(1));
    const scanStream = await waitFor(() =>
      source(`/api/operations/${scanId}/events`),
    );

    // The successful scan publishes a new generation and its fresh location.
    generation = 1;
    published = [location];
    scanSnapshot = operation(scanId, "scan_source", "succeeded", "succeeded");
    const scanRead = nextResponse(`/api/operations/${scanId}`);
    const refreshedRoot = nextResponse(rootPath);
    const refreshedLocations = nextResponse(`${rootPath}/locations`);
    await act(async () => {
      scanStream.dispatchEvent(new Event("operation-changed"));
      await scanRead;
    });
    await act(async () => Promise.all([refreshedRoot, refreshedLocations]));
    fireEvent.click(
      await screen.findByRole("link", { name: location.relative_path }),
    );

    // The location's active analysis ID is already persisted in REST; opening
    // the inspector must observe it rather than issue a new analysis request.
    expect(
      await screen.findByRole("heading", { name: "Инспектор файла" }),
    ).toBeVisible();
    expect(
      await screen.findByText("Этап анализа поставлен в очередь."),
    ).toBeVisible();
    const analysisStream = await waitFor(() =>
      source(`/api/operations/${analysisId}/events`),
    );
    expect(analysisStarts).not.toHaveBeenCalled();
    expect(
      screen.queryByRole("button", { name: /анализировать/i }),
    ).not.toBeInTheDocument();

    // REST operation and detail snapshots advance only after payload-free SSE
    // wake-ups; they remain the source of truth for the inspector UI.
    analysisSnapshot = operation(
      analysisId,
      "analyze_source",
      "running",
      "fingerprinting",
    );
    analysisPhase = "running";
    const runningOperation = nextResponse(`/api/operations/${analysisId}`);
    const runningDetail = nextResponse(locationPath);
    await act(async () => {
      analysisStream.dispatchEvent(new Event("operation-changed"));
      await Promise.all([runningOperation, runningDetail]);
    });
    expect(await screen.findByText("Состояние: Выполняется")).toBeVisible();

    analysisSnapshot = operation(
      analysisId,
      "analyze_source",
      "succeeded",
      "succeeded",
    );
    analysisPhase = "succeeded";
    const succeededOperation = nextResponse(`/api/operations/${analysisId}`);
    const succeededDetail = nextResponse(locationPath);
    await act(async () => {
      analysisStream.dispatchEvent(new Event("operation-changed"));
      await Promise.all([succeededOperation, succeededDetail]);
    });

    expect(await screen.findByText("automatic-fingerprint")).toBeVisible();
    expect(screen.getByText("automatic-digest")).toBeVisible();
    expect(screen.getAllByText("Состояние: Завершено")).toHaveLength(3);
    expect(analysisDetailReads).toHaveBeenCalled();
    expect(analysisStarts).not.toHaveBeenCalled();
  });
});
