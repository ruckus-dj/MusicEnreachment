import { useCallback, useEffect, useRef, useState } from "react";
import { apiResponseError, errorMessage } from "../../api/client/errors";
import { useOperationFollower } from "../../api/client/useOperationFollower";
import { retryOperation } from "../../api/generated/client";
import type { OperationResponse } from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";

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
  const completion = useRef("");
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
        setRefreshError(errorMessage(reason, "Неизвестная ошибка."));
      } finally {
        refreshing.current = false;
        setPending(false);
      }
    },
    [id, onSuccess, onFinished],
  );
  const { acceptSnapshot, refresh } = useOperationFollower(id, {
    initialSnapshot: initial,
    onSnapshot: (next) => {
      setSnapshot(next);
      setError("");
      if (next.state === "queued" || next.state === "running") {
        completion.current = "";
      } else if (next.state === "succeeded") {
        const key = `${next.id}:${next.state}`;
        if (completion.current !== key) {
          completion.current = key;
          void refreshInstallations(next);
        }
      }
    },
    onHttpError: (response) =>
      apiResponseError({ status: response.status, data: response.data ?? {} }),
    onError: (reason) => setError(errorMessage(reason, "Неизвестная ошибка.")),
    onStreamError: (reason) => setError(reason.message),
  });
  useEffect(() => {
    if (snapshot?.state !== "succeeded") return;
    const key = `${snapshot.id}:${snapshot.state}`;
    if (completion.current !== key) {
      completion.current = key;
      void refreshInstallations(snapshot);
    }
  }, [snapshot, refreshInstallations]);
  const refreshSnapshot = useCallback(async () => {
    setSnapshotPending(true);
    setError("");
    await refresh();
    setSnapshotPending(false);
  }, [refresh]);

  async function retry() {
    setPending(true);
    setError("");
    try {
      const response = await retryOperation(id);
      if (response.status !== 200) throw apiResponseError(response);
      acceptSnapshot(response.data, "action", true);
    } catch (reason) {
      setError(errorMessage(reason, "Неизвестная ошибка."));
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
