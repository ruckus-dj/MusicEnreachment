import { useState } from "react";
import { CandidateReview } from "../components/CandidateReview";
import { MetadataComparison } from "../components/MetadataComparison";
import { latestRevision, TAG_FIELDS, tagsFor, workflowStatus } from "../domain/metadata";
import type { Detail, Tags } from "../types";

export function TrackDetail({
  detail,
  sourceId,
  draft,
  setDraft,
  saving,
  reprocessing,
  onSave,
  onRetryAcoustId,
  onRetryMusicBrainz,
  onOverrideRelease,
  onOverrideRecording,
  onSelectCandidate,
}: {
  readonly detail: Detail | null;
  readonly sourceId: string;
  readonly draft: Tags;
  readonly setDraft: (value: Tags) => void;
  readonly saving: boolean;
  readonly reprocessing: boolean;
  readonly onSave: () => Promise<boolean>;
  readonly onRetryAcoustId?: () => void;
  readonly onRetryMusicBrainz?: () => void;
  readonly onOverrideRelease?: (releaseMbid: string) => void;
  readonly onOverrideRecording?: (recordingMbid: string) => void;
  readonly onSelectCandidate: (selection: string) => void;
}) {
  const [editing, setEditing] = useState(false);
  const [releaseOverride, setReleaseOverride] = useState("");
  const [recordingOverride, setRecordingOverride] = useState("");
  const source = detail?.sources.find((item) => item.source_id === sourceId);
  if (!detail || !source) return <div className="empty-state">Открываем данные трека…</div>;

  const status = workflowStatus(detail);
  const finalRevision = latestRevision(detail, sourceId, "final");
  const fingerprint = source.fingerprints?.at(-1);
  const attempts = source.provider_attempts ?? [];
  const originalTags = tagsFor(detail, sourceId, "original");
  const analyzedTags = tagsFor(detail, sourceId, "analyzed");
  const candidates = source.candidates ?? [];
  const acoustIdCandidates = candidates.filter(
    (candidate) => candidate.evidence.provider === "acoustid",
  );
  const selectedAcoustId = detail.musicbrainz_recording_id ?? null;
  const selectedMusicBrainz = detail.musicbrainz_release_id ?? null;
  const musicBrainzCandidates = candidates.filter(
    (candidate) =>
      candidate.evidence.provider === "musicbrainz" &&
      (!selectedAcoustId || candidate.evidence.tags.MUSICBRAINZ_TRACKID === selectedAcoustId),
  );
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
  const hasProviderEvidence =
    acoustIdCandidates.length > 0 ||
    musicBrainzCandidates.length > 0 ||
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
    onOverrideRecording?.(recordingMbid);
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

      <div className="track-context">
        <p className="eyebrow">Сравнение файла</p>
        <h2>{trackTitle}</h2>
        <p>
          {originalTags.ARTIST || draft.ARTIST || "Исполнитель не указан"} · {trackAlbum}
        </p>
      </div>

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
          {acoustIdCandidates.length > 0 || selectedAcoustId ? (
            <CandidateReview
              provider="acoustid"
              candidates={acoustIdCandidates}
              selectedKey={selectedAcoustId}
              reason={`Сравните исполнителя и название записи «${trackTitle}» с исходными тегами.`}
              disabled={reprocessing}
              onSelect={onSelectCandidate}
            />
          ) : null}
          {musicBrainzCandidates.length > 0 || selectedMusicBrainz ? (
            <CandidateReview
              provider="musicbrainz"
              candidates={musicBrainzCandidates}
              selectedKey={selectedMusicBrainz}
              reason={`Выберите релиз MusicBrainz для трека «${trackTitle}» из альбома «${trackAlbum}».`}
              disabled={reprocessing}
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
                aria-label="MusicBrainz recording ID"
                value={recordingOverride}
                onChange={(event) => setRecordingOverride(event.target.value)}
                placeholder="recording MBID"
              />
              <button
                type="button"
                className="primary"
                disabled={!recordingOverride.trim() || reprocessing}
                onClick={overrideRecording}
              >
                {reprocessing ? "Загружаем…" : "Загрузить recording"}
              </button>
            </div>
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
                <span>AcousticID</span>
                <strong>{attempts.at(-1)?.outcome ?? "Не запускался"}</strong>
                <small>
                  {attempts.length
                    ? `проверок провайдеров: ${attempts.length}`
                    : "после сканирования появится здесь"}
                </small>
              </div>
            </div>
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
