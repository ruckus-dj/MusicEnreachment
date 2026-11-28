import { useCallback, useEffect, useRef, useState } from "react";
import { apiResponseError } from "../../api/client/errors";
import {
  useListLibraryArtists,
  useListLibraryPositions,
  useListLibraryReleases,
} from "../../api/generated/client";
import type { LibraryArtistPage } from "../../api/generated/client.schemas";
import { useI18n } from "../../app/i18n/host";
import { useHashObservation } from "../../app/navigation/hashLocation";
import {
  ApplicationLibraryCatalogBinder,
  type ApplicationLibraryCatalogModel,
  type ApplicationRouteProviderProps,
} from "../../ui/app/ApplicationRouteOutlet";
import type {
  ArtistCardSelectionControl,
  LibraryArtistCard,
} from "../../ui/features/library/LibraryArtistCards";
import {
  type LibraryStateFilter,
  libraryFilterOptions,
} from "../../ui/features/library/LibraryCatalogFilterView";
import type { LibraryCatalogPageState } from "../../ui/features/library/LibraryCatalogPageView";
import type {
  LibraryCatalogArtistResults,
  LibraryCatalogResults,
} from "../../ui/features/library/LibraryCatalogResults";
import { libraryCatalogLoadError } from "../../ui/features/library/libraryCatalogMessages";
import {
  type LibraryAlbumRowsData,
  ProductionLibraryAlbumRowsDataProvider,
  ProductionLibraryAlbumRowsProvider,
} from "./LibraryAlbumRows";
import { ProductionLibraryArtistCards } from "./LibraryArtistPhotos";
import { LibrarySelectionDock } from "./LibrarySelectionDock";
import { LibrarySelectionPreview } from "./LibrarySelectionPreview";
import { libraryPositionRowsModel } from "./LibraryTrackArtistRows";
import {
  type LibraryRoute,
  type LibraryView,
  libraryArtistRouteHref,
  libraryRouteHref,
  parseLibraryRoute,
} from "./libraryRoute";
import {
  type LibrarySelectionScope,
  useLibrarySelection,
} from "./useLibrarySelection";

const PAGE_SIZE = 50;
const SEARCH_DEBOUNCE_MS = 250;

const fallbackRoute: LibraryRoute = {
  view: "artists",
  query: "",
  publication: "all",
  availability: "all",
  offset: 0,
};
function requestError(
  response: { status: number; data?: unknown },
  locale: "en" | "ru",
) {
  return apiResponseError(
    response,
    libraryCatalogLoadError(locale, response.status),
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

/**
 * Reads the canonical axis/offset route and also accepts the approved
 * `filter`/`page` spelling used by the design prototype. Explicit axis or
 * offset params win, so sharing an older library link keeps its meaning and a
 * mixed-axis link stays intact.
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
  return next;
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
 * Pure artist-card projection for the portable artist leaf. The live screen
 * and tests can build the typed result without mounting the route.
 */
function libraryArtistCardsModel({
  items,
  route,
  selectionControl,
}: {
  items: LibraryArtistPage["items"];
  route: LibraryRoute;
  selectionControl: (
    scope: LibrarySelectionScope,
  ) => ArtistCardSelectionControl;
}): LibraryCatalogArtistResults {
  const artists: LibraryArtistCard[] = items.map((artist) => ({
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
    photoEligible: artist.provider_links.some(
      (link) => link.provider_code === "musicbrainz",
    ),
    selection: selectionControl({ kind: "artist", id: artist.id }),
  }));
  return {
    artists,
    pageSelectionControl: selectionControl({ kind: "page" }),
  };
}

/**
 * Keeps the single state filter, debounced search and server page in sync with
 * the hash route. The filter maps to the API's independent publication and
 * availability axes, while page maps to the server offset at PAGE_SIZE.
 */
function useLibraryController() {
  const observation = useHashObservation();
  const initialRoute =
    libraryControllerRoute(observation.hash) ?? fallbackRoute;
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
  // Depend on the observation identity, not just the hash text: a queued native
  // event can confirm an earlier accepted hash after an immediate Back.
  useEffect(() => {
    const next = libraryControllerRoute(observation.hash);
    if (!next) return;
    const href = libraryRouteHref(next);
    if (observation.hash === lastHref.current || href === lastHref.current)
      return;
    lastHref.current = href;
    skipNextUrlWrite.current = true;
    clearPendingSearch();
    setSearchInput(next.query);
    setRoute(next);
  }, [observation, clearPendingSearch]);

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
      const next = withStateFilter(current, filter);
      if (
        current.publication === next.publication &&
        current.availability === next.availability
      )
        return current;
      return { ...next, offset: 0 };
    });
  }, []);

  const setPublication = useCallback(
    (publication: LibraryRoute["publication"]) => {
      setRoute((current) =>
        current.publication === publication
          ? current
          : { ...current, publication, offset: 0 },
      );
    },
    [],
  );

  const setAvailability = useCallback(
    (availability: LibraryRoute["availability"]) => {
      setRoute((current) =>
        current.availability === availability
          ? current
          : { ...current, availability, offset: 0 },
      );
    },
    [],
  );

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
    setPublication,
    setAvailability,
    setPage,
    setSearch,
    reset,
  };
}

export function ProductionLibraryCatalogBinder({
  children,
}: ApplicationRouteProviderProps) {
  const { model, albumRows } = useLibraryCatalogModel();
  return (
    <ProductionLibraryAlbumRowsDataProvider value={albumRows}>
      <ApplicationLibraryCatalogBinder model={model}>
        {children}
      </ApplicationLibraryCatalogBinder>
    </ProductionLibraryAlbumRowsDataProvider>
  );
}

function useLibraryCatalogModel(): {
  model: ApplicationLibraryCatalogModel;
  albumRows: LibraryAlbumRowsData | null;
} {
  const { locale } = useI18n();
  const {
    route,
    view,
    filter,
    page,
    searchInput,
    setView,
    setFilter,
    setPublication,
    setAvailability,
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
        : libraryCatalogLoadError(locale)
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
  const isPristine =
    route.query === "" &&
    route.publication === "all" &&
    route.availability === "all";
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
                : route.publication === "published" &&
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

  const artistResults: LibraryCatalogArtistResults =
    view === "artists" && artistsBody
      ? {
          ...libraryArtistCardsModel({
            items: artistsBody.items,
            route,
            selectionControl: selection.selectionControl,
          }),
          View: ProductionLibraryArtistCards,
        }
      : { artists: [] };

  const trackResults =
    view === "tracks" && positionsBody
      ? libraryPositionRowsModel({
          items: positionsBody.items,
          selectionControl: selection.selectionControl,
          locale,
        })
      : { rows: [] };

  const albumRows: LibraryAlbumRowsData | null =
    view === "albums" && releasesBody
      ? {
          items: releasesBody.items,
          selectionControl: selection.selectionControl,
        }
      : null;

  const results: LibraryCatalogResults =
    view === "artists"
      ? { kind: "artists", ...artistResults }
      : view === "tracks"
        ? { kind: "tracks", ...trackResults }
        : { kind: "albums", DataProvider: ProductionLibraryAlbumRowsProvider };

  const model: ApplicationLibraryCatalogModel = {
    locale,
    view,
    summary: summary
      ? {
          artists: summary.artist_count,
          releases: summary.release_count,
          positions: summary.position_count,
          published: summary.published_position_count,
        }
      : null,
    incomingHref: "#/incoming-groups",
    search: searchInput,
    onSearch: setSearch,
    filter,
    onFilter: setFilter,
    publication: route.publication,
    availability: route.availability,
    onPublicationChange: setPublication,
    onAvailabilityChange: setAvailability,
    onResetFilters: reset,
    onViewChange: setView,
    state: pageState,
    results,
    page: currentPage,
    totalPages,
    totalItems: total,
    hasPrevious: route.offset !== 0,
    hasNext: route.offset + PAGE_SIZE < total,
    onPageChange: (next) => setPage(next, totalPages),
    selectionDock: (
      <LibrarySelectionDock
        positions={selection.positions}
        hiddenCount={selection.hiddenCount}
        onDeselect={selection.deselect}
        onClear={selection.clear}
        onPreview={selection.setFrozen}
      />
    ),
    selectionPreview: selection.frozen ? (
      <LibrarySelectionPreview
        positions={selection.frozen}
        onClose={() => selection.setFrozen(undefined)}
      />
    ) : undefined,
    dockSpace: selection.positions.length > 0,
  };

  return { model, albumRows };
}
