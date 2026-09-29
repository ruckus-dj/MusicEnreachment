import { useCallback, useEffect, useRef, useState } from "react";
import { subscribeToOperation } from "../../api/client/operations";
import { getOperation, retryOperation } from "../../api/generated/client";
import type { OperationResponse } from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { errorMessage, setupError } from "./setupApi";

export function OperationProgress({
  id,
  initial,
  onSuccess,
  onFinished,
}: {
  id: string;
  initial?: OperationResponse;
  onSuccess: () => Promise<void>;
  onFinished: (id: string, snapshot: OperationResponse) => void;
}) {
  const [snapshot, setSnapshot] = useState(initial);
  const [error, setError] = useState("");
  const [refreshError, setRefreshError] = useState("");
  const [pending, setPending] = useState(false);
  const [snapshotPending, setSnapshotPending] = useState(false);
  const refreshing = useRef(false);
  const request = useRef<AbortController | null>(null);
  const generation = useRef(0);
  const alert = useRef<HTMLParagraphElement>(null);
  const refreshAlert = useRef<HTMLParagraphElement>(null);
  useEffect(() => {
    if (error) alert.current?.focus();
  }, [error]);
  useEffect(() => {
    if (refreshError) refreshAlert.current?.focus();
  }, [refreshError]);
  const refreshInstallations = useCallback(
    async (next: OperationResponse) => {
      if (refreshing.current) return;
      refreshing.current = true;
      setPending(true);
      setRefreshError("");
      try {
        await onSuccess();
        onFinished(id, next);
      } catch (reason) {
        setRefreshError(errorMessage(reason));
      } finally {
        refreshing.current = false;
        setPending(false);
      }
    },
    [id, onSuccess, onFinished],
  );
  const refreshSnapshot = useCallback(async () => {
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    const startedAt = generation.current;
    setSnapshotPending(true);
    setError("");
    try {
      const response = await getOperation(id, {
        cache: "no-store",
        signal: controller.signal,
      });
      if (controller.signal.aborted || startedAt !== generation.current) return;
      if (response.status !== 200) throw setupError(response);
      setSnapshot(response.data);
      if (response.data.state === "succeeded")
        void refreshInstallations(response.data);
    } catch (reason) {
      if (!controller.signal.aborted && startedAt === generation.current)
        setError(errorMessage(reason));
    } finally {
      if (request.current === controller) {
        request.current = null;
        setSnapshotPending(false);
      }
    }
  }, [id, refreshInstallations]);
  useEffect(() => {
    // The first REST read does not wait for EventSource to connect. Later SSE
    // wake-ups still read REST; cancel this read if a newer one wins.
    const unsubscribe = subscribeToOperation(
      id,
      (next) => {
        generation.current += 1;
        request.current?.abort();
        setSnapshot(next);
        setError("");
        if (next.state === "succeeded") void refreshInstallations(next);
      },
      (reason) => setError(reason.message),
    );
    void refreshSnapshot();
    return () => {
      generation.current += 1;
      request.current?.abort();
      unsubscribe();
    };
  }, [id, refreshInstallations, refreshSnapshot]);

  async function retry() {
    setPending(true);
    setError("");
    try {
      const response = await retryOperation(id);
      if (response.status !== 200) throw setupError(response);
      generation.current += 1;
      request.current?.abort();
      setSnapshot(response.data);
    } catch (reason) {
      setError(errorMessage(reason));
    } finally {
      setPending(false);
    }
  }
  return (
    <div role="status" className="setup-operation">
      Операция {id}:{" "}
      {snapshot
        ? `${snapshot.state}, ${snapshot.stage}, ${snapshot.bytes_completed}${snapshot.bytes_total == null ? "" : ` / ${snapshot.bytes_total}`} bytes. ${snapshot.safe_error || ""}`
        : "Ожидается REST snapshot…"}
      {error && (
        <AppButton isDisabled={snapshotPending} onPress={refreshSnapshot}>
          Повторить загрузку операции {id}
        </AppButton>
      )}
      {snapshotPending && <span> Загрузка состояния операции…</span>}
      {snapshot?.state === "failed" && (
        <AppButton isDisabled={pending} onPress={retry}>
          Повторить операцию {id}
        </AppButton>
      )}
      {snapshot?.state === "succeeded" && refreshError && (
        <AppButton
          isDisabled={pending}
          onPress={() => {
            void refreshInstallations(snapshot);
          }}
        >
          Повторить загрузку установок {id}
        </AppButton>
      )}
      {pending && (
        <span>
          {" "}
          {snapshot?.state === "succeeded"
            ? "Загрузка установок…"
            : "Повтор операции…"}
        </span>
      )}
      {refreshError && (
        <p role="alert" ref={refreshAlert} tabIndex={-1}>
          {refreshError}
        </p>
      )}
      {error && (
        <p role="alert" ref={alert} tabIndex={-1}>
          {error}
        </p>
      )}
    </div>
  );
}
