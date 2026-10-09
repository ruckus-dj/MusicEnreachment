import "@testing-library/jest-dom/vitest";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { subscribeToOperation } from "../../api/client/operations";
import type { OperationResponse } from "../../api/generated/client.schemas";
import { server } from "../../test/server";
import {
  isOperationSnapshotNewer,
  useSourceArtifactCleanup,
} from "./useSourceArtifactCleanup";

vi.mock("../../api/client/operations", () => ({
  subscribeToOperation: vi.fn(),
}));

const candidatesApi = "/api/source-analysis/artifacts/cleanup";
const operationApi = "/api/operations/cleanup-op";

function operation(
  overrides: Partial<OperationResponse> = {},
): OperationResponse {
  return {
    id: "cleanup-op",
    kind: "cleanup_source_analysis_artifacts",
    state: "queued",
    stage: "queued",
    bytes_completed: 0,
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
    ...overrides,
  };
}

afterEach(() => {
  cleanup();
});

describe("isOperationSnapshotNewer", () => {
  it("accepts the first snapshot and a snapshot for a new operation", () => {
    const first = operation();
    expect(isOperationSnapshotNewer(undefined, first)).toBe(true);
    expect(isOperationSnapshotNewer(first, operation({ id: "other-op" }))).toBe(
      true,
    );
  });

  it("rejects an older snapshot for the same operation", () => {
    const current = operation({ updated_at: "2026-10-01T00:02:00Z" });
    expect(
      isOperationSnapshotNewer(
        current,
        operation({ updated_at: "2026-10-01T00:01:00Z" }),
      ),
    ).toBe(false);
  });

  it("accepts a pending to terminal transition that shares the timestamp", () => {
    const current = operation({
      state: "running",
      updated_at: "2026-10-01T00:01:00Z",
    });
    expect(
      isOperationSnapshotNewer(
        current,
        operation({
          state: "succeeded",
          stage: "complete",
          updated_at: "2026-10-01T00:01:00Z",
        }),
      ),
    ).toBe(true);
  });

  it("never lets a terminal regress to a non-terminal snapshot at the same timestamp", () => {
    const current = operation({
      state: "succeeded",
      stage: "complete",
      updated_at: "2026-10-01T00:01:00Z",
    });
    expect(
      isOperationSnapshotNewer(
        current,
        operation({ state: "running", updated_at: "2026-10-01T00:01:00Z" }),
      ),
    ).toBe(false);
  });
});

describe("useSourceArtifactCleanup snapshot freshness", () => {
  beforeEach(() => {
    vi.mocked(subscribeToOperation).mockReset();
  });

  it("ignores a delayed older SSE snapshot after a terminal snapshot and refreshes candidates once", async () => {
    let candidateReads = 0;
    let captured: ((snapshot: OperationResponse) => void) | undefined;
    vi.mocked(subscribeToOperation).mockImplementation((_id, onSnapshot) => {
      captured = onSnapshot;
      return () => {};
    });
    const queued = operation({ updated_at: "2026-10-01T00:00:00Z" });
    const terminal = operation({
      state: "succeeded",
      stage: "complete",
      updated_at: "2026-10-01T00:02:00Z",
    });
    const older = operation({
      state: "running",
      stage: "deleting",
      updated_at: "2026-10-01T00:01:00Z",
    });
    server.use(
      http.get(candidatesApi, () => {
        candidateReads += 1;
        return HttpResponse.json({ candidates: [], count: 0 });
      }),
      http.post(candidatesApi, () =>
        HttpResponse.json(queued, { status: 202 }),
      ),
      http.get(operationApi, () => HttpResponse.json(queued)),
    );

    const { result } = renderHook(() => useSourceArtifactCleanup());
    await waitFor(() => expect(result.current.loading).toBe(false));
    await act(async () => {
      await result.current.start(["a"]);
    });
    await waitFor(() => expect(captured).toBeDefined());
    await waitFor(() => expect(result.current.isPending).toBe(true));

    act(() => {
      captured?.(terminal);
      captured?.(older);
    });

    expect(result.current.operation?.state).toBe("succeeded");
    expect(result.current.isPending).toBe(false);
    await waitFor(() => expect(candidateReads).toBe(2));
  });

  it("keeps the terminal view when a delayed older SSE snapshot arrives after the immediate REST read settled", async () => {
    let captured: ((snapshot: OperationResponse) => void) | undefined;
    vi.mocked(subscribeToOperation).mockImplementation((_id, onSnapshot) => {
      captured = onSnapshot;
      return () => {};
    });
    const queued = operation({ updated_at: "2026-10-01T00:00:00Z" });
    const terminal = operation({
      state: "succeeded",
      stage: "complete",
      updated_at: "2026-10-01T00:02:00Z",
    });
    server.use(
      http.get(candidatesApi, () =>
        HttpResponse.json({ candidates: [], count: 0 }),
      ),
      http.post(candidatesApi, () =>
        HttpResponse.json(queued, { status: 202 }),
      ),
      http.get(operationApi, () => HttpResponse.json(terminal)),
    );

    const { result } = renderHook(() => useSourceArtifactCleanup());
    await waitFor(() => expect(result.current.loading).toBe(false));
    await act(async () => {
      await result.current.start(["a"]);
    });
    await waitFor(() =>
      expect(result.current.operation?.state).toBe("succeeded"),
    );

    act(() => {
      captured?.(
        operation({ state: "running", updated_at: "2026-10-01T00:01:00Z" }),
      );
    });

    expect(result.current.operation?.state).toBe("succeeded");
    expect(result.current.isPending).toBe(false);
  });
});
