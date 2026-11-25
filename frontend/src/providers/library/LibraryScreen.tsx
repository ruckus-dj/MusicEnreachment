import { useCallback, useEffect, useRef, useState } from "react";
import { apiResponseError } from "../../api/client/errors";
import {
  useListLibraryArtists,
  useListLibraryPositions,
  useListLibraryReleases,
} from "../../api/generated/client";
import { useI18n } from "../../app/i18n/host";
import { useHashLocation } from "../../app/navigation/hashLocation";
import {
  type LibraryArtistCard,
  LibraryArtistCards,
} from "../../ui/features/library/LibraryArtistCards";
import {
  type LibraryStateFilter,
  libraryFilterOptions,
} from "../../ui/features/library/LibraryCatalogFilterView";
import {
  type LibraryCatalogPageState,
  LibraryCatalogPageView,
} from "../../ui/features/library/LibraryCatalogPageView";
import { librarySelectionButtonClass } from "../../ui/features/library/LibrarySelectionPreviewView";
import { LibraryAlbumRows } from "./LibraryAlbumRows";
import { LibrarySelectionDock } from "./LibrarySelectionDock";
import { LibrarySelectionPreview } from "./LibrarySelectionPreview";
import { LibraryPositionRows } from "./LibraryTrackArtistRows";
import {
  type LibraryRoute,
  type LibraryView,
  libraryArtistRouteHref,
  libraryRouteHref,
  parseLibraryRoute,
} from "./libraryRoute";
import { useLibrarySelection } from "./useLibrarySelection";

const PAGE_SIZE = 50;
const SEARCH_DEBOUNCE_MS = 250;

const fallbackRoute: LibraryRoute = {
  view: "artists",
  query: "",
  publication: "all",
  availability: "all",
  offset: 0,
};
const screenCopy = {
  en: {
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
    requestError: "Could not load the library (HTTP {status}).",
    error: "Could not load the library.",
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
    requestError: "Не удалось загрузить медиатеку (HTTP {status}).",
    error: "Не удалось загрузить медиатеку.",
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

function stateFilterFor(route: LibraryRoute): LibraryStateFilter {
  if (route.publication === "published") return "published";
  if (route.availability === "available") return "available";
  if (route.availability === "missing") return "missing";
  return "all";
}

function withStateFilter(
  route: LibraryRoute,
  filter: LibraryStateFilter,
): LibraryRoute {
  return {
    ...route,
    publication: filter === "published" ? "published" : "all",
    availability:
      filter === "available"
        ? "available"
        : filter === "missing"
          ? "missing"
          : "all",
  };
}

function normalizedRoute(route: LibraryRoute): LibraryRoute {
  return withStateFilter(route, stateFilterFor(route));
}

/**
 * Reads the canonical axis/offset route and also accepts the approved
 * `filter`/`page` spelling used by the design prototype. Explicit axis or
 * offset params win, so sharing an older library link keeps its meaning.
 */
function libraryControllerRoute(hash: string): LibraryRoute | undefined {
  const route = parseLibraryRoute(hash);
  if (!route) return undefined;
  const path = hash.startsWith("#") ? hash.slice(1) : hash;
  const [, search = ""] = path.split("?", 2);
  const params = new URLSearchParams(search);
  let next = route;
  const requestedFilter = params.get("filter");
  if (
    !params.has("publication") &&
    !params.has("availability") &&
    requestedFilter &&
    libraryFilterOptions.includes(requestedFilter as LibraryStateFilter)
  ) {
    next = withStateFilter(next, requestedFilter as LibraryStateFilter);
  }
  const requestedPage = Number(params.get("page") ?? "");
  if (
    !params.has("offset") &&
    Number.isSafeInteger(requestedPage) &&
    requestedPage > 0
  ) {
    next = { ...next, offset: (requestedPage - 1) * PAGE_SIZE };
  }
  return normalizedRoute(next);
}

function pageForOffset(offset: number) {
  return Math.floor(offset / PAGE_SIZE) + 1;
}

const artistPalettes = ["clay", "forest", "dusk", "sand", "slate"] as const;

function artistPalette(id: string) {
  let hash = 0;
  for (const character of id) {
    hash = (hash * 31 + (character.codePointAt(0) ?? 0)) | 0;
  }
  return artistPalettes[Math.abs(hash) % artistPalettes.length];
}

function artistMonogram(name: string) {
  return name.trim().charAt(0).toLocaleUpperCase() || "?";
}

/**
 * Keeps the single state filter, debounced search and server page in sync with
 * the hash route. The filter maps to the API's independent publication and
 * availability axes, while page maps to the server offset at PAGE_SIZE.
 */
export function useLibraryController() {
  const hash = useHashLocation();
  const initialRoute =
    libraryControllerRoute(hash) ?? normalizedRoute(fallbackRoute);
  const [route, setRoute] = useState(initialRoute);
  const [searchInput, setSearchInput] = useState(initialRoute.query);
  const lastHref = useRef(libraryRouteHref(initialRoute));
  const pendingSearch = useRef<number | null>(null);
  const skipNextUrlWrite = useRef(false);

  const clearPendingSearch = useCallback(() => {
    if (pendingSearch.current !== null) {
      window.clearTimeout(pendingSearch.current);
      pendingSearch.current = null;
    }
  }, []);

  // Adopt browser back/forward or a copied link without fighting our own writes.
  useEffect(() => {
    const next = libraryControllerRoute(hash);
    if (!next) return;
    const href = libraryRouteHref(next);
    if (hash === lastHref.current || href === lastHref.current) return;
    lastHref.current = href;
    skipNextUrlWrite.current = true;
    clearPendingSearch();
    setSearchInput(next.query);
    setRoute(next);
  }, [hash, clearPendingSearch]);

  useEffect(() => {
    if (skipNextUrlWrite.current) {
      skipNextUrlWrite.current = false;
      return;
    }
    const href = libraryRouteHref(route);
    if (href === lastHref.current) return;
    lastHref.current = href;
    window.location.hash = href;
  }, [route]);

  useEffect(() => () => clearPendingSearch(), [clearPendingSearch]);

  const setView = useCallback((view: LibraryView) => {
    setRoute((current) =>
      current.view === view ? current : { ...current, view, offset: 0 },
    );
  }, []);

  const setFilter = useCallback((filter: LibraryStateFilter) => {
    setRoute((current) => {
      if (stateFilterFor(current) === filter) return current;
      return { ...withStateFilter(current, filter), offset: 0 };
    });
  }, []);

  const setPage = useCallback(
    (page: number, totalPages = Number.MAX_SAFE_INTEGER) => {
      const nextPage = Math.max(
        1,
        Math.min(Math.trunc(page), Math.max(1, totalPages)),
      );
      setRoute((current) => {
        const offset = (nextPage - 1) * PAGE_SIZE;
        return current.offset === offset ? current : { ...current, offset };
      });
    },
    [],
  );

  const setSearch = useCallback(
    (value: string) => {
      setSearchInput(value);
      clearPendingSearch();
      pendingSearch.current = window.setTimeout(() => {
        pendingSearch.current = null;
        setRoute((current) =>
          current.query === value
            ? current
            : { ...current, query: value, offset: 0 },
        );
      }, SEARCH_DEBOUNCE_MS);
    },
    [clearPendingSearch],
  );

  const reset = useCallback(() => {
    clearPendingSearch();
    setSearchInput("");
    setRoute((current) => ({
      ...current,
      query: "",
      publication: "all",
      availability: "all",
      offset: 0,
    }));
  }, [clearPendingSearch]);

  return {
    route,
    view: route.view,
    query: route.query,
    filter: stateFilterFor(route),
    page: pageForOffset(route.offset),
    searchInput,
    setView,
    setFilter,
    setPage,
    setSearch,
    reset,
  };
}

export function LibraryScreen() {
  const { locale } = useI18n();
  const copy = screenCopy[locale];
  const {
    route,
    view,
    filter,
    page,
    searchInput,
    setView,
    setFilter,
    setPage,
    setSearch,
    reset,
  } = useLibraryController();
  const artists = useListLibraryArtists(
    {
      limit: PAGE_SIZE,
      offset: route.offset,
      q: route.query || undefined,
      publication: route.publication,
      availability: route.availability,
    },
    { query: { enabled: view === "artists" } },
  );
  const releases = useListLibraryReleases(
    {
      limit: PAGE_SIZE,
      offset: route.offset,
      q: route.query || undefined,
      publication: route.publication,
      availability: route.availability,
    },
    { query: { enabled: view === "albums" } },
  );
  const positions = useListLibraryPositions(
    {
      limit: PAGE_SIZE,
      offset: route.offset,
      q: route.query || undefined,
      publication: route.publication,
      availability: route.availability,
    },
    { query: { enabled: view === "tracks" } },
  );
  const activeQuery =
    view === "artists" ? artists : view === "albums" ? releases : positions;
  const response = activeQuery.data;
  const failedResponse =
    response && response.status !== 200 ? requestError(response, locale) : "";
  const error =
    failedResponse ||
    (activeQuery.error
      ? activeQuery.error instanceof Error
        ? activeQuery.error.message
        : copy.error
      : "");
  const artistsBody =
    artists.data?.status === 200 ? artists.data.data : undefined;
  const releasesBody =
    releases.data?.status === 200 ? releases.data.data : undefined;
  const positionsBody =
    positions.data?.status === 200 ? positions.data.data : undefined;
  const body =
    view === "artists"
      ? artistsBody
      : view === "albums"
        ? releasesBody
        : positionsBody;
  const summary = body?.summary;
  const total = body?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const currentPage = Math.min(page, totalPages);
  useEffect(() => {
    if (body && !error && page > totalPages) setPage(totalPages, totalPages);
  }, [body, error, page, setPage, totalPages]);
  const hasResults =
    view === "artists"
      ? (artistsBody?.items.length ?? 0) > 0
      : view === "albums"
        ? (releasesBody?.items.length ?? 0) > 0
        : (positionsBody?.items.length ?? 0) > 0;
  const catalogIsEmpty =
    summary !== undefined &&
    summary.artist_count === 0 &&
    summary.release_count === 0 &&
    summary.position_count === 0;
  const isPristine = route.query === "" && filter === "all";
  const selection = useLibrarySelection({
    artistIDs:
      view === "artists"
        ? artistsBody?.items.map((artist) => artist.id)
        : undefined,
    releaseIDs:
      view === "albums"
        ? releasesBody?.items.flatMap((item) =>
            item.library_release ? [item.library_release.id] : [],
          )
        : undefined,
    trackPositions:
      view === "tracks"
        ? positionsBody?.items.map((position) => ({
            release_id: position.release.id,
            position_id: position.id,
            title: position.title,
            release_title: position.release.title,
          }))
        : undefined,
    pageReady: !!body && !error,
  });
  const artistCards: LibraryArtistCard[] =
    view === "artists" && artistsBody
      ? artistsBody.items.map((artist) => ({
          id: artist.id,
          name: artist.name,
          href: libraryArtistRouteHref(artist.id, route),
          monogram: artistMonogram(artist.name),
          palette: artistPalette(artist.id),
          genres: artist.genres,
          releaseCount: artist.release_count,
          positionCount: artist.position_count,
          availableCount: artist.available_position_count,
          publishedCount: artist.published_position_count,
          partialProviderScope: true,
          selection: selection.selectionControl({
            kind: "artist",
            id: artist.id,
          }),
        }))
      : [];

  function retry() {
    void activeQuery.refetch();
  }

  const pageState: LibraryCatalogPageState = activeQuery.isLoading
    ? { status: "loading" }
    : error
      ? {
          status: "error",
          message: error,
          onRetry: retry,
        }
      : !hasResults
        ? {
            status: "empty",
            reason:
              catalogIsEmpty && isPristine
                ? "library"
                : filter === "published" &&
                    summary?.published_position_count === 0
                  ? "unpublished"
                  : !isPristine
                    ? "filtered"
                    : "view",
            onReset: isPristine ? undefined : reset,
            addSourceHref:
              catalogIsEmpty && isPristine ? "#/sources" : undefined,
          }
        : {
            status: "results",
            membershipError: selection.membershipError
              ? {
                  message: selection.membershipError,
                  onRetry: selection.retryMembership,
                }
              : undefined,
          };

  return (
    <LibraryCatalogPageView
      locale={locale}
      view={view}
      summary={
        summary
          ? `${countLabel(summary.artist_count, locale, copy.artistCount)} · ${countLabel(summary.release_count, locale, copy.releaseCount)} · ${countLabel(summary.position_count, locale, copy.positionCount)} · ${summary.published_position_count} ${copy.countPublished}`
          : copy.summaryFallback
      }
      headerAction={
        <a
          className={`${librarySelectionButtonClass} inline-flex items-center text-xs`}
          href="#/incoming-groups"
        >
          {copy.incoming}
        </a>
      }
      search={searchInput}
      onSearch={setSearch}
      filter={filter}
      onFilter={setFilter}
      onViewChange={setView}
      state={pageState}
      results={
        <>
          {view === "artists" && artistsBody && (
            <LibraryArtistCards
              locale={locale}
              artists={artistCards}
              pageSelectionControl={selection.selectionControl({
                kind: "page",
              })}
            />
          )}
          {view === "albums" && releasesBody && (
            <LibraryAlbumRows
              items={releasesBody.items}
              selectionControl={selection.selectionControl}
            />
          )}
          {view === "tracks" && positionsBody && (
            <LibraryPositionRows
              items={positionsBody.items}
              selectionControl={selection.selectionControl}
            />
          )}
        </>
      }
      page={currentPage}
      totalPages={totalPages}
      totalItems={total}
      hasPrevious={route.offset !== 0}
      hasNext={route.offset + PAGE_SIZE < total}
      onPageChange={(next) => setPage(next, totalPages)}
      selectionDock={
        <LibrarySelectionDock
          positions={selection.positions}
          hiddenCount={selection.hiddenCount}
          onDeselect={selection.deselect}
          onClear={selection.clear}
          onPreview={selection.setFrozen}
        />
      }
      selectionPreview={
        selection.frozen ? (
          <LibrarySelectionPreview
            positions={selection.frozen}
            onClose={() => selection.setFrozen(undefined)}
          />
        ) : undefined
      }
      dockSpace={selection.positions.length > 0}
    />
  );
}
