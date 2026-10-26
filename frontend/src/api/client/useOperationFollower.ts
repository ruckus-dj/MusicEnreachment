import { useCallback, useEffect, useRef } from "react";
import { getOperation } from "../generated/client";
import type { OperationResponse } from "../generated/client.schemas";
import { subscribeToOperation } from "./operations";

export type OperationSnapshotSource =
  | "initial"
  | "stream"
  | "refresh"
  | "action";

type OperationFollowerOptions = {
  enabled?: boolean;
  initialSnapshot?: OperationResponse;
  onSnapshot: (
    snapshot: OperationResponse,
    source: OperationSnapshotSource,
  ) => void;
  onSnapshotRead?: (
    snapshot: OperationResponse,
    source: OperationSnapshotSource,
    accepted: boolean,
  ) => void;
  onError?: (error: Error, source: OperationSnapshotSource) => void;
  onHttpError?: (response: {
    status: number;
    data?: { detail?: string };
  }) => Error;
  onStreamError?: (error: Error) => void;
  refreshOnStreamError?: boolean;
};

const pending = (operation: OperationResponse) =>
  operation.state === "queued" || operation.state === "running";

function parseRFC3339Nano(value: string) {
  const parts =
    /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?(Z|[+-]\d{2}:\d{2})$/.exec(
      value,
    );
  if (!parts) return undefined;

  const [, yearText, monthText, dayText, hourText, minuteText, secondText] =
    parts;
  const date = new Date(0);
  date.setUTCFullYear(Number(yearText), Number(monthText) - 1, Number(dayText));
  date.setUTCHours(Number(hourText), Number(minuteText), Number(secondText), 0);
  const localSeconds = date.getTime() / 1000;
  if (!Number.isFinite(localSeconds)) return undefined;

  const zone = parts[8];
  let offsetSeconds = 0;
  if (zone !== "Z") {
    const sign = zone[0] === "+" ? 1 : -1;
    const offsetHours = Number(zone.slice(1, 3));
    const offsetMinutes = Number(zone.slice(4, 6));
    offsetSeconds = sign * (offsetHours * 60 + offsetMinutes) * 60;
  }

  return {
    seconds: localSeconds - offsetSeconds,
    nanoseconds: Number((parts[7] ?? "").padEnd(9, "0") || 0),
  };
}

function compareUpdatedAt(current: string, incoming: string) {
  const currentTime = parseRFC3339Nano(current);
  const incomingTime = parseRFC3339Nano(incoming);
  if (currentTime && incomingTime) {
    if (incomingTime.seconds !== currentTime.seconds)
      return incomingTime.seconds > currentTime.seconds ? 1 : -1;
    if (incomingTime.nanoseconds !== currentTime.nanoseconds)
      return incomingTime.nanoseconds > currentTime.nanoseconds ? 1 : -1;
    return 0;
  }
  return incoming === current ? 0 : incoming > current ? 1 : -1;
}

function equalTimeProgresses(
  current: OperationResponse,
  incoming: OperationResponse,
) {
  if (!pending(current) || !pending(incoming)) return false;
  if (current.state === "running" && incoming.state === "queued") return false;
  if (incoming.bytes_completed < current.bytes_completed) return false;
  if (incoming.bytes_completed > current.bytes_completed) return true;
  if (current.state !== incoming.state)
    return current.state === "queued" && incoming.state === "running";
  return (
    current.stage !== incoming.stage ||
    current.bytes_total !== incoming.bytes_total
  );
}

export function isOperationSnapshotNewer(
  current: OperationResponse | undefined,
  incoming: OperationResponse,
): boolean {
  if (!current || current.id !== incoming.id) return true;
  const timestampOrder = compareUpdatedAt(
    current.updated_at,
    incoming.updated_at,
  );
  if (timestampOrder > 0) return true;
  if (timestampOrder < 0) return false;
  if (pending(current) && !pending(incoming)) return true;
  return equalTimeProgresses(current, incoming);
}

/**
 * Follows one operation with an immediate REST read and an SSE wake-up stream.
 * The stream helper re-reads REST for every wake; callbacks never trust event
 * payloads. Reads are latest-wins and both the stream and read are disposed
 * when the operation changes or following is disabled.
 */
export function useOperationFollower(
  operationId: string | undefined,
  {
    enabled = true,
    initialSnapshot,
    onSnapshot,
    onSnapshotRead,
    onError,
    onHttpError,
    onStreamError,
    refreshOnStreamError = false,
  }: OperationFollowerOptions,
) {
  const handlers = useRef({
    onSnapshot,
    onSnapshotRead,
    onError,
    onHttpError,
    onStreamError,
  });
  handlers.current = {
    onSnapshot,
    onSnapshotRead,
    onError,
    onHttpError,
    onStreamError,
  };
  const accepted = useRef<{
    operationId: string | undefined;
    snapshot?: OperationResponse;
    rearmedFrom?: { state: string; updatedAt: string };
  }>({ operationId, snapshot: initialSnapshot });
  if (accepted.current.operationId !== operationId) {
    accepted.current = {
      operationId,
      snapshot:
        initialSnapshot?.id === operationId ? initialSnapshot : undefined,
    };
  } else if (
    initialSnapshot !== undefined &&
    initialSnapshot.id === operationId &&
    isOperationSnapshotNewer(accepted.current.snapshot, initialSnapshot)
  ) {
    accepted.current.snapshot = initialSnapshot;
  }

  const acceptSnapshot = useCallback(
    (
      snapshot: OperationResponse,
      source: OperationSnapshotSource,
      force = false,
    ) => {
      if (
        snapshot.id !== accepted.current.operationId ||
        (!force &&
          accepted.current.rearmedFrom?.state === snapshot.state &&
          compareUpdatedAt(
            accepted.current.rearmedFrom.updatedAt,
            snapshot.updated_at,
          ) >= 0) ||
        (!force &&
          !isOperationSnapshotNewer(accepted.current.snapshot, snapshot))
      )
        return false;
      const previous = accepted.current.snapshot;
      if (
        force &&
        source === "action" &&
        previous &&
        !pending(previous) &&
        pending(snapshot)
      ) {
        accepted.current.rearmedFrom = {
          state: previous.state,
          updatedAt: previous.updated_at,
        };
      } else if (
        accepted.current.rearmedFrom &&
        (!pending(snapshot) ||
          compareUpdatedAt(
            accepted.current.rearmedFrom.updatedAt,
            snapshot.updated_at,
          ) < 0)
      ) {
        accepted.current.rearmedFrom = undefined;
      }
      accepted.current.snapshot = snapshot;
      handlers.current.onSnapshot(snapshot, source);
      return true;
    },
    [],
  );

  const read = useRef<
    ((source: OperationSnapshotSource) => Promise<void>) | null
  >(null);
  const refresh = useCallback(() => {
    return read.current?.("refresh") ?? Promise.resolve();
  }, []);

  useEffect(() => {
    if (!operationId || !enabled) return;
    let disposed = false;
    let request: AbortController | undefined;
    let generation = 0;

    const reread = async (source: OperationSnapshotSource) => {
      if (disposed) return;
      request?.abort();
      const current = new AbortController();
      request = current;
      const startedAt = ++generation;
      try {
        const response = await getOperation(operationId, {
          signal: current.signal,
          cache: "no-store",
        });
        if (disposed || current.signal.aborted || startedAt !== generation)
          return;
        if (response.status !== 200) {
          handlers.current.onError?.(
            handlers.current.onHttpError?.(response) ??
              new Error(`Ошибка сервера (${response.status}).`),
            source,
          );
          return;
        }
        const accepted = acceptSnapshot(response.data, source);
        handlers.current.onSnapshotRead?.(response.data, source, accepted);
      } catch (reason) {
        if (!disposed && !current.signal.aborted && startedAt === generation)
          handlers.current.onError?.(
            reason instanceof Error
              ? reason
              : new Error("Operation snapshot request failed."),
            source,
          );
      } finally {
        if (request === current) request = undefined;
      }
    };
    read.current = reread;

    const unsubscribe = subscribeToOperation(
      operationId,
      (snapshot) => {
        const accepted = acceptSnapshot(snapshot, "stream");
        handlers.current.onSnapshotRead?.(snapshot, "stream", accepted);
        if (accepted) {
          request?.abort();
          generation += 1;
        }
      },
      (reason) => {
        handlers.current.onStreamError?.(reason);
        if (refreshOnStreamError) void reread("refresh");
      },
    );
    void reread("initial");

    return () => {
      disposed = true;
      generation += 1;
      unsubscribe();
      request?.abort();
      if (read.current === reread) read.current = null;
    };
  }, [operationId, enabled, refreshOnStreamError, acceptSnapshot]);

  return { acceptSnapshot, refresh };
}
