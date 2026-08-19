import type { CatalogAlbum, CatalogTrack } from "../app/useAppController";
import { albumArtistsFor, albumKeyFor, titleFor, trackNumberLabelFor } from "../domain/metadata";
import type { Screen } from "../types";

type LibraryCatalogProps = {
  screen: Extract<Screen, "artists" | "albums" | "tracks">;
  artist: string;
  album: string;
  artists: string[];
  albums: CatalogAlbum[];
  tracks: CatalogTrack[];
  albumTracks: CatalogTrack[];
  loading: boolean;
  onNavigate: (route: {
    screen: Screen;
    artist?: string;
    album?: string;
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
  albums,
  tracks,
  albumTracks,
  loading,
  onNavigate,
  onRefresh,
}: LibraryCatalogProps) {
  if (loading) return <div className="empty-state">Загрузка медиатеки…</div>;
  if (screen === "artists") {
    return (
      <section className="catalog-grid">
        {artists.map((name) => (
          <button
            type="button"
            className="entity-card"
            key={name}
            onClick={() => onNavigate({ screen: "albums", artist: name })}
          >
            <span className="entity-art">{name.slice(0, 1)}</span>
            <span>
              <strong>{name}</strong>
              <small>
                {
                  tracks.filter(({ item, source }) =>
                    albumArtistsFor(item, source.source_id).includes(name),
                  ).length
                }{" "}
                треков
              </small>
            </span>
            <b>→</b>
          </button>
        ))}
      </section>
    );
  }
  if (screen === "albums") {
    return (
      <section className="catalog-grid">
        {albums.map((albumEntry) => {
          const albumTrack = tracks.find(
            ({ item, source }) =>
              albumArtistsFor(item, source.source_id).includes(artist) &&
              albumKeyFor(item, source.source_id) === albumEntry.key,
          );
          return (
            <button
              type="button"
              className="entity-card album-card"
              key={albumEntry.key}
              onClick={() => onNavigate({ screen: "tracks", artist, album: albumEntry.key })}
            >
              {albumTrack?.item.artwork?.url ? (
                <img
                  className="entity-art album-cover"
                  src={albumTrack.item.artwork.url}
                  alt=""
                  width={56}
                  height={56}
                />
              ) : (
                <span className="entity-art disc">◉</span>
              )}
              <span>
                <strong>{albumEntry.title}</strong>
                <small>
                  {
                    tracks.filter(
                      ({ item, source }) =>
                        albumArtistsFor(item, source.source_id).includes(artist) &&
                        albumKeyFor(item, source.source_id) === albumEntry.key,
                    ).length
                  }{" "}
                  треков
                </small>
              </span>
              <b>→</b>
            </button>
          );
        })}
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
        {albumTracks.map(({ item, source }) => (
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
              <small>{source.path}</small>
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
        ))}
      </div>
    </section>
  );
}
