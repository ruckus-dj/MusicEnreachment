import { useCallback, useEffect, useMemo, useRef, useState } from "react";
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
import { useI18n } from "../../app/i18n/host";
import {
  type GenreCatalogFeedback,
  GenreCatalogView,
} from "../../ui/features/settings/GenreCatalogView";
import { SettingsMessageError, settingsHttpError } from "./settingsMessages";

function genreFeedback(
  reason: unknown,
  fallback: "status" | "refresh" | "operation",
): GenreCatalogFeedback {
  if (reason instanceof SettingsMessageError) {
    const status = reason.descriptor.values?.status;
    if (
      reason.descriptor.key === "httpError" &&
      (typeof status === "string" || typeof status === "number")
    ) {
      return { kind: "http", status };
    }
  }
  return reason instanceof Error && reason.message
    ? { kind: "detail", text: reason.message }
    : { kind: fallback };
}

export function GenreCatalogPanel() {
  const { locale } = useI18n();
  const [status, setStatus] = useState<GenreCatalogStatus>();
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [operationId, setOperationId] = useState<string>();
  const [operation, setOperation] = useState<OperationResponse>();
  const [error, setError] = useState<GenreCatalogFeedback>();
  const [refreshRequested, setRefreshRequested] = useState(false);
  const mounted = useRef(false);
  const statusRequestGeneration = useRef(0);
  const items = useMemo(
    () =>
      (status?.items ?? []).map(({ id, name, source_name }) => ({
        id,
        name,
        sourceName: source_name,
      })),
    [status?.items],
  );

  const loadStatus = useCallback(async () => {
    const generation = ++statusRequestGeneration.current;
    const response = await getGenreCatalog({ cache: "no-store" });
    if (response.status !== 200) throw settingsHttpError(response);
    if (mounted.current && generation === statusRequestGeneration.current) {
      const next = response.data;
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
            setError(genreFeedback(settingsHttpError(response), "status"));
            return;
          }
          const next = response.data;
          setStatus(next);
          if (next.active_operation_id)
            setOperationId(next.active_operation_id);
        },
        (reason: unknown) => {
          if (active && generation === statusRequestGeneration.current)
            setError(genreFeedback(reason, "status"));
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
          setError(genreFeedback(reason, "status")),
        );
        setOperationId(undefined);
      }
    },
    [loadStatus],
  );
  const onOperationError = useCallback((reason: Error) => {
    setError(genreFeedback(reason, "operation"));
  }, []);
  const onOperationHttpError = useCallback(
    (response: { status: number; data?: { detail?: string } }) =>
      settingsHttpError(response),
    [],
  );
  useOperationFollower(operationId, {
    enabled: Boolean(operationId),
    onSnapshot: onOperationSnapshot,
    onError: onOperationError,
    onHttpError: onOperationHttpError,
  });

  async function refresh() {
    if (refreshing || operationId) return;
    setRefreshing(true);
    setError(undefined);
    setRefreshRequested(false);
    try {
      const response = await refreshGenreCatalog();
      if (response.status !== 200) throw settingsHttpError(response);
      const admitted = response.data as unknown as OperationResponse;
      setOperation(admitted);
      setOperationId(admitted.id);
      setRefreshRequested(true);
    } catch (reason) {
      setError(genreFeedback(reason, "refresh"));
    } finally {
      setRefreshing(false);
    }
  }

  return (
    <GenreCatalogView
      locale={locale}
      items={items}
      status={
        status && {
          count: status.count,
          available: status.count > 0,
          updatedAt: status.updated_at,
          attemptedAt: status.last_attempt_at,
          lastError: status.last_error,
        }
      }
      loading={loading}
      refreshState={
        refreshing ? "submitting" : operationId ? "admitted" : "idle"
      }
      operation={
        operation && {
          active:
            operation.state === "queued" || operation.state === "running"
              ? operation.state
              : undefined,
          stage: operation.stage,
          error: operation.safe_error,
        }
      }
      refreshRequested={refreshRequested}
      error={error}
      onRefresh={() => void refresh()}
    />
  );
}
