import { useRef, useState } from "react";
import { CandidateReview } from "../components/CandidateReview";
import { ConfirmationDialog } from "../components/ConfirmationDialog";
import { MetadataComparison } from "../components/MetadataComparison";
import { SourceEncoding } from "../components/SourceEncoding";
import { lyricsDisplay, lyricsSynced } from "../domain/lyrics";
import {
  isPublicationHash,
  latestRevision,
  TAG_FIELDS,
  tagsFor,
  workflowStatus,
} from "../domain/metadata";
import type { Detail, MusicBrainzCandidateLookup, Tags } from "../types";

export function TrackDetail({
  detail,
  sourceId,
  draft,
  setDraft,
  saving,
  reprocessing,
  removingPublication,
  musicbrainzHost,
  onSave,
  onEncodingApplied,
  onRetryAcoustId,
  onRetryMusicBrainz,
  onLoadMusicBrainzCandidates,
  onSelectCandidate,
  onSelectEffectiveSource,
  onRemovePublication,
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
  readonly removingPublication: boolean;
  readonly musicbrainzHost?: string | null;
  readonly onSave: () => Promise<boolean>;
  readonly onEncodingApplied?: (queued: boolean) => Promise<void>;
  readonly onRetryAcoustId?: () => void;
  readonly onRetryMusicBrainz?: () => void;
  readonly onLoadMusicBrainzCandidates?: (request: MusicBrainzCandidateLookup) => void;
  readonly onSelectCandidate: (selection: string) => void;
  readonly onSelectEffectiveSource: (sourceId: string) => void;
  readonly onRemovePublication: () => Promise<void>;
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
  const [publicationRemovalOpen, setPublicationRemovalOpen] = useState(false);
  const publicationTriggerRef = useRef<HTMLButtonElement>(null);
  const publicationOutcomeRef = useRef<HTMLHeadingElement>(null);
  const publicationCloseFocusRef = useRef<"trigger" | "outcome" | null>(null);
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
      candidate.evidence.provider === "musicbrainz" &&
      (candidate.evidence.entity === "recording_release" ||
        candidate.evidence.entity === undefined),
  );
  const recordingCandidates = candidates
    .filter(
      (candidate) =>
        (candidate.evidence.entity === "recording" || candidate.evidence.entity === undefined) &&
        candidate.evidence.provider === "acoustid",
    )
    .map((candidate) => {
      if ((candidate.evidence.compatible_ids?.length ?? 0) > 0) return candidate;
      const compatibleReleaseIds = releaseCandidates
        .filter(
          (release) =>
            release.evidence.recording_mbid === candidate.evidence.recording_mbid ||
            release.evidence.compatible_ids?.includes(candidate.evidence.recording_mbid ?? ""),
        )
        .map((release) => release.evidence.release_mbid ?? release.candidate_key);
      return {
        ...candidate,
        evidence: { ...candidate.evidence, compatible_ids: compatibleReleaseIds },
      };
    });
  const acousticCandidatesByRecording = new Map(
    recordingCandidates
      .filter((candidate) => candidate.evidence.provider === "acoustid")
      .map((candidate) => [candidate.evidence.recording_mbid, candidate]),
  );
  const unifiedCandidates = releaseCandidates.map((candidate) => {
    const acoustic = acousticCandidatesByRecording.get(candidate.evidence.recording_mbid);
    if (!acoustic) return candidate;
    return {
      ...candidate,
      evidence: {
        ...candidate.evidence,
        acoustid_score: acoustic.evidence.score,
      },
    };
  });
  const unifiedRecordingIds = new Set(
    unifiedCandidates.map((candidate) => candidate.evidence.recording_mbid),
  );
  const unmatchedAcousticCandidates = recordingCandidates.filter(
    (candidate) => !unifiedRecordingIds.has(candidate.evidence.recording_mbid),
  );
  const selectedCandidateKey = selectedMusicBrainz ?? selectedAcoustId;
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
    unifiedCandidates.length > 0 ||
    unmatchedAcousticCandidates.length > 0 ||
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
  const confirmPublicationRemoval = async () => {
    await onRemovePublication();
    publicationCloseFocusRef.current = "outcome";
    setPublicationRemovalOpen(false);
  };
  const cancelPublicationRemoval = () => {
    publicationCloseFocusRef.current = "trigger";
    setPublicationRemovalOpen(false);
  };
  const focusAfterPublicationDialog = () => {
    const target =
      publicationCloseFocusRef.current === "trigger"
        ? publicationTriggerRef.current
        : publicationCloseFocusRef.current === "outcome"
          ? publicationOutcomeRef.current
          : null;
    publicationCloseFocusRef.current = null;
    target?.focus();
  };
  const overrideRelease = () => {
    const releaseMbid = releaseOverride.trim();
    const recordingMbid = recordingOverride.trim() || detail.musicbrainz_recording_id?.trim();
    if (!releaseMbid || !recordingMbid) return;
    setRecordingCorrectionValidationError("");
    onLoadMusicBrainzCandidates?.({ recording_mbid: recordingMbid, release_mbid: releaseMbid });
  };
  const overrideRecording = () => {
    const recordingMbid = recordingOverride.trim();
    if (!recordingMbid) return;
    const releaseMbid = releaseOverride.trim();
    setRecordingCorrectionValidationError("");
    onLoadMusicBrainzCandidates?.({
      recording_mbid: recordingMbid,
      ...(releaseMbid ? { release_mbid: releaseMbid } : {}),
    });
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

  const lyrics = lyricsDisplay(detail.lyrics_status);
  const syncedLyrics = lyricsSynced(detail.lyrics_status, detail.lyrics_synced);

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
            <h2 ref={publicationOutcomeRef} id="publication-status-title" tabIndex={-1}>
              Выходной файл
            </h2>
          </div>
          <div className="provider-actions">
            <span
              data-testid="output-status"
              className={`badge ${currentPublication ? "success" : ""}`}
            >
              {lifecycleLabels.publication}
            </span>
            {currentPublication ? (
              <button
                ref={publicationTriggerRef}
                type="button"
                className="secondary danger"
                disabled={removingPublication}
                aria-describedby="publication-remove-help"
                aria-haspopup="dialog"
                aria-expanded={publicationRemovalOpen}
                onClick={() => setPublicationRemovalOpen(true)}
              >
                {removingPublication ? "Удаляем публикацию…" : "Удалить публикацию"}
              </button>
            ) : null}
          </div>
        </div>
        {currentPublication ? (
          <p id="publication-remove-help" className="settings-help">
            {
              "Удаляется только управляемый выходной аудиофайл. Исходный файл и\u00a0.nfo останутся без изменений."
            }
          </p>
        ) : null}
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

      <ConfirmationDialog
        open={publicationRemovalOpen}
        title="Удалить публикацию?"
        description={
          "Будет удалён только управляемый выходной аудиофайл. Исходный файл и\u00a0.nfo останутся без изменений."
        }
        confirmLabel="Удалить публикацию"
        busyLabel="Удаляем публикацию…"
        busy={removingPublication}
        onCancel={cancelPublicationRemoval}
        onConfirm={() => void confirmPublicationRemoval()}
        onAfterCloseFocus={focusAfterPublicationDialog}
      />

      <section className="evidence-card lyrics-panel" aria-labelledby="lyrics-panel-title">
        <div className="section-heading">
          <div>
            <p className="eyebrow">Текст песни</p>
            <h2 id="lyrics-panel-title">Синхронный текст</h2>
          </div>
          <span
            className={`badge ${lyrics.tone === "ready" ? "success" : lyrics.tone}`}
            data-testid="lyrics-status-badge"
          >
            {lyrics.label}
          </span>
        </div>
        <div className="evidence-grid">
          <div data-testid="lyrics-status-card">
            <span>Состояние</span>
            <strong data-testid="lyrics-status-state">{lyrics.label}</strong>
            <small data-testid="lyrics-status-detail">{lyrics.detail}</small>
          </div>
          <div>
            <span>Синхронизация</span>
            <strong data-testid="lyrics-status-sync">{syncedLyrics ? "Есть" : "Нет"}</strong>
            <small>Строки с таймингом; отдельно от текста без тайминга</small>
          </div>
        </div>
      </section>

      {onEncodingApplied && (
        <SourceEncoding
          sourceId={sourceId}
          onApplied={onEncodingApplied}
          disabled={reprocessing || saving}
        />
      )}

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

      {(hasProviderEvidence || !detail.musicbrainz_recording_id) && (
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
              {hasProviderEvidence
                ? detail.states.match === "matched"
                  ? "Выбранные варианты сохранены"
                  : "Выберите варианты, чтобы продолжить"
                : "Провайдеры не вернули варианты для этого трека"}
            </span>
            <small>
              {hasProviderEvidence
                ? "Списки кандидатов скрыты до раскрытия."
                : "Укажите известный recording MBID вручную или повторите анализ."}
            </small>
          </div>
          {hasProviderEvidence && (
            <CandidateReview
              entity="recording_release"
              candidates={[...unmatchedAcousticCandidates, ...unifiedCandidates]}
              sourceTags={originalTags}
              sourceDurationSeconds={source.duration_seconds ?? fingerprintDuration ?? null}
              selectedKey={selectedCandidateKey}
              compatibleWith={selectedMusicBrainz}
              reason={`Сравните одну строку кандидата: запись «${trackTitle}», альбом «${trackAlbum}», позицию и факторы скоринга.`}
              disabled={reprocessing}
              musicbrainzHost={musicbrainzHost ?? null}
              onSelect={onSelectCandidate}
            />
          )}
          <details className="release-override">
            <summary>Выбрать release MBID вручную</summary>
            <p className="candidate-reason">
              Укажите recording ниже или используйте уже выбранный recording, чтобы проверить точную
              пару.
            </p>
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
                disabled={
                  !releaseOverride.trim() ||
                  !(recordingOverride.trim() || detail.musicbrainz_recording_id?.trim()) ||
                  reprocessing
                }
                onClick={overrideRelease}
              >
                {reprocessing ? "Проверяем…" : "Проверить пару"}
              </button>
            </div>
          </details>
          <details className="release-override">
            <summary>Выбрать recording MBID вручную</summary>
            <p className="candidate-reason">
              {hasProviderEvidence
                ? "Используйте это, если автоматически выбрана неверная запись трека."
                : "Если анализ не нашёл вариантов, укажите известный MBID записи трека."}
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
                {reprocessing ? "Ищем…" : "Найти связанные релизы"}
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
              <strong>{source.path.split("/").at(-1) ?? "Без названия"}</strong>
              <small className="source-path" title={source.path} data-testid="source-path">
                {source.path}
              </small>
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
