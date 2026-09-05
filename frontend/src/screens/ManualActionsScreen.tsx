import type { CatalogTrack } from "../app/useAppController";
import { albumFor, artistFor, titleFor } from "../domain/metadata";
import type { ManualActionFilter, Route } from "../types";

const ACTION_FILTERS = ["analysis-error", "needs-review"] as const;

type ManualActionsScreenProps = {
  readonly tracks: readonly CatalogTrack[];
  readonly filter: ManualActionFilter;
  readonly counts: Readonly<Record<ManualActionFilter, number>>;
  readonly reprocessing: boolean;
  readonly onNavigate: (route: Route) => void;
  readonly onFilterChange: (value: ManualActionFilter) => void;
  readonly onRetry: (recordId: string, sourceId: string) => void;
};

function hasAnalysisError(track: CatalogTrack): boolean {
  return (
    track.item.processing_state === "retrying" ||
    track.item.processing_state === "blocked_infrastructure" ||
    track.item.processing_state === "quarantined" ||
    track.item.publication_state === "failed" ||
    track.source.state === "invalid_audio"
  );
}

function needsReview(track: CatalogTrack): boolean {
  return (
    track.item.processing_state === "needs_review" || track.item.match_state === "needs_review"
  );
}

function matchesFilter(track: CatalogTrack, filter: ManualActionFilter): boolean {
  return (
    track.source.state !== "replaced" &&
    (filter === "analysis-error" ? hasAnalysisError(track) : needsReview(track))
  );
}

function filterLabel(filter: ManualActionFilter): string {
  return filter === "analysis-error" ? "Ошибки анализа" : "Нужна проверка";
}

function trackStatus(filter: ManualActionFilter): string {
  return filter === "analysis-error" ? "Анализ не завершён" : "Проверка оператора";
}

export function ManualActionsScreen({
  tracks,
  filter,
  counts,
  reprocessing,
  onNavigate,
  onFilterChange,
  onRetry,
}: ManualActionsScreenProps) {
  const visibleTracks = tracks.filter((track) => matchesFilter(track, filter));

  return (
    <section className="manual-actions-screen" aria-labelledby="manual-actions-heading">
      <div className="screen-heading">
        <div>
          <p className="eyebrow">Ручная обработка</p>
          <h2 id="manual-actions-heading">Треки, требующие действия</h2>
        </div>
        <span className="badge">{visibleTracks.length} в списке</span>
      </div>
      <fieldset className="manual-action-filters">
        <legend>Фильтр ручных действий</legend>
        {ACTION_FILTERS.map((item) => (
          <button
            type="button"
            key={item}
            className={filter === item ? "secondary active" : "secondary"}
            aria-pressed={filter === item}
            onClick={() => onFilterChange(item)}
          >
            {filterLabel(item)} {counts[item]}
          </button>
        ))}
      </fieldset>
      {visibleTracks.length === 0 ? (
        <div className="empty-state">В этой категории нет треков, требующих ручного действия.</div>
      ) : (
        <div className="track-table">
          {visibleTracks.map(({ item, source }, index) => (
            <div className="track-line" key={`${item.record_id}-${source.source_id}`}>
              <b>{String(index + 1).padStart(2, "0")}</b>
              <span>
                <strong>{titleFor(item, source.source_id)}</strong>
                <small>{source.path}</small>
              </span>
              <span className="track-meta">{trackStatus(filter)}</span>
              {filter === "analysis-error" ? (
                <button
                  type="button"
                  className="secondary retry-action"
                  disabled={reprocessing}
                  onClick={() => onRetry(item.record_id, source.source_id)}
                >
                  {reprocessing ? "Ставим в очередь…" : "Повторить"}
                </button>
              ) : null}
              <button
                type="button"
                className="track-open-action"
                aria-label={`Открыть ${titleFor(item, source.source_id)}`}
                onClick={() =>
                  onNavigate({
                    screen: "track",
                    recordId: item.record_id,
                    sourceId: source.source_id,
                    artist: artistFor(item, source.source_id),
                    album: albumFor(item, source.source_id),
                  })
                }
              >
                →
              </button>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
