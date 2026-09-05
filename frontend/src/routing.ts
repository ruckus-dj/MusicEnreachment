import type { ManualActionFilter, Route } from "./types";

function decodeRoutePart(value: string | undefined): string {
  return value ? decodeURIComponent(value) : "";
}

function decodeRouteIdentifier(value: string | undefined): string {
  return decodeRoutePart(value).trim();
}

function publicationFilter(search: string): "all" | "published" | "unpublished" {
  const value = new URLSearchParams(search).get("published");
  return value === "true" ? "published" : value === "false" ? "unpublished" : "all";
}

function withPublicationFilter(path: string, filter: Route["publicationFilter"]): string {
  if (!filter || filter === "all") return path;
  return `${path}${path.includes("?") ? "&" : "?"}published=${filter === "published"}`;
}

function manualActionFilter(search: string): ManualActionFilter {
  return new URLSearchParams(search).get("action") === "needs-review"
    ? "needs-review"
    : "analysis-error";
}

export function parseRoute(pathname: string, search = ""): Route {
  const parts = pathname.split("/").filter(Boolean);
  const params = new URLSearchParams(search);
  const filter = publicationFilter(search);
  const sourceId = decodeRouteIdentifier(params.get("source_id") ?? undefined);
  if (parts[0] === "settings") return { screen: "settings" };
  if (parts[0] === "manual-actions") {
    return { screen: "manual-actions", manualActionFilter: manualActionFilter(search) };
  }
  if (parts[0] === "workers") return { screen: "workers" };
  if (parts[0] !== "library") return { screen: "artists", publicationFilter: filter };
  if (parts[1] === "albums")
    return { screen: "albums", artist: params.get("artist_name") ?? "", publicationFilter: filter };
  if (parts[1] === "tracks")
    return {
      screen: "tracks",
      artist: params.get("artist_name") ?? "",
      album: params.has("album_id")
        ? `id:${decodeRouteIdentifier(params.get("album_id") ?? undefined)}`
        : `album:${decodeRouteIdentifier(params.get("album_name") ?? undefined)}`,
      publicationFilter: filter,
    };
  if (parts[1] === "track" && parts[2])
    return {
      screen: "track",
      recordId: decodeRouteIdentifier(parts[2]),
      sourceId: sourceId || undefined,
      publicationFilter: filter,
    };
  if (parts.length === 1) return { screen: "artists", publicationFilter: filter };
  if (parts[1] === "artist" && parts[3] === "album" && parts[5] === "track" && parts[7])
    return {
      screen: "track",
      artist: decodeRoutePart(parts[2]),
      album: decodeRoutePart(parts[4]),
      recordId: decodeRouteIdentifier(parts[6]),
      sourceId: decodeRouteIdentifier(parts[7]),
      publicationFilter: filter,
    };
  if (parts[1] === "artist" && parts[3] === "album" && parts[5] === "tracks") {
    const album = decodeRoutePart(parts[4]);
    return {
      screen: "tracks",
      artist: decodeRoutePart(parts[2]),
      album: album.includes(" ") ? `album:${album.trim()}` : album,
      publicationFilter: filter,
    };
  }
  if (parts[1] === "artist" && parts[3] === "album")
    return {
      screen: "albums",
      artist: decodeRoutePart(parts[2]),
      album: decodeRoutePart(parts[4]),
      publicationFilter: filter,
    };
  if (parts[1] === "artist")
    return { screen: "albums", artist: decodeRoutePart(parts[2]), publicationFilter: filter };
  if (parts[1] === "record" && parts[3] === "source")
    return {
      screen: "track",
      recordId: decodeRouteIdentifier(parts[2]),
      sourceId: decodeRouteIdentifier(parts[4]),
      publicationFilter: filter,
    };
  return { screen: "artists" };
}

export function routePath(route: Route): string {
  if (route.screen === "settings") return "/settings";
  if (route.screen === "manual-actions")
    return `/manual-actions?action=${route.manualActionFilter ?? "analysis-error"}`;
  if (route.screen === "workers") return "/workers";
  if (route.screen === "track" && route.recordId) {
    const source = route.sourceId ? `?source_id=${encodeURIComponent(route.sourceId)}` : "";
    return withPublicationFilter(
      `/library/track/${encodeURIComponent(route.recordId)}${source}`,
      route.publicationFilter,
    );
  }
  if (route.screen === "tracks" && route.artist && route.album) {
    const albumQuery = route.album.startsWith("album:")
      ? `album_name=${encodeURIComponent(route.album.slice("album:".length))}`
      : `album_id=${encodeURIComponent(route.album.replace(/^id:/, ""))}`;
    return withPublicationFilter(
      `/library/tracks?artist_name=${encodeURIComponent(route.artist)}&${albumQuery}`,
      route.publicationFilter,
    );
  }
  if (route.screen === "albums" && route.artist)
    return withPublicationFilter(
      `/library/albums?artist_name=${encodeURIComponent(route.artist)}`,
      route.publicationFilter,
    );
  return withPublicationFilter("/library", route.publicationFilter);
}
