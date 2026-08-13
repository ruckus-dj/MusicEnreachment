import type { Route } from "./types";

function decodeRoutePart(value: string | undefined): string {
  return value ? decodeURIComponent(value) : "";
}

export function parseRoute(pathname: string): Route {
  const parts = pathname.split("/").filter(Boolean);
  if (parts[0] === "settings") return { screen: "settings" };
  if (parts[0] === "manual-actions") return { screen: "manual-actions" };
  if (parts[0] !== "library") return { screen: "artists" };
  if (parts[1] === "artist" && parts[3] === "album" && parts[5] === "track" && parts[7])
    return {
      screen: "track",
      artist: decodeRoutePart(parts[2]),
      album: decodeRoutePart(parts[4]),
      recordId: decodeRoutePart(parts[6]),
      sourceId: decodeRoutePart(parts[7]),
    };
  if (parts[1] === "artist" && parts[3] === "album" && parts[5] === "tracks")
    return {
      screen: "tracks",
      artist: decodeRoutePart(parts[2]),
      album: decodeRoutePart(parts[4]),
    };
  if (parts[1] === "artist" && parts[3] === "album")
    return {
      screen: "albums",
      artist: decodeRoutePart(parts[2]),
      album: decodeRoutePart(parts[4]),
    };
  if (parts[1] === "artist") return { screen: "albums", artist: decodeRoutePart(parts[2]) };
  if (parts[1] === "record" && parts[3] === "source")
    return {
      screen: "track",
      recordId: decodeRoutePart(parts[2]),
      sourceId: decodeRoutePart(parts[4]),
    };
  return { screen: "artists" };
}

export function routePath(route: Route): string {
  if (route.screen === "settings") return "/settings";
  if (route.screen === "manual-actions") return "/manual-actions";
  if (route.screen === "track" && route.recordId && route.sourceId && route.artist && route.album)
    return `/library/artist/${encodeURIComponent(route.artist)}/album/${encodeURIComponent(route.album)}/track/${encodeURIComponent(route.recordId)}/${encodeURIComponent(route.sourceId)}`;
  if (route.screen === "track" && route.recordId && route.sourceId)
    return `/library/record/${encodeURIComponent(route.recordId)}/source/${encodeURIComponent(route.sourceId)}`;
  if (route.screen === "tracks" && route.artist && route.album)
    return `/library/artist/${encodeURIComponent(route.artist)}/album/${encodeURIComponent(route.album)}/tracks`;
  if (route.screen === "albums" && route.artist && route.album)
    return `/library/artist/${encodeURIComponent(route.artist)}/album/${encodeURIComponent(route.album)}`;
  if (route.screen === "albums" && route.artist)
    return `/library/artist/${encodeURIComponent(route.artist)}`;
  return "/";
}
