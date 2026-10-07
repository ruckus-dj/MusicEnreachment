import { useCallback, useEffect, useRef, useState } from "react";
import { subscribeToOperation } from "../../api/client/operations";
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
        setOperation(active.data);
      } else {
        setOperation(undefined);
      }
    } catch (reason) {
      if (!request.signal.aborted && !lifetime.current.signal.aborted)
        setError(message(reason));
    } finally {
      if (!request.signal.aborted && !lifetime.current.signal.aborted)
        setLoading(false);
    }
  }, [sourceId, locationId]);

  useEffect(() => {
    lifetime.current = new AbortController();
    void load();
    return () => {
      lifetime.current.abort();
      detailRequest.current?.abort();
      actionRequest.current?.abort();
    };
  }, [load]);

  const apply = useCallback((snapshot: OperationResponse) => {
    if (lifetime.current.signal.aborted) return;
    setOperation(snapshot);
  }, []);

  const operationId = operation?.id;
  const pending =
    operation?.state === "queued" || operation?.state === "running";
  useEffect(() => {
    if (!operationId || !pending) return;
    // Effect-local disposal guard: a late SSE wake-up or an in-flight reread
    // must never issue a new request or reload the inspector after navigation.
    let disposed = false;
    let request: AbortController | undefined;
    const read = async () => {
      if (disposed) return;
      request?.abort();
      const current = new AbortController();
      request = current;
      try {
        const response = await getOperation(operationId, {
          signal: current.signal,
          cache: "no-store",
        });
        if (disposed || current.signal.aborted) return;
        if (response.status !== 200) throw inspectorFailure(response);
        apply(response.data);
        if (disposed) return;
        // SSE is only a wake-up channel. Every wake re-reads the inspector,
        // including intermediate updates from independently running steps.
        void load();
      } catch (reason) {
        if (!disposed && !current.signal.aborted)
          setStreamError(message(reason));
      }
    };
    const close = subscribeToOperation(
      operationId,
      (snapshot) => {
        if (disposed) return;
        request?.abort();
        setStreamError("");
        apply(snapshot);
        void load();
      },
      (reason) => {
        if (disposed) return;
        setStreamError(reason.message);
        void read();
      },
    );
    void read();
    return () => {
      disposed = true;
      close();
      request?.abort();
    };
  }, [operationId, pending, apply, load]);

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
    step?: "sha256" | "probe" | "fingerprint",
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
          apply(response.data);
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
          apply(response.data);
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
