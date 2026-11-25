import { type OwnCatalogType, ownCatalogTypes } from "./ownCatalogModel";

type OwnCatalogRoute = { type: OwnCatalogType; id?: string };

export const defaultOwnCatalogRoute: OwnCatalogRoute = { type: "releases" };
const catalogPath = "/own-catalog";

export function parseOwnCatalogRoute(
  hash: string,
): OwnCatalogRoute | undefined {
  const path = hash.startsWith("#") ? hash.slice(1) : hash;
  const [pathname, search] = path.split("?", 2);
  if (pathname !== catalogPath) return undefined;
  const params = new URLSearchParams(search);
  const requested = params.get("type");
  const type = ownCatalogTypes.find((kind) => kind === requested);
  if (!type) return defaultOwnCatalogRoute;
  const id = params.get("id");
  return id ? { type, id } : { type };
}

export function ownCatalogRouteHref({ type, id }: OwnCatalogRoute) {
  if (type === "releases" && !id) return `#${catalogPath}`;
  const params = new URLSearchParams({ type });
  if (id) params.set("id", id);
  return `#${catalogPath}?${params}`;
}
