import { useQuery } from "@tanstack/react-query";
import {
  type ReactNode,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
import packageMetadata from "../../package.json";
import {
  getGetIncomingGroupsQueryKey,
  getIncomingGroupCount,
  getIncomingGroups,
  getSetupState,
  useListSources,
} from "../api/generated/client";
import type { SetupStateBody } from "../api/generated/client.schemas";
import { AppButton } from "../components/AppButton";
import { LanguageControl } from "../components/LanguageControl";
import { ThemeControl } from "../components/ThemeControl";
import { IncomingGroupReviewScreen } from "../features/incoming/IncomingGroupReviewScreen";
import {
  type IncomingGroupsRoute,
  parseIncomingGroupsRoute,
} from "../features/incoming/incomingGroupsRoute";
import { LibraryArtistScreen } from "../features/library/LibraryArtistScreen";
import { LibraryReleaseScreen } from "../features/library/LibraryReleaseScreen";
import { LibraryScreen } from "../features/library/LibraryScreen";
import {
  parseLibraryArtistRoute,
  parseLibraryPositionRoute,
  parseLibraryReleaseRoute,
  parseLibraryRoute,
} from "../features/library/libraryRoute";
import { OperationsScreen } from "../features/operations/OperationsScreen";
import { OwnCatalogScreen } from "../features/own-catalog/OwnCatalogScreen";
import { SettingsScreen } from "../features/settings/SettingsScreen";
import { SetupManager } from "../features/setup/SetupManager";
import { SourcesScreen } from "../features/sources/SourcesScreen";
import { parseSourcesRoute } from "../features/sources/sourcesRoute";
import { useI18n } from "../i18n/I18nProvider";
import { type ShellMessageKey, shellMessages } from "../i18n/shellMessages";
import { useHashLocation } from "../navigation/hashLocation";
import { type ShellNavigationItem, ShellView } from "./ShellView";

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

const shellContentClass = "min-w-0 p-2.5 [&>:first-child]:mt-0 min-[600px]:p-3";
const skipLinkClass =
  "fixed left-3 top-[-80px] z-[200] border-0 bg-[var(--surface)] p-2 focus:top-2";
const rootLandingActionClass =
  "inline-flex min-h-[34px] items-center rounded border border-[var(--button)] bg-[var(--button)] px-3.5 py-1.5 font-semibold text-[var(--button-text)] no-underline hover:opacity-90 focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-2";

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
export function AppShell() {
  const { locale } = useI18n();
  const copy = shellMessages[locale];
  const hash = useHashLocation();
  const sourcesRoute = parseSourcesRoute(hash);
  const incomingGroupsRoute = parseIncomingGroupsRoute(hash);
  const route = currentRoute(
    hash,
    sourcesRoute !== undefined,
    incomingGroupsRoute,
  );
  const [state, setState] = useState<SetupStateBody>();
  const [error, setError] = useState<ShellError | null>(null);
  const [loading, setLoading] = useState(true);
  const productAvailable =
    state?.completed && !state.platform.diagnostic && state.platform.supported;
  const errorRef = useRef<HTMLDivElement>(null);
  const diagnosticRef = useRef<HTMLHeadingElement>(null);
  const contentRef = useRef<HTMLElement>(null);
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

  useEffect(() => {
    if (!loading && error) errorRef.current?.focus();
  }, [loading, error]);
  useEffect(() => {
    if (
      !loading &&
      (state?.platform.diagnostic || state?.platform.supported === false)
    )
      diagnosticRef.current?.focus();
  }, [loading, state]);

  const content = renderRouteContent();

  if (productAvailable)
    return <ProductShell route={route}>{content}</ProductShell>;

  return (
    <main className="setup-shell">
      <button
        type="button"
        className={skipLinkClass}
        onClick={() => {
          contentRef.current?.focus();
        }}
      >
        {copy.skip}
      </button>
      <header className="m-0 flex flex-wrap items-center justify-between gap-3">
        <div className="flex min-w-0 flex-col gap-px p-1">
          <strong className="text-[15px] leading-5">MeloTrove</strong>
          <span className="text-[11px] leading-4 text-[var(--muted)]">
            {copy.library}
          </span>
        </div>
        <div className="flex items-center gap-2">
          <LanguageControl />
          <ThemeControl />
        </div>
      </header>
      <section
        ref={contentRef}
        id="app-content"
        className="min-w-0 pt-3"
        tabIndex={-1}
      >
        {content}
      </section>
    </main>
  );

  function renderRouteContent() {
    return loading ? (
      <p role="status">{copy.loadingServer}</p>
    ) : error ? (
      <div ref={errorRef} tabIndex={-1} role="alert">
        {"fallback" in error ? copy.loadError : error.message}{" "}
        <AppButton onPress={load}>{copy.retry}</AppButton>
      </div>
    ) : state && (state.platform.diagnostic || !state.platform.supported) ? (
      <section aria-labelledby="diagnostic-title">
        <h1 ref={diagnosticRef} tabIndex={-1} id="diagnostic-title">
          {copy.diagnose}
        </h1>
        <p role="alert">{state.platform.reason || copy.unsupportedPlatform}</p>
        <p>
          {state.platform.goos}/{state.platform.goarch}
        </p>
        <AppButton onPress={load}>{copy.refreshDiagnostics}</AppButton>
      </section>
    ) : state && !state.completed ? (
      <SetupManager
        initialState={state}
        onCompleted={() => {
          void load();
        }}
      />
    ) : state && route === "/settings" ? (
      <SettingsScreen />
    ) : state && route === "/sources" ? (
      <SourcesScreen route={sourcesRoute} />
    ) : state && incomingGroupsRoute ? (
      <IncomingGroupReviewScreen route={incomingGroupsRoute} />
    ) : state && route === "/operations" ? (
      <OperationsScreen />
    ) : state && route === "/library" ? (
      <LibraryScreen />
    ) : state && route === "/own-catalog" ? (
      <OwnCatalogScreen />
    ) : state && route.startsWith("/library/artists/") ? (
      <LibraryArtistScreen
        artistId={parseLibraryArtistRoute(hash)?.artistId ?? ""}
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

function ProductShell({
  route,
  children,
}: {
  route: Route;
  children: ReactNode;
}) {
  const { locale } = useI18n();
  const copy = shellMessages[locale];
  const incomingActive =
    route === "/incoming-groups" || route.startsWith("/incoming-groups/");
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
    queryKey: [...getGetIncomingGroupsQueryKey(), "count"],
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
  const navigation: ShellNavigationItem[] = [
    {
      id: "incoming",
      label: copy.incoming,
      href: "#/incoming-groups",
      current:
        route === "/incoming-groups" || route.startsWith("/incoming-groups/"),
      count: incomingCount,
    },
    {
      id: "library",
      label: copy.library,
      href: "#/library",
      current: route === "/library" || route.startsWith("/library/"),
    },
    {
      id: "own-catalog",
      label: copy.ownCatalog,
      href: "#/own-catalog",
      current: route === "/own-catalog",
    },
    {
      id: "sources",
      label: copy.sources,
      href: "#/sources",
      current: route === "/sources",
    },
    {
      id: "operations",
      label: copy.operations,
      href: "#/operations",
      current: route === "/operations",
    },
    {
      id: "settings",
      label: copy.settings,
      href: "#/settings",
      current: route === "/settings",
    },
  ];

  return (
    <div className="font-[system-ui,sans-serif] text-[13px]/[1.4]">
      <ShellView
        brand={{ href: "#/", label: "MeloTrove", mark: "mt" }}
        skipLabel={copy.skip}
        navigationLabel={copy.sections}
        sectionLabel={sectionLabel(route, copy)}
        navigation={navigation}
        controls={{
          language: <LanguageControl />,
          theme: <ThemeControl />,
        }}
        footerText={`v${packageMetadata.version}`}
      >
        <div className={shellContentClass}>{children}</div>
      </ShellView>
    </div>
  );
}

function RootLanding() {
  const { locale } = useI18n();
  const copy = shellMessages[locale];
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
  const sourceCount = sources?.length ?? 0;
  const currentFetchSettled =
    sourcesQuery.isFetchedAfterMount && !sourcesQuery.isFetching;

  useEffect(() => {
    if (currentFetchSettled && !sourcesQuery.isError && sourceCount > 0)
      window.location.hash = "/incoming-groups";
  }, [currentFetchSettled, sourceCount, sourcesQuery.isError]);

  if (sourcesQuery.isPending || !currentFetchSettled)
    return <p role="status">{copy.loadingSources}</p>;

  if (sourcesQuery.isError || sources === null)
    return (
      <section aria-labelledby="home-title">
        <h1 id="home-title">{copy.homeTitle}</h1>
        <p role="alert">{errorMessage ?? copy.sourcesLoadError}</p>
        <AppButton onPress={() => void sourcesQuery.refetch()}>
          {copy.retry}
        </AppButton>
      </section>
    );

  if (sources.length > 0) return <p role="status">{copy.loadingIncoming}</p>;

  return (
    <section aria-labelledby="home-title">
      <h1 id="home-title">{copy.addSourceTitle}</h1>
      <p>{copy.addSourceDescription}</p>
      <a className={rootLandingActionClass} href="#/sources">
        {copy.addSource}
      </a>
    </section>
  );
}

function sectionLabel(
  route: Route,
  copy: Record<ShellMessageKey, string>,
): string {
  if (route === "/incoming-groups" || route.startsWith("/incoming-groups/"))
    return copy.incoming;
  if (route === "/library" || route.startsWith("/library/"))
    return copy.library;
  if (route === "/own-catalog") return copy.ownCatalog;
  if (route === "/sources") return copy.sources;
  if (route === "/operations") return copy.operations;
  if (route === "/settings") return copy.settings;
  return copy.home;
}
