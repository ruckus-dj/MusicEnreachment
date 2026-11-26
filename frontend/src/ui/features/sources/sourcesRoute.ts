/**
 * Portable source hash addresses. The codec owns the route shapes and URI
 * encoding only; identifier policy belongs to the consuming host.
 */

export type SourcesRoute =
  | { view: "list" }
  | { view: "detail"; id: string }
  | { view: "location"; id: string; locationId: string };

export const sourcesListRoute: SourcesRoute = { view: "list" };

export const sourcesHref = "#/sources";

export const sourceDetailHref = (id: string) =>
  `${sourcesHref}/${encodeURIComponent(id)}`;

export const sourceLocationHref = (id: string, locationId: string) =>
  `${sourceDetailHref(id)}/locations/${encodeURIComponent(locationId)}`;

export function parseSourcesRoute(hash: string): SourcesRoute | undefined {
  const path = hash.startsWith("#") ? hash.slice(1) : hash;
  if (path === "/sources") return sourcesListRoute;
  const match = /^\/sources\/([^/]+)(?:\/locations\/([^/]+))?$/.exec(path);
  if (!match) return undefined;
  try {
    const id = decodeURIComponent(match[1]);
    return match[2]
      ? { view: "location", id, locationId: decodeURIComponent(match[2]) }
      : { view: "detail", id };
  } catch {
    return undefined;
  }
}
