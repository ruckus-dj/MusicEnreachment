import {
  type ComponentType,
  createContext,
  type ReactNode,
  useContext,
} from "react";
import type { ThemePreferenceStore } from "../components/themePreference";
import type { IncomingGroupsRoute } from "../features/incoming/incomingGroupsRoute";
import {
  IncomingWorkspaceView,
  type IncomingWorkspaceViewProps,
} from "../features/incoming/IncomingWorkspaceView";
import type {
  LibraryArtistAddress,
  LibraryIdValidator,
} from "../features/library/libraryDetailRoute";
import {
  LibraryReleaseWorkspaceView,
  type LibraryReleaseWorkspaceViewProps,
} from "../features/library/LibraryReleaseWorkspaceView";
import {
  LibrarySelectionPreviewView,
  type LibrarySelectionPreviewViewProps,
} from "../features/library/LibrarySelectionPreviewView";
import { LibrarySelectionScreenFrame } from "../features/library/LibrarySelectionScreenFrame";
import {
  SetupScreenView,
  type SetupScreenViewProps,
} from "../features/setup/SetupScreenView";
import type { SourcesRoute } from "../features/sources/sourcesRoute";
import {
  type SourcesPage,
  SourcesPageView,
} from "../features/sources/SourcesRouteOutlet";
import type { Locale } from "../i18n/types";
import type { ShellFrameData } from "../shell/shellRoute";
import type { ShellHomeSourceRead } from "../shell/ShellStartupView";
import {
  type ApplicationRoute,
  type ApplicationRouteDataProvider,
  ApplicationRouteOutlet,
} from "./ApplicationRouteOutlet";
import type { MeloTroveRoute } from "./MeloTroveRoute";

export interface MeloTroveLocation {
  hash: string;
  navigate: (href: string) => void;
}

export type MeloTroveStartup =
  | { kind: "ready" }
  | { kind: "loading-server" }
  | { kind: "setup" }
  | { kind: "setup-error"; message?: string; retry: () => void }
  | {
      kind: "diagnostic";
      message?: string;
      platform: string;
      retry: () => void;
    };

export interface MeloTroveModel {
  locale: Locale;
  themeStore: ThemePreferenceStore;
  footer: ShellFrameData["footer"];
  setupLink?: boolean;
  incomingCount?: number;
  startup: MeloTroveStartup;
  screens: MeloTroveScreens;
}

/** Both hosts must bind data for every destination. The UI selects the view. */
export interface MeloTroveScreens {
  home: () => MeloTroveScreenBinding;
  setup: () => MeloTroveScreenBinding;
  sources: (route: SourcesRoute) => MeloTroveScreenBinding;
  incoming: (route: IncomingGroupsRoute) => MeloTroveScreenBinding;
  libraryRelease: (address: {
    releaseId: string;
    positionId?: string;
  }) => MeloTroveScreenBinding;
  libraryArtist: (address: LibraryArtistAddress) => MeloTroveRouteBinding;
  libraryCatalog: () => MeloTroveRouteBinding;
  ownCatalog: () => MeloTroveRouteBinding;
  settings: () => MeloTroveRouteBinding;
  operations: () => MeloTroveRouteBinding;
}

/** The host implements the data hooks and browser adapter, not the screens. */
export interface MeloTroveProvider {
  useLocation: () => MeloTroveLocation;
  useModel: (route: MeloTroveRoute) => MeloTroveModel;
  validateLibraryId?: LibraryIdValidator;
}

export type MeloTroveScreenBinding = (outlet: ReactNode) => ReactNode;
export type MeloTroveRouteBinding = (route: ApplicationRoute) => ReactNode;

export function bindMeloTroveScreen<Props extends object>(
  Provider: ApplicationRouteDataProvider<Props>,
  props: Props,
): MeloTroveScreenBinding {
  return (outlet) => <Provider {...props}>{outlet}</Provider>;
}

export function bindMeloTroveRoute<Props extends object>(
  provider: ApplicationRouteDataProvider<Props>,
  props: Props,
  options?: {
    key?: string;
    selection?: {
      dock?: ReactNode;
      preview?: LibrarySelectionPreviewViewProps;
    };
  },
): MeloTroveRouteBinding {
  return (route) => {
    const screen = (
      <ApplicationRouteOutlet
        key={options?.key}
        route={route}
        provider={provider}
        providerProps={props}
      />
    );
    const selection = options?.selection;
    return selection ? (
      <LibrarySelectionScreenFrame>
        {screen}
        {selection.dock}
        {selection.preview && (
          <LibrarySelectionPreviewView {...selection.preview} />
        )}
      </LibrarySelectionScreenFrame>
    ) : (
      screen
    );
  };
}

const HomeContext = createContext<ShellHomeSourceRead | null>(null);

export function MeloTroveHomeBinder({
  sources,
  children,
}: {
  sources: ShellHomeSourceRead;
  children: ReactNode;
}) {
  return (
    <HomeContext.Provider value={sources}>{children}</HomeContext.Provider>
  );
}

export function useMeloTroveHomeSources() {
  const sources = useContext(HomeContext);
  if (!sources) throw new Error("Missing MeloTrove home model");
  return sources;
}

export const bindMeloTroveHome = bindMeloTroveScreen;

export function bindMeloTroveHomeModel(sources: ShellHomeSourceRead) {
  return bindMeloTroveScreen(MeloTroveHomeBinder, { sources });
}

const SetupContext = createContext<ReactNode>(null);

export function MeloTroveSetupBinder<
  SummaryProps extends object,
  ToolsProps extends object,
  MetadataProps extends object,
>({
  model,
  children,
}: {
  model: SetupScreenViewProps<SummaryProps, ToolsProps, MetadataProps>;
  children: ReactNode;
}) {
  return (
    <SetupContext.Provider value={<SetupScreenView {...model} />}>
      {children}
    </SetupContext.Provider>
  );
}

export function MeloTroveSetupContent() {
  const content = useContext(SetupContext);
  if (!content) throw new Error("Missing MeloTrove setup model");
  return content;
}

const IncomingContext = createContext<ReactNode>(null);

export function MeloTroveIncomingBinder<Props extends object>({
  model,
  children,
}: {
  model: IncomingWorkspaceViewProps<Props>;
  children: ReactNode;
}) {
  return (
    <IncomingContext.Provider value={<IncomingWorkspaceView {...model} />}>
      {children}
    </IncomingContext.Provider>
  );
}

export function MeloTroveIncomingContent() {
  const content = useContext(IncomingContext);
  if (!content) throw new Error("Missing MeloTrove incoming model");
  return content;
}

// Keep provider-specific slot props typed up to the portable view, without
// making the root provider contract generic over each nested slot.
const ReleaseContext = createContext<ReactNode>(null);

export function MeloTroveReleaseBinder<Props extends object>({
  model,
  children,
}: {
  model: LibraryReleaseWorkspaceViewProps<Props>;
  children: ReactNode;
}) {
  return (
    <ReleaseContext.Provider value={<LibraryReleaseWorkspaceView {...model} />}>
      {children}
    </ReleaseContext.Provider>
  );
}

export function MeloTroveReleaseContent() {
  const content = useContext(ReleaseContext);
  if (!content) throw new Error("Missing MeloTrove release model");
  return content;
}

export function bindMeloTroveReleaseModel<Props extends object>(
  model: LibraryReleaseWorkspaceViewProps<Props>,
) {
  return bindMeloTroveScreen(MeloTroveReleaseBinder<Props>, { model });
}

const SourcesContext = createContext<ReactNode>(null);

export function MeloTroveSourcesBinder<Props extends object>({
  model,
  children,
}: {
  model: SourcesPage<Props>;
  children: ReactNode;
}) {
  return (
    <SourcesContext.Provider value={<SourcesPageView page={model} />}>
      {children}
    </SourcesContext.Provider>
  );
}

export function MeloTroveSourcesContent() {
  const content = useContext(SourcesContext);
  if (!content) throw new Error("Missing MeloTrove sources model");
  return content;
}
