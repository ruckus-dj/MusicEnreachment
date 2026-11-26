import type { LibraryStateFilter } from "./LibraryCatalogFilterView";
import type {
  LibraryAvailabilityFilter,
  LibraryPublicationFilter,
} from "./libraryDetailRoute";
import {
  defaultLibraryListQuery,
  type LibraryListQuery,
  libraryListRouteHref,
  parseLibraryListRoute,
} from "./libraryListRoute";

export type { LibraryView } from "./libraryView";

export const LIBRARY_PAGE_SIZE = 50;

export type PublicationFilter = LibraryPublicationFilter;
export type AvailabilityFilter = LibraryAvailabilityFilter;

export type LibraryRouteState = LibraryListQuery;

export const defaultLibraryRouteState: LibraryRouteState =
  defaultLibraryListQuery;

/** Collapses the two canonical filter axes into the single visible filter. */
export function libraryFilterFor(
  state: Pick<LibraryRouteState, "publication" | "availability">,
): LibraryStateFilter {
  if (state.publication === "published") return "published";
  if (state.availability === "available") return "available";
  if (state.availability === "missing") return "missing";
  return "all";
}

/** Applies the single visible filter to both canonical filter axes. */
export function withLibraryFilter(
  state: LibraryRouteState,
  filter: LibraryStateFilter,
): LibraryRouteState {
  return {
    ...state,
    publication: filter === "published" ? "published" : "all",
    availability:
      filter === "available"
        ? "available"
        : filter === "missing"
          ? "missing"
          : "all",
  };
}

/** Reads the canonical paged library state; other routes use defaults. */
export function parseLibraryRoute(route: string): LibraryRouteState {
  return parseLibraryListRoute(route) ?? defaultLibraryRouteState;
}

/** Produces the canonical `#/library` hash route. */
export function buildLibraryRouteHref(state: LibraryRouteState): string {
  return libraryListRouteHref(state);
}

/** Canonical root-list destination for links from detail and preview screens. */
export function libraryRootHref(): string {
  return "#/library";
}
