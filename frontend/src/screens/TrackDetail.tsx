import { useState } from "react";
import { CandidateReview } from "../components/CandidateReview";
import { MetadataComparison } from "../components/MetadataComparison";
import {
  isPublicationHash,
  latestRevision,
  TAG_FIELDS,
  tagsFor,
  workflowStatus,
} from "../domain/metadata";
import type { Detail, RecordingCorrection, Tags } from "../types";

export function TrackDetail({
  detail,
  sourceId,
  draft,
  setDraft,
  saving,
  reprocessing,
  musicbrainzHost,
  onSave,
  onRetryAcoustId,
  onRetryMusicBrainz,
  onOverrideRelease,
  onOverrideRecording,
  onSelectCandidate,
  onSelectEffectiveSource,
  effectiveSourceId,
  effectiveSourceError,
  effectiveSourceSuccess,
  recordingCorrectionError,
  recordingCorrectionReview,
}: {
  readonly detail: Detail | null;
  readonly sourceId: string;
  readonly draft: Tags;
  readonly setDraft: (value: Tags) => void;
  readonly saving: boolean;
  readonly reprocessing: boolean;
  readonly musicbrainzHost?: string | null;
  readonly onSave: () => Promise<boolean>;
  readonly onRetryAcoustId?: () => void;
  readonly onRetryMusicBrainz?: () => void;
  readonly onOverrideRelease?: (releaseMbid: string) => void;
  readonly onOverrideRecording?: (request: RecordingCorrection) => void;
  readonly onSelectCandidate: (selection: string) => void;
  readonly onSelectEffectiveSource: (sourceId: string) => void;
  readonly effectiveSourceId: string | null;
  readonly effectiveSourceError: string;
  readonly effectiveSourceSuccess: string;
  readonly recordingCorrectionError: string;
  readonly recordingCorrectionReview: string;
}) {
  const [editing, setEditing] = useState(false);
  const [releaseOverride, setReleaseOverride] = useState("");
  const [recordingOverride, setRecordingOverride] = useState("");
  const [recordingCorrectionValidationError, setRecordingCorrectionValidationError] = useState("");
  const source = detail?.sources.find((item) => item.source_id === sourceId);
  if (!detail || !source) return <div className="empty-state">Открываем данные трека…</div>;

  const status = workflowStatus(detail);
  const finalRevision = latestRevision(detail, sourceId, "final");
  const currentPublication = detail.publications.find(
    (item) => item.state === "current" && isPublicationHash(item.sha256),
  );
  const publicationRevision = currentPublication?.metadata_revision_id
    ? detail.metadata_revisions?.find((item) => item.id === currentPublication.metadata_revision_id)
    : undefined;
  const fingerprint = source.fingerprints?.at(-1);
  const fingerprintDuration = fingerprint?.duration_seconds;
  const roundedDurationSeconds =
    fingerprintDuration === null || fingerprintDuration === undefined
      ? null
      : Math.round(fingerprintDuration);
  const duration =
    roundedDurationSeconds === null
      ? "Не определена"
      : `${Math.floor(roundedDurationSeconds / 60)}:${String(roundedDurationSeconds % 60).padStart(2, "0")}`;
  const attempts = source.provider_attempts ?? [];
  const originalTags = tagsFor(detail, sourceId, "original");
  const analyzedTags = tagsFor(detail, sourceId, "analyzed");
  const candidates = source.candidates ?? [];
  const selectedAcoustId = detail.musicbrainz_recording_id ?? null;
  const selectedMusicBrainz = detail.musicbrainz_release_id ?? null;
  const releaseCandidates = candidates.filter(
    (candidate) =>
      (candidate.evidence.entity ??
        (candidate.evidence.provider === "acoustid" ? "recording" : "release")) === "release",
  );
  const recordingCandidates = candidates
    .filter(
      (candidate) =>
        (candidate.evidence.entity ??
          (candidate.evidence.provider === "acoustid" ? "recording" : "release")) === "recording",
    )
    .map((candidate) => {
      if ((candidate.evidence.compatible_ids?.length ?? 0) > 0) return candidate;
      const compatibleReleaseIds = releaseCandidates
        .filter((release) => release.evidence.compatible_ids?.includes(candidate.candidate_key))
        .map((release) => release.candidate_key);
      return {
        ...candidate,
        evidence: { ...candidate.evidence, compatible_ids: compatibleReleaseIds },
      };
    });
  const visibleTagFields = [
    ...new Set([
      ...TAG_FIELDS,
      ...Object.keys(originalTags),
      ...Object.keys(analyzedTags),
      ...Object.keys(draft),
    ]),
  ];
  const trackTitle =
    originalTags.TITLE || draft.TITLE || source.path.split("/").at(-1) || "Без названия";
  const trackAlbum = originalTags.ALBUM || draft.ALBUM || "Без альбома";
  const selectableSources = detail.sources.filter((item) => item.state !== "disappeared");
  const effectiveSource = detail.sources.find((item) => item.source_id === effectiveSourceId);
  const hasProviderEvidence =
    recordingCandidates.length > 0 ||
    releaseCandidates.length > 0 ||
    selectedAcoustId ||
    selectedMusicBrainz;
  const retryAcoustId = onRetryAcoustId ?? (() => undefined);
  const retryMusicBrainz = onRetryMusicBrainz ?? (() => undefined);
  const cancelEditing = () => {
    setDraft({ ...tagsFor(detail, sourceId, "final") });
    setEditing(false);
  };
  const saveEditing = async () => {
    if (await onSave()) setEditing(false);
  };
  const overrideRelease = () => {
    const releaseMbid = releaseOverride.trim();
    if (!releaseMbid) return;
    onOverrideRelease?.(releaseMbid);
  };
  const overrideRecording = () => {
    const recordingMbid = recordingOverride.trim();
    if (!recordingMbid) return;
    setRecordingCorrectionValidationError("");
    onOverrideRecording?.({ recording_mbid: recordingMbid });
  };

  const lifecycleLabels = {
    source:
      source.state === "present"
        ? "На месте"
        : source.state === "disappeared"
          ? "Файл отсутствует"
          : source.state,
    processing:
      detail.states.processing === "complete"
        ? "Завершена"
        : detail.states.processing === "needs_review"
          ? "Проверка оператора"
          : detail.states.processing,
    match:
      detail.states.match === "matched"
        ? "Подтверждено"
        : detail.states.match === "needs_review"
          ? "Проверка оператора"
          : detail.states.match,
    publication: currentPublication
      ? "Актуальна"
      : detail.states.publication === "absent"
        ? "Нет публикации"
        : detail.states.publication,
    metadata: detail.states.metadata === "final" ? "Финальная" : detail.states.metadata,
  };

  return (
    <section className="inspector">
      <div
        className={`workflow-banner ${status.tone}`}
        role={status.tone === "error" ? "alert" : "status"}
      >
        <span className="workflow-dot" />
        <div>
          <strong>{status.label}</strong>
          <small>{status.detail}</small>
        </div>
        <span className="workflow-revision">
          {finalRevision ? `Final rev ${finalRevision.revision}` : "Final ещё не создана"}
        </span>
      </div>
      {source.state === "disappeared" && (
        <p data-testid="source-unavailable" role="status" className="source-unavailable">
          Исходный файл не найден. Исходные теги и анализ сохранены для текущей публикации.
        </p>
      )}

      <div className="track-context">
        <p className="eyebrow">Сравнение файла</p>
        <h2>{trackTitle}</h2>
        <p>
          {originalTags.ARTIST || draft.ARTIST || "Исполнитель не указан"} · {trackAlbum}
        </p>
      </div>

      <section className="evidence-card provider-panel" aria-labelledby="effective-source-title">
        <div className="section-heading">
          <div>
            <p className="eyebrow">Источник публикации</p>
            <h2 id="effective-source-title">Выберите файл для публикации</h2>
          </div>
          <span data-testid="effective-source-current" className="badge">
            {effectiveSource
              ? `Текущий: ${effectiveSource.path.split("/").at(-1)}`
              : "Источник не выбран"}
          </span>
        </div>
        <label>
          Файл-источник
          <select
            data-testid="effective-source-choice"
            value={effectiveSourceId ?? ""}
            disabled={reprocessing}
            onChange={(event) => onSelectEffectiveSource(event.target.value)}
          >
            <option value="" disabled>
              Выберите источник
            </option>
            {selectableSources.map((item) => (
              <option value={item.source_id} key={item.source_id}>
                {item.path.split("/").at(-1) ?? item.source_id}
              </option>
            ))}
          </select>
        </label>
        {effectiveSourceSuccess && (
          <p data-testid="effective-source-success" role="status" className="settings-help">
            {effectiveSourceSuccess}
          </p>
        )}
        {effectiveSourceError && (
          <p data-testid="effective-source-error" role="alert" className="settings-help">
            {effectiveSourceError}
          </p>
        )}
      </section>

      <section
        className="evidence-card publication-panel"
        aria-labelledby="publication-status-title"
      >
        <div className="section-heading">
          <div>
            <p className="eyebrow">Публикация</p>
            <h2 id="publication-status-title">Выходной файл</h2>
          </div>
          <span
            data-testid="output-status"
            className={`badge ${currentPublication ? "success" : ""}`}
          >
            {lifecycleLabels.publication}
          </span>
        </div>
        {!currentPublication && (
          <p data-testid="no-output" role="status" className="settings-help">
            Выходной файл ещё не создан. После проверки оператором появится управляемая медиакопия.
          </p>
        )}
        <div className="evidence-grid">
          <div>
            <span>Путь выхода</span>
            <strong data-testid="output-path">{currentPublication?.path ?? "Не создан"}</strong>
            <small>Управляемая медиакопия</small>
          </div>
          <div>
            <span>Хэш выхода</span>
            <strong data-testid="output-hash">
              {currentPublication?.sha256 ?? "Не рассчитана"}
            </strong>
            <small>SHA-256 опубликованного файла</small>
          </div>
          <div>
            <span>Ревизия выхода</span>
            <strong data-testid="output-revision">
              {publicationRevision ? `Final rev ${publicationRevision.revision}` : "Нет данных"}
            </strong>
            <small>Метаданные, с которыми создан выход</small>
          </div>
          <div data-testid="source-lifecycle">
            <span>Жизненный цикл</span>
            <strong>Источник: {lifecycleLabels.source}</strong>
            <small>
              Обработка: {lifecycleLabels.processing} · Сопоставление: {lifecycleLabels.match} ·
              Метаданные: {lifecycleLabels.metadata}
            </small>
          </div>
        </div>
      </section>

      <MetadataComparison
        fields={visibleTagFields}
        originalTags={originalTags}
        analyzedTags={analyzedTags}
        draft={draft}
        editing={editing}
        saving={saving}
        onEdit={() => setEditing(true)}
        onCancel={cancelEditing}
        onChange={(field, value) => setDraft({ ...draft, [field]: value })}
        onSave={() => void saveEditing()}
      />

      {hasProviderEvidence && (
        <section className="evidence-card provider-panel" aria-labelledby="provider-panel-title">
          <div className="section-heading">
            <div>
              <p className="eyebrow">Провайдеры</p>
              <h2 id="provider-panel-title">Подтверждение источника</h2>
            </div>
            <div className="provider-actions">
              <button
                type="button"
                className="secondary"
                disabled={reprocessing}
                onClick={retryAcoustId}
              >
                {reprocessing ? "В очереди…" : "Повторить AcousticID"}
              </button>
              <button
                type="button"
                className="secondary"
                disabled={reprocessing}
                onClick={retryMusicBrainz}
              >
                {reprocessing ? "В очереди…" : "Повторить MusicBrainz"}
              </button>
            </div>
          </div>
          <div className="provider-summary">
            <span>
              {detail.states.match === "matched"
                ? "Выбранные варианты сохранены"
                : "Выберите варианты, чтобы продолжить"}
            </span>
            <small>Списки кандидатов скрыты до раскрытия.</small>
          </div>
          {recordingCandidates.length > 0 || selectedAcoustId ? (
            <CandidateReview
              entity="recording"
              candidates={recordingCandidates}
              selectedKey={selectedAcoustId}
              compatibleWith={selectedMusicBrainz}
              reason={`Сравните исполнителя и название записи «${trackTitle}» с исходными тегами.`}
              disabled={reprocessing}
              musicbrainzHost={musicbrainzHost ?? null}
              onSelect={onSelectCandidate}
            />
          ) : null}
          {releaseCandidates.length > 0 || selectedMusicBrainz ? (
            <CandidateReview
              entity="release"
              candidates={releaseCandidates}
              selectedKey={selectedMusicBrainz}
              compatibleWith={selectedAcoustId}
              reason={`Выберите релиз MusicBrainz для трека «${trackTitle}» из альбома «${trackAlbum}».`}
              disabled={reprocessing}
              musicbrainzHost={musicbrainzHost ?? null}
              onSelect={onSelectCandidate}
            />
          ) : null}
          <details className="release-override">
            <summary>Выбрать release MBID вручную</summary>
            <p className="candidate-reason">Используйте это только если найденный релиз неверен.</p>
            <div className="provider-actions">
              <input
                aria-label="MusicBrainz release ID"
                value={releaseOverride}
                onChange={(event) => setReleaseOverride(event.target.value)}
                placeholder="release MBID"
              />
              <button
                type="button"
                className="primary"
                disabled={!releaseOverride.trim() || reprocessing}
                onClick={overrideRelease}
              >
                {reprocessing ? "Загружаем…" : "Загрузить release"}
              </button>
            </div>
          </details>
          <details className="release-override">
            <summary>Выбрать recording MBID вручную</summary>
            <p className="candidate-reason">
              Используйте это, если автоматически выбрана неверная запись трека.
            </p>
            <div className="provider-actions">
              <input
                data-testid="recording-mbid-input"
                aria-label="MusicBrainz recording ID"
                aria-required="true"
                value={recordingOverride}
                onChange={(event) => setRecordingOverride(event.target.value)}
                placeholder="recording MBID"
              />
              {(recordingCorrectionError || recordingCorrectionValidationError) && (
                <p data-testid="recording-correction-error" role="alert" className="settings-help">
                  {recordingCorrectionError || recordingCorrectionValidationError}
                </p>
              )}
              <button
                data-testid="recording-correction-submit"
                type="button"
                className="primary"
                disabled={!recordingOverride.trim() || reprocessing}
                onClick={overrideRecording}
              >
                {reprocessing ? "Загружаем…" : "Загрузить recording"}
              </button>
            </div>
            {recordingCorrectionReview && (
              <p data-testid="recording-review-status" role="status" className="settings-help">
                {recordingCorrectionReview}
              </p>
            )}
          </details>
        </section>
      )}

      <div className="inspector-grid track-evidence-grid">
        <div>
          <div className="file-card">
            <span className="entity-art disc">◉</span>
            <div>
              <p className="eyebrow">Исходный файл</p>
              <strong>{source.path.split("/").at(-1)}</strong>
              <small>
                {source.format?.toUpperCase() ?? "AUDIO"} ·{" "}
                {source.size_bytes
                  ? `${Math.round(source.size_bytes / 1024)} KB`
                  : "размер неизвестен"}{" "}
                · {source.state}
              </small>
            </div>
          </div>
          <div className="evidence-card">
            <div className="section-heading">
              <div>
                <p className="eyebrow">Анализ файла</p>
                <h2>Технические данные</h2>
              </div>
              <button
                type="button"
                className="secondary"
                disabled={reprocessing}
                onClick={retryAcoustId}
              >
                {reprocessing ? "В очереди…" : "Повторить анализ"}
              </button>
            </div>
            <div className="evidence-grid">
              <div>
                <span>Fingerprint</span>
                <strong>{fingerprint?.fingerprint ? "Сформирован" : "Не найден"}</strong>
                <small>{fingerprint?.tool_version ?? "инструмент не указан"}</small>
              </div>
              <div>
                <span>Длительность</span>
                <strong data-testid="track-duration">{duration}</strong>
                <small>По данным fingerprint</small>
              </div>
              <div>
                <span>Провайдеры</span>
                <strong>{attempts.length ? "История доступна" : "Не запускались"}</strong>
                <small>
                  {attempts.length
                    ? `проверок провайдеров: ${attempts.length}`
                    : "после сканирования появится здесь"}
                </small>
              </div>
            </div>
            {attempts.length > 0 && (
              <ul className="provider-attempts" aria-label="Попытки провайдеров">
                {attempts.map((attempt) => (
                  <li key={`${attempt.provider}-${attempt.created_at ?? attempt.snapshot_sha256}`}>
                    <strong>{attempt.provider}</strong>: {attempt.outcome} ·{" "}
                    {attempt.created_at
                      ? new Date(attempt.created_at).toLocaleString("ru-RU")
                      : "время не сохранено"}
                  </li>
                ))}
              </ul>
            )}
            <p className="hash">SHA-256: {source.sha256}</p>
          </div>
        </div>
        <div className="history-card">
          <p className="eyebrow">История</p>
          {detail.events.length ? (
            detail.events
              .slice()
              .reverse()
              .map((event) => (
                <div className="history-line" key={`${event.kind}-${event.created_at}`}>
                  <span />
                  <div>
                    <strong>{event.kind.replaceAll("_", " ")}</strong>
                    <small>
                      {new Date(event.created_at).toLocaleString("ru-RU")} ·{" "}
                      {event.reason ?? event.state}
                    </small>
                  </div>
                </div>
              ))
          ) : (
            <p className="muted">История появится после обработки файла.</p>
          )}
        </div>
      </div>
    </section>
  );
}
