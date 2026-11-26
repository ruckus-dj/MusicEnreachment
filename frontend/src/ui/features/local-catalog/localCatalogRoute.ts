/**
 * Portable local catalog hash addresses. The codec owns the route shapes,
 * query defaults and URI encoding only; identifier policy belongs to the
 * consuming host.
 */

export const localCatalogTypes = ["releases", "recordings", "artists"] as const;

export type LocalCatalogType = (typeof localCatalogTypes)[number];

export type LocalCatalogRoute = { type: LocalCatalogType; id?: string };

export const defaultLocalCatalogRoute: LocalCatalogRoute = {
  type: "releases",
};

const catalogPath = "/own-catalog";

/** Reads the canonical `#/own-catalog` route; other paths are not this screen. */
export function parseLocalCatalogRoute(
  hash: string,
): LocalCatalogRoute | undefined {
  const path = hash.startsWith("#") ? hash.slice(1) : hash;
  const [pathname, search = ""] = path.split("?", 2);
  if (pathname !== catalogPath) return undefined;
  const params = new URLSearchParams(search);
  const requested = params.get("type");
  const type = localCatalogTypes.find((kind) => kind === requested);
  if (!type) return defaultLocalCatalogRoute;
  const id = params.get("id");
  return id ? { type, id } : { type };
}

/** Produces the canonical hash, omitting the default releases list. */
export function localCatalogRouteHref({ type, id }: LocalCatalogRoute) {
  if (type === "releases" && !id) return `#${catalogPath}`;
  const params = new URLSearchParams({ type });
  if (id) params.set("id", id);
  return `#${catalogPath}?${params}`;
}
