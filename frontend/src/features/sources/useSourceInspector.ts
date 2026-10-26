import { useCallback, useEffect, useRef, useState } from "react";
import {
  isOperationSnapshotNewer,
  useOperationFollower,
} from "../../api/client/useOperationFollower";
import {
  getOperation,
  getSourceLocation,
  rerunSourceFingerprint,
  retrySourceAnalysisStep,
} from "../../api/generated/client";
import type {
  OperationResponse,
  SourceLocationDetailResponse,
} from "../../api/generated/client.schemas";
import { message } from "./sourcesApi";

function inspectorFailure(response: {
  readonly status: number;
  readonly data?: { readonly detail?: string };
}) {
  if (response.status === 404)
    return new Error("Файл или каталог не найден: возможно, запись удалили.");
  if (response.status === 409)
    return new Error(
      `${response.data?.detail || "Запрос отклонён: состояние файла или каталога изменилось, либо выполняется другая операция."} Обновите сведения о файле и повторите доступный этап.`,
    );
  return new Error(
    response.data?.detail || `Сервер вернул ошибку ${response.status}.`,
  );
}

export function useSourceInspector(sourceId: string, locationId: string) {
  const [detail, setDetail] = useState<SourceLocationDetailResponse>();
  const [operation, setOperation] = useState<OperationResponse>();
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [streamError, setStreamError] = useState("");
  const lifetime = useRef(new AbortController());
  const detailRequest = useRef<AbortController | undefined>(undefined);
  const actionRequest = useRef<AbortController | undefined>(undefined);
  const completion = useRef("");
  const operationSnapshot = useRef<OperationResponse | undefined>(undefined);

  const apply = useCallback((snapshot: OperationResponse, force = false) => {
    if (
      lifetime.current.signal.aborted ||
      (!force && !isOperationSnapshotNewer(operationSnapshot.current, snapshot))
    )
      return;
    operationSnapshot.current = snapshot;
    setOperation(snapshot);
  }, []);

  const load = useCallback(async () => {
    detailRequest.current?.abort();
    const request = new AbortController();
    detailRequest.current = request;
    setLoading(true);
    setError("");
    try {
      const response = await getSourceLocation(sourceId, locationId, {
        signal: request.signal,
        cache: "no-store",
      });
      if (request.signal.aborted || lifetime.current.signal.aborted) return;
      if (response.status !== 200) throw inspectorFailure(response);
      setDetail(response.data);
      const activeId = response.data.active_analysis_operation_id;
      if (activeId) {
        const active = await getOperation(activeId, {
          signal: request.signal,
          cache: "no-store",
        });
        if (request.signal.aborted || lifetime.current.signal.aborted) return;
        if (active.status !== 200) throw inspectorFailure(active);
        apply(active.data);
      } else {
        operationSnapshot.current = undefined;
        setOperation(undefined);
      }
    } catch (reason) {
      if (!request.signal.aborted && !lifetime.current.signal.aborted)
        setError(message(reason));
    } finally {
      if (!request.signal.aborted && !lifetime.current.signal.aborted)
        setLoading(false);
    }
  }, [sourceId, locationId, apply]);

  useEffect(() => {
    lifetime.current = new AbortController();
    void load();
    return () => {
      lifetime.current.abort();
      detailRequest.current?.abort();
      actionRequest.current?.abort();
    };
  }, [load]);

  const operationId = operation?.id;
  const pending =
    operation?.state === "queued" || operation?.state === "running";
  useOperationFollower(operationId, {
    enabled: Boolean(operationId && pending),
    initialSnapshot: operation,
    onSnapshot: (snapshot, source) => {
      apply(snapshot, source === "action");
    },
    onSnapshotRead: (_snapshot, source) => {
      if (source === "initial") return;
      if (source === "stream") setStreamError("");
      // SSE is only a wake-up channel; the follower supplies REST snapshots.
      // Refresh detail on wakes and recovery reads, including duplicate states.
      // A fallback REST read does not prove that EventSource has reconnected.
      void load();
    },
    onHttpError: inspectorFailure,
    onError: (reason) => setStreamError(message(reason)),
    onStreamError: (reason) => setStreamError(reason.message),
    refreshOnStreamError: true,
  });

  // Terminal refresh owns its own lifecycle. It must not go through the
  // subscription wake-up channel: that channel is a no-op when the initial
  // snapshot is already terminal, and it must not outlive the subscription.
  // Reloading detail also clears stale active controls.
  useEffect(() => {
    if (!operation) return;
    const terminal =
      operation.state !== "queued" && operation.state !== "running";
    const key = terminal ? `${operation.id}:${operation.state}` : "";
    if (key && key !== completion.current) void load();
    completion.current = key;
  }, [operation, load]);

  async function action(
    kind: "retry" | "rerun",
    step?: "sha256" | "probe" | "fingerprint" | "metadata",
  ) {
    if (!detail || loading || actionRequest.current) return;
    const request = new AbortController();
    actionRequest.current = request;
    setBusy(true);
    setError("");
    try {
      switch (kind) {
        case "retry": {
          if (!step) return;
          const response = await retrySourceAnalysisStep(
            sourceId,
            locationId,
            {
              step,
              expected_size_bytes: detail.size_bytes,
              expected_mtime: detail.mtime,
            },
            {
              signal: request.signal,
            },
          );
          if (request.signal.aborted) return;
          if (response.status !== 202) {
            void load();
            throw inspectorFailure(response);
          }
          apply(response.data, true);
          void load();
          break;
        }
        case "rerun": {
          const response = await rerunSourceFingerprint(
            sourceId,
            locationId,
            {
              expected_size_bytes: detail.size_bytes,
              expected_mtime: detail.mtime,
            },
            {
              signal: request.signal,
            },
          );
          if (request.signal.aborted) return;
          if (response.status !== 202) {
            void load();
            throw inspectorFailure(response);
          }
          apply(response.data, true);
          void load();
          break;
        }
      }
    } catch (reason) {
      if (!request.signal.aborted) setError(message(reason));
    } finally {
      if (!request.signal.aborted) {
        actionRequest.current = undefined;
        setBusy(false);
      }
    }
  }

  return {
    detail,
    operation,
    loading,
    busy,
    pending,
    error,
    streamError,
    load,
    action,
  };
}
