import type { CatalogAlbum, CatalogTrack } from "../app/useAppController";
import { titleFor, trackNumberLabelFor, UNKNOWN_ARTIST_LABEL } from "../domain/metadata";
import type { Screen } from "../types";

type LibraryCatalogProps = {
  screen: Extract<Screen, "artists" | "albums" | "tracks">;
  artist: string;
  album: string;
  artists: string[];
  artistTrackCounts: Readonly<Record<string, number>>;
  albums: CatalogAlbum[];
  albumTracks: CatalogTrack[];
  loading: boolean;
  onNavigate: (route: {
    screen: Screen;
    artist?: string;
    artistMissing?: boolean;
    album?: string;
    albumMissing?: boolean;
    recordId?: string;
    sourceId?: string;
  }) => void;
  onRefresh: () => void;
};

export function LibraryCatalog({
  screen,
  artist,
  album,
  artists,
  artistTrackCounts,
  albums,
  albumTracks,
  loading,
  onNavigate,
  onRefresh,
}: LibraryCatalogProps) {
  if (loading) return <div className="empty-state">Загрузка медиатеки…</div>;
  if (screen === "artists") {
    return (
      <section className="catalog-grid">
        {artists.length === 0 ? (
          <div className="empty-state">По выбранному фильтру исполнителей нет.</div>
        ) : (
          artists.map((name) => (
            <button
              type="button"
              className="entity-card"
              key={name}
              onClick={() =>
                onNavigate({
                  screen: "albums",
                  artist: name,
                  artistMissing: name === UNKNOWN_ARTIST_LABEL,
                })
              }
            >
              <span className="entity-art">{name.slice(0, 1)}</span>
              <span>
                <strong>{name}</strong>
                <small>{artistTrackCounts[name] ?? 0} треков</small>
              </span>
              <b>→</b>
            </button>
          ))
        )}
      </section>
    );
  }
  if (screen === "albums") {
    return (
      <section className="catalog-grid">
        {albums.length === 0 ? (
          <div className="empty-state">По выбранному фильтру альбомов нет.</div>
        ) : (
          albums.map((albumEntry) => {
            return (
              <button
                type="button"
                className="entity-card album-card"
                key={albumEntry.key}
                onClick={() =>
                  onNavigate({
                    screen: "tracks",
                    artist,
                    album: albumEntry.key,
                    artistMissing: artist === UNKNOWN_ARTIST_LABEL,
                    albumMissing: albumEntry.key === "album:",
                  })
                }
              >
                {albumEntry.artworkUrl ? (
                  <img
                    className="entity-art album-cover"
                    src={albumEntry.artworkUrl}
                    alt=""
                    width={56}
                    height={56}
                  />
                ) : (
                  <span className="entity-art disc">◉</span>
                )}
                <span>
                  <strong>{albumEntry.title}</strong>
                  <small>{albumEntry.trackCount} треков</small>
                </span>
                <b>→</b>
              </button>
            );
          })
        )}
      </section>
    );
  }
  return (
    <section className="track-screen">
      <div className="screen-heading">
        <div>
          <p className="eyebrow">{albumTracks.length} треков</p>
          <h2>Список треков</h2>
        </div>
        <button type="button" className="secondary" disabled={loading} onClick={onRefresh}>
          {loading ? "Обновляем…" : "Обновить список"}
        </button>
      </div>
      <div className="track-table">
        {albumTracks.length === 0 ? (
          <div className="empty-state">По выбранному фильтру треков нет.</div>
        ) : (
          albumTracks.map(({ item, source }) => (
            <button
              type="button"
              className={`track-line ${source.state === "disappeared" ? "unavailable" : ""}`}
              key={`${item.record_id}-${source.source_id}`}
              onClick={() =>
                onNavigate({
                  screen: "track",
                  recordId: item.record_id,
                  sourceId: source.source_id,
                  artist,
                  album,
                })
              }
            >
              <b>{trackNumberLabelFor(item, source.source_id)}</b>
              <span>
                <strong>{titleFor(item, source.source_id)}</strong>
                <small className="source-path" title={source.path}>
                  {source.path}
                </small>
              </span>
              <span className="track-meta">
                {source.state === "disappeared"
                  ? "Исходный файл отсутствует"
                  : item.processing_state === "analyzing"
                    ? "Анализируется"
                    : item.match_state === "matched"
                      ? "MusicBrainz подтверждён"
                      : "Нужна проверка"}
              </span>
              <i>→</i>
            </button>
          ))
        )}
      </div>
    </section>
  );
}
