import { useState } from "react";
import type { CatalogTrack } from "../app/useAppController";
import { albumFor, artistFor, titleFor } from "../domain/metadata";
import type { Route } from "../types";

const ACTION_FILTERS = ["analysis-error", "needs-review"] as const;
type ActionFilter = (typeof ACTION_FILTERS)[number];

type ManualActionsScreenProps = {
  readonly tracks: readonly CatalogTrack[];
  readonly onNavigate: (route: Route) => void;
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

function matchesFilter(track: CatalogTrack, filter: ActionFilter): boolean {
  return filter === "analysis-error" ? hasAnalysisError(track) : needsReview(track);
}

function filterLabel(filter: ActionFilter): string {
  return filter === "analysis-error" ? "Ошибки анализа" : "Нужна проверка";
}

function trackStatus(filter: ActionFilter): string {
  return filter === "analysis-error" ? "Анализ не завершён" : "Проверка оператора";
}

export function ManualActionsScreen({ tracks, onNavigate }: ManualActionsScreenProps) {
  const [filter, setFilter] = useState<ActionFilter>("analysis-error");
  const visibleTracks = tracks.filter((track) => matchesFilter(track, filter));
  const counts = new Map(
    ACTION_FILTERS.map((item) => [
      item,
      tracks.filter((track) => matchesFilter(track, item)).length,
    ]),
  );

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
            onClick={() => setFilter(item)}
          >
            {filterLabel(item)} {counts.get(item) ?? 0}
          </button>
        ))}
      </fieldset>
      {visibleTracks.length === 0 ? (
        <div className="empty-state">В этой категории нет треков, требующих ручного действия.</div>
      ) : (
        <div className="track-table">
          {visibleTracks.map(({ item, source }, index) => (
            <button
              type="button"
              className="track-line"
              key={`${item.record_id}-${source.source_id}`}
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
              <b>{String(index + 1).padStart(2, "0")}</b>
              <span>
                <strong>{titleFor(item, source.source_id)}</strong>
                <small>{source.path}</small>
              </span>
              <span className="track-meta">{trackStatus(filter)}</span>
              <i>→</i>
            </button>
          ))}
        </div>
      )}
    </section>
  );
}
