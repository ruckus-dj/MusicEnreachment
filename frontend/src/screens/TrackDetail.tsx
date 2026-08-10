import { useState } from "react";
import { CandidateReview } from "../components/CandidateReview";
import { OriginalTags } from "../components/OriginalTags";
import { latestRevision, TAG_FIELDS, tagsFor, workflowStatus } from "../domain/metadata";
import type { Detail, Layer, Tags } from "../types";

export function TrackDetail({
  detail,
  sourceId,
  layer,
  setLayer,
  tags,
  draft,
  setDraft,
  saving,
  reprocessing,
  onSave,
  onRetry = () => undefined,
  onRetryAcoustId,
  onRetryMusicBrainz,
  onOverrideRelease,
  onSelectCandidate,
}: {
  readonly detail: Detail | null;
  readonly sourceId: string;
  readonly layer: Layer;
  readonly setLayer: (value: Layer) => void;
  readonly tags: Tags;
  readonly draft: Tags;
  readonly setDraft: (value: Tags) => void;
  readonly saving: boolean;
  readonly reprocessing: boolean;
  readonly onSave: () => void;
  readonly onRetry?: () => void;
  readonly onRetryAcoustId?: () => void;
  readonly onRetryMusicBrainz?: () => void;
  readonly onOverrideRelease?: (releaseMbid: string) => void;
  readonly onSelectCandidate: (selection: string) => void;
}) {
  const [releaseOverride, setReleaseOverride] = useState("");
  const source = detail?.sources.find((item) => item.source_id === sourceId);
  if (!detail || !source) return <div className="empty-state">Открываем данные трека…</div>;
  const attempts = source.provider_attempts ?? [];
  const fingerprint = source.fingerprints?.at(-1);
  const status = workflowStatus(detail);
  const finalRevision = latestRevision(detail, sourceId, "final");
  const published = detail.publications.find((item) => item.state === "current");
  const visibleTagFields = [...new Set([...TAG_FIELDS, ...Object.keys(tags)])];
  const reason =
    detail.events
      .slice()
      .reverse()
      .find((event) => event.reason)?.reason ?? "";
  const candidates = source.candidates ?? [];
  const acoustIdCandidates = candidates.filter(
    (candidate) => candidate.evidence.provider === "acoustid",
  );
  const musicBrainzCandidates = candidates.filter(
    (candidate) => candidate.evidence.provider === "musicbrainz",
  );
  const originalTags = tagsFor(detail, sourceId, "original");
  const trackTitle =
    originalTags.TITLE || tags.TITLE || source.path.split("/").at(-1) || "Без названия";
  const trackAlbum = originalTags.ALBUM || tags.ALBUM || "Без альбома";
  const retryAcoustId = onRetryAcoustId ?? (() => undefined);
  const retryMusicBrainz = onRetryMusicBrainz ?? (() => undefined);
  const overrideRelease = () => {
    const releaseMbid = releaseOverride.trim();
    if (!releaseMbid) return;
    onOverrideRelease?.(releaseMbid);
  };
  if (
    acoustIdCandidates.length > 0 ||
    musicBrainzCandidates.length > 0 ||
    detail.states.match !== "matched"
  )
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
        </div>
        <div className="track-context">
          <p className="eyebrow">Сравнение файла</p>
          <h2>{trackTitle}</h2>
          <p>
            {originalTags.ARTIST || tags.ARTIST || "Исполнитель не указан"} · {trackAlbum}
          </p>
        </div>
        <OriginalTags tags={originalTags} />
        <div className="evidence-card">
          <div className="section-heading">
            <div>
              <p className="eyebrow">Провайдеры</p>
              <h2>Результаты анализа</h2>
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
          <div className="evidence-grid">
            <div>
              <span>AcousticID</span>
              <strong>
                {acoustIdCandidates.length
                  ? `${acoustIdCandidates.length} записей`
                  : "Нет вариантов"}
              </strong>
              <small>Выбирайте запись, совпадающую с исходным исполнителем и названием трека</small>
            </div>
            <div>
              <span>MusicBrainz</span>
              <strong>
                {musicBrainzCandidates.length
                  ? `${musicBrainzCandidates.length} релизов`
                  : "Ожидает выбора записи"}
              </strong>
              <small>После выбора AcousticID сравнивайте альбом и номер трека</small>
            </div>
          </div>
        </div>
        {acoustIdCandidates.length > 0 && (
          <CandidateReview
            provider="acoustid"
            candidates={acoustIdCandidates}
            reason={`Выберите правильную запись AcousticID для трека «${trackTitle}» из альбома «${trackAlbum}». Сравнивайте исполнителя, название и ссылку MusicBrainz с исходными тегами выше.`}
            disabled={reprocessing}
            onSelect={onSelectCandidate}
          />
        )}
        {musicBrainzCandidates.length > 0 && (
          <CandidateReview
            provider="musicbrainz"
            candidates={musicBrainzCandidates}
            reason={`Выберите релиз MusicBrainz для трека «${trackTitle}» из альбома «${trackAlbum}».`}
            disabled={reprocessing}
            onSelect={onSelectCandidate}
          />
        )}
        <div className="evidence-card">
          <div className="section-heading">
            <div>
              <p className="eyebrow">MusicBrainz</p>
              <h3>Явный release override</h3>
            </div>
          </div>
          <p className="candidate-reason">
            Используйте этот вариант, если автоматическое сопоставление выбрало неправильный релиз.
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
              disabled={!releaseOverride.trim() || reprocessing}
              onClick={overrideRelease}
            >
              {reprocessing ? "Загружаем…" : "Загрузить release"}
            </button>
          </div>
        </div>
      </section>
    );
  if (
    acoustIdCandidates.length > 0 ||
    musicBrainzCandidates.length > 0 ||
    detail.states.match !== "matched"
  )
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
        </div>
        <div className="evidence-card">
          <div className="section-heading">
            <div>
              <p className="eyebrow">Провайдеры</p>
              <h2>Результаты анализа</h2>
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
          <div className="evidence-grid">
            <div>
              <span>AcousticID</span>
              <strong>
                {acoustIdCandidates.length
                  ? `${acoustIdCandidates.length} записей`
                  : "Нет вариантов"}
              </strong>
              <small>Выбор записи запускает отдельный запрос MusicBrainz</small>
            </div>
            <div>
              <span>MusicBrainz</span>
              <strong>
                {musicBrainzCandidates.length
                  ? `${musicBrainzCandidates.length} релизов`
                  : "Ожидает выбора записи"}
              </strong>
              <small>Можно повторить независимо от AcousticID</small>
            </div>
          </div>
        </div>
        {acoustIdCandidates.length > 0 && (
          <CandidateReview
            provider="acoustid"
            candidates={acoustIdCandidates}
            reason="Выберите правильную запись AcousticID для этого файла."
            disabled={reprocessing}
            onSelect={onSelectCandidate}
          />
        )}
        {musicBrainzCandidates.length > 0 && (
          <CandidateReview
            provider="musicbrainz"
            candidates={musicBrainzCandidates}
            reason={reason}
            disabled={reprocessing}
            onSelect={onSelectCandidate}
          />
        )}
        <div className="evidence-card">
          <div className="section-heading">
            <div>
              <p className="eyebrow">MusicBrainz</p>
              <h3>Явный release override</h3>
            </div>
          </div>
          <p className="candidate-reason">
            Используйте этот вариант, если автоматическое сопоставление выбрало неправильный релиз.
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
              disabled={!releaseOverride.trim() || reprocessing}
              onClick={overrideRelease}
            >
              Загрузить release
            </button>
          </div>
        </div>
      </section>
    );
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
          {published?.metadata_revision_id === finalRevision?.id ? "FLAC rev " : "Final rev "}
          {finalRevision?.revision ?? "—"}
        </span>
      </div>
      <div className="inspector-grid">
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
                <h2>Что найдено</h2>
              </div>
              <div className="provider-actions">
                <span className={`badge ${detail.states.match === "matched" ? "success" : ""}`}>
                  {detail.states.match === "matched" ? "MusicBrainz подтверждён" : "Нужна проверка"}
                </span>
                <button
                  type="button"
                  className="secondary"
                  disabled={reprocessing}
                  onClick={onRetry}
                >
                  {reprocessing ? "В очереди…" : "Повторить анализ"}
                </button>
              </div>
            </div>
            <div className="evidence-grid">
              <div>
                <span>Fingerprint</span>
                <strong>{fingerprint?.fingerprint ? "Сформирован" : "Не найден"}</strong>
                <small>{fingerprint?.tool_version ?? "инструмент не указан"}</small>
              </div>
              <div>
                <span>AcousticID</span>
                <strong>
                  {attempts
                    .slice()
                    .reverse()
                    .find((attempt) => attempt.provider === "acoustid")?.outcome ?? "Не запускался"}
                </strong>
                <small>
                  {attempts.length
                    ? `проверок провайдеров: ${attempts.length}`
                    : "после сканирования появится здесь"}
                </small>
              </div>
            </div>
            <p className="hash">SHA-256: {source.sha256}</p>
          </div>
          {detail.states.match !== "matched" && (
            <CandidateReview
              candidates={source.candidates ?? []}
              reason={reason}
              disabled={reprocessing}
              onSelect={onSelectCandidate}
            />
          )}
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
        <div className="metadata-card">
          <div className="section-heading">
            <div>
              <p className="eyebrow">Final metadata</p>
              <h2>Публикуемые теги</h2>
            </div>
            <span className="revision">rev {finalRevision?.revision ?? "—"}</span>
          </div>
          <div className="layer-tabs" role="tablist" aria-label="Слои метаданных">
            {(["original", "analyzed", "final"] as const).map((tab) => {
              const revision = latestRevision(detail, sourceId, tab);
              const readable =
                tab === "original" ? "Исходные" : tab === "analyzed" ? "Анализ" : "Final";
              return (
                <button
                  type="button"
                  key={tab}
                  className={layer === tab ? "layer-tab active" : "layer-tab"}
                  role="tab"
                  aria-selected={layer === tab}
                  onClick={() => setLayer(tab)}
                >
                  <span>{readable}</span>
                  <small>
                    {revision
                      ? `rev ${revision.revision} · ${revision.actor ?? "system"}`
                      : "нет ревизии"}
                  </small>
                </button>
              );
            })}
          </div>
          <div className="tag-grid">
            {visibleTagFields.map((field) => (
              <label key={field}>
                <span>{field}</span>
                <input
                  value={tags[field] ?? ""}
                  readOnly={layer !== "final"}
                  onChange={(event) => setDraft({ ...draft, [field]: event.target.value })}
                />
              </label>
            ))}
          </div>
          <div className="editor-footer">
            <span className="muted">
              {layer === "final"
                ? "Эти значения попадут в следующую публикацию FLAC."
                : "Этот слой только для чтения; исходник не изменяется."}
            </span>
            {layer === "final" && (
              <button type="button" className="primary" disabled={saving} onClick={onSave}>
                {saving ? "Сохраняем…" : "Сохранить Final и опубликовать"}
              </button>
            )}
          </div>
        </div>
      </div>
    </section>
  );
}
