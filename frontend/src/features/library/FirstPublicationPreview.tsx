import { useEffect, useRef, useState } from "react";
import {
  getLibraryFirstPublicationPreview,
  startLibraryFirstPublication,
} from "../../api/generated/client";
import type { LibraryFirstPublicationPreviewResponse } from "../../api/generated/client.schemas";

function responseError(data: { detail?: string }, status: number) {
  return data.detail || `Запрос не выполнен (HTTP ${status}).`;
}

export function FirstPublicationPreview({
  releaseId,
  positionId,
  onAdmitted,
  onRefresh,
}: {
  releaseId: string;
  positionId: string;
  onAdmitted: (operationID: string) => void;
  onRefresh: () => void;
}) {
  const [preview, setPreview] =
    useState<LibraryFirstPublicationPreviewResponse>();
  const [loading, setLoading] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [error, setError] = useState("");
  const [admittedOperationID, setAdmittedOperationID] = useState("");
  const [admissionUncertain, setAdmissionUncertain] = useState(false);
  const mounted = useRef(false);
  const requestSequence = useRef(0);
  const currentIdentity = useRef({ releaseId, positionId });
  currentIdentity.current = { releaseId, positionId };

  useEffect(() => {
    currentIdentity.current = { releaseId, positionId };
    mounted.current = true;
    setPreview(undefined);
    setLoading(false);
    setConfirming(false);
    setError("");
    setAdmittedOperationID("");
    setAdmissionUncertain(false);
    return () => {
      mounted.current = false;
      requestSequence.current += 1;
    };
  }, [releaseId, positionId]);

  function isCurrentRequest(
    request: number,
    requestIdentity: { releaseId: string; positionId: string },
  ) {
    return (
      mounted.current &&
      requestSequence.current === request &&
      currentIdentity.current.releaseId === requestIdentity.releaseId &&
      currentIdentity.current.positionId === requestIdentity.positionId
    );
  }

  async function loadPreview() {
    const request = ++requestSequence.current;
    const requestIdentity = { releaseId, positionId };
    setLoading(true);
    setError("");
    setPreview(undefined);
    try {
      const response = await getLibraryFirstPublicationPreview(
        releaseId,
        positionId,
        { cache: "no-store" },
      );
      if (!isCurrentRequest(request, requestIdentity)) return;
      if (response.status !== 200) {
        setError(
          response.status === 409
            ? "Снимок устарел. Обновите данные релиза и запросите новый предпросмотр."
            : responseError(response.data, response.status),
        );
        return;
      }
      setPreview(response.data);
    } catch (reason) {
      if (!isCurrentRequest(request, requestIdentity)) return;
      setError(
        reason instanceof Error && reason.message
          ? reason.message
          : "Не удалось загрузить предпросмотр. Проверьте соединение и повторите запрос.",
      );
    } finally {
      if (isCurrentRequest(request, requestIdentity)) setLoading(false);
    }
  }

  async function confirmPreview() {
    if (!preview || !canConfirm || confirming || admissionUncertain) return;
    const request = ++requestSequence.current;
    const requestIdentity = { releaseId, positionId };
    const confirmedPreview = preview;
    setConfirming(true);
    setError("");
    try {
      const response = await startLibraryFirstPublication(
        releaseId,
        positionId,
        {
          preview_digest: confirmedPreview.context_digest,
          expected_epoch: confirmedPreview.epoch,
        },
      );
      if (!isCurrentRequest(request, requestIdentity)) return;
      if (response.status === 202) {
        setAdmittedOperationID(response.data.id);
        setPreview(undefined);
        onAdmitted(response.data.id);
        return;
      }
      if (response.status === 409) {
        setPreview(undefined);
        setError(
          "Предпросмотр устарел. Обновите состояние и запросите новый предпросмотр; подтверждение не повторено.",
        );
        onRefresh();
        return;
      }
      setAdmissionUncertain(true);
      setError(
        `${responseError(response.data, response.status)} Результат запуска неизвестен. Обновите состояние; повторное подтверждение отключено.`,
      );
    } catch (reason) {
      if (!isCurrentRequest(request, requestIdentity)) return;
      setAdmissionUncertain(true);
      setError(
        `${reason instanceof Error && reason.message ? reason.message : "Не удалось подтвердить предпросмотр."} Результат запуска неизвестен. Обновите состояние; повторное подтверждение отключено.`,
      );
    } finally {
      if (isCurrentRequest(request, requestIdentity)) setConfirming(false);
    }
  }

  const canConfirm = Boolean(
    preview &&
      preview.blockers?.length === 0 &&
      preview.selected_source &&
      preview.transform &&
      preview.path_components?.length,
  );

  return (
    <section
      className="space-y-3 rounded border border-stone-300 p-4"
      aria-label="Предпросмотр первой публикации"
    >
      <button
        type="button"
        disabled={
          loading ||
          confirming ||
          Boolean(admittedOperationID) ||
          admissionUncertain
        }
        onClick={() => void loadPreview()}
      >
        {loading ? "Загрузка предпросмотра…" : "Предпросмотр публикации"}
      </button>
      {error && <p role="alert">{error}</p>}
      {preview && (
        <div className="space-y-2">
          <h3 className="font-semibold">План первой публикации</h3>
          <p>
            Источник:{" "}
            {preview.selected_source
              ? `${preview.selected_source.codec}, поток ${preview.selected_source.stream_index} · вариант ${preview.selected_source.media_variant_id}`
              : "не выбран"}
          </p>
          <p>
            Преобразование:{" "}
            {preview.transform
              ? `${preview.transform.action} → ${preview.transform.output_container}${preview.transform.extension ? ` (.${preview.transform.extension})` : ""}${preview.transform.bitrate ? `, ${preview.transform.bitrate} бит/с` : ""}`
              : "не определено"}
          </p>
          <p>
            Планируемый относительный путь:{" "}
            {preview.path_components?.length
              ? preview.path_components.join("/")
              : "не определён"}
          </p>
          <div>
            <h4 className="font-medium">Проекции метаданных (ревизии)</h4>
            {Object.keys(preview.metadata_revisions ?? {}).length ? (
              <ul>
                {Object.entries(preview.metadata_revisions).map(
                  ([field, revision]) => (
                    <li key={field}>
                      {field}: ревизия {revision}
                    </li>
                  ),
                )}
              </ul>
            ) : (
              <p>Нет проекций метаданных.</p>
            )}
          </div>
          <div>
            <h4 className="font-medium">Предупреждения</h4>
            {preview.warnings?.length ? (
              <ul>
                {Array.from(new Set(preview.warnings ?? [])).map((item) => (
                  <li key={item}>{item}</li>
                ))}
              </ul>
            ) : (
              <p>Нет.</p>
            )}
          </div>
          <div>
            <h4 className="font-medium">Блокирующие условия</h4>
            {preview.blockers?.length ? (
              <ul>
                {Array.from(new Set(preview.blockers ?? [])).map((item) => (
                  <li key={item}>{item}</li>
                ))}
              </ul>
            ) : (
              <p>Нет.</p>
            )}
          </div>
          <p role="status">
            Проверка namespace перед запуском: {preview.native_status}.
            Предпросмотр не проверяет возможность публикации.
          </p>
          <button
            type="button"
            disabled={!canConfirm || confirming || admissionUncertain}
            onClick={() => void confirmPreview()}
          >
            {confirming ? "Проверка и запуск…" : "Опубликовать"}
          </button>
        </div>
      )}
      {admittedOperationID && (
        <div>
          <p role="status">
            Операция публикации поставлена в очередь: {admittedOperationID}.
          </p>
          <button type="button" onClick={onRefresh}>
            Обновить состояние публикации
          </button>
        </div>
      )}
      {admissionUncertain && (
        <button type="button" onClick={onRefresh}>
          Обновить состояние публикации
        </button>
      )}
    </section>
  );
}
