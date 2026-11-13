import { useEffect, useState } from "react";
import { Label, Radio, RadioGroup } from "react-aria-components";
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
  LibraryReleaseSummary,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";
import { useHashLocation } from "../../navigation/hashLocation";
import {
  type LibraryRoute,
  type LibraryView,
  libraryArtistRouteHref,
  libraryRouteHref,
  parseLibraryRoute,
} from "./libraryRoute";
import "./catalog.css";
import { MissingReleaseGroupRow } from "./MissingReleaseGroupRow";

const PAGE_SIZE = 50;
const screenCopy = {
  en: {
    title: "Library",
    summaryFallback: "Releases and positions in your library",
    artistCount: {
      en: ["artist", "artists"],
      ru: ["артист", "артиста", "артистов"],
    },
    releaseCount: {
      en: ["release", "releases"],
      ru: ["релиз", "релиза", "релизов"],
    },
    positionCount: {
      en: ["position", "positions"],
      ru: ["позиция", "позиции", "позиций"],
    },
    incoming: "Review incoming groups",
    view: "Library view",
    views: { artists: "Artists", albums: "All releases", tracks: "All tracks" },
    search: "Search the library",
    searchPlaceholder: "Artist, release, track…",
    searchButton: "Search",
    publication: "Publication",
    publicationOptions: {
      all: "All",
      published: "Published",
      unpublished: "Unpublished",
    },
    availability: "Audio availability",
    availabilityOptions: {
      all: "All",
      available: "Available",
      missing: "Missing",
    },
    loading: "Loading library…",
    requestError: "Could not load the library (HTTP {status}).",
    error: "Could not load the library.",
    retry: "Retry",
    emptyLibrary: "Your library is empty",
    noResults: "No results",
    addAndMatch:
      "Add and analyze a source, then match provider records into the library.",
    noPublished:
      "There are no published positions in the library yet. Unpublished releases are available under All.",
    changeSearch: "Change the search query or reset the filters.",
    noViewRecords: "There are no records in this view yet.",
    resetFilters: "Reset filters",
    addSource: "Add a source",
    catalogPages: "Catalog pages",
    of: "of",
    previous: "Previous",
    next: "Next",
    countPublished: "published",
    countAvailable: "with audio",
    completenessUnknown: "completeness unknown",
    cacheNotice:
      "includes known groups from the cache; the discography may be incomplete",
    audio: "Audio",
    album: "Release",
    artist: "Artist",
    yearEdition: "Date / edition",
    positions: "Positions",
    availabilityColumn: "Availability",
    track: "Track",
    position: "Position",
    sources: "Sources",
    publicationColumn: "Publication",
    durationUnknown: "Duration not specified",
    available: "Available",
    missing: "Missing",
    sourceCount: "Sources: {count}",
    publishedStatus: "Published",
    unpublishedStatus: "Unpublished",
    unnamed: "Untitled",
    unknownDate: "Date not specified",
    unknownEdition: "Edition not specified",
    unknownArtist: "Unnamed artist",
  },
  ru: {
    title: "Медиатека",
    summaryFallback: "Релизы и позиции медиатеки",
    artistCount: {
      en: ["artist", "artists"],
      ru: ["артист", "артиста", "артистов"],
    },
    releaseCount: {
      en: ["release", "releases"],
      ru: ["релиз", "релиза", "релизов"],
    },
    positionCount: {
      en: ["position", "positions"],
      ru: ["позиция", "позиции", "позиций"],
    },
    incoming: "Разобрать входящие группы",
    view: "Раздел медиатеки",
    views: { artists: "Артисты", albums: "Все релизы", tracks: "Все треки" },
    search: "Поиск в медиатеке",
    searchPlaceholder: "Артист, релиз, трек…",
    searchButton: "Найти",
    publication: "Публикация",
    publicationOptions: {
      all: "Все",
      published: "Опубликованные",
      unpublished: "Неопубликованные",
    },
    availability: "Наличие аудио",
    availabilityOptions: {
      all: "Все",
      available: "В наличии",
      missing: "Отсутствует",
    },
    loading: "Загрузка медиатеки…",
    requestError: "Не удалось загрузить медиатеку (HTTP {status}).",
    error: "Не удалось загрузить медиатеку.",
    retry: "Повторить",
    emptyLibrary: "Медиатека пока пуста",
    noResults: "Ничего не найдено",
    addAndMatch:
      "Добавьте источник и проанализируйте файлы, затем сопоставьте записи провайдера с медиатекой.",
    noPublished:
      "В медиатеке пока нет опубликованных позиций. Неопубликованные релизы доступны в режиме «Все».",
    changeSearch: "Измените поисковый запрос или сбросьте фильтры.",
    noViewRecords: "В этом разделе пока нет записей.",
    resetFilters: "Сбросить фильтры",
    addSource: "Добавить источник",
    catalogPages: "Страницы каталога",
    of: "из",
    previous: "Назад",
    next: "Далее",
    countPublished: "опубликовано",
    countAvailable: "с аудио",
    completenessUnknown: "полнота неизвестна",
    cacheNotice:
      "включая известные группы из кэша; дискография может быть неполной",
    audio: "Аудио",
    album: "Релиз",
    artist: "Артист",
    yearEdition: "Дата / издание",
    positions: "Позиции",
    availabilityColumn: "Наличие",
    track: "Трек",
    position: "Позиция",
    sources: "Исходники",
    publicationColumn: "Публикация",
    durationUnknown: "Длительность не указана",
    available: "В наличии",
    missing: "Отсутствует",
    sourceCount: "Исходники: {count}",
    publishedStatus: "Опубликовано",
    unpublishedStatus: "Не опубликовано",
    unnamed: "Без названия",
    unknownDate: "Дата не указана",
    unknownEdition: "Издание не указано",
    unknownArtist: "Артист не указан",
  },
} as const;

function countLabel(
  count: number,
  locale: "en" | "ru",
  forms: {
    en: readonly [string, string];
    ru: readonly [string, string, string];
  },
) {
  const formatted = new Intl.NumberFormat(
    locale === "ru" ? "ru-RU" : "en-US",
  ).format(count);
  if (locale === "en") return `${formatted} ${forms.en[count === 1 ? 0 : 1]}`;
  const plural = new Intl.PluralRules("ru-RU").select(count);
  return `${formatted} ${forms.ru[plural === "one" ? 0 : plural === "few" ? 1 : 2]}`;
}

function requestError(
  response: { status: number; data?: unknown },
  locale: "en" | "ru",
) {
  return apiResponseError(
    response,
    screenCopy[locale].requestError.replace(
      "{status}",
      String(response.status),
    ),
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
    availability: "all" as const,
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
  const { locale } = useI18n();
  const copy = screenCopy[locale];
  const viewLabels: Record<LibraryView, string> = copy.views;
  const [route, updateRoute] = useLibraryRoute();
  const [search, setSearch] = useState(route.query);
  useEffect(() => setSearch(route.query), [route.query]);
  const artists = useListLibraryArtists(
    {
      limit: PAGE_SIZE,
      offset: route.offset,
      q: route.query || undefined,
      publication: route.publication,
      availability: route.availability,
    },
    { query: { enabled: route.view === "artists" } },
  );
  const releases = useListLibraryReleases(
    {
      limit: PAGE_SIZE,
      offset: route.offset,
      q: route.query || undefined,
      publication: route.publication,
      availability: route.availability,
    },
    { query: { enabled: route.view === "albums" } },
  );
  const positions = useListLibraryPositions(
    {
      limit: PAGE_SIZE,
      offset: route.offset,
      q: route.query || undefined,
      publication: route.publication,
      availability: route.availability,
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
    response && response.status !== 200 ? requestError(response, locale) : "";
  const error =
    failedResponse ||
    (query.error
      ? query.error instanceof Error
        ? query.error.message
        : copy.error
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
          <h1 id="catalog-title">{copy.title}</h1>
          <p className="catalog-muted" aria-live="polite">
            {summary
              ? `${countLabel(summary.artist_count, locale, copy.artistCount)} · ${countLabel(summary.release_count, locale, copy.releaseCount)} · ${countLabel(summary.position_count, locale, copy.positionCount)} · ${summary.published_position_count} ${copy.countPublished}`
              : copy.summaryFallback}
          </p>
        </div>
        <div className="catalog-header-actions">
          <a className="catalog-button" href="#/incoming-groups">
            {copy.incoming}
          </a>
        </div>
      </header>

      <div className="catalog-toolbar">
        <nav className="catalog-segments" aria-label={copy.view}>
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
            {copy.search}
          </label>
          <input
            id="catalog-query"
            type="search"
            value={search}
            placeholder={copy.searchPlaceholder}
            onChange={(event) => setSearch(event.currentTarget.value)}
          />
          <button type="submit">{copy.searchButton}</button>
        </form>
        <label className="catalog-filter">
          <span>{copy.publication}</span>
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
            <option value="all">{copy.publicationOptions.all}</option>
            <option value="published">
              {copy.publicationOptions.published}
            </option>
            <option value="unpublished">
              {copy.publicationOptions.unpublished}
            </option>
          </select>
        </label>
        <RadioGroup
          orientation="horizontal"
          className="catalog-availability"
          value={route.availability}
          onChange={(availability) =>
            updateRoute({
              availability: availability as LibraryRoute["availability"],
              offset: 0,
            })
          }
        >
          <Label>{copy.availability}</Label>
          <div className="catalog-availability-segments">
            <Radio value="all">{copy.availabilityOptions.all}</Radio>
            <Radio value="available">
              {copy.availabilityOptions.available}
            </Radio>
            <Radio value="missing">{copy.availabilityOptions.missing}</Radio>
          </div>
        </RadioGroup>
      </div>

      {query.isLoading ? (
        <p className="catalog-state" role="status">
          {copy.loading}
        </p>
      ) : error ? (
        <section className="catalog-state catalog-state--error" role="alert">
          <p>{error}</p>
          <button type="button" onClick={retry}>
            {copy.retry}
          </button>
        </section>
      ) : !hasResults ? (
        <section className="catalog-state" aria-live="polite">
          <h2>
            {catalogIsEmpty && !route.query
              ? copy.emptyLibrary
              : copy.noResults}
          </h2>
          <p>
            {catalogIsEmpty && !route.query
              ? copy.addAndMatch
              : route.publication === "published" &&
                  summary?.published_position_count === 0
                ? copy.noPublished
                : route.query ||
                    route.publication !== "all" ||
                    route.availability !== "all"
                  ? copy.changeSearch
                  : copy.noViewRecords}
          </p>
          {(route.query ||
            route.publication !== "all" ||
            route.availability !== "all") && (
            <button
              type="button"
              onClick={() => {
                setSearch("");
                updateRoute({
                  query: "",
                  publication: "all",
                  availability: "all",
                  offset: 0,
                });
              }}
            >
              {copy.resetFilters}
            </button>
          )}
          {catalogIsEmpty && !route.query && (
            <a href="#/sources">{copy.addSource}</a>
          )}
        </section>
      ) : (
        <>
          {route.view === "artists" && artistsBody && (
            <ArtistCards
              items={artistsBody.items}
              route={route}
              locale={locale}
              copy={copy}
            />
          )}
          {route.view === "albums" && releasesBody && (
            <ReleaseTable items={releasesBody.items} copy={copy} />
          )}
          {route.view === "tracks" && positionsBody && (
            <PositionTable items={positionsBody.items} copy={copy} />
          )}
          <nav className="catalog-pagination" aria-label={copy.catalogPages}>
            <span>
              {route.offset + 1}–{Math.min(route.offset + PAGE_SIZE, total)}{" "}
              {copy.of} {total}
            </span>
            <button
              type="button"
              disabled={route.offset === 0}
              onClick={() =>
                updateRoute({ offset: Math.max(0, route.offset - PAGE_SIZE) })
              }
            >
              {copy.previous}
            </button>
            <button
              type="button"
              disabled={route.offset + PAGE_SIZE >= total}
              onClick={() => updateRoute({ offset: route.offset + PAGE_SIZE })}
            >
              {copy.next}
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
  locale,
  copy,
}: {
  items: LibraryArtistPage["items"];
  route: LibraryRoute;
  locale: "en" | "ru";
  copy: (typeof screenCopy)["en"] | (typeof screenCopy)["ru"];
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
              {countLabel(artist.release_count, locale, copy.releaseCount)} ·{" "}
              {countLabel(artist.position_count, locale, copy.positionCount)}
              <span className="catalog-sub">{copy.cacheNotice}</span>
            </span>
            <span>
              {copy.audio}: {artist.available_position_count} ·{" "}
              {artist.published_position_count} {copy.countPublished}
              {artist.genres.length ? ` · ${artist.genres.join(" · ")}` : ""}
            </span>
          </span>
        </a>
      ))}
    </div>
  );
}

function CreditNames({
  credits,
  unknownArtist,
}: {
  credits: LibraryArtistCreditResponse[];
  unknownArtist: string;
}) {
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
              {credit.credit_name || credit.artist_name || unknownArtist}
            </a>
          ) : (
            credit.credit_name || credit.artist_name || unknownArtist
          )}
          {credit.join_phrase ?? (index < credits.length - 1 ? " " : "")}
        </span>
      ))}
    </span>
  );
}

function Cover({ release }: { release: LibraryReleaseSummary }) {
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

function ReleaseTable({
  items,
  copy,
}: {
  items: LibraryReleasePage["items"];
  copy: (typeof screenCopy)["en"] | (typeof screenCopy)["ru"];
}) {
  return (
    <div className="catalog-table-wrap">
      <table className="catalog-table">
        <thead>
          <tr>
            <th>{copy.album}</th>
            <th>{copy.artist}</th>
            <th>{copy.yearEdition}</th>
            <th>{copy.positions}</th>
            <th>{copy.availabilityColumn}</th>
          </tr>
        </thead>
        <tbody>
          {items.map((item) =>
            item.kind === "library_release" && item.library_release ? (
              <tr key={`library_release:${item.library_release.id}`}>
                <td>
                  <span className="catalog-entity">
                    <Cover release={item.library_release} />
                    <a
                      href={`#/library/releases/${encodeURIComponent(item.library_release.id)}`}
                    >
                      {item.library_release.title || copy.unnamed}
                    </a>
                  </span>
                </td>
                <td>
                  <CreditNames
                    credits={item.library_release.artists}
                    unknownArtist={copy.unknownArtist}
                  />
                </td>
                <td>
                  {item.library_release.edition.release_date ??
                    item.library_release.edition.original_release_date ??
                    copy.unknownDate}
                  <span className="catalog-sub">
                    {[
                      item.library_release.edition.country,
                      ...item.library_release.edition.labels,
                      ...item.library_release.edition.catalog_numbers,
                    ]
                      .filter(Boolean)
                      .join(" · ") || copy.unknownEdition}
                  </span>
                </td>
                <td>
                  {item.library_release.counts.position_count}{" "}
                  <span className="catalog-sub">
                    {item.library_release.counts.published_position_count}{" "}
                    {copy.countPublished}
                  </span>
                </td>
                <td>
                  {item.library_release.counts.provider_position_count == null
                    ? `${item.library_release.counts.available_position_count} ${copy.countAvailable} · ${copy.completenessUnknown}`
                    : `${item.library_release.counts.available_position_count} / ${item.library_release.counts.provider_position_count}`}
                </td>
              </tr>
            ) : item.kind === "missing_release_group" &&
              item.missing_release_group ? (
              <MissingReleaseGroupRow
                key={`missing_release_group:${item.missing_release_group.provider_key}`}
                group={item.missing_release_group}
                presentation="table"
              />
            ) : null,
          )}
        </tbody>
      </table>
    </div>
  );
}

function PositionTable({
  items,
  copy,
}: {
  items: LibraryPositionPage["items"];
  copy: (typeof screenCopy)["en"] | (typeof screenCopy)["ru"];
}) {
  return (
    <div className="catalog-table-wrap">
      <table className="catalog-table">
        <thead>
          <tr>
            <th>{copy.track}</th>
            <th>{copy.album}</th>
            <th>{copy.artist}</th>
            <th>{copy.position}</th>
            <th>{copy.audio}</th>
            <th>{copy.sources}</th>
            <th>{copy.publicationColumn}</th>
          </tr>
        </thead>
        <tbody>
          {items.map((position) => (
            <tr key={position.id}>
              <td>
                <a
                  href={`#/library/releases/${encodeURIComponent(position.release.id)}/positions/${encodeURIComponent(position.id)}`}
                >
                  {position.title || copy.unnamed}
                </a>
                <span className="catalog-sub">
                  {position.provider_duration_ms == null
                    ? copy.durationUnknown
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
                <CreditNames
                  credits={position.artists}
                  unknownArtist={copy.unknownArtist}
                />
              </td>
              <td>
                {position.medium} ·{" "}
                {position.displayed_number || position.position}
              </td>
              <td>
                {position.audio_available ? copy.available : copy.missing}
              </td>
              <td>
                {copy.sourceCount.replace(
                  "{count}",
                  String(position.source_variant_count),
                )}
              </td>
              <td>
                {position.publication
                  ? copy.publishedStatus
                  : copy.unpublishedStatus}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
