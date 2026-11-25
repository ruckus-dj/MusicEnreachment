import {
  type ComponentProps,
  type ComponentType,
  createContext,
  type ReactNode,
  useContext,
} from "react";
import { GitHubTokenFormView } from "../tools/GitHubTokenFormView";

/** Pure token form model owned by a host controller. */
export type SettingsTokenModel = ComponentProps<typeof GitHubTokenFormView>;

/** Inputs a host controller needs to own the token draft. */
export type SettingsTokenProviderProps = {
  hasToken: boolean;
  onChanged?: () => void | Promise<void>;
};

type SettingsTokenProvider = {
  Component: ComponentType<
    SettingsTokenProviderProps & { children: ReactNode }
  >;
  props: SettingsTokenProviderProps;
};

/**
 * A pure token model or a mounted controller that owns the draft. A provider
 * mounts with the Tools panel, so its lifetime matches the rendered panel.
 */
export type SettingsTokenSource = SettingsTokenModel | SettingsTokenProvider;

const SettingsTokenModelContext = createContext<SettingsTokenModel | null>(
  null,
);

/** Host-facing adapter from a mounted token controller to its view model. */
export function SettingsTokenDataBinder({
  model,
  children,
}: {
  model: SettingsTokenModel;
  children: ReactNode;
}) {
  return (
    <SettingsTokenModelContext.Provider value={model}>
      {children}
    </SettingsTokenModelContext.Provider>
  );
}

function BoundSettingsTokenView() {
  const model = useContext(SettingsTokenModelContext);
  if (!model) throw new Error("missing settings token view model");
  return <GitHubTokenFormView {...model} />;
}

function isSettingsTokenProvider(
  source: SettingsTokenSource,
): source is SettingsTokenProvider {
  return "Component" in source;
}

/** Renders the pure token model or mounts the host controller that supplies it. */
export function SettingsTokenOutlet({
  source,
}: {
  source: SettingsTokenSource;
}) {
  if (isSettingsTokenProvider(source)) {
    const { Component, props } = source;
    return (
      <Component {...props}>
        <BoundSettingsTokenView />
      </Component>
    );
  }
  return (
    <SettingsTokenDataBinder model={source}>
      <BoundSettingsTokenView />
    </SettingsTokenDataBinder>
  );
}
