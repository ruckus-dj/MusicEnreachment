import { useState } from "react";
import { apiResponseError, errorMessage } from "../../api/client/errors";
import { retryOperation, useListOperations } from "../../api/generated/client";
import type { OperationResponse } from "../../api/generated/client.schemas";
import "./operations.css";

const CURRENT_STATES = new Set(["queued", "running", "failed"]);
const GENERIC_RETRY_KINDS = new Set([
  "install",
  "move_tools_root",
  "matching_evaluation_v1",
  "matching_apply_v1",
  "publish_track_v1",
  "provider_release_lookup",
  "provider_recording_lookup",
  "provider_release_search",
  "provider_recording_search",
  "provider_artist_search",
  "provider_artist_release_browse",
  "provider_acoustid_lookup",
  "scan_source",
]);

function currentOperations(operations: OperationResponse[]) {
  return operations
    .filter((operation) => CURRENT_STATES.has(operation.state))
    .sort((left, right) => right.created_at.localeCompare(left.created_at));
}

function formatDate(value?: string) {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

function formatProgress(operation: OperationResponse) {
  if (operation.bytes_total === undefined) return "—";
  return `${operation.bytes_completed.toLocaleString()} / ${operation.bytes_total.toLocaleString()} байт`;
}

export function OperationsScreen() {
  const operationsQuery = useListOperations();
  const [actionError, setActionError] = useState("");
  const [retryingID, setRetryingID] = useState("");

  async function refresh() {
    await operationsQuery.refetch();
  }

  async function retry(operation: OperationResponse) {
    if (retryingID) return;
    setRetryingID(operation.id);
    setActionError("");
    try {
      const response = await retryOperation(operation.id);
      if (response.status !== 200) {
        setActionError(
          apiResponseError(
            response,
            `Не удалось повторить операцию (HTTP ${response.status}).`,
          ).message,
        );
        return;
      }
      await operationsQuery.refetch();
    } catch (reason) {
      setActionError(errorMessage(reason, "Не удалось повторить операцию."));
    } finally {
      setRetryingID("");
    }
  }

  const allOperations =
    operationsQuery.data?.status === 200
      ? (operationsQuery.data.data.operations ?? [])
      : [];
  const operations = currentOperations(allOperations);

  return (
    <section className="operations-screen" aria-labelledby="operations-title">
      <header className="operations-header">
        <div>
          <h1 id="operations-title">Текущие операции</h1>
          <p>
            Незавершённая работа и ошибки, требующие внимания. История не
            хранится.
          </p>
        </div>
        <button
          type="button"
          onClick={() => void refresh()}
          disabled={operationsQuery.isFetching}
        >
          {operationsQuery.isFetching ? "Обновление…" : "Обновить"}
        </button>
      </header>

      {operationsQuery.isPending && (
        <p className="operations-state" role="status">
          Загрузка операций…
        </p>
      )}

      {operationsQuery.isError && (
        <section className="operations-state operations-error" role="alert">
          <p>
            {errorMessage(
              operationsQuery.error,
              "Не удалось загрузить операции.",
            )}
          </p>
          <button
            type="button"
            onClick={() => void refresh()}
            disabled={operationsQuery.isFetching}
          >
            Повторить загрузку
          </button>
        </section>
      )}

      {operationsQuery.data && operationsQuery.data.status !== 200 && (
        <section className="operations-state operations-error" role="alert">
          <p>
            {
              apiResponseError(
                operationsQuery.data,
                `Не удалось загрузить операции (HTTP ${operationsQuery.data.status}).`,
              ).message
            }
          </p>
          <button
            type="button"
            onClick={() => void refresh()}
            disabled={operationsQuery.isFetching}
          >
            Повторить загрузку
          </button>
        </section>
      )}

      {actionError && (
        <p className="operations-error" role="alert">
          {actionError}
        </p>
      )}

      {operationsQuery.data?.status === 200 && operations.length === 0 && (
        <section className="operations-empty">
          <h2>Нет текущих операций</h2>
          <p>Незавершённая работа и операции с ошибкой появятся здесь.</p>
        </section>
      )}

      {operations.length > 0 && (
        <div className="operations-table-wrap">
          <table className="operations-table">
            <thead>
              <tr>
                <th scope="col">Объект</th>
                <th scope="col">Операция</th>
                <th scope="col">Состояние / этап</th>
                <th scope="col">Прогресс</th>
                <th scope="col">Создана</th>
                <th scope="col">Ошибка</th>
                <th scope="col">
                  <span className="operations-visually-hidden">Действия</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {operations.map((operation) => (
                <tr key={operation.id}>
                  <td>
                    <code>
                      {operation.target_identity ||
                        operation.target_installation_id ||
                        operation.target_source_location_id ||
                        operation.target_source_root_id ||
                        operation.id}
                    </code>
                  </td>
                  <td>
                    <strong>{operation.kind}</strong>
                    <small>{operation.id}</small>
                  </td>
                  <td>
                    <span
                      className={`operations-badge operations-${operation.state}`}
                    >
                      {operation.state}
                    </span>
                    <small>{operation.stage || "—"}</small>
                  </td>
                  <td>{formatProgress(operation)}</td>
                  <td>{formatDate(operation.created_at)}</td>
                  <td className="operations-diagnostic">
                    {operation.safe_error || "—"}
                  </td>
                  <td>
                    {operation.state === "failed" &&
                      GENERIC_RETRY_KINDS.has(operation.kind) && (
                        <button
                          type="button"
                          onClick={() => void retry(operation)}
                          disabled={Boolean(retryingID)}
                        >
                          {retryingID === operation.id
                            ? "Повтор…"
                            : "Повторить"}
                        </button>
                      )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <p className="operations-footnote">
        Показаны только queued, running и failed. Завершённые операции скрыты.
      </p>
    </section>
  );
}
