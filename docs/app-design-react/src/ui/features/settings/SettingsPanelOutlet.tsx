import {
  type ComponentType,
  createContext,
  type ReactNode,
  useContext,
} from "react";

type SettingsPanelProvider<Props extends object> = {
  Component: ComponentType<Props & { children: ReactNode }>;
  props: Props;
};

/**
 * A pure panel model or a mounted controller that supplies it. A provider
 * mounts with the panel, so its lifetime matches the rendered panel.
 */
export type SettingsPanelSource<Model extends object, Props extends object> =
  | Model
  | SettingsPanelProvider<Props>;

/**
 * Builds the typed boundary shared by portable Settings panels. Hosts pass a
 * pure view model or a mounted controller; the controller publishes its model
 * through context for the outlet to render.
 */
export function createSettingsPanelOutlet<
  Model extends object,
  Props extends object,
>(name: string, View: ComponentType<Model>) {
  const ModelContext = createContext<Model | null>(null);

  function DataBinder({
    model,
    children,
  }: {
    model: Model;
    children: ReactNode;
  }) {
    return (
      <ModelContext.Provider value={model}>{children}</ModelContext.Provider>
    );
  }

  function BoundView() {
    const model = useContext(ModelContext);
    if (!model) throw new Error(`missing ${name} view model`);
    return <View {...model} />;
  }

  function isProvider(
    source: SettingsPanelSource<Model, Props>,
  ): source is SettingsPanelProvider<Props> {
    return "Component" in source;
  }

  function Outlet({ source }: { source: SettingsPanelSource<Model, Props> }) {
    if (isProvider(source)) {
      const { Component, props } = source;
      return (
        <Component {...props}>
          <BoundView />
        </Component>
      );
    }
    return (
      <DataBinder model={source}>
        <BoundView />
      </DataBinder>
    );
  }

  return { DataBinder, Outlet };
}
