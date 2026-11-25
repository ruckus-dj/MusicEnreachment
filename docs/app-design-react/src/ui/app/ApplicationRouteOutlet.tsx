import {
  type ComponentType,
  createContext,
  type ReactNode,
  useContext,
} from "react";
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
export type ApplicationRoute = "operations" | "settings";

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

const OperationsModelContext = createContext<ApplicationOperationsModel | null>(
  null,
);
const SettingsModelContext = createContext<ApplicationSettingsModel | null>(
  null,
);

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
