import { useQuery } from "@tanstack/react-query";
import { type ReactNode, useCallback, useEffect, useState } from "react";
import packageMetadata from "../../package.json";
import {
  getGetIncomingGroupsQueryKey,
  getIncomingGroupCount,
  getIncomingGroups,
  getSetupState,
  useListSources,
} from "../api/generated/client";
import type { SetupStateBody } from "../api/generated/client.schemas";
import { useI18n } from "../app/i18n/host";
import { useHashLocation } from "../app/navigation/hashLocation";
import { themePreferenceStore } from "../app/theme/themePreferenceHost";
import {
  bindMeloTroveRoute,
  bindMeloTroveScreen,
  MeloTroveHomeBinder,
  type MeloTroveModel,
  type MeloTroveProvider,
  type MeloTroveScreens,
} from "../ui/app/MeloTroveProvider";
import type { MeloTroveRoute } from "../ui/app/MeloTroveRoute";
import type { ShellHomeSourceRead } from "../ui/shell/ShellStartupView";
import { IncomingGroupReviewScreen } from "./incoming/IncomingGroupReviewScreen";
import { incomingGroupCountQueryKey } from "./incoming/incomingGroupsQueryKeys";
import { ProductionLibraryArtistBinder } from "./library/LibraryArtistScreen";
import { LibraryReleaseScreen } from "./library/LibraryReleaseScreen";
import { ProductionLibraryCatalogBinder } from "./library/LibraryScreen";
import { isUuid } from "./library/libraryRoute";
import { ProductionOwnCatalogBinder } from "./local-catalog/OwnCatalogScreen";
import { ProductionOperationsBinder } from "./operations/OperationsScreen";
import { ProductionSettingsBinder } from "./settings/SettingsScreen";
import { SetupManager } from "./setup/SetupManager";
import { SourcesScreen } from "./sources/SourcesScreen";

type ShellError = { message: string } | { fallback: true };

function ProductionHomeBinder({ children }: { children: ReactNode }) {
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
    <MeloTroveHomeBinder sources={sourceRead}>{children}</MeloTroveHomeBinder>
  );
}

function navigateHash(href: string) {
  if (window.location.hash === href) return;
  window.location.hash = href;
}

function useProductionMeloTroveLocation() {
  return { hash: useHashLocation(), navigate: navigateHash };
}

function useProductionMeloTroveModel(route: MeloTroveRoute): MeloTroveModel {
  const { locale } = useI18n();
  const [state, setState] = useState<SetupStateBody>();
  const [error, setError] = useState<ShellError | null>(null);
  const [loading, setLoading] = useState(true);
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
  const reload = useCallback(() => {
    void load();
  }, [load]);

  const productAvailable =
    Boolean(state?.completed) &&
    !state?.platform.diagnostic &&
    Boolean(state?.platform.supported);
  const incomingActive = route.family === "review";
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
    enabled: productAvailable && !incomingActive,
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
  const incomingCount = !productAvailable
    ? undefined
    : incomingActive
      ? incomingGroups.data === undefined
        ? undefined
        : (incomingGroups.data.groups?.length ?? 0)
      : incomingGroupCount.data;

  const startup = startupState();
  const screens: MeloTroveScreens = {
    home: () => bindMeloTroveScreen(ProductionHomeBinder, {}),
    setup: () =>
      state
        ? bindMeloTroveScreen(SetupManager, {
            initialState: state,
            onCompleted: reload,
          })
        : () => null,
    sources: (sourcesRoute) =>
      bindMeloTroveScreen(SourcesScreen, { route: sourcesRoute }),
    incoming: (incomingRoute) =>
      bindMeloTroveScreen(IncomingGroupReviewScreen, { route: incomingRoute }),
    libraryRelease: (address) =>
      bindMeloTroveScreen(LibraryReleaseScreen, address),
    libraryArtist: (address) =>
      bindMeloTroveRoute(ProductionLibraryArtistBinder, {
        artistId: address.artistId,
      }),
    libraryCatalog: () =>
      bindMeloTroveRoute(ProductionLibraryCatalogBinder, {}),
    ownCatalog: () => bindMeloTroveRoute(ProductionOwnCatalogBinder, {}),
    settings: () => bindMeloTroveRoute(ProductionSettingsBinder, {}),
    operations: () => bindMeloTroveRoute(ProductionOperationsBinder, {}),
  };

  return {
    locale,
    themeStore: themePreferenceStore,
    footer: { kind: "version", version: packageMetadata.version },
    incomingCount,
    startup,
    screens,
  };

  function startupState(): MeloTroveModel["startup"] {
    if (loading) return { kind: "loading-server" };
    if (error)
      return {
        kind: "setup-error",
        message: "message" in error ? error.message : undefined,
        retry: reload,
      };
    if (state && (state.platform.diagnostic || !state.platform.supported))
      return {
        kind: "diagnostic",
        message: state.platform.reason,
        platform: `${state.platform.goos}/${state.platform.goarch}`,
        retry: reload,
      };
    if (state && !state.completed) return { kind: "setup" };
    return { kind: "ready" };
  }
}

export const productionMeloTroveProvider = {
  useLocation: useProductionMeloTroveLocation,
  useModel: useProductionMeloTroveModel,
  validateLibraryId: isUuid,
} satisfies MeloTroveProvider;
