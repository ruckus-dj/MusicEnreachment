import { useState } from "react";
import { apiResponseError } from "../../api/client/errors";
import {
  useGetLibraryArtist,
  useListLibraryPositions,
  useListLibraryReleases,
} from "../../api/generated/client";
import { useHashLocation } from "../../navigation/hashLocation";
import { libraryRouteHref, parseLibraryArtistRoute } from "./libraryRoute";
import "./catalog.css";

const PAGE_SIZE = 50;

export function LibraryArtistScreen({ artistId }: { artistId: string }) {
  const hash = useHashLocation();
  const route = parseLibraryArtistRoute(hash) ?? {
    artistId,
    view: "artists" as const,
    query: "",
    publication: "all" as const,
    offset: 0,
  };
  const artistQuery = useGetLibraryArtist(artistId);
  const releasesQuery = useListLibraryReleases({
    artist_id: artistId,
    limit: PAGE_SIZE,
    offset: route.offset,
    q: route.query || undefined,
    publication: route.publication,
  });
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const error =
    artistQuery.data?.status !== undefined && artistQuery.data.status !== 200
      ? apiResponseError(
          artistQuery.data,
          `Не удалось загрузить артиста (HTTP ${artistQuery.data.status}).`,
        ).message
      : artistQuery.error instanceof Error
        ? artistQuery.error.message
        : "";
  const artist =
    artistQuery.data?.status === 200 ? artistQuery.data.data : undefined;
  const releasesError =
    releasesQuery.data?.status !== undefined &&
    releasesQuery.data.status !== 200
      ? apiResponseError(
          releasesQuery.data,
          `Не удалось загрузить релизы (HTTP ${releasesQuery.data.status}).`,
        ).message
      : releasesQuery.error instanceof Error
        ? releasesQuery.error.message
        : "";
  const releases =
    releasesQuery.data?.status === 200 ? releasesQuery.data.data : undefined;

  return (
    <main className="library-catalog" aria-labelledby="artist-title">
      <a className="catalog-back" href={libraryRouteHref(route)}>
        ← Медиатека
      </a>
      {artistQuery.isLoading ? (
        <p role="status">Загрузка артиста…</p>
      ) : error ? (
        <section className="catalog-state catalog-state--error" role="alert">
          <p>{error}</p>
          <button type="button" onClick={() => void artistQuery.refetch()}>
            Повторить
          </button>
        </section>
      ) : artist ? (
        <>
          <header className="catalog-header">
            <div>
              <h1 id="artist-title">{artist.name}</h1>
              {artist.disambiguation && (
                <p className="catalog-muted">{artist.disambiguation}</p>
              )}
              <p className="catalog-muted">
                {artist.release_count} релизов · {artist.position_count} позиций
                · {artist.published_position_count} опубликовано
              </p>
            </div>
            <div className="catalog-genre-list">
              {artist.genres.map((genre: string) => (
                <span key={genre}>{genre}</span>
              ))}
            </div>
          </header>
          {releasesQuery.isLoading ? (
            <p role="status">Загрузка релизов…</p>
          ) : releasesError ? (
            <section
              className="catalog-state catalog-state--error"
              role="alert"
            >
              <p>{releasesError}</p>
              <button
                type="button"
                onClick={() => void releasesQuery.refetch()}
              >
                Повторить
              </button>
            </section>
          ) : releases?.items.length ? (
            <section
              className="catalog-artist-releases"
              aria-label="Релизы артиста"
            >
              {releases.items.map((release) => (
                <article className="catalog-artist-release" key={release.id}>
                  <div className="catalog-release-heading">
                    <span className="catalog-initial" aria-hidden="true">
                      {release.title.trim().charAt(0).toLocaleUpperCase() ||
                        "M"}
                    </span>
                    <div>
                      <h2>
                        <a
                          href={`#/library/releases/${encodeURIComponent(release.id)}`}
                        >
                          {release.title || "Без названия"}
                        </a>
                      </h2>
                      <p>
                        {release.edition.release_date ??
                          release.edition.original_release_date ??
                          "Дата не указана"}{" "}
                        · {release.counts.position_count} позиций ·{" "}
                        {release.counts.published_position_count} опубликовано
                      </p>
                    </div>
                    <button
                      type="button"
                      aria-expanded={Boolean(expanded[release.id])}
                      onClick={() =>
                        setExpanded((value) => ({
                          ...value,
                          [release.id]: !value[release.id],
                        }))
                      }
                    >
                      {expanded[release.id]
                        ? "Скрыть позиции"
                        : "Показать позиции"}
                    </button>
                  </div>
                  {expanded[release.id] && (
                    <ReleasePositions releaseId={release.id} />
                  )}
                </article>
              ))}
            </section>
          ) : (
            <section className="catalog-state">
              <h2>Релизов не найдено</h2>
              <p>
                {route.query || route.publication !== "all"
                  ? "Попробуйте изменить фильтры."
                  : "У этого артиста пока нет релизов."}
              </p>
            </section>
          )}
          {releases && releases.total > PAGE_SIZE && (
            <nav className="catalog-pagination" aria-label="Страницы релизов">
              <span>
                {route.offset + 1}–
                {Math.min(route.offset + PAGE_SIZE, releases.total)} из{" "}
                {releases.total}
              </span>
              <a
                aria-disabled={route.offset === 0}
                href={`#/library/artists/${encodeURIComponent(artistId)}?view=artists&q=${encodeURIComponent(route.query)}&publication=${route.publication}&offset=${Math.max(0, route.offset - PAGE_SIZE)}`}
              >
                Назад
              </a>
              <a
                aria-disabled={route.offset + PAGE_SIZE >= releases.total}
                href={`#/library/artists/${encodeURIComponent(artistId)}?view=artists&q=${encodeURIComponent(route.query)}&publication=${route.publication}&offset=${route.offset + PAGE_SIZE}`}
              >
                Далее
              </a>
            </nav>
          )}
        </>
      ) : null}
    </main>
  );
}

function ReleasePositions({ releaseId }: { releaseId: string }) {
  const [offset, setOffset] = useState(0);
  const query = useListLibraryPositions({
    release_id: releaseId,
    limit: PAGE_SIZE,
    offset,
    publication: "all",
  });
  const failure =
    query.data?.status !== undefined && query.data.status !== 200
      ? apiResponseError(
          query.data,
          `Не удалось загрузить позиции (HTTP ${query.data.status}).`,
        ).message
      : query.error instanceof Error
        ? query.error.message
        : "";
  if (query.isLoading) return <p role="status">Загрузка позиций…</p>;
  if (failure)
    return (
      <section className="catalog-state catalog-state--error" role="alert">
        <p>{failure}</p>
        <button type="button" onClick={() => void query.refetch()}>
          Повторить
        </button>
      </section>
    );
  const page = query.data?.status === 200 ? query.data.data : undefined;
  const positions = page?.items ?? [];
  return positions.length ? (
    <>
      <ol className="catalog-position-list">
        {positions.map((position) => (
          <li key={position.id}>
            <a
              href={`#/library/releases/${encodeURIComponent(releaseId)}/positions/${encodeURIComponent(position.id)}`}
            >
              {position.medium}.{position.displayed_number || position.position}{" "}
              {position.title || "Без названия"}
            </a>
            <span>
              {position.publication ? "Опубликовано" : "Не опубликовано"}
            </span>
          </li>
        ))}
      </ol>
      {page && page.total > PAGE_SIZE && (
        <nav className="catalog-pagination" aria-label="Страницы позиций">
          <span>
            {offset + 1}–{Math.min(offset + PAGE_SIZE, page.total)} из{" "}
            {page.total}
          </span>
          <button
            type="button"
            disabled={offset === 0}
            onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}
          >
            Назад
          </button>
          <button
            type="button"
            disabled={offset + PAGE_SIZE >= page.total}
            onClick={() => setOffset(offset + PAGE_SIZE)}
          >
            Далее
          </button>
        </nav>
      )}
    </>
  ) : (
    <p className="catalog-muted">В этом релизе пока нет позиций.</p>
  );
}
