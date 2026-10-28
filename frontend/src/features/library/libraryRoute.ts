export type LibraryView = "artists" | "albums" | "tracks";
export type PublicationFilter = "all" | "published" | "unpublished";

export interface LibraryRoute {
  view: LibraryView;
  query: string;
  publication: PublicationFilter;
  offset: number;
}

export interface LibraryArtistRoute extends LibraryRoute {
  artistId: string;
}

export interface LibraryReleaseRoute {
  releaseId: string;
}

export interface LibraryPositionRoute extends LibraryReleaseRoute {
  positionId: string;
}

const views = new Set<LibraryView>(["artists", "albums", "tracks"]);
const publications = new Set<PublicationFilter>([
  "all",
  "published",
  "unpublished",
]);
const uuidPattern =
  "[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}";

export function parseLibraryReleaseRoute(
  hash: string,
): LibraryReleaseRoute | undefined {
  const path = hash.startsWith("#") ? hash.slice(1) : hash;
  const [pathname] = path.split("?", 2);
  const match = new RegExp(`^/library/releases/(${uuidPattern})$`, "i").exec(
    pathname,
  );
  return match ? { releaseId: match[1] } : undefined;
}

export function parseLibraryPositionRoute(
  hash: string,
): LibraryPositionRoute | undefined {
  const path = hash.startsWith("#") ? hash.slice(1) : hash;
  const [pathname] = path.split("?", 2);
  const match = new RegExp(
    `^/library/releases/(${uuidPattern})/positions/(${uuidPattern})$`,
    "i",
  ).exec(pathname);
  return match ? { releaseId: match[1], positionId: match[2] } : undefined;
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
  const requestedOffset = Number(params.get("offset") ?? 0);
  return {
    view: requestedView && views.has(requestedView) ? requestedView : "artists",
    query: params.get("q") ?? "",
    publication:
      requestedPublication && publications.has(requestedPublication)
        ? requestedPublication
        : "all",
    offset:
      Number.isSafeInteger(requestedOffset) && requestedOffset > 0
        ? requestedOffset
        : 0,
  };
}

export function parseLibraryArtistRoute(
  hash: string,
): LibraryArtistRoute | undefined {
  const path = hash.startsWith("#") ? hash.slice(1) : hash;
  const [pathname, search = ""] = path.split("?", 2);
  const match = /^\/library\/artists\/([^/]+)$/.exec(pathname);
  if (!match) return undefined;
  try {
    const artistId = decodeURIComponent(match[1]);
    const params = new URLSearchParams(search);
    const view = params.get("view") as LibraryView | null;
    const publication = params.get("publication") as PublicationFilter | null;
    const offset = Number(params.get("offset") ?? 0);
    return {
      artistId,
      view: view && views.has(view) ? view : "artists",
      query: params.get("q") ?? "",
      publication:
        publication && publications.has(publication) ? publication : "all",
      offset: Number.isSafeInteger(offset) && offset > 0 ? offset : 0,
    };
  } catch {
    return undefined;
  }
}

export function libraryRouteHref(route: LibraryRoute) {
  const params = new URLSearchParams();
  params.set("view", route.view);
  if (route.query) params.set("q", route.query);
  params.set("publication", route.publication);
  if (route.offset > 0) params.set("offset", String(route.offset));
  return `#/library?${params.toString()}`;
}

export function libraryArtistRouteHref(artistId: string, route: LibraryRoute) {
  const params = new URLSearchParams();
  params.set("view", route.view);
  if (route.query) params.set("q", route.query);
  params.set("publication", route.publication);
  if (route.offset > 0) params.set("offset", String(route.offset));
  return `#/library/artists/${encodeURIComponent(artistId)}?${params.toString()}`;
}
