import { useCallback, useEffect, useRef, useState } from "react";
import { subscribeToOperation } from "../../api/client/operations";
import {
  analyzeSourceLocation,
  dismissOperation,
  getOperation,
  getSourceLocation,
  retryOperation,
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
      `${response.data?.detail || "Анализ отклонён: состояние файла или каталога изменилось, либо выполняется другая операция."} Обновите файл и запустите новый анализ текущего файла.`,
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
    let request: AbortController | undefined;
    const read = async () => {
      request?.abort();
      const current = new AbortController();
      request = current;
      try {
        const response = await getOperation(operationId, {
          signal: current.signal,
          cache: "no-store",
        });
        if (current.signal.aborted) return;
        if (response.status !== 200) throw inspectorFailure(response);
        apply(response.data);
      } catch (reason) {
        if (!current.signal.aborted) setStreamError(message(reason));
      }
    };
    const close = subscribeToOperation(
      operationId,
      (snapshot) => {
        request?.abort();
        setStreamError("");
        apply(snapshot);
      },
      (reason) => {
        setStreamError(reason.message);
        void read();
      },
    );
    void read();
    return () => {
      close();
      request?.abort();
    };
  }, [operationId, pending, apply]);

  useEffect(() => {
    if (!operation) return;
    const terminal =
      operation.state === "succeeded" || operation.state === "failed";
    const key = terminal ? `${operation.id}:${operation.state}` : "";
    if (key && key !== completion.current) void load();
    completion.current = key;
  }, [operation, load]);

  async function action(kind: "start" | "retry" | "dismiss") {
    if (!detail || loading || actionRequest.current) return;
    const request = new AbortController();
    actionRequest.current = request;
    setBusy(true);
    setError("");
    try {
      switch (kind) {
        case "start": {
          const response = await analyzeSourceLocation(
            sourceId,
            locationId,
            {
              expected_size_bytes: detail.size_bytes,
              expected_mtime: detail.mtime,
            },
            { signal: request.signal },
          );
          if (request.signal.aborted) return;
          if (response.status !== 202) {
            void load();
            throw inspectorFailure(response);
          }
          apply(response.data);
          break;
        }
        case "retry": {
          if (!operation) return;
          const response = await retryOperation(operation.id, {
            signal: request.signal,
          });
          if (request.signal.aborted) return;
          if (response.status !== 200) {
            void load();
            throw inspectorFailure(response);
          }
          apply(response.data);
          break;
        }
        case "dismiss": {
          if (!operation) return;
          const response = await dismissOperation(operation.id, {
            signal: request.signal,
          });
          if (request.signal.aborted) return;
          if (response.status !== 204) throw inspectorFailure(response);
          setOperation(undefined);
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
