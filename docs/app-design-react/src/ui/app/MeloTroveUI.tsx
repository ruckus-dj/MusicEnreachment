import { useCallback, useEffect } from "react";
import { catalogMessage } from "../features/library/catalogMessages";
import {
  ApplicationFrame,
  ShellHomeView,
  ShellStartupView,
} from "../shell/ShellStartupView";
import { shellApplicationFrameState } from "../shell/shellRoute";
import { useShellPageEffects } from "../shell/useShellPageEffects";
import {
  MeloTroveIncomingContent,
  type MeloTroveProvider,
  MeloTroveReleaseContent,
  type MeloTroveScreens,
  MeloTroveSetupContent,
  MeloTroveSourcesContent,
  useMeloTroveHomeSources,
} from "./MeloTroveProvider";
import {
  meloTrovePaths,
  type MeloTroveRoute,
  parseMeloTroveRoute,
} from "./MeloTroveRoute";

const sectionLinks = {
  review: meloTrovePaths.incoming,
  library: meloTrovePaths.library,
  local: meloTrovePaths.ownCatalog,
  sources: meloTrovePaths.sources,
  operations: meloTrovePaths.operations,
  settings: meloTrovePaths.settings,
};

export function MeloTroveUI({ provider }: { provider: MeloTroveProvider }) {
  const { useLocation, useModel, validateLibraryId } = provider;
  const { hash, navigate } = useLocation();
  const route = parseMeloTroveRoute(hash, validateLibraryId);
  const {
    locale,
    themeStore,
    footer,
    setupLink,
    incomingCount,
    startup,
    screens,
  } = useModel(route);
  const setupRequested =
    startup.kind === "ready" && route.kind === "setup" && setupLink;
  const redirect =
    startup.kind === "setup" && route.kind !== "setup"
      ? meloTrovePaths.setup
      : startup.kind === "ready" && route.kind === "setup" && !setupRequested
        ? meloTrovePaths.home
        : undefined;

  useEffect(() => {
    if (redirect) navigate(redirect);
  }, [navigate, redirect]);

  const openIncoming = useCallback(
    () => navigate(meloTrovePaths.incoming),
    [navigate],
  );
  const hasPage =
    !redirect && (startup.kind === "ready" || startup.kind === "setup");
  const focusKey =
    route.kind === "library-artist"
      ? `${meloTrovePaths.library}/artists/${route.address.artistId}`
      : route.kind === "library-catalog" ||
          route.kind === "own-catalog" ||
          route.kind === "operations" ||
          route.kind === "settings"
        ? route.pathname
        : hash;
  useShellPageEffects({
    focusKey: hasPage ? focusKey : "",
    titleKey: hasPage && footer.kind === "prototype" ? hash : "",
    titleSuffix: catalogMessage(locale, "documentTitleSuffix"),
    scrollResetKey:
      hasPage && route.kind === "incoming" ? route.pathname : undefined,
  });

  const content =
    startup.kind === "setup" || setupRequested ? (
      screens.setup()(<MeloTroveSetupContent />)
    ) : startup.kind !== "ready" ? (
      <ShellStartupView locale={locale} state={startup} />
    ) : (
      !redirect && renderRoute(route, screens, locale, openIncoming)
    );
  return (
    <ApplicationFrame
      locale={locale}
      themeStore={themeStore}
      state={shellApplicationFrameState(
        route.family,
        startup.kind === "ready" && !setupRequested ? "product" : "startup",
        {
          brandHref: meloTrovePaths.home,
          sectionLinks,
          incomingCount,
          footer,
          setupHref: setupLink ? meloTrovePaths.setup : undefined,
        },
      )}
    >
      {content}
    </ApplicationFrame>
  );
}

function renderRoute(
  route: MeloTroveRoute,
  screens: MeloTroveScreens,
  locale: Parameters<typeof catalogMessage>[0],
  openIncoming: () => void,
) {
  switch (route.kind) {
    case "home":
      return screens.home()(
        <MeloTroveHomeContent locale={locale} openIncoming={openIncoming} />,
      );
    case "setup":
      return null;
    case "incoming":
      return screens.incoming(route.route)(<MeloTroveIncomingContent />);
    case "sources":
      return screens.sources(route.route)(<MeloTroveSourcesContent />);
    case "library-release":
      return screens.libraryRelease(route.address)(<MeloTroveReleaseContent />);
    case "library-artist":
      return screens.libraryArtist(route.address)("library-artist");
    case "library-catalog":
      return screens.libraryCatalog()("library-catalog");
    case "own-catalog":
      return screens.ownCatalog()("own-catalog");
    case "settings":
      return screens.settings()("settings");
    case "operations":
      return screens.operations()("operations");
  }
}

function MeloTroveHomeContent({
  locale,
  openIncoming,
}: {
  locale: Parameters<typeof catalogMessage>[0];
  openIncoming: () => void;
}) {
  const sources = useMeloTroveHomeSources();
  return (
    <ShellHomeView
      locale={locale}
      sources={sources}
      sourcesHref={meloTrovePaths.sources}
      onOpenIncoming={openIncoming}
    />
  );
}
