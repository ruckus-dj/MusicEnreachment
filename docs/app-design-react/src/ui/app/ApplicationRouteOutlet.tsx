import {
  type ComponentType,
  createContext,
  type ReactNode,
  useContext,
} from "react";
import {
  IncomingScreenView,
  type IncomingScreenViewProps,
} from "../features/incoming/IncomingScreenView";
import {
  LibraryCatalogPageView,
  type LibraryCatalogPageViewProps,
} from "../features/library/LibraryCatalogPageView";
import {
  LocalCatalogScreenView,
  type LocalCatalogScreenViewProps,
} from "../features/local-catalog/LocalCatalogScreenView";
import {
  OperationsScreenView,
  type OperationsScreenViewProps,
} from "../features/operations/OperationsScreenView";
import {
  SettingsScreenView,
  type SettingsScreenViewProps,
} from "../features/settings/SettingsScreenView";

/**
 * Semantic routes owned by the portable tree. A host selects the route and its
 * data provider; it does not choose or compose the screen.
 */
export type ApplicationRoute =
  | "incoming-list"
  | "library-catalog"
  | "own-catalog"
  | "operations"
  | "settings";

/** The complete data-only model of the portable Incoming list screen. */
export type ApplicationIncomingListModel = IncomingScreenViewProps;

/** The complete data-only model of the portable library catalog screen. */
export type ApplicationLibraryCatalogModel = LibraryCatalogPageViewProps;

/** The complete data-only model of the portable Local catalog screen. */
export type ApplicationOwnCatalogModel = LocalCatalogScreenViewProps;

/** The complete data-only model of the portable operations screen. */
export type ApplicationOperationsModel = OperationsScreenViewProps;

/** The complete data-only model of the portable settings screen. */
export type ApplicationSettingsModel = SettingsScreenViewProps;

export interface ApplicationRouteProviderProps {
  children: ReactNode;
}

export type ApplicationRouteDataProvider<
  ProviderProps extends object = object,
> = ComponentType<ProviderProps & ApplicationRouteProviderProps>;

const IncomingListModelContext =
  createContext<ApplicationIncomingListModel | null>(null);
const LibraryCatalogModelContext =
  createContext<ApplicationLibraryCatalogModel | null>(null);
const OwnCatalogModelContext = createContext<ApplicationOwnCatalogModel | null>(
  null,
);
const OperationsModelContext = createContext<ApplicationOperationsModel | null>(
  null,
);
const SettingsModelContext = createContext<ApplicationSettingsModel | null>(
  null,
);

/** Host-facing adapter for the portable Incoming list screen. */
export function ApplicationIncomingListBinder({
  model,
  children,
}: {
  model: ApplicationIncomingListModel;
  children: ReactNode;
}) {
  return (
    <IncomingListModelContext.Provider value={model}>
      {children}
    </IncomingListModelContext.Provider>
  );
}

/** Host-facing adapter for the portable library catalog screen. */
export function ApplicationLibraryCatalogBinder({
  model,
  children,
}: {
  model: ApplicationLibraryCatalogModel;
  children: ReactNode;
}) {
  return (
    <LibraryCatalogModelContext.Provider value={model}>
      {children}
    </LibraryCatalogModelContext.Provider>
  );
}

/** Host-facing adapter for the portable Local catalog screen. */
export function ApplicationOwnCatalogBinder({
  model,
  children,
}: {
  model: ApplicationOwnCatalogModel;
  children: ReactNode;
}) {
  return (
    <OwnCatalogModelContext.Provider value={model}>
      {children}
    </OwnCatalogModelContext.Provider>
  );
}

/**
 * Host-facing adapter. It wraps the exact outlet child with a model value and
 * never supplies a screen, a render slot or a hook.
 */
export function ApplicationOperationsBinder({
  model,
  children,
}: {
  model: ApplicationOperationsModel;
  children: ReactNode;
}) {
  return (
    <OperationsModelContext.Provider value={model}>
      {children}
    </OperationsModelContext.Provider>
  );
}

/** Host-facing adapter for the portable settings screen. */
export function ApplicationSettingsBinder({
  model,
  children,
}: {
  model: ApplicationSettingsModel;
  children: ReactNode;
}) {
  return (
    <SettingsModelContext.Provider value={model}>
      {children}
    </SettingsModelContext.Provider>
  );
}

function IncomingListRoute() {
  const model = useContext(IncomingListModelContext);
  if (!model) throw new Error("missing incoming list view model");
  return <IncomingScreenView {...model} />;
}

function LibraryCatalogRoute() {
  const model = useContext(LibraryCatalogModelContext);
  if (!model) throw new Error("missing library catalog view model");
  return <LibraryCatalogPageView {...model} />;
}

function OwnCatalogRoute() {
  const model = useContext(OwnCatalogModelContext);
  if (!model) throw new Error("missing own catalog view model");
  return <LocalCatalogScreenView {...model} />;
}

function OperationsRoute() {
  const model = useContext(OperationsModelContext);
  if (!model) throw new Error("missing operations view model");
  return <OperationsScreenView {...model} />;
}

function SettingsRoute() {
  const model = useContext(SettingsModelContext);
  if (!model) throw new Error("missing settings view model");
  return <SettingsScreenView {...model} />;
}

const applicationRoutes: Record<ApplicationRoute, ComponentType> = {
  "incoming-list": IncomingListRoute,
  "library-catalog": LibraryCatalogRoute,
  "own-catalog": OwnCatalogRoute,
  operations: OperationsRoute,
  settings: SettingsRoute,
};

/**
 * Mounts the canonical screen for a semantic route inside the passed data
 * provider. The provider binds data only; presentation stays in this tree.
 */
export function ApplicationRouteOutlet<ProviderProps extends object = object>({
  route,
  provider: DataProvider,
  providerProps,
}: {
  route: ApplicationRoute;
  provider: ApplicationRouteDataProvider<ProviderProps>;
  providerProps: ProviderProps;
}) {
  const Screen = applicationRoutes[route];
  return (
    <DataProvider {...providerProps}>
      <Screen />
    </DataProvider>
  );
}
