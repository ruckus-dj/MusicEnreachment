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

/**
 * Semantic routes owned by the portable tree. A host selects the route and its
 * data provider; it does not choose or compose the screen.
 */
export type ApplicationRoute = "operations";

/** The complete data-only model of the portable operations screen. */
export type ApplicationOperationsModel = OperationsScreenViewProps;

export interface ApplicationRouteProviderProps {
  children: ReactNode;
}

export type ApplicationRouteDataProvider<
  ProviderProps extends object = object,
> = ComponentType<ProviderProps & ApplicationRouteProviderProps>;

const OperationsModelContext = createContext<ApplicationOperationsModel | null>(
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

function OperationsRoute() {
  const model = useContext(OperationsModelContext);
  if (!model) throw new Error("missing operations view model");
  return <OperationsScreenView {...model} />;
}

const applicationRoutes: Record<ApplicationRoute, ComponentType> = {
  operations: OperationsRoute,
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
