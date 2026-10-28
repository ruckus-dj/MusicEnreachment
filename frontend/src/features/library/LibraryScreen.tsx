import { useEffect, useState } from "react";
import { apiResponseError } from "../../api/client/errors";
import {
  getGetLibraryReleaseCoverImageUrl,
  useListLibraryArtists,
  useListLibraryPositions,
  useListLibraryReleases,
} from "../../api/generated/client";
import type {
  LibraryArtistCreditResponse,
  LibraryArtistPage,
  LibraryPositionPage,
  LibraryReleasePage,
} from "../../api/generated/client.schemas";
import { useHashLocation } from "../../navigation/hashLocation";
import {
  type LibraryRoute,
  type LibraryView,
  libraryArtistRouteHref,
  libraryRouteHref,
  parseLibraryRoute,
} from "./libraryRoute";
import "./catalog.css";

const PAGE_SIZE = 50;
const viewLabels: Record<LibraryView, string> = {
  artists: "Артисты",
  albums: "Все альбомы",
  tracks: "Все треки",
};

function requestError(response: { status: number; data?: unknown }) {
  return apiResponseError(
    response,
    `Не удалось загрузить медиатеку (HTTP ${response.status}).`,
  ).message;
}

function useLibraryRoute(): [
  LibraryRoute,
  (next: Partial<LibraryRoute>) => void,
] {
  const hash = useHashLocation();
  const route = parseLibraryRoute(hash) ?? {
    view: "artists" as const,
    query: "",
    publication: "all" as const,
    offset: 0,
  };
  return [
    route,
    (next) => {
      window.location.hash = libraryRouteHref({ ...route, ...next });
    },
  ];
}

export function LibraryScreen() {
  const [route, updateRoute] = useLibraryRoute();
  const [search, setSearch] = useState(route.query);
  useEffect(() => setSearch(route.query), [route.query]);
  const artists = useListLibraryArtists(
    {
      limit: PAGE_SIZE,
      offset: route.offset,
      q: route.query || undefined,
      publication: route.publication,
    },
    { query: { enabled: route.view === "artists" } },
  );
  const releases = useListLibraryReleases(
    {
      limit: PAGE_SIZE,
      offset: route.offset,
      q: route.query || undefined,
      publication: route.publication,
    },
    { query: { enabled: route.view === "albums" } },
  );
  const positions = useListLibraryPositions(
    {
      limit: PAGE_SIZE,
      offset: route.offset,
      q: route.query || undefined,
      publication: route.publication,
    },
    { query: { enabled: route.view === "tracks" } },
  );
  const query =
    route.view === "artists"
      ? artists
      : route.view === "albums"
        ? releases
        : positions;
  const response = query.data;
  const failedResponse =
    response && response.status !== 200 ? requestError(response) : "";
  const error =
    failedResponse ||
    (query.error
      ? query.error instanceof Error
        ? query.error.message
        : "Не удалось загрузить медиатеку."
      : "");
  const artistsBody =
    artists.data?.status === 200 ? artists.data.data : undefined;
  const releasesBody =
    releases.data?.status === 200 ? releases.data.data : undefined;
  const positionsBody =
    positions.data?.status === 200 ? positions.data.data : undefined;
  const body =
    route.view === "artists"
      ? artistsBody
      : route.view === "albums"
        ? releasesBody
        : positionsBody;
  const summary = body?.summary;
  const total = body?.total ?? 0;
  const hasResults =
    route.view === "artists"
      ? (artistsBody?.items.length ?? 0) > 0
      : route.view === "albums"
        ? (releasesBody?.items.length ?? 0) > 0
        : (positionsBody?.items.length ?? 0) > 0;
  const catalogIsEmpty =
    summary !== undefined &&
    summary.artist_count === 0 &&
    summary.release_count === 0 &&
    summary.position_count === 0;

  function retry() {
    void query.refetch();
  }

  return (
    <main className="library-catalog" aria-labelledby="catalog-title">
      <header className="catalog-header">
        <div>
          <h1 id="catalog-title">Медиатека</h1>
          <p className="catalog-muted" aria-live="polite">
            {summary
              ? `${summary.artist_count} артистов · ${summary.release_count} релизов · ${summary.position_count} позиций · ${summary.published_position_count} опубликовано`
              : "Каталог релизов и позиций"}
          </p>
        </div>
        <div className="catalog-header-actions">
          <a
            className="catalog-button catalog-button--primary"
            href="#/library/create"
          >
            Создать вручную
          </a>
          <a className="catalog-button" href="#/incoming-groups">
            Разобрать входящие
          </a>
        </div>
      </header>

      <div className="catalog-toolbar">
        <nav className="catalog-segments" aria-label="Вид медиатеки">
          {(Object.keys(viewLabels) as LibraryView[]).map((view) => (
            <a
              key={view}
              href={libraryRouteHref({ ...route, view, offset: 0 })}
              aria-current={route.view === view ? "page" : undefined}
            >
              {viewLabels[view]}
            </a>
          ))}
        </nav>
        <form
          className="catalog-search"
          onSubmit={(event) => {
            event.preventDefault();
            updateRoute({ query: search.trim(), offset: 0 });
          }}
        >
          <label className="catalog-sr-only" htmlFor="catalog-query">
            Поиск в медиатеке
          </label>
          <input
            id="catalog-query"
            type="search"
            value={search}
            placeholder="Артист, альбом, трек…"
            onChange={(event) => setSearch(event.currentTarget.value)}
          />
          <button type="submit">Найти</button>
        </form>
        <label className="catalog-filter">
          <span>Публикация</span>
          <select
            value={route.publication}
            onChange={(event) =>
              updateRoute({
                publication: event.currentTarget
                  .value as LibraryRoute["publication"],
                offset: 0,
              })
            }
          >
            <option value="all">Все</option>
            <option value="published">Опубликованные</option>
            <option value="unpublished">Не опубликованные</option>
          </select>
        </label>
      </div>

      {query.isLoading ? (
        <p className="catalog-state" role="status">
          Загрузка каталога…
        </p>
      ) : error ? (
        <section className="catalog-state catalog-state--error" role="alert">
          <p>{error}</p>
          <button type="button" onClick={retry}>
            Повторить
          </button>
        </section>
      ) : !hasResults ? (
        <section className="catalog-state" aria-live="polite">
          <h2>
            {catalogIsEmpty && !route.query
              ? "Медиатека пока пуста"
              : "Ничего не найдено"}
          </h2>
          <p>
            {catalogIsEmpty && !route.query
              ? "Добавьте источник и проанализируйте музыкальные файлы или создайте релиз вручную."
              : route.publication === "published" &&
                  summary?.published_position_count === 0
                ? "В медиатеке пока нет опубликованных позиций. Неопубликованные релизы доступны в режиме «Все»."
                : route.query || route.publication !== "all"
                  ? "Измените поисковый запрос или сбросьте фильтры."
                  : "Для этого представления пока нет записей."}
          </p>
          {(route.query || route.publication !== "all") && (
            <button
              type="button"
              onClick={() => {
                setSearch("");
                updateRoute({ query: "", publication: "all", offset: 0 });
              }}
            >
              Сбросить фильтры
            </button>
          )}
          {catalogIsEmpty && !route.query && (
            <a href="#/sources">Добавить источник</a>
          )}
        </section>
      ) : (
        <>
          {route.view === "artists" && artistsBody && (
            <ArtistCards items={artistsBody.items} route={route} />
          )}
          {route.view === "albums" && releasesBody && (
            <ReleaseTable items={releasesBody.items} />
          )}
          {route.view === "tracks" && positionsBody && (
            <PositionTable items={positionsBody.items} />
          )}
          <nav className="catalog-pagination" aria-label="Страницы каталога">
            <span>
              {route.offset + 1}–{Math.min(route.offset + PAGE_SIZE, total)} из{" "}
              {total}
            </span>
            <button
              type="button"
              disabled={route.offset === 0}
              onClick={() =>
                updateRoute({ offset: Math.max(0, route.offset - PAGE_SIZE) })
              }
            >
              Назад
            </button>
            <button
              type="button"
              disabled={route.offset + PAGE_SIZE >= total}
              onClick={() => updateRoute({ offset: route.offset + PAGE_SIZE })}
            >
              Далее
            </button>
          </nav>
        </>
      )}
    </main>
  );
}

function ArtistCards({
  items,
  route,
}: {
  items: LibraryArtistPage["items"];
  route: LibraryRoute;
}) {
  return (
    <div className="catalog-artist-grid">
      {items.map((artist) => (
        <a
          className="catalog-artist-card"
          href={libraryArtistRouteHref(artist.id, route)}
          key={artist.id}
        >
          <span className="catalog-initial" aria-hidden="true">
            {artist.name.trim().charAt(0).toLocaleUpperCase() || "?"}
          </span>
          <span className="catalog-artist-copy">
            <strong>{artist.name}</strong>
            <span>
              {artist.release_count} релизов · {artist.position_count} позиций
            </span>
            <span>
              {artist.published_position_count} опубликовано
              {artist.genres.length ? ` · ${artist.genres.join(" · ")}` : ""}
            </span>
          </span>
        </a>
      ))}
    </div>
  );
}

function CreditNames({ credits }: { credits: LibraryArtistCreditResponse[] }) {
  const occurrences = new Map<string, number>();
  const keyedCredits = credits.map((credit) => {
    const identity = JSON.stringify(credit);
    const occurrence = occurrences.get(identity) ?? 0;
    occurrences.set(identity, occurrence + 1);
    return { credit, key: `${identity}-${occurrence}` };
  });
  return (
    <span>
      {keyedCredits.map(({ credit, key }, index) => (
        <span key={key}>
          {credit.artist_id ? (
            <a
              href={`#/library/artists/${encodeURIComponent(credit.artist_id)}`}
            >
              {credit.credit_name || credit.artist_name || "Без имени"}
            </a>
          ) : (
            credit.credit_name || credit.artist_name || "Без имени"
          )}
          {credit.join_phrase ?? (index < credits.length - 1 ? " " : "")}
        </span>
      ))}
    </span>
  );
}

function Cover({ release }: { release: LibraryReleasePage["items"][number] }) {
  const generation = release.cover?.selected_generation;
  return (
    <span className="catalog-cover">
      {generation ? (
        <img
          src={getGetLibraryReleaseCoverImageUrl(release.id, generation)}
          alt=""
        />
      ) : (
        <span aria-hidden="true">
          {release.title.trim().charAt(0).toLocaleUpperCase() || "M"}
        </span>
      )}
    </span>
  );
}

function ReleaseTable({ items }: { items: LibraryReleasePage["items"] }) {
  return (
    <div className="catalog-table-wrap">
      <table className="catalog-table">
        <thead>
          <tr>
            <th>Альбом</th>
            <th>Артист</th>
            <th>Год / издание</th>
            <th>Позиции</th>
            <th>Доступность</th>
          </tr>
        </thead>
        <tbody>
          {items.map((release) => (
            <tr key={release.id}>
              <td>
                <span className="catalog-entity">
                  <Cover release={release} />
                  <a
                    href={`#/library/releases/${encodeURIComponent(release.id)}`}
                  >
                    {release.title || "Без названия"}
                  </a>
                </span>
              </td>
              <td>
                <CreditNames credits={release.artists} />
              </td>
              <td>
                {release.edition.release_date ??
                  release.edition.original_release_date ??
                  "Дата не указана"}
                <span className="catalog-sub">
                  {[
                    release.edition.country,
                    ...release.edition.labels,
                    ...release.edition.catalog_numbers,
                  ]
                    .filter(Boolean)
                    .join(" · ") || "Издание не указано"}
                </span>
              </td>
              <td>
                {release.counts.position_count}{" "}
                <span className="catalog-sub">
                  {release.counts.published_position_count} опубликовано
                </span>
              </td>
              <td>
                {release.counts.provider_position_count == null
                  ? "—"
                  : `${release.counts.available_position_count} / ${release.counts.provider_position_count}`}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function PositionTable({ items }: { items: LibraryPositionPage["items"] }) {
  return (
    <div className="catalog-table-wrap">
      <table className="catalog-table">
        <thead>
          <tr>
            <th>Трек</th>
            <th>Альбом</th>
            <th>Артист</th>
            <th>Позиция</th>
            <th>Исходники</th>
            <th>Публикация</th>
          </tr>
        </thead>
        <tbody>
          {items.map((position) => (
            <tr key={position.id}>
              <td>
                <a
                  href={`#/library/releases/${encodeURIComponent(position.release.id)}/positions/${encodeURIComponent(position.id)}`}
                >
                  {position.title || "Без названия"}
                </a>
                <span className="catalog-sub">
                  {position.provider_duration_ms == null
                    ? "Длительность не указана"
                    : `${Math.floor(position.provider_duration_ms / 60000)}:${String(Math.floor(position.provider_duration_ms / 1000) % 60).padStart(2, "0")}`}
                </span>
              </td>
              <td>
                <a
                  href={`#/library/releases/${encodeURIComponent(position.release.id)}`}
                >
                  {position.release.title}
                </a>
              </td>
              <td>
                <CreditNames credits={position.artists} />
              </td>
              <td>
                {position.medium} ·{" "}
                {position.displayed_number || position.position}
              </td>
              <td>{position.source_variant_count}</td>
              <td>
                {position.publication ? "Опубликовано" : "Не опубликовано"}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
