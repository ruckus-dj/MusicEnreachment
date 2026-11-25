/**
 * Portable artist, release (edition) and position hash addresses. The codec
 * owns the route shapes, query defaults and URI encoding only; identifier
 * policy, including UUID validation for live identifiers, belongs to the host
 * route adapter.
 */

import type { LibraryView } from "./libraryView";

export type LibraryPublicationFilter = "all" | "published" | "unpublished";
export type LibraryAvailabilityFilter = "all" | "available" | "missing";

export interface LibraryArtistQueryState {
  view: LibraryView;
  query: string;
  publication: LibraryPublicationFilter;
  availability: LibraryAvailabilityFilter;
  offset: number;
}

export interface LibraryArtistAddress extends LibraryArtistQueryState {
  artistId: string;
}

export interface LibraryReleaseAddress {
  releaseId: string;
}

export interface LibraryPositionAddress extends LibraryReleaseAddress {
  positionId: string;
}

/** Host-supplied check for a raw, still-encoded path segment. */
export type LibraryIdValidator = (rawId: string) => boolean;

const releasePrefix = "/library/releases/";
const releasePattern = /^\/library\/releases\/([^/]+)$/;
const positionPattern = /^\/library\/releases\/([^/]+)\/positions\/([^/]+)$/;

function pathnameOf(route: string) {
  const path = route.startsWith("#") ? route.slice(1) : route;
  const queryIndex = path.indexOf("?");
  return queryIndex === -1 ? path : path.slice(0, queryIndex);
}

function decodeId(value: string) {
  try {
    return decodeURIComponent(value) || undefined;
  } catch {
    return undefined;
  }
}

const artistViews = new Set<LibraryView>(["artists", "albums", "tracks"]);
const artistPublications = new Set<LibraryPublicationFilter>([
  "all",
  "published",
  "unpublished",
]);
const artistAvailabilities = new Set<LibraryAvailabilityFilter>([
  "all",
  "available",
  "missing",
]);
const artistPattern = /^\/library\/artists\/([^/]+)$/;

/** Parses `#/library/artists/<artist>` with the live query defaults. */
export function parseLibraryArtistAddress(
  route: string,
): LibraryArtistAddress | undefined {
  const path = route.startsWith("#") ? route.slice(1) : route;
  const [pathname, search = ""] = path.split("?", 2);
  const match = artistPattern.exec(pathname);
  if (!match) return undefined;
  try {
    const artistId = decodeURIComponent(match[1]);
    const params = new URLSearchParams(search);
    const view = params.get("view") as LibraryView | null;
    const publication = params.get(
      "publication",
    ) as LibraryPublicationFilter | null;
    const availability = params.get(
      "availability",
    ) as LibraryAvailabilityFilter | null;
    const offset = Number(params.get("offset") ?? 0);
    return {
      artistId,
      view: view && artistViews.has(view) ? view : "artists",
      query: params.get("q") ?? "",
      publication:
        publication && artistPublications.has(publication)
          ? publication
          : "all",
      availability:
        availability && artistAvailabilities.has(availability)
          ? availability
          : "all",
      offset: Number.isSafeInteger(offset) && offset > 0 ? offset : 0,
    };
  } catch {
    return undefined;
  }
}

/**
 * Builds the artist address. Without state the href is the bare canonical
 * path; a supplied query state appends the live filter/search shape.
 */
export function libraryArtistAddressHref(
  artistId: string,
  state?: LibraryArtistQueryState,
) {
  const base = `#/library/artists/${encodeURIComponent(artistId)}`;
  if (!state) return base;
  const params = new URLSearchParams();
  params.set("view", state.view);
  if (state.query) params.set("q", state.query);
  params.set("publication", state.publication);
  params.set("availability", state.availability);
  if (state.offset > 0) params.set("offset", String(state.offset));
  return `${base}?${params.toString()}`;
}

/**
 * Parses the exact edition address `#/library/releases/<release>`. The optional
 * validator sees the raw segment, before URI decoding, so a host can require an
 * exact identifier form instead of accepting its encoded equivalent.
 */
export function parseLibraryReleaseAddress(
  route: string,
  validateRawId?: LibraryIdValidator,
): LibraryReleaseAddress | undefined {
  const match = releasePattern.exec(pathnameOf(route));
  if (!match) return undefined;
  const [rawReleaseId] = match.slice(1);
  if (validateRawId && !validateRawId(rawReleaseId)) return undefined;
  const releaseId = decodeId(rawReleaseId);
  return releaseId ? { releaseId } : undefined;
}

/**
 * Parses the exact `#/library/releases/<release>/positions/<position>` tuple.
 * The optional validator sees both raw segments before URI decoding.
 */
export function parseLibraryPositionAddress(
  route: string,
  validateRawId?: LibraryIdValidator,
): LibraryPositionAddress | undefined {
  const match = positionPattern.exec(pathnameOf(route));
  if (!match) return undefined;
  const [rawReleaseId, rawPositionId] = match.slice(1);
  if (
    validateRawId &&
    (!validateRawId(rawReleaseId) || !validateRawId(rawPositionId))
  )
    return undefined;
  const releaseId = decodeId(rawReleaseId);
  const positionId = decodeId(rawPositionId);
  return releaseId && positionId ? { releaseId, positionId } : undefined;
}

/** Builds the canonical edition address for a release identifier. */
export function libraryReleaseHref(releaseId: string) {
  return `#${releasePrefix}${encodeURIComponent(releaseId)}`;
}

/** Builds the canonical nested address for a release position. */
export function libraryPositionHref(releaseId: string, positionId: string) {
  return `${libraryReleaseHref(releaseId)}/positions/${encodeURIComponent(positionId)}`;
}
