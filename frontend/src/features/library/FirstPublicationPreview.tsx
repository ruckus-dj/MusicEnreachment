import { useEffect, useRef, useState } from "react";
import {
  getLibraryFirstPublicationPreview,
  startLibraryFirstPublication,
} from "../../api/generated/client";
import type {
  FirstPublicationPreviewSource,
  LibraryFirstPublicationPreviewResponse,
} from "../../api/generated/client.schemas";

type SourceChoice = Pick<
  FirstPublicationPreviewSource,
  "location_id" | "media_variant_id" | "stream_index"
>;

function choiceKey(choice: SourceChoice | null) {
  return choice
    ? `${choice.location_id}:${choice.media_variant_id}:${choice.stream_index}`
    : "unresolved";
}

function sameChoice(
  source: FirstPublicationPreviewSource,
  choice: SourceChoice,
) {
  return (
    source.location_id === choice.location_id &&
    source.media_variant_id === choice.media_variant_id &&
    source.stream_index === choice.stream_index
  );
}

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
  const [selectedChoice, setSelectedChoice] = useState<SourceChoice | null>(
    null,
  );
  const [sourceCandidates, setSourceCandidates] = useState<
    FirstPublicationPreviewSource[]
  >([]);
  const [previewChoiceKey, setPreviewChoiceKey] = useState("");
  const [loading, setLoading] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [error, setError] = useState("");
  const [admittedOperationID, setAdmittedOperationID] = useState("");
  const [admissionUncertain, setAdmissionUncertain] = useState(false);
  const mounted = useRef(false);
  const requestSequence = useRef(0);
  const currentIdentity = useRef({
    releaseId,
    positionId,
    choiceKey: "unresolved",
  });
  currentIdentity.current = {
    releaseId,
    positionId,
    choiceKey: choiceKey(selectedChoice),
  };

  useEffect(() => {
    currentIdentity.current = {
      releaseId,
      positionId,
      choiceKey: "unresolved",
    };
    mounted.current = true;
    setSelectedChoice(null);
    setSourceCandidates([]);
    setPreview(undefined);
    setPreviewChoiceKey("");
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
    requestIdentity: {
      releaseId: string;
      positionId: string;
      choiceKey: string;
    },
  ) {
    return (
      mounted.current &&
      requestSequence.current === request &&
      currentIdentity.current.releaseId === requestIdentity.releaseId &&
      currentIdentity.current.positionId === requestIdentity.positionId &&
      currentIdentity.current.choiceKey === requestIdentity.choiceKey
    );
  }

  async function loadPreview(choice: SourceChoice | null = selectedChoice) {
    const request = ++requestSequence.current;
    const requestIdentity = {
      releaseId,
      positionId,
      choiceKey: choiceKey(choice),
    };
    currentIdentity.current = requestIdentity;
    setLoading(true);
    setError("");
    setPreview(undefined);
    setPreviewChoiceKey("");
    try {
      const response = await getLibraryFirstPublicationPreview(
        releaseId,
        positionId,
        choice
          ? {
              location_id: choice.location_id,
              media_variant_id: choice.media_variant_id,
              stream_index: String(choice.stream_index),
            }
          : undefined,
        { cache: "no-store" },
      );
      if (!isCurrentRequest(request, requestIdentity)) return;
      if (response.status !== 200) {
        setError(
          response.status === 409 && choice
            ? "Выбранный источник больше недоступен. Выберите источник заново и запросите новый предпросмотр."
            : response.status === 409
              ? "Снимок устарел. Обновите данные релиза и запросите новый предпросмотр."
              : responseError(response.data, response.status),
        );
        return;
      }
      setPreview(response.data);
      setSourceCandidates(response.data.source_candidates ?? []);
      setPreviewChoiceKey(requestIdentity.choiceKey);
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

  function changeChoice(choice: SourceChoice | null) {
    if (confirming || admissionUncertain || admittedOperationID) return;
    const nextKey = choiceKey(choice);
    ++requestSequence.current;
    currentIdentity.current = { releaseId, positionId, choiceKey: nextKey };
    setSelectedChoice(choice);
    setPreview(undefined);
    setPreviewChoiceKey("");
    setLoading(false);
    setError("");
    setAdmissionUncertain(false);
    setAdmittedOperationID("");
    void loadPreview(choice);
  }

  async function confirmPreview() {
    if (!preview || !canConfirm || confirming || admissionUncertain) return;
    const request = ++requestSequence.current;
    const confirmedChoice = selectedChoice;
    const requestIdentity = {
      releaseId,
      positionId,
      choiceKey: choiceKey(confirmedChoice),
    };
    const confirmedPreview = preview;
    if (previewChoiceKey !== requestIdentity.choiceKey) return;
    setConfirming(true);
    setError("");
    try {
      const response = await startLibraryFirstPublication(
        releaseId,
        positionId,
        {
          preview_digest: confirmedPreview.context_digest,
          expected_epoch: confirmedPreview.epoch,
          ...(confirmedChoice && confirmedPreview.selected_source
            ? {
                location_id: confirmedPreview.selected_source.location_id,
                media_variant_id:
                  confirmedPreview.selected_source.media_variant_id,
                stream_index: confirmedPreview.selected_source.stream_index,
              }
            : {}),
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
          confirmedChoice
            ? "Выбранный источник больше недоступен. Выберите источник заново и запросите новый предпросмотр."
            : "Предпросмотр устарел. Обновите состояние и запросите новый предпросмотр; подтверждение не повторено.",
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

  const candidates = sourceCandidates;
  const needsExplicitChoice =
    candidates.length > 1 ||
    candidates.some((candidate) => candidate.audio_stream_count > 1);
  const choiceMatchesPreview = Boolean(
    selectedChoice &&
      preview?.selected_source &&
      sameChoice(preview.selected_source, selectedChoice),
  );
  const canConfirm = Boolean(
    preview &&
      previewChoiceKey === choiceKey(selectedChoice) &&
      preview.blockers?.length === 0 &&
      preview.selected_source &&
      (!needsExplicitChoice || choiceMatchesPreview) &&
      preview.transform &&
      preview.resolved_path_components?.length,
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
      {needsExplicitChoice && (
        <fieldset
          className="space-y-2"
          disabled={
            confirming || admissionUncertain || Boolean(admittedOperationID)
          }
        >
          <legend className="font-medium">
            Выберите аудиопоток для публикации
          </legend>
          <label className="block">
            <input
              type="radio"
              name={`publication-source-${releaseId}-${positionId}`}
              checked={selectedChoice === null}
              onChange={() => changeChoice(null)}
            />{" "}
            Источник не выбран
          </label>
          {candidates.map((candidate) => {
            const candidateChoice: SourceChoice = {
              location_id: candidate.location_id,
              media_variant_id: candidate.media_variant_id,
              stream_index: candidate.stream_index,
            };
            return (
              <label className="block" key={choiceKey(candidateChoice)}>
                <input
                  type="radio"
                  name={`publication-source-${releaseId}-${positionId}`}
                  checked={
                    choiceKey(selectedChoice) === choiceKey(candidateChoice)
                  }
                  onChange={() => changeChoice(candidateChoice)}
                />{" "}
                {candidate.title || candidate.codec} · {candidate.codec}, поток{" "}
                {candidate.stream_index}
                {candidate.language ? ` · ${candidate.language}` : ""}
                {candidate.channels ? ` · ${candidate.channels} канал(а)` : ""}
                {candidate.unsupported_audio_stream_count > 0
                  ? ` · неподдерживаемых аудиопотоков: ${candidate.unsupported_audio_stream_count}`
                  : ""}
                {` · корень ${candidate.root_id}, расположение ${candidate.location_id}, вариант ${candidate.media_variant_id}`}
              </label>
            );
          })}
        </fieldset>
      )}
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
            {preview.resolved_path_components?.length
              ? preview.resolved_path_components.join("/")
              : "не определён; namespace не подтверждён"}
          </p>
          <div className="rounded bg-amber-50 p-3">
            <h4 className="font-medium">
              Планируемые теги (не фактическое чтение файла)
            </h4>
            {Object.entries(preview.desired_tags ?? {}).map(
              ([name, values]) => (
                <p key={name}>
                  {name}: {(values ?? []).join(", ")}
                </p>
              ),
            )}
          </div>
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
