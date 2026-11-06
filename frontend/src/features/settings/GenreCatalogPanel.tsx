import { useCallback, useEffect, useRef, useState } from "react";
import { apiResponseError, errorMessage } from "../../api/client/errors";
import type { OperationSnapshotSource } from "../../api/client/useOperationFollower";
import { useOperationFollower } from "../../api/client/useOperationFollower";
import {
  getGenreCatalog,
  refreshGenreCatalog,
} from "../../api/generated/client";
import type {
  GenreCatalogStatus,
  OperationResponse,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";

type GenreCatalogPanelStatus = GenreCatalogStatus & {
  active_operation_id?: string | null;
};

function displayTime(value: string | null) {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

export function GenreCatalogPanel() {
  const [status, setStatus] = useState<GenreCatalogPanelStatus>();
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [operationId, setOperationId] = useState<string>();
  const [operation, setOperation] = useState<OperationResponse>();
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const mounted = useRef(false);
  const statusRequestGeneration = useRef(0);

  const loadStatus = useCallback(async () => {
    const generation = ++statusRequestGeneration.current;
    const response = await getGenreCatalog({ cache: "no-store" });
    if (response.status !== 200) throw apiResponseError(response);
    if (mounted.current && generation === statusRequestGeneration.current) {
      const next = response.data as GenreCatalogPanelStatus;
      setStatus(next);
      if (next.active_operation_id) setOperationId(next.active_operation_id);
    }
  }, []);

  useEffect(() => {
    mounted.current = true;
    let active = true;
    const generation = ++statusRequestGeneration.current;
    void getGenreCatalog({ cache: "no-store" })
      .then(
        (response) => {
          if (!active || generation !== statusRequestGeneration.current) return;
          if (response.status !== 200) {
            setError(apiResponseError(response).message);
            return;
          }
          const next = response.data as GenreCatalogPanelStatus;
          setStatus(next);
          if (next.active_operation_id)
            setOperationId(next.active_operation_id);
        },
        (reason: unknown) => {
          if (active && generation === statusRequestGeneration.current)
            setError(
              errorMessage(
                reason,
                "Не удалось загрузить статус жанрового каталога.",
              ),
            );
        },
      )
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
      mounted.current = false;
      statusRequestGeneration.current += 1;
    };
  }, []);

  const onOperationSnapshot = useCallback(
    (snapshot: OperationResponse, _source: OperationSnapshotSource) => {
      setOperation(snapshot);
      if (snapshot.state !== "queued" && snapshot.state !== "running") {
        void loadStatus().catch((reason: unknown) =>
          setError(
            errorMessage(
              reason,
              "Не удалось загрузить статус жанрового каталога.",
            ),
          ),
        );
        setOperationId(undefined);
      }
    },
    [loadStatus],
  );
  const onOperationError = useCallback((reason: Error) => {
    setError(errorMessage(reason, "Не удалось загрузить статус операции."));
  }, []);
  useOperationFollower(operationId, {
    enabled: Boolean(operationId),
    onSnapshot: onOperationSnapshot,
    onError: onOperationError,
  });

  async function refresh() {
    if (refreshing || operationId) return;
    setRefreshing(true);
    setError("");
    setNotice("");
    try {
      const response = await refreshGenreCatalog();
      if (response.status !== 200) throw apiResponseError(response);
      const admitted = response.data as unknown as OperationResponse;
      setOperation(admitted);
      setOperationId(admitted.id);
      setNotice("Обновление жанрового каталога запрошено.");
    } catch (reason) {
      setError(errorMessage(reason, "Не удалось обновить жанровый каталог."));
    } finally {
      setRefreshing(false);
    }
  }

  const operationActive =
    operation?.state === "queued" || operation?.state === "running";

  return (
    <section
      className="settings-panel genre-catalog-panel"
      aria-labelledby="genre-catalog-title"
    >
      <div className="genre-catalog-heading">
        <div>
          <h2 id="genre-catalog-title">Жанровый каталог</h2>
          <p>Каталог MusicBrainz используется для подбора жанров.</p>
        </div>
        <AppButton
          isDisabled={refreshing || Boolean(operationId)}
          onPress={() => void refresh()}
        >
          {refreshing ? "Запрос отправляется…" : "Обновить каталог"}
        </AppButton>
      </div>
      {loading && <p role="status">Загрузка статуса…</p>}
      {status && (
        <dl className="genre-catalog-status">
          <div>
            <dt>Жанров</dt>
            <dd>{status.count.toLocaleString()}</dd>
          </div>
          <div>
            <dt>Последнее успешное обновление</dt>
            <dd>{displayTime(status.updated_at)}</dd>
          </div>
          <div>
            <dt>Последняя попытка</dt>
            <dd>{displayTime(status.last_attempt_at)}</dd>
          </div>
        </dl>
      )}
      {status?.last_error && (
        <p className="genre-catalog-last-error">
          Последняя ошибка: {status.last_error}
        </p>
      )}
      {operationActive && (
        <p role="status">
          Обновление каталога:{" "}
          {operation.state === "queued" ? "в очереди" : "выполняется"}
          {operation.stage ? ` — ${operation.stage}` : ""}
        </p>
      )}
      {operation?.safe_error && (
        <p className="genre-catalog-last-error">
          Ошибка обновления: {operation.safe_error}
        </p>
      )}
      {error && <p role="alert">{error}</p>}
      {notice && <p role="status">{notice}</p>}
    </section>
  );
}
