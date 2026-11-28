import {
  type LibraryArtistAddress,
  type LibraryArtistQueryState,
  type LibraryAvailabilityFilter,
  type LibraryPositionAddress,
  type LibraryPublicationFilter,
  type LibraryReleaseAddress,
  libraryArtistAddressHref,
  libraryReleaseHref,
  parseLibraryArtistAddress,
  parseLibraryPositionAddress,
  parseLibraryReleaseAddress,
} from "../../ui/features/library/libraryDetailRoute";
import type { LibraryView } from "../../ui/features/library/libraryView";

export type { LibraryView } from "../../ui/features/library/libraryView";
export type PublicationFilter = LibraryPublicationFilter;
export type AvailabilityFilter = LibraryAvailabilityFilter;

export type LibraryRoute = LibraryArtistQueryState;

export type LibraryArtistRoute = LibraryArtistAddress;

export type LibraryReleaseRoute = LibraryReleaseAddress;

export type LibraryPositionRoute = LibraryPositionAddress;

const views = new Set<LibraryView>(["artists", "albums", "tracks"]);
const publications = new Set<PublicationFilter>([
  "all",
  "published",
  "unpublished",
]);
const availabilities = new Set<AvailabilityFilter>([
  "all",
  "available",
  "missing",
]);
const uuidPattern =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
/** Exact host identifier policy for live release and position path segments. */
export const isUuid = (rawId: string) => uuidPattern.test(rawId);

export function parseLibraryReleaseRoute(
  hash: string,
): LibraryReleaseRoute | undefined {
  return parseLibraryReleaseAddress(hash, isUuid);
}

export function parseLibraryPositionRoute(
  hash: string,
): LibraryPositionRoute | undefined {
  return parseLibraryPositionAddress(hash, isUuid);
}

/** Whether the release overview was opened to resolve its primary artist. */
export function libraryReleaseResolvePrimaryArtist(hash: string): boolean {
  const path = hash.startsWith("#") ? hash.slice(1) : hash;
  const [, search = ""] = path.split("?", 2);
  return new URLSearchParams(search).get("resolve") === "primary-artist";
}

export function libraryReleasePrimaryArtistHref(
  releaseId: string,
  positionId: string,
) {
  const params = new URLSearchParams({
    resolve: "primary-artist",
    position: positionId,
  });
  return `${libraryReleaseHref(releaseId)}?${params}`;
}

export function libraryReleasePrimaryArtistPosition(hash: string) {
  if (!libraryReleaseResolvePrimaryArtist(hash)) return undefined;
  const positionId = new URLSearchParams(hash.split("?", 2)[1]).get("position");
  return positionId && isUuid(positionId) ? positionId : undefined;
}

export function parseLibraryRoute(hash: string): LibraryRoute | undefined {
  const path = hash.startsWith("#") ? hash.slice(1) : hash;
  const [pathname, search = ""] = path.split("?", 2);
  if (pathname !== "/library") return undefined;
  const params = new URLSearchParams(search);
  const requestedView = params.get("view") as LibraryView | null;
  const requestedPublication = params.get(
    "publication",
  ) as PublicationFilter | null;
  const requestedAvailability = params.get(
    "availability",
  ) as AvailabilityFilter | null;
  const requestedOffset = Number(params.get("offset") ?? 0);
  return {
    view: requestedView && views.has(requestedView) ? requestedView : "artists",
    query: params.get("q") ?? "",
    publication:
      requestedPublication && publications.has(requestedPublication)
        ? requestedPublication
        : "all",
    availability:
      requestedAvailability && availabilities.has(requestedAvailability)
        ? requestedAvailability
        : "all",
    offset:
      Number.isSafeInteger(requestedOffset) && requestedOffset > 0
        ? requestedOffset
        : 0,
  };
}

export const parseLibraryArtistRoute = parseLibraryArtistAddress;

export function libraryRouteHref(route: LibraryRoute) {
  const params = new URLSearchParams();
  params.set("view", route.view);
  if (route.query) params.set("q", route.query);
  params.set("publication", route.publication);
  params.set("availability", route.availability);
  if (route.offset > 0) params.set("offset", String(route.offset));
  return `#/library?${params.toString()}`;
}

export const libraryArtistRouteHref = libraryArtistAddressHref;
