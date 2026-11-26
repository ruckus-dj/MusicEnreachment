/**
 * Portable library root-list hash query. The codec owns the route shape, query
 * defaults and URI encoding only.
 */

import type {
  LibraryArtistQueryState,
  LibraryAvailabilityFilter,
  LibraryPublicationFilter,
} from "./libraryDetailRoute";
import type { LibraryView } from "./libraryView";

export type LibraryListQuery = LibraryArtistQueryState;

export const defaultLibraryListQuery: LibraryListQuery = {
  view: "artists",
  query: "",
  publication: "all",
  availability: "all",
  offset: 0,
};

const views = new Set<LibraryView>(["artists", "albums", "tracks"]);
const publications = new Set<LibraryPublicationFilter>([
  "all",
  "published",
  "unpublished",
]);
const availabilities = new Set<LibraryAvailabilityFilter>([
  "all",
  "available",
  "missing",
]);

/**
 * Reads `#/library` using the product query names and defaults. Query-less and
 * malformed values fall back to the defaults; other routes return `undefined`.
 */
export function parseLibraryListRoute(
  route: string,
): LibraryListQuery | undefined {
  const path = route.startsWith("#") ? route.slice(1) : route;
  const [pathname, search = ""] = path.split("?", 2);
  if (pathname !== "/library") return undefined;
  const params = new URLSearchParams(search);
  const requestedView = params.get("view") as LibraryView | null;
  const requestedPublication = params.get(
    "publication",
  ) as LibraryPublicationFilter | null;
  const requestedAvailability = params.get(
    "availability",
  ) as LibraryAvailabilityFilter | null;
  const requestedOffset = Number(params.get("offset") ?? 0);
  return {
    view:
      requestedView && views.has(requestedView)
        ? requestedView
        : defaultLibraryListQuery.view,
    query: params.get("q") ?? "",
    publication:
      requestedPublication && publications.has(requestedPublication)
        ? requestedPublication
        : defaultLibraryListQuery.publication,
    availability:
      requestedAvailability && availabilities.has(requestedAvailability)
        ? requestedAvailability
        : defaultLibraryListQuery.availability,
    offset:
      Number.isSafeInteger(requestedOffset) && requestedOffset > 0
        ? requestedOffset
        : defaultLibraryListQuery.offset,
  };
}

/** Produces the canonical `#/library` hash route. */
export function libraryListRouteHref(state: LibraryListQuery): string {
  const params = new URLSearchParams();
  params.set("view", state.view);
  if (state.query) params.set("q", state.query);
  params.set("publication", state.publication);
  params.set("availability", state.availability);
  if (state.offset > 0) params.set("offset", String(state.offset));
  return `#/library?${params.toString()}`;
}
