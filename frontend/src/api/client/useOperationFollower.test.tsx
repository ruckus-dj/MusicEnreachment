import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { server } from "../../test/server";
import type { OperationResponse } from "../generated/client.schemas";
import { subscribeToOperation } from "./operations";
import {
  isOperationSnapshotNewer,
  useOperationFollower,
} from "./useOperationFollower";

vi.mock("./operations", () => ({
  subscribeToOperation: vi.fn(),
}));

function operation(
  overrides: Partial<OperationResponse> = {},
): OperationResponse {
  return {
    id: "operation-a",
    kind: "test",
    state: "running",
    stage: "work",
    bytes_completed: 0,
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:01Z",
    ...overrides,
  };
}

afterEach(cleanup);

beforeEach(() => {
  vi.mocked(subscribeToOperation).mockReset();
});

describe("operation follower", () => {
  it("reads REST immediately and accepts only newer REST snapshots from the stream", async () => {
    let onSnapshot: ((snapshot: OperationResponse) => void) | undefined;
    vi.mocked(subscribeToOperation).mockImplementation((_id, snapshot) => {
      onSnapshot = snapshot;
      return vi.fn();
    });
    const read = vi.fn();
    const current = operation();
    server.use(
      http.get(`/api/operations/${current.id}`, () => {
        read();
        return HttpResponse.json(current);
      }),
    );
    const onAccepted = vi.fn();

    renderHook(() =>
      useOperationFollower(current.id, { onSnapshot: onAccepted }),
    );

    await waitFor(() => expect(read).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(onAccepted).toHaveBeenCalledTimes(1));
    expect(onAccepted).toHaveBeenCalledWith(current, "initial");

    const terminal = operation({
      state: "succeeded",
      updated_at: "2026-10-01T00:00:02Z",
    });
    const older = operation({
      updated_at: "2026-10-01T00:00:01Z",
    });
    act(() => {
      onSnapshot?.(terminal);
      onSnapshot?.(older);
    });

    expect(onAccepted).toHaveBeenLastCalledWith(terminal, "stream");
    expect(onAccepted).toHaveBeenCalledTimes(2);
  });

  it("re-arms a terminal operation for an explicit retry action", async () => {
    vi.mocked(subscribeToOperation).mockReturnValue(vi.fn());
    const failed = operation({ state: "failed" });
    server.use(
      http.get(`/api/operations/${failed.id}`, () => HttpResponse.json(failed)),
    );
    const onAccepted = vi.fn();
    const { result } = renderHook(() =>
      useOperationFollower(failed.id, {
        initialSnapshot: failed,
        onSnapshot: onAccepted,
      }),
    );

    const retried = operation({ state: "queued" });
    act(() => {
      expect(result.current.acceptSnapshot(retried, "action", true)).toBe(true);
      expect(result.current.acceptSnapshot(failed, "stream")).toBe(false);
    });

    expect(onAccepted).toHaveBeenCalledWith(retried, "action");
    expect(onAccepted).toHaveBeenCalledTimes(1);
  });

  it("aborts the old operation read when the ID changes and on unmount", async () => {
    vi.mocked(subscribeToOperation).mockReturnValue(vi.fn());
    const requests = new Map<string, Request>();
    server.use(
      http.get("/api/operations/:id", ({ params, request }) => {
        requests.set(String(params.id), request);
        return new Promise<Response>(() => {});
      }),
    );
    const { rerender, unmount } = renderHook(
      ({ id }: { id: string }) =>
        useOperationFollower(id, { onSnapshot: vi.fn() }),
      { initialProps: { id: "operation-a" } },
    );

    await waitFor(() => expect(requests.has("operation-a")).toBe(true));
    const first = requests.get("operation-a");
    rerender({ id: "operation-b" });
    await waitFor(() => expect(requests.has("operation-b")).toBe(true));
    expect(first?.signal.aborted).toBe(true);

    const second = requests.get("operation-b");
    unmount();
    expect(second?.signal.aborted).toBe(true);
  });
});

describe("operation snapshot ordering", () => {
  it("allows equal-time pending-to-terminal transitions but never regresses", () => {
    const running = operation();
    const succeeded = operation({ state: "succeeded" });
    expect(isOperationSnapshotNewer(running, succeeded)).toBe(true);
    expect(isOperationSnapshotNewer(succeeded, running)).toBe(false);
  });

  it("orders whole seconds before fractional timestamps", () => {
    expect(
      isOperationSnapshotNewer(
        operation({ updated_at: "2026-10-01T00:00:01Z" }),
        operation({ updated_at: "2026-10-01T00:00:01.000000001Z" }),
      ),
    ).toBe(true);
  });

  it("compares fractions at their actual precision", () => {
    expect(
      isOperationSnapshotNewer(
        operation({ updated_at: "2026-10-01T00:00:01.1234Z" }),
        operation({ updated_at: "2026-10-01T00:00:01.123400001Z" }),
      ),
    ).toBe(true);
    expect(
      isOperationSnapshotNewer(
        operation({ updated_at: "2026-10-01T00:00:01.1234Z" }),
        operation({ updated_at: "2026-10-01T00:00:01.123400000Z" }),
      ),
    ).toBe(false);
  });

  it("recognizes equal instants with different timezone and fraction notation", () => {
    expect(
      isOperationSnapshotNewer(
        operation({
          state: "succeeded",
          updated_at: "2026-10-01T12:00:00.1Z",
        }),
        operation({
          state: "succeeded",
          updated_at: "2026-10-01T14:00:00.100000000+02:00",
        }),
      ),
    ).toBe(false);
  });

  it("preserves sub-millisecond ordering", () => {
    expect(
      isOperationSnapshotNewer(
        operation({ updated_at: "2026-10-01T00:00:01.123456788Z" }),
        operation({ updated_at: "2026-10-01T00:00:01.123456789Z" }),
      ),
    ).toBe(true);
  });

  it("accepts equal-time active progress without accepting byte regressions", () => {
    const traversing = operation({
      state: "running",
      stage: "traversing",
      bytes_completed: 12,
    });
    expect(
      isOperationSnapshotNewer(
        traversing,
        operation({
          state: "running",
          stage: "applying",
          bytes_completed: 12,
        }),
      ),
    ).toBe(true);
    expect(
      isOperationSnapshotNewer(
        traversing,
        operation({
          state: "running",
          stage: "traversing",
          bytes_completed: 11,
        }),
      ),
    ).toBe(false);
  });
});
