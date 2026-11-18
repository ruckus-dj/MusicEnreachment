import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useRef, useState } from "react";
import { apiResponseError, errorMessage } from "../../api/client/errors";
import {
  dismissOperation,
  getListOperationsQueryKey,
  type listOperationsResponse,
  retryOperation,
  useListOperations,
} from "../../api/generated/client";
import type { OperationResponse } from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";
import {
  operationKindLabel,
  operationProgressLabel,
  operationStageLabel,
  operationStateLabel,
  operationTargetLabel,
} from "./operationPresentation";
import "./operations.css";

const CURRENT_STATES = new Set(["queued", "running", "failed"]);
const DISMISSIBLE_TOOL_KINDS = new Set(["install", "move_tools_root"]);
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

const messages = {
  en: {
    title: "Current operations",
    description:
      "Work in progress and errors that need attention. History is not retained.",
    refresh: "Refresh",
    refreshing: "Refreshing…",
    loading: "Loading operations…",
    loadError: "Could not load operations.",
    retryLoad: "Retry loading",
    emptyTitle: "No current operations",
    emptyDescription:
      "Work in progress and failed operations will appear here.",
    target: "Target",
    operation: "Operation",
    stateStage: "Status / stage",
    progress: "Progress",
    created: "Created",
    error: "Error",
    actions: "Actions",
    retrying: "Retrying…",
    retry: "Retry",
    dismissing: "Dismissing…",
    dismiss: "Dismiss error",
    dismissed: "Error dismissed.",
    footnote:
      "Queued, running, and failed operations are shown. Completed operations are hidden.",
    retryError: "Could not retry operation.",
    retryHttpError: (status: number) =>
      `Could not retry operation (HTTP ${status}).`,
    dismissError: "Could not dismiss operation.",
    dismissHttpError: (status: number) =>
      `Could not dismiss operation (HTTP ${status}).`,
    loadHttpError: (status: number) =>
      `Could not load operations (HTTP ${status}).`,
  },
  ru: {
    title: "Текущие операции",
    description:
      "Незавершённая работа и ошибки, требующие внимания. История не хранится.",
    refresh: "Обновить",
    refreshing: "Обновление…",
    loading: "Загрузка операций…",
    loadError: "Не удалось загрузить операции.",
    retryLoad: "Повторить загрузку",
    emptyTitle: "Нет текущих операций",
    emptyDescription:
      "Незавершённая работа и операции с ошибкой появятся здесь.",
    target: "Объект",
    operation: "Операция",
    stateStage: "Состояние / этап",
    progress: "Прогресс",
    created: "Создана",
    error: "Ошибка",
    actions: "Действия",
    retrying: "Повтор…",
    retry: "Повторить",
    dismissing: "Закрываем…",
    dismiss: "Закрыть ошибку",
    dismissed: "Ошибка закрыта.",
    footnote:
      "Показаны операции в очереди, выполняющиеся и требующие внимания. Завершённые операции скрыты.",
    retryError: "Не удалось повторить операцию.",
    retryHttpError: (status: number) =>
      `Не удалось повторить операцию (HTTP ${status}).`,
    dismissError: "Не удалось закрыть ошибку операции.",
    dismissHttpError: (status: number) =>
      `Не удалось закрыть ошибку операции (HTTP ${status}).`,
    loadHttpError: (status: number) =>
      `Не удалось загрузить операции (HTTP ${status}).`,
  },
} as const;

function currentOperations(operations: OperationResponse[]) {
  return operations
    .filter((operation) => CURRENT_STATES.has(operation.state))
    .sort((left, right) => right.created_at.localeCompare(left.created_at));
}

function formatDate(value: string | undefined, locale: string) {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString(locale);
}

export function OperationsScreen() {
  const { locale } = useI18n();
  const copy = messages[locale];
  const queryClient = useQueryClient();
  const operationsQuery = useListOperations();
  const heading = useRef<HTMLHeadingElement>(null);
  const [dismissed, setDismissed] = useState(false);
  const [actionError, setActionError] = useState<
    | ({ action: "retry" | "dismiss" } & (
        | { reason: unknown }
        | { response: { status: number; data?: unknown } }
      ))
    | null
  >(null);
  const [pending, setPending] = useState<{
    id: string;
    action: "retry" | "dismiss";
  }>();

  const refresh = useCallback(async () => {
    await operationsQuery.refetch();
  }, [operationsQuery.refetch]);

  async function perform(
    id: string,
    action: "retry" | "dismiss",
    trigger?: HTMLButtonElement,
  ) {
    if (pending) return;
    setPending({ id, action });
    setActionError(null);
    setDismissed(false);
    try {
      const response =
        action === "retry"
          ? await retryOperation(id)
          : await dismissOperation(id);
      if (response.status !== (action === "retry" ? 200 : 204)) {
        setActionError({ action, response });
        return;
      }
      if (action === "dismiss") {
        const queryKey = getListOperationsQueryKey();
        await queryClient.cancelQueries({ queryKey, exact: true });
        queryClient.setQueryData<listOperationsResponse>(queryKey, (current) =>
          current?.status === 200
            ? {
                ...current,
                data: {
                  ...current.data,
                  operations:
                    current.data.operations?.filter(
                      (operation) => operation.id !== id,
                    ) ?? [],
                },
              }
            : current,
        );
        if (trigger && document.activeElement === trigger) {
          heading.current?.focus({ preventScroll: true });
        }
        setDismissed(true);
      }
      await operationsQuery.refetch();
    } catch (reason) {
      setActionError({ action, reason });
    } finally {
      setPending(undefined);
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
          <h1 id="operations-title" ref={heading} tabIndex={-1}>
            {copy.title}
          </h1>
          <p>{copy.description}</p>
        </div>
        <button
          type="button"
          onClick={() => void refresh()}
          disabled={operationsQuery.isFetching}
        >
          {operationsQuery.isFetching ? copy.refreshing : copy.refresh}
        </button>
      </header>

      {operationsQuery.isPending && (
        <p className="operations-state" role="status">
          {copy.loading}
        </p>
      )}

      {operationsQuery.isError && (
        <section className="operations-state operations-error" role="alert">
          <p>{errorMessage(operationsQuery.error, copy.loadError)}</p>
          <button
            type="button"
            onClick={() => void refresh()}
            disabled={operationsQuery.isFetching}
          >
            {copy.retryLoad}
          </button>
        </section>
      )}

      {operationsQuery.data && operationsQuery.data.status !== 200 && (
        <section className="operations-state operations-error" role="alert">
          <p>
            {
              apiResponseError(
                operationsQuery.data,
                copy.loadHttpError(operationsQuery.data.status),
              ).message
            }
          </p>
          <button
            type="button"
            onClick={() => void refresh()}
            disabled={operationsQuery.isFetching}
          >
            {copy.retryLoad}
          </button>
        </section>
      )}

      {actionError && (
        <p className="operations-error" role="alert">
          {"response" in actionError
            ? apiResponseError(
                actionError.response,
                actionError.action === "retry"
                  ? copy.retryHttpError(actionError.response.status)
                  : copy.dismissHttpError(actionError.response.status),
              ).message
            : errorMessage(
                actionError.reason,
                actionError.action === "retry"
                  ? copy.retryError
                  : copy.dismissError,
              )}
        </p>
      )}

      {dismissed && <p role="status">{copy.dismissed}</p>}

      {operationsQuery.data?.status === 200 && operations.length === 0 && (
        <section className="operations-empty">
          <h2>{copy.emptyTitle}</h2>
          <p>{copy.emptyDescription}</p>
        </section>
      )}

      {operations.length > 0 && (
        <div className="operations-table-wrap">
          <table className="operations-table">
            <thead>
              <tr>
                <th scope="col">{copy.target}</th>
                <th scope="col">{copy.operation}</th>
                <th scope="col">{copy.stateStage}</th>
                <th scope="col">{copy.progress}</th>
                <th scope="col">{copy.created}</th>
                <th scope="col">{copy.error}</th>
                <th scope="col">
                  <span className="operations-visually-hidden">
                    {copy.actions}
                  </span>
                </th>
              </tr>
            </thead>
            <tbody>
              {operations.map((operation) => (
                <tr key={operation.id}>
                  <td>{operationTargetLabel(operation, locale)}</td>
                  <td>
                    <strong>
                      {operationKindLabel(operation.kind, locale)}
                    </strong>
                  </td>
                  <td>
                    <span
                      className={`operations-badge operations-${operation.state}`}
                    >
                      {operationStateLabel(operation.state, locale)}
                    </span>
                    {operationStageLabel(operation.stage, locale) && (
                      <small>
                        {operationStageLabel(operation.stage, locale)}
                      </small>
                    )}
                  </td>
                  <td>{operationProgressLabel(operation, locale)}</td>
                  <td>{formatDate(operation.created_at, locale)}</td>
                  <td className="operations-diagnostic">
                    {operation.safe_error || "—"}
                  </td>
                  <td>
                    {operation.state === "failed" && (
                      <div className="flex flex-wrap gap-1">
                        {GENERIC_RETRY_KINDS.has(operation.kind) && (
                          <button
                            type="button"
                            onClick={() => void perform(operation.id, "retry")}
                            disabled={Boolean(pending)}
                          >
                            {pending?.id === operation.id &&
                            pending.action === "retry"
                              ? copy.retrying
                              : copy.retry}
                          </button>
                        )}
                        {DISMISSIBLE_TOOL_KINDS.has(operation.kind) && (
                          <button
                            type="button"
                            onClick={(event) =>
                              void perform(
                                operation.id,
                                "dismiss",
                                event.currentTarget,
                              )
                            }
                            disabled={Boolean(pending)}
                          >
                            {pending?.id === operation.id &&
                            pending.action === "dismiss"
                              ? copy.dismissing
                              : copy.dismiss}
                          </button>
                        )}
                      </div>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <p className="operations-footnote">{copy.footnote}</p>
    </section>
  );
}
