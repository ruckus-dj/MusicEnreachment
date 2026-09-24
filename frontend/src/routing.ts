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
  const artistMissing = params.get("artist_missing") === "true";
  const artistName = artistMissing ? "" : (params.get("artist_name") ?? "");
  const albumMissing = params.get("album_missing") === "true";
  const sourceId = decodeRouteIdentifier(params.get("source_id") ?? undefined);
  if (parts.length === 0 || parts[0] === "dashboard") return { screen: "dashboard" };
  if (parts[0] === "settings") return { screen: "settings" };
  if (parts[0] === "manual-actions") {
    return { screen: "manual-actions", manualActionFilter: manualActionFilter(search) };
  }
  if (parts[0] === "workers") return { screen: "workers" };
  if (parts[0] !== "library") return { screen: "dashboard" };
  if (parts[1] === "albums")
    return {
      screen: "albums",
      artist: artistName,
      ...(artistMissing ? { artistMissing: true } : {}),
      ...(albumMissing ? { albumMissing: true } : {}),
      publicationFilter: filter,
    };
  if (parts[1] === "tracks")
    return {
      screen: "tracks",
      artist: artistName,
      ...(artistMissing ? { artistMissing: true } : {}),
      ...(albumMissing ? { albumMissing: true } : {}),
      ...(params.has("album_id")
        ? { album: `id:${decodeRouteIdentifier(params.get("album_id") ?? undefined)}` }
        : params.has("album_name")
          ? { album: `album:${decodeRouteIdentifier(params.get("album_name") ?? undefined)}` }
          : {}),
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
  if (route.screen === "dashboard") return "/dashboard";
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
  if (route.screen === "tracks") {
    if (!(route.artist || route.artistMissing || route.album || route.albumMissing))
      return withPublicationFilter("/library/tracks", route.publicationFilter);
    const album = route.album ?? "";
    const albumQuery = route.albumMissing
      ? "album_missing=true"
      : album.startsWith("album:")
        ? `album_name=${encodeURIComponent(album.slice("album:".length))}`
        : album
          ? `album_id=${encodeURIComponent(album.replace(/^id:/, ""))}`
          : "";
    const artistQuery = route.artistMissing
      ? "artist_missing=true"
      : route.artist
        ? `artist_name=${encodeURIComponent(route.artist)}`
        : "";
    const query = [artistQuery, albumQuery].filter(Boolean).join("&");
    return withPublicationFilter(
      `/library/tracks${query ? `?${query}` : ""}`,
      route.publicationFilter,
    );
  }
  if (route.screen === "albums")
    return withPublicationFilter(
      route.artistMissing
        ? "/library/albums?artist_missing=true"
        : route.artist
          ? `/library/albums?artist_name=${encodeURIComponent(route.artist)}`
          : "/library/albums",
      route.publicationFilter,
    );
  return withPublicationFilter("/library", route.publicationFilter);
}
