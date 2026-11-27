import {
  type IncomingGroupsRoute,
  incomingGroupsListHref,
  parseIncomingGroupsRoute,
} from "../features/incoming/incomingGroupsRoute";
import {
  type LibraryArtistAddress,
  type LibraryIdValidator,
  type LibraryPositionAddress,
  type LibraryReleaseAddress,
  parseLibraryArtistAddress,
  parseLibraryPositionAddress,
  parseLibraryReleaseAddress,
} from "../features/library/libraryDetailRoute";
import {
  parseSourcesRoute,
  type SourcesRoute,
  sourcesHref,
} from "../features/sources/sourcesRoute";
import type { ShellRouteFamily } from "../shell/shellRoute";

export const meloTrovePaths = {
  home: "#/",
  setup: "#/setup",
  incoming: incomingGroupsListHref(),
  library: "#/library",
  ownCatalog: "#/own-catalog",
  sources: sourcesHref,
  operations: "#/operations",
  settings: "#/settings",
} as const;

type Destination =
  | { kind: "home" }
  | { kind: "setup" }
  | { kind: "incoming"; route: IncomingGroupsRoute }
  | { kind: "sources"; route: SourcesRoute }
  | {
      kind: "library-release";
      address: LibraryReleaseAddress | LibraryPositionAddress;
    }
  | { kind: "library-artist"; address: LibraryArtistAddress }
  | { kind: "library-catalog" }
  | { kind: "own-catalog" }
  | { kind: "operations" }
  | { kind: "settings" };

export type MeloTroveRoute = Destination & {
  hash: string;
  pathname: string;
  family: ShellRouteFamily;
};

const routeFamilies = {
  home: "home",
  setup: "setup",
  incoming: "review",
  sources: "sources",
  "library-release": "library",
  "library-artist": "library",
  "library-catalog": "library",
  "own-catalog": "local",
  operations: "operations",
  settings: "settings",
} as const satisfies Record<Destination["kind"], ShellRouteFamily>;

export function parseMeloTroveRoute(
  location: string,
  validateLibraryId?: LibraryIdValidator,
): MeloTroveRoute {
  const path = location.startsWith("#") ? location.slice(1) : location;
  const [originalPath, search] = path.split("?", 2);
  const pathname = originalPath || "/";
  const hash = `#${pathname}${search === undefined ? "" : `?${search}`}`;
  const destination = parseDestination(hash, pathname, validateLibraryId);
  return {
    ...destination,
    hash,
    pathname,
    family: routeFamilies[destination.kind],
  };
}

function parseDestination(
  hash: string,
  pathname: string,
  validateLibraryId?: LibraryIdValidator,
): Destination {
  switch (`#${pathname}`) {
    case meloTrovePaths.setup:
      return { kind: "setup" };
    case meloTrovePaths.library:
      return { kind: "library-catalog" };
    case meloTrovePaths.ownCatalog:
      return { kind: "own-catalog" };
    case meloTrovePaths.operations:
      return { kind: "operations" };
    case meloTrovePaths.settings:
      return { kind: "settings" };
  }
  const sources = parseSourcesRoute(pathname);
  if (sources) return { kind: "sources", route: sources };
  const incoming = parseIncomingGroupsRoute(hash);
  if (incoming) return { kind: "incoming", route: incoming };
  const artist = parseLibraryArtistAddress(hash);
  if (artist) return { kind: "library-artist", address: artist };
  const release =
    parseLibraryPositionAddress(hash, validateLibraryId) ??
    parseLibraryReleaseAddress(hash, validateLibraryId);
  return release
    ? { kind: "library-release", address: release }
    : { kind: "home" };
}
