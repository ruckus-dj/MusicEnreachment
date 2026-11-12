import { useCallback, useEffect, useRef, useState } from "react";
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
import { useI18n } from "../../i18n/I18nProvider";
import {
  SettingsMessageError,
  type SettingsMessageKey,
  type SettingsMessageValues,
  settingsHttpError,
  settingsMessage,
} from "./settingsMessages";

type GenreCatalogPanelStatus = GenreCatalogStatus & {
  active_operation_id?: string | null;
};
type GenreNotice = {
  key: SettingsMessageKey;
  values?: SettingsMessageValues;
};
type GenreFeedback = string | GenreNotice;

function genreFeedback(
  reason: unknown,
  key: SettingsMessageKey,
): GenreFeedback {
  if (reason instanceof SettingsMessageError) return reason.descriptor;
  return reason instanceof Error && reason.message ? reason.message : { key };
}

function displayTime(value: string | null, locale: "en" | "ru") {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString(locale);
}

export function GenreCatalogPanel() {
  const { locale } = useI18n();
  const t = (
    key: Parameters<typeof settingsMessage>[1],
    values?: Parameters<typeof settingsMessage>[2],
  ) => settingsMessage(locale, key, values);
  const [status, setStatus] = useState<GenreCatalogPanelStatus>();
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [operationId, setOperationId] = useState<string>();
  const [operation, setOperation] = useState<OperationResponse>();
  const [error, setError] = useState<GenreFeedback | "">("");
  const [notice, setNotice] = useState<GenreNotice | null>(null);
  const mounted = useRef(false);
  const statusRequestGeneration = useRef(0);

  const loadStatus = useCallback(async () => {
    const generation = ++statusRequestGeneration.current;
    const response = await getGenreCatalog({ cache: "no-store" });
    if (response.status !== 200) throw settingsHttpError(response);
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
            setError(
              genreFeedback(settingsHttpError(response), "genreStatusError"),
            );
            return;
          }
          const next = response.data as GenreCatalogPanelStatus;
          setStatus(next);
          if (next.active_operation_id)
            setOperationId(next.active_operation_id);
        },
        (reason: unknown) => {
          if (active && generation === statusRequestGeneration.current)
            setError(genreFeedback(reason, "genreStatusError"));
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
          setError(genreFeedback(reason, "genreStatusError")),
        );
        setOperationId(undefined);
      }
    },
    [loadStatus],
  );
  const onOperationError = useCallback((reason: Error) => {
    setError(genreFeedback(reason, "operationStatusError"));
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
    setError("");
    setNotice(null);
    try {
      const response = await refreshGenreCatalog();
      if (response.status !== 200) throw settingsHttpError(response);
      const admitted = response.data as unknown as OperationResponse;
      setOperation(admitted);
      setOperationId(admitted.id);
      setNotice({ key: "genreRefreshRequested" });
    } catch (reason) {
      setError(genreFeedback(reason, "genreRefreshError"));
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
          <h2 id="genre-catalog-title">{t("genres")}</h2>
          <p>{t("genreCatalogDescription")}</p>
        </div>
        <AppButton
          isDisabled={refreshing || Boolean(operationId)}
          onPress={() => void refresh()}
        >
          {refreshing ? t("genreRefreshSending") : t("genreRefresh")}
        </AppButton>
      </div>
      {loading && <p role="status">{t("loadingStatus")}</p>}
      {status && (
        <dl className="genre-catalog-status">
          <div>
            <dt>{t("genreCount")}</dt>
            <dd>{status.count.toLocaleString(locale)}</dd>
          </div>
          <div>
            <dt>{t("lastSuccessfulUpdate")}</dt>
            <dd>{displayTime(status.updated_at, locale)}</dd>
          </div>
          <div>
            <dt>{t("lastAttempt")}</dt>
            <dd>{displayTime(status.last_attempt_at, locale)}</dd>
          </div>
        </dl>
      )}
      {status?.last_error && (
        <p className="genre-catalog-last-error">
          {t("lastError")} {status.last_error}
        </p>
      )}
      {operationActive && (
        <p role="status">
          {t("catalogUpdating")}{" "}
          {operation.state === "queued" ? t("queued") : t("running")}
          {operation.stage ? ` — ${operation.stage}` : ""}
        </p>
      )}
      {operation?.safe_error && (
        <p className="genre-catalog-last-error">
          {t("genreRefreshError")} {operation.safe_error}
        </p>
      )}
      {error && (
        <p role="alert">
          {typeof error === "string" ? error : t(error.key, error.values)}
        </p>
      )}
      {notice && <p role="status">{t(notice.key)}</p>}
    </section>
  );
}
