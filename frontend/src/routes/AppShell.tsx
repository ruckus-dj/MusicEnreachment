import { useCallback, useEffect, useRef, useState } from "react";
import { getSetupState, useListSources } from "../api/generated/client";
import type { SetupStateBody } from "../api/generated/client.schemas";
import { AppButton } from "../components/AppButton";
import { LanguageControl } from "../components/LanguageControl";
import { ThemeControl } from "../components/ThemeControl";
import { IncomingGroupReviewScreen } from "../features/incoming/IncomingGroupReviewScreen";
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
import { useI18n } from "../i18n/I18nProvider";
import "../features/incoming/incoming.css";
import "./app-shell.css";
import { SettingsScreen } from "../features/settings/SettingsScreen";
import { SetupManager } from "../features/setup/SetupManager";
import { SourcesScreen } from "../features/sources/SourcesScreen";
import { parseSourcesRoute } from "../features/sources/sourcesRoute";
import type { Locale } from "../i18n/locale";
import { type ShellMessageKey, shellMessages } from "../i18n/shellMessages";
import { useHashLocation } from "../navigation/hashLocation";

type ShellError = { message: string } | { fallback: true };

type Route =
  | "/"
  | "/settings"
  | "/setup"
  | "/sources"
  | "/incoming-groups"
  | "/operations"
  | "/library"
  | "/own-catalog"
  | `/library/artists/${string}`
  | `/library/releases/${string}`
  | `/library/releases/${string}/positions/${string}`;

function currentRoute(hash: string, isSourcesRoute: boolean): Route {
  const pathname = (hash.startsWith("#") ? hash.slice(1) : hash).split(
    "?",
    1,
  )[0];
  if (hash === "#/settings") return "/settings";
  if (hash === "#/setup") return "/setup";
  if (isSourcesRoute) return "/sources";
  if (hash === "#/incoming-groups") return "/incoming-groups";
  if (hash === "#/operations") return "/operations";
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
  const route = currentRoute(hash, sourcesRoute !== undefined);
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

  return (
    <main
      className={`app-shell ${
        !productAvailable
          ? "setup-shell"
          : route === "/incoming-groups"
            ? "settings-screen settings-shell incoming-screen"
            : route === "/sources"
              ? "sources-screen sources-shell"
              : "settings-screen settings-shell"
      }`}
    >
      <button
        type="button"
        className="app-skip-link"
        onClick={() => {
          contentRef.current?.focus();
        }}
      >
        {copy.skip}
      </button>
      {productAvailable ? (
        <>
          <aside className="app-sidebar" aria-label={copy.navigation}>
            <a className="app-brand" href="#/" aria-label={copy.brandHome}>
              <strong>MeloTrove</strong>
              <span>{copy.library}</span>
            </a>
            <ShellNavigation route={route} locale={locale} />
          </aside>
          <div className="app-workspace">
            <header className="app-topbar">
              <a className="app-brand app-brand--compact" href="#/">
                <strong>MeloTrove</strong>
                <span>{copy.library}</span>
              </a>
              <span className="app-section-label">
                {sectionLabel(route, copy)}
              </span>
              <div className="flex items-center gap-2">
                <LanguageControl />
                <ThemeControl />
              </div>
            </header>
            <section
              ref={contentRef}
              id="app-content"
              className="app-content"
              tabIndex={-1}
            >
              {renderRouteContent()}
            </section>
          </div>
        </>
      ) : (
        <>
          <header className="app-setup-header">
            <div className="app-brand">
              <strong>MeloTrove</strong>
              <span>{copy.library}</span>
            </div>
            <div className="flex items-center gap-2">
              <LanguageControl />
              <ThemeControl />
            </div>
          </header>
          <section
            ref={contentRef}
            id="app-content"
            className="app-content app-content--setup"
            tabIndex={-1}
          >
            {renderRouteContent()}
          </section>
        </>
      )}
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
    ) : state && route === "/incoming-groups" ? (
      <IncomingGroupReviewScreen />
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
      <a className="root-landing-action" href="#/sources">
        {copy.addSource}
      </a>
    </section>
  );
}

function sectionLabel(
  route: Route,
  copy: Record<ShellMessageKey, string>,
): string {
  if (route === "/incoming-groups") return copy.incoming;
  if (route === "/library" || route.startsWith("/library/"))
    return copy.library;
  if (route === "/own-catalog") return copy.ownCatalog;
  if (route === "/sources") return copy.sources;
  if (route === "/operations") return copy.operations;
  if (route === "/settings") return copy.settings;
  return copy.home;
}

function ShellNavigation({ route, locale }: { route: Route; locale: Locale }) {
  const copy = shellMessages[locale];
  const items = [
    {
      label: copy.incoming,
      href: "#/incoming-groups",
      active: route === "/incoming-groups",
    },
    {
      label: copy.library,
      href: "#/library",
      active: route === "/library" || route.startsWith("/library/"),
    },
    {
      label: copy.ownCatalog,
      href: "#/own-catalog",
      active: route === "/own-catalog",
    },
    { label: copy.sources, href: "#/sources", active: route === "/sources" },
    {
      label: copy.operations,
      href: "#/operations",
      active: route === "/operations",
    },
    { label: copy.settings, href: "#/settings", active: route === "/settings" },
  ];
  return (
    <nav className="app-navigation" aria-label={copy.sections}>
      {items.map((item) => (
        <a
          key={item.href}
          href={item.href}
          aria-current={item.active ? "page" : undefined}
        >
          {item.label}
        </a>
      ))}
    </nav>
  );
}
