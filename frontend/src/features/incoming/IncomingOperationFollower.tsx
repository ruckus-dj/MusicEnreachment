import type { MutableRefObject } from "react";
import { useEffect } from "react";
import type { OperationSnapshotSource } from "../../api/client/useOperationFollower";
import { useOperationFollower } from "../../api/client/useOperationFollower";
import type { OperationResponse } from "../../api/generated/client.schemas";

export type IncomingOperationError = {
  message: string;
  kind: "rest" | "stream";
};

export function errorAfterSnapshotRead(
  error: IncomingOperationError | undefined,
  source: OperationSnapshotSource,
): IncomingOperationError | undefined {
  if (!error || error.kind === "rest" || source === "stream") return undefined;
  return error;
}

export function IncomingOperationFollower({
  operationId,
  onSnapshot,
  onSnapshotRead,
  onError,
  onStreamError,
  refreshers,
}: {
  operationId: string;
  onSnapshot: (
    snapshot: OperationResponse,
    source: OperationSnapshotSource,
  ) => void;
  onSnapshotRead: (
    snapshot: OperationResponse,
    source: OperationSnapshotSource,
    accepted: boolean,
  ) => void;
  onError: (error: Error) => void;
  onStreamError: (error: Error) => void;
  refreshers: MutableRefObject<Record<string, () => Promise<void>>>;
}) {
  const { refresh } = useOperationFollower(operationId, {
    onSnapshot,
    onSnapshotRead,
    onError: (error) => onError(error),
    onStreamError,
    refreshOnStreamError: true,
  });
  useEffect(() => {
    refreshers.current[operationId] = refresh;
    return () => {
      delete refreshers.current[operationId];
    };
  }, [operationId, refresh, refreshers]);
  return null;
}
