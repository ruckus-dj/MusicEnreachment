import { useCallback, useEffect, useRef, useState } from "react";
import { navigateHash } from "../../app/hashLocation";
import type { LibraryStateFilter } from "./LibraryCatalogFilterView";
import type {
  LibraryAvailabilityFilter,
  LibraryPublicationFilter,
} from "./libraryDetailRoute";
import {
  buildLibraryRouteHref,
  LIBRARY_PAGE_SIZE,
  type LibraryRouteState,
  type LibraryView,
  libraryFilterFor,
  parseLibraryRoute,
  withLibraryFilter,
} from "./libraryRouteState";

export const LIBRARY_SEARCH_DEBOUNCE_MS = 250;

const pageForOffset = (offset: number) =>
  Math.floor(offset / LIBRARY_PAGE_SIZE) + 1;

/**
 * Keeps the paged library controls and the hash route in sync. Search input is
 * immediate while the effective query, page and URL settle after the debounce.
 */
export function useLibraryRouteState(route: string) {
  const routeHash = route.startsWith("#") ? route : `#${route}`;
  const [state, setState] = useState<LibraryRouteState>(() =>
    parseLibraryRoute(route),
  );
  const [searchInput, setSearchInput] = useState(
    () => parseLibraryRoute(route).query,
  );
  const lastHref = useRef(buildLibraryRouteHref(state));
  const pendingSearch = useRef<number | null>(null);
  const skipNextUrlWrite = useRef(false);
  const isLibraryRoute = routeHash.split("?", 1)[0] === "#/library";

  const clearPendingSearch = useCallback(() => {
    if (pendingSearch.current !== null) {
      window.clearTimeout(pendingSearch.current);
      pendingSearch.current = null;
    }
  }, []);

  // Adopt browser back/forward or a copied link without fighting our own writes.
  useEffect(() => {
    if (!isLibraryRoute) return;
    const next = parseLibraryRoute(route);
    const href = buildLibraryRouteHref(next);
    if (routeHash === lastHref.current || href === lastHref.current) return;
    lastHref.current = href;
    skipNextUrlWrite.current = true;
    clearPendingSearch();
    setSearchInput(next.query);
    setState(next);
  }, [route, routeHash, isLibraryRoute, clearPendingSearch]);

  useEffect(() => {
    if (isLibraryRoute) return;
    clearPendingSearch();
    setSearchInput(state.query);
  }, [isLibraryRoute, state.query, clearPendingSearch]);

  useEffect(() => {
    if (!isLibraryRoute) return;
    if (skipNextUrlWrite.current) {
      skipNextUrlWrite.current = false;
      return;
    }
    const href = buildLibraryRouteHref(state);
    if (href === lastHref.current) return;
    lastHref.current = href;
    navigateHash(href);
  }, [state, isLibraryRoute]);

  useEffect(
    () => () => {
      clearPendingSearch();
    },
    [clearPendingSearch],
  );

  const setView = useCallback((view: LibraryView) => {
    setState((current) =>
      current.view === view ? current : { ...current, view, offset: 0 },
    );
  }, []);

  const setFilter = useCallback((filter: LibraryStateFilter) => {
    setState((current) => {
      const next = withLibraryFilter(current, filter);
      if (
        next.publication === current.publication &&
        next.availability === current.availability
      )
        return current;
      return { ...next, offset: 0 };
    });
  }, []);

  const setPublication = useCallback(
    (publication: LibraryPublicationFilter) => {
      clearPendingSearch();
      setState((current) => {
        if (
          current.publication === publication &&
          current.query === searchInput &&
          current.offset === 0
        )
          return current;
        return { ...current, publication, query: searchInput, offset: 0 };
      });
    },
    [clearPendingSearch, searchInput],
  );

  const setAvailability = useCallback(
    (availability: LibraryAvailabilityFilter) => {
      clearPendingSearch();
      setState((current) => {
        if (
          current.availability === availability &&
          current.query === searchInput &&
          current.offset === 0
        )
          return current;
        return { ...current, availability, query: searchInput, offset: 0 };
      });
    },
    [clearPendingSearch, searchInput],
  );

  const setPage = useCallback(
    (page: number, totalPages = Number.MAX_SAFE_INTEGER) => {
      const nextPage = Math.max(
        1,
        Math.min(Math.trunc(page), Math.max(1, totalPages)),
      );
      const offset = (nextPage - 1) * LIBRARY_PAGE_SIZE;
      setState((current) =>
        current.offset === offset ? current : { ...current, offset },
      );
    },
    [],
  );

  const setSearch = useCallback(
    (value: string) => {
      setSearchInput(value);
      clearPendingSearch();
      pendingSearch.current = window.setTimeout(() => {
        pendingSearch.current = null;
        setState((current) =>
          current.query === value
            ? current
            : { ...current, query: value, offset: 0 },
        );
      }, LIBRARY_SEARCH_DEBOUNCE_MS);
    },
    [clearPendingSearch],
  );

  const reset = useCallback(() => {
    clearPendingSearch();
    setSearchInput("");
    setState((current) => ({
      ...current,
      query: "",
      publication: "all",
      availability: "all",
      offset: 0,
    }));
  }, [clearPendingSearch]);

  return {
    view: state.view,
    query: state.query,
    filter: libraryFilterFor(state),
    page: pageForOffset(state.offset),
    offset: state.offset,
    publication: state.publication,
    availability: state.availability,
    searchInput,
    isLibraryRoute,
    setView,
    setFilter,
    setPublication,
    setAvailability,
    setPage,
    setSearch,
    reset,
  };
}
