import { useQuery } from "@tanstack/react-query";
import {
  type ComponentProps,
  type ReactNode,
  useCallback,
  useEffect,
  useState,
} from "react";
import packageMetadata from "../../../package.json";
import {
  getGetIncomingGroupsQueryKey,
  getIncomingGroupCount,
  getIncomingGroups,
  getSetupState,
  useListSources,
} from "../../api/generated/client";
import type { SetupStateBody } from "../../api/generated/client.schemas";
import { IncomingGroupReviewScreen } from "../../providers/incoming/IncomingGroupReviewScreen";
import { incomingGroupCountQueryKey } from "../../providers/incoming/incomingGroupsQueryKeys";
import {
  type IncomingGroupsRoute,
  parseIncomingGroupsRoute,
} from "../../providers/incoming/incomingGroupsRoute";
import { ProductionLibraryArtistBinder } from "../../providers/library/LibraryArtistScreen";
import { LibraryReleaseScreen } from "../../providers/library/LibraryReleaseScreen";
import { ProductionLibraryCatalogBinder } from "../../providers/library/LibraryScreen";
import {
  parseLibraryArtistRoute,
  parseLibraryPositionRoute,
  parseLibraryReleaseRoute,
  parseLibraryRoute,
} from "../../providers/library/libraryRoute";
import { ProductionOwnCatalogBinder } from "../../providers/local-catalog/OwnCatalogScreen";
import { ProductionOperationsBinder } from "../../providers/operations/OperationsScreen";
import { ProductionSettingsBinder } from "../../providers/settings/SettingsScreen";
import { SetupManager } from "../../providers/setup/SetupManager";
import { SourcesScreen } from "../../providers/sources/SourcesScreen";
import { parseSourcesRoute } from "../../providers/sources/sourcesRoute";
import { ApplicationRouteOutlet } from "../../ui/app/ApplicationRouteOutlet";
import {
  ShellStartupContent,
  type ShellStartupFacts,
} from "../../ui/shell/ShellStartupContent";
import {
  ApplicationFrame,
  type ShellHomeSourceRead,
  ShellHomeView,
} from "../../ui/shell/ShellStartupView";
import {
  type ShellFrameData,
  type ShellRouteFamily,
  shellApplicationFrameState,
  shellSectionForFamily,
} from "../../ui/shell/shellRoute";
import { useI18n } from "../i18n/host";
import { useHashLocation } from "../navigation/hashLocation";
import { themePreferenceStore } from "../theme/themePreferenceHost";

type ShellError = { message: string } | { fallback: true };

type Route =
  | "/"
  | "/settings"
  | "/setup"
  | "/sources"
  | "/incoming-groups"
  | `/incoming-groups/${string}/matching`
  | "/operations"
  | "/library"
  | "/own-catalog"
  | `/library/artists/${string}`
  | `/library/releases/${string}`
  | `/library/releases/${string}/positions/${string}`;

function familyForRoute(route: Route): ShellRouteFamily {
  switch (route) {
    case "/":
      return "home";
    case "/setup":
      return "setup";
    case "/settings":
      return "settings";
    case "/sources":
      return "sources";
    case "/operations":
      return "operations";
    case "/own-catalog":
      return "local";
    default:
      return route.startsWith("/incoming-groups") ? "review" : "library";
  }
}

const applicationShellFrame: ShellFrameData = {
  brandHref: "#/",
  sectionLinks: {
    review: "#/incoming-groups",
    library: "#/library",
    local: "#/own-catalog",
    sources: "#/sources",
    operations: "#/operations",
    settings: "#/settings",
  },
  footer: { kind: "version", version: packageMetadata.version },
};

function currentRoute(
  hash: string,
  isSourcesRoute: boolean,
  incomingGroupsRoute: IncomingGroupsRoute | undefined,
): Route {
  const pathname = (hash.startsWith("#") ? hash.slice(1) : hash).split(
    "?",
    1,
  )[0];
  if (pathname === "/settings") return "/settings";
  if (pathname === "/setup") return "/setup";
  if (isSourcesRoute) return "/sources";
  if (incomingGroupsRoute)
    return incomingGroupsRoute.view === "list"
      ? "/incoming-groups"
      : `/incoming-groups/${incomingGroupsRoute.groupId}/matching`;
  if (pathname === "/operations") return "/operations";
  if (pathname === "/publications") return "/library";
  if (pathname === "/own-catalog") return "/own-catalog";
  if (parseLibraryRoute(hash)) return "/library";
  const libraryArtist = parseLibraryArtistRoute(hash);
  if (libraryArtist) return `/library/artists/${libraryArtist.artistId}`;
  const libraryPosition = parseLibraryPositionRoute(hash);
  if (libraryPosition)
    return `/library/releases/${libraryPosition.releaseId}/positions/${libraryPosition.positionId}`;
  const libraryRelease = parseLibraryReleaseRoute(hash);
  if (libraryRelease) return `/library/releases/${libraryRelease.releaseId}`;
  return "/";
}
const emptyProviderProps = {};

export function AppShell() {
  const { locale } = useI18n();
  const hash = useHashLocation();
  const sourcesRoute = parseSourcesRoute(hash);
  const incomingGroupsRoute = parseIncomingGroupsRoute(hash);
  const route = currentRoute(
    hash,
    sourcesRoute !== undefined,
    incomingGroupsRoute,
  );
  const family = familyForRoute(route);
  const [state, setState] = useState<SetupStateBody>();
  const [error, setError] = useState<ShellError | null>(null);
  const [loading, setLoading] = useState(true);
  const productAvailable =
    state?.completed && !state.platform.diagnostic && state.platform.supported;
  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const response = await getSetupState({ cache: "no-store" });
      if (response.status !== 200) {
        setError(
          response.data.detail
            ? { message: response.data.detail }
            : { fallback: true },
        );
        return;
      }
      setState(response.data);
    } catch (reason) {
      setError(
        reason instanceof Error && reason.message
          ? { message: reason.message }
          : { fallback: true },
      );
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => {
    if (!state || state.platform.diagnostic || !state.platform.supported)
      return;
    if (!state.completed && route !== "/setup") window.location.hash = "/setup";
    if (state.completed && route === "/setup") window.location.hash = "/";
  }, [state, route]);

  const content = renderContent();

  if (productAvailable)
    return (
      <ApplicationProductShell family={family}>
        {content}
      </ApplicationProductShell>
    );

  return (
    <ApplicationFrame
      locale={locale}
      themeStore={themePreferenceStore}
      state={shellApplicationFrameState(
        family,
        "startup",
        applicationShellFrame,
      )}
    >
      {content}
    </ApplicationFrame>
  );

  function renderContent() {
    const facts = startupFacts();
    return facts ? (
      <ShellStartupContent locale={locale} facts={facts} />
    ) : (
      renderProductContent()
    );
  }

  function startupFacts():
    | ShellStartupFacts<ComponentProps<typeof SetupManager>>
    | undefined {
    if (loading) return { kind: "loading-server" };
    if (error)
      return {
        kind: "setup-error",
        message: "message" in error ? error.message : undefined,
        retry: load,
      };
    if (state && (state.platform.diagnostic || !state.platform.supported))
      return {
        kind: "diagnostic",
        message: state.platform.reason,
        platform: `${state.platform.goos}/${state.platform.goarch}`,
        retry: load,
      };
    if (state && !state.completed)
      return {
        kind: "setup",
        setup: {
          component: SetupManager,
          props: {
            initialState: state,
            onCompleted: () => {
              void load();
            },
          },
        },
      };
    return undefined;
  }

  function renderProductContent() {
    return state && route === "/settings" ? (
      <ApplicationRouteOutlet
        route="settings"
        provider={ProductionSettingsBinder}
        providerProps={emptyProviderProps}
      />
    ) : state && route === "/sources" ? (
      <SourcesScreen route={sourcesRoute} />
    ) : state && incomingGroupsRoute ? (
      <IncomingGroupReviewScreen route={incomingGroupsRoute} />
    ) : state && route === "/operations" ? (
      <ApplicationRouteOutlet
        route="operations"
        provider={ProductionOperationsBinder}
        providerProps={emptyProviderProps}
      />
    ) : state && route === "/library" ? (
      <ApplicationRouteOutlet
        route="library-catalog"
        provider={ProductionLibraryCatalogBinder}
        providerProps={emptyProviderProps}
      />
    ) : state && route === "/own-catalog" ? (
      <ApplicationRouteOutlet
        route="own-catalog"
        provider={ProductionOwnCatalogBinder}
        providerProps={emptyProviderProps}
      />
    ) : state && route.startsWith("/library/artists/") ? (
      <ApplicationRouteOutlet
        route="library-artist"
        provider={ProductionLibraryArtistBinder}
        providerProps={{
          artistId: parseLibraryArtistRoute(hash)?.artistId ?? "",
        }}
      />
    ) : state && route.startsWith("/library/releases/") ? (
      <LibraryReleaseScreen
        releaseId={decodeURIComponent(route.split("/")[3] ?? "")}
        positionId={parseLibraryPositionRoute(hash)?.positionId}
      />
    ) : state ? (
      <RootLanding />
    ) : null;
  }
}

function ApplicationProductShell({
  family,
  children,
}: {
  family: ShellRouteFamily;
  children: ReactNode;
}) {
  const { locale } = useI18n();
  const activeSection = shellSectionForFamily(family);
  const incomingActive = activeSection === "review";
  // Observe the shared group-list cache without fetching: the Incoming review
  // screen owns that request, so the shell only mirrors it while an Incoming
  // route is active. The separate count query key extends the generated
  // group-list key, keeping existing prefix invalidations effective.
  const incomingGroups = useQuery({
    queryKey: getGetIncomingGroupsQueryKey(),
    enabled: false,
    // Same body shape as the review screen caches; disabled here so the shell
    // never issues a duplicate list request.
    queryFn: async ({ signal }) => {
      const response = await getIncomingGroups({ signal, cache: "no-store" });
      if (response.status !== 200)
        throw new Error("incoming groups unavailable");
      return response.data;
    },
  });
  const incomingGroupCount = useQuery({
    queryKey: incomingGroupCountQueryKey(),
    enabled: !incomingActive,
    retry: false,
    queryFn: async ({ signal }) => {
      const response = await getIncomingGroupCount({
        signal,
        cache: "no-store",
      });
      if (response.status !== 200)
        throw new Error("incoming group count unavailable");
      return response.data.count;
    },
  });
  const incomingCount = incomingActive
    ? incomingGroups.data === undefined
      ? undefined
      : (incomingGroups.data.groups?.length ?? 0)
    : incomingGroupCount.data;

  return (
    <ApplicationFrame
      locale={locale}
      themeStore={themePreferenceStore}
      state={shellApplicationFrameState(family, "product", {
        ...applicationShellFrame,
        incomingCount,
      })}
    >
      {children}
    </ApplicationFrame>
  );
}

function openIncomingFromHome() {
  window.location.hash = "/incoming-groups";
}

function RootLanding() {
  const { locale } = useI18n();
  const sourcesQuery = useListSources({
    fetch: { cache: "no-store" },
    query: { retry: false, refetchOnMount: "always" },
  });
  const response = sourcesQuery.data;
  const sources = response?.status === 200 ? response.data.sources || [] : null;
  const errorMessage =
    sourcesQuery.error instanceof Error && sourcesQuery.error.message
      ? sourcesQuery.error.message
      : response && response.status !== 200
        ? response.data.detail || null
        : null;
  const currentFetchSettled =
    sourcesQuery.isFetchedAfterMount && !sourcesQuery.isFetching;
  const sourceRead: ShellHomeSourceRead =
    sourcesQuery.isPending || !currentFetchSettled
      ? { kind: "loading" }
      : sourcesQuery.isError || sources === null
        ? {
            kind: "error",
            message: errorMessage ?? undefined,
            retry: () => void sourcesQuery.refetch(),
          }
        : { kind: "ready", count: sources.length };

  return (
    <ShellHomeView
      locale={locale}
      sources={sourceRead}
      sourcesHref="#/sources"
      onOpenIncoming={openIncomingFromHome}
    />
  );
}
