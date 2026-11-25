export type SourcesRoute =
  | { view: "list" }
  | { view: "detail"; id: string }
  | { view: "location"; id: string; locationId: string };

export const sourcesListRoute: SourcesRoute = { view: "list" };

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
